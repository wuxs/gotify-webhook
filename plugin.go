package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/gorilla/websocket"
	"github.com/gotify/plugin-api"
)

// GetGotifyPluginInfo returns gotify plugin info.
func GetGotifyPluginInfo() plugin.Info {
	return plugin.Info{
		ModulePath:  "github.com/wuxs/gotify-webhook",
		Author:      "wuxs",
		Version:     "0.1.0",
		Description: "forward message to others webhook server",
		Name:        "WebHook",
	}
}

type MessageExternal struct {
	ID            uint                   `json:"id"`
	ApplicationID uint                   `json:"appid"`
	Message       string                 `form:"message" query:"message" json:"message" binding:"required"`
	Title         string                 `form:"title" query:"title" json:"title"`
	Priority      int                    `form:"priority" query:"priority" json:"priority"`
	Extras        map[string]interface{} `form:"-" query:"-" json:"extras,omitempty"`
	Date          time.Time              `json:"date"`
}

// EchoPlugin is the gotify plugin instance.
type MultiNotifierPlugin struct {
	msgHandler     plugin.MessageHandler
	storageHandler plugin.StorageHandler
	config         *Config
	cancel         context.CancelFunc
}

// Enable enables the plugin.
func (p *MultiNotifierPlugin) Enable() error {
	if len(p.config.HostServer) < 1 {
		return errors.New("please enter the correct web server")
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	serverUrl := p.config.HostServer + "/stream"

	go func() {
		for {
			select {
			case <-ctx.Done():
				slog.Info("Plugin stopped")
				return
			default:
				err := p.receiveMessages(ctx, serverUrl)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						slog.Info("ReceiveMessages canceled")
						return
					}
					slog.Error("Read message error, retrying after 1s", slog.Any("err", err))
					time.Sleep(time.Second)
				} else {
					return
				}
			}
		}
	}()

	slog.Info("Webhook plugin enabled", slog.Any("config", GetGotifyPluginInfo()))

	return nil
}

// Disable disables the plugin.
func (p *MultiNotifierPlugin) Disable() error {
	if p.cancel != nil {
		p.cancel()
	}
	slog.Info("Webhook plugin disbled", slog.Any("config", GetGotifyPluginInfo()))
	return nil
}

// SetStorageHandler implements plugin.Storager
func (p *MultiNotifierPlugin) SetStorageHandler(h plugin.StorageHandler) {
	p.storageHandler = h
}

// SetMessageHandler implements plugin.Messenger.
func (p *MultiNotifierPlugin) SetMessageHandler(h plugin.MessageHandler) {
	p.msgHandler = h
}

// Storage defines the plugin storage scheme
type Storage struct {
	CalledTimes int `json:"called_times"`
}

type WebHook struct {
	Url    string            `yaml:"url"`
	Method string            `yaml:"method"`
	Body   string            `yaml:"body"`
	Header map[string]string `yaml:"header"`
	Apps   []uint            `yaml:"apps"`
}

// Config defines the plugin config scheme
type Config struct {
	ClientToken string     `yaml:"client_token" validate:"required"`
	HostServer  string     `yaml:"host_server" validate:"required"`
	WebHooks    []*WebHook `yaml:"web_hooks"`
}

// DefaultConfig implements plugin.Configurer
func (p *MultiNotifierPlugin) DefaultConfig() interface{} {
	c := &Config{
		ClientToken: "CrMo3UaAQG1H37G",
		HostServer:  "ws://localhost",
	}
	return c
}

// ValidateAndSetConfig implements plugin.Configurer
func (p *MultiNotifierPlugin) ValidateAndSetConfig(config interface{}) error {
	p.config = config.(*Config)
	validWebhooks := make([]*WebHook, 0)

	for _, webhook := range p.config.WebHooks {
		if webhook.Method == "" {
			webhook.Method = "POST"
		}

		parsedURL, err := url.Parse(webhook.Url)
		if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
			return fmt.Errorf("invalid webhook URL: %s", webhook.Url)
		}

		if _, exists := webhook.Header["Content-Type"]; !exists {
			if webhook.Header == nil {
				webhook.Header = make(map[string]string)
			}
			webhook.Header["Content-Type"] = "text/plain"
		}

		validWebhooks = append(validWebhooks, webhook)
	}

	p.config.WebHooks = validWebhooks

	return nil
}

// GetDisplay implements plugin.Displayer.
func (p *MultiNotifierPlugin) GetDisplay(location *url.URL) string {
	message := `
	Guide:

	1. Create a new client and put its token into the client_token option.
	2. Update the host_server option if it is different with the default 'ws://localhost'.
	3. Configurate webhooks.

	Webhook example:

	web_hooks: 
	  - url: http://example.com/api/messages
	    body: "{{.title}}\n\n{{.message}}"
	  - url: http://192.168.1.2:10201/api/sendTextMsg	
	    apps:
	      - 1
	    method: POST
	    header:
	      Content-Type: application/json
	    body: "{\"wxid\":\"xxxxxxxx\",\"msg\":\"{{.title}}\n{{.message}}\"}"

	Note: Re-enable the plugin after making changes.
	`
	return message
}

func (p *MultiNotifierPlugin) receiveMessages(ctx context.Context, serverUrl string) (err error) {
	header := http.Header{}
	header.Add("Authorization", "Bearer "+p.config.ClientToken)
	conn, _, err := websocket.DefaultDialer.Dial(serverUrl, header)
	if err != nil {
		return fmt.Errorf("dial error: %w", err)
	}
	defer conn.Close()

	slog.Info("Connected to Websocket server", slog.String("url", serverUrl))

	readErrCh := make(chan error, 1)

	go func() {
		defer close(readErrCh)
		for {
			select {
			case <-ctx.Done():
				readErrCh <- ctx.Err()
				return
			default:
				_, message, err := conn.ReadMessage()
				if err != nil {
					readErrCh <- fmt.Errorf("read message error: %w", err)
					return
				}

				msg := &MessageExternal{}
				if err := json.Unmarshal(message, msg); err != nil {
					slog.Warn("Unsupported message format", slog.Any("message", string(message)))
					continue
				}

				errs := p.sendMessage(ctx, msg, p.config.WebHooks)
				if len(errs) > 0 {
					for _, err := range errs {
						slog.Error("Failed to send message", slog.Any("error", err))
					}
				}
			}
		}
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Context cancelled, closing WebSocket connection")
			err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				return fmt.Errorf("write close message error: %w", err)
			}
			return nil
		case err := <-readErrCh:
			return err
		case t := <-ticker.C:
			err := conn.WriteMessage(websocket.TextMessage, []byte(t.String()))
			if err != nil {
				return fmt.Errorf("write heartbeat message error: %w", err)
			}
			ticker.Reset(time.Second)
		}
	}
}

func (p *MultiNotifierPlugin) sendMessage(ctx context.Context, msg *MessageExternal, webhooks []*WebHook) (errors []error) {
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	for _, webhook := range webhooks {
		webhook := webhook // Create local variable for closure, for golang 1.22 and older versions.
		wg.Add(1)
		go func() {
			defer wg.Done()

			if ctx.Err() != nil {
				mu.Lock()
				errors = append(errors, ctx.Err())
				mu.Unlock()
				return
			}

			// Only messages from white-listed applications can be forwarded.
			if len(webhook.Apps) > 0 {
				appAllowed := false
				for _, appID := range webhook.Apps {
					if appID == msg.ApplicationID {
						appAllowed = true
						break
					}
				}
				if !appAllowed {
					return
				}
			}

			// Process the webhook body
			body, err := p.processWebhookBody(webhook.Body, msg)
			if err != nil {
				err = fmt.Errorf("failed to process webhook body for %s: %w", webhook.Url, err)
				mu.Lock()
				errors = append(errors, err)
				mu.Unlock()
				return
			}

			// Send the HTTP request
			err = p.sendHTTPRequest(ctx, webhook, body)
			if err != nil {
				err = fmt.Errorf("failed to send webhook request to %s: %w", webhook.Url, err)
				mu.Lock()
				errors = append(errors, err)
				mu.Unlock()
				return
			}
		}()
	}

	wg.Wait()

	return errors
}

func (p *MultiNotifierPlugin) processWebhookBody(body string, msg *MessageExternal) (string, error) {
	var jsonBody map[string]interface{}
	isJSON := json.Unmarshal([]byte(body), &jsonBody) == nil

	if isJSON {
		// Process JSON structured template
		err := processJSONRecursive(jsonBody, msg)
		if err != nil {
			return "", fmt.Errorf("failed to process JSON body: %w", err)
		}

		newBody, err := json.Marshal(jsonBody)
		if err != nil {
			return "", fmt.Errorf("failed to marshal body: %w", err)
		}
		return string(newBody), nil
	} else {
		// Process plain text template
		return processTemplateString(body, msg)
	}
}

func (p *MultiNotifierPlugin) sendHTTPRequest(ctx context.Context, webhook *WebHook, body string) error {
	req, err := http.NewRequestWithContext(ctx, webhook.Method, webhook.Url, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	for k, v := range webhook.Header {
		req.Header.Add(k, v)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}

	return nil
}

func processJSONRecursive(m map[string]interface{}, msg *MessageExternal) (err error) {
	for k, v := range m {
		switch vv := v.(type) {
		case string:
			m[k], err = processTemplateString(vv, msg)
		case map[string]interface{}:
			err = processJSONRecursive(vv, msg)
		case []interface{}:
			for i, item := range vv {
				if itemString, ok := item.(string); ok {
					vv[i], err = processTemplateString(itemString, msg)
				} else if itemMap, ok := item.(map[string]interface{}); ok {
					err = processJSONRecursive(itemMap, msg)
				}
			}
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func processTemplateString(s string, msg *MessageExternal) (string, error) {
	tmpl, err := template.New("").Parse(s)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]interface{}{
		"title":   msg.Title,
		"message": msg.Message,
	})
	if err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// NewGotifyPluginInstance creates a plugin instance for a user context.
func NewGotifyPluginInstance(ctx plugin.UserContext) plugin.Plugin {
	return &MultiNotifierPlugin{}
}

func main() {
	panic("This should be built as a Go plugin.")
}
