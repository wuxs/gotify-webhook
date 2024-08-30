package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
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
	done           chan struct{}
}

func (p *MultiNotifierPlugin) TestSocket(serverUrl string) (err error) {
	_, _, err = websocket.DefaultDialer.Dial(serverUrl, nil)
	if err != nil {
		slog.Error("Test dial error :", slog.Any("error", err), slog.Any("url", serverUrl))
		return err
	}
	return nil
}

// Enable enables the plugin.
func (p *MultiNotifierPlugin) Enable() error {
	if len(p.config.HostServer) < 1 {
		return errors.New("please enter the correct web server")
	}
	p.done = make(chan struct{})
	slog.Info("webhook plugin enabled", slog.Any("config", GetGotifyPluginInfo()))
	serverUrl := p.config.HostServer + "/stream?token=" + p.config.ClientToken
	slog.Info("Websocket url :" + serverUrl)
	go p.ReceiveMessages(serverUrl)
	return nil
}

// Disable disables the plugin.
func (p *MultiNotifierPlugin) Disable() error {
	slog.Info("webhook plugin disbled", slog.Any("config", GetGotifyPluginInfo()))
	close(p.done)
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

type Body struct {
	Text  string `json:"text"`
	Image string `json:"image"`
	File  string `json:"file"`
}

type WebHook struct {
	Url    string            `yaml:"url"`
	Method string            `yaml:"method"`
	Body   *Body             `yaml:"body"`
	Header map[string]string `yaml:"header"`
	Tags   []string          `yaml:"tags"`
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
	for i, webhook := range p.config.WebHooks {
		if webhook.Method == "" {
			webhook.Method = "POST"
		}
		if webhook.Url == "" {
			slog.Warn("webhook url is empty", slog.Any("webhook", webhook))
			p.config.WebHooks = append(p.config.WebHooks[:i], p.config.WebHooks[i+1:]...)
		}
		if webhook.Body == nil {
			slog.Warn("webhook body is invalid", slog.Any("webhook", webhook))
			p.config.WebHooks = append(p.config.WebHooks[:i], p.config.WebHooks[i+1:]...)
		}
	}
	return nil
}

// GetDisplay implements plugin.Displayer.
func (p *MultiNotifierPlugin) GetDisplay(location *url.URL) string {
	message := `
	如何填写配置：

	1. 创建一个新的 Client，获取 token，更新配置中的 client_token
	2. 修改 gotify 服务器地址，默认为 ws://localhost
	3. 填写需要接受通知的 webhook 配置

	webhook 示例:
	web_hooks: 
	  - url: http://192.168.1.2:10201/api/sendTextMsg	
		method: POST
		body: 
		  text: "{\"wxid\":\"xxxxxxxx\",\"msg\":\"$title\n$message\"}"
	  - url: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxxxxx"
		method: "POST"
		body: 
		  text: "{\"msgtype\":\"text\",\"text\":{\"content\":\"$title\n$message\"}}"
		  image: "$message"

	注：请在更改后重新启用插件。
	`
	return message
}

func (p *MultiNotifierPlugin) SendMessage(msg *MessageExternal, webhooks []*WebHook) (err error) {
	var msgTag = ""
	var msgType = ""
	if val, ok := msg.Extras["tag"]; ok {
		msgTag = val.(string)
	}
	if val, ok := msg.Extras["type"]; ok {
		msgType = val.(string)
	}
	for _, webhook := range webhooks {
		if len(webhook.Tags) > 0 {
			if msgTag != "" && !slices.Contains(webhook.Tags, msgTag) {
				slog.Warn("webhook dont match tag, skip", slog.Any("msgTag", msgTag), slog.Any("webhookTag", webhook.Tags))
				continue
			}
		} else if msgTag != "" {
			slog.Warn("msg tag dont match , skip", slog.Any("msgTag", msgTag), slog.Any("webhookTag", webhook.Tags))
			continue
		}
		body := webhook.Body.Text
		switch msgType {
		case "text":
			if webhook.Body.Text != "" {
				body = webhook.Body.Text
			}
		case "image":
			if webhook.Body.Image != "" {
				body = webhook.Body.Image
			}
		case "file":
			if webhook.Body.File != "" {
				body = webhook.Body.File
			}
		default:
			slog.Warn("msg type dont match , skip", slog.Any("msgType", msgType), slog.Any("webhookType", webhook.Tags))
		}
		if body == "" {
			slog.Error("webhook body is empty, skip", slog.Any("webhook", webhook))
			continue
		}

		body = strings.Replace(body, "$title", msg.Title, -1)
		body = strings.Replace(body, "$message", msg.Message, -1)
		slog.Info("webhook body ", slog.Any("body", body))
		payload := strings.NewReader(body)
		req, err := http.NewRequest(webhook.Method, webhook.Url, payload)
		if err != nil {
			slog.Error("NewRequest error", slog.Any("webhook", webhook), slog.Any("err", err))
			continue
		}
		for k, v := range webhook.Header {
			req.Header.Add(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			slog.Error("Do request error", slog.Any("webhook", webhook), slog.Any("err", err))
			continue
		}
		slog.Info("webhook response", slog.Any("webhook", webhook), slog.Any("res", res), slog.Any("msg", msg))
	}

	return nil
}

func (p *MultiNotifierPlugin) ReceiveMessages(serverUrl string) {
	time.Sleep(1 * time.Second)

	err := p.receiveMessages(serverUrl)
	if err != nil {
		slog.Error("read message error, retry after 1s", slog.Any("err", err))
	}
}

func (p *MultiNotifierPlugin) receiveMessages(serverUrl string) (err error) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	conn, _, err := websocket.DefaultDialer.Dial(serverUrl, nil)
	if err != nil {
		slog.Error("Dial error", slog.Any("err", err), slog.Any("serverUrl", serverUrl))
		return err
	}
	slog.Info("Connected to " + serverUrl)
	defer conn.Close()
	go func() {
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				slog.Error("Websocket read message error", slog.Any("err", err))
				return
			}
			if message[0] == '{' {
				msg := &MessageExternal{}
				if err := json.Unmarshal(message, msg); err != nil {
					slog.Error("Json Unmarshal error", slog.Any("err", err))
					continue
				}
				err = p.SendMessage(msg, p.config.WebHooks)
				if err != nil {
					slog.Error("SendMessage error", slog.Any("err", err))
				}
			} else {
				slog.Warn("unsupported message format", slog.Any("message", string(message)))
			}
		}
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.done:
			slog.Info("plugin stopped")
			return
		case t := <-ticker.C:
			err := conn.WriteMessage(websocket.TextMessage, []byte(t.String()))
			if err != nil {
				slog.Error("write text message error", slog.Any("err", err))
				return err
			}
			ticker.Reset(time.Second)
		case <-interrupt:
			slog.Info("plugin interrupt")
			err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				slog.Error("write close message error", slog.Any("err", err))
				return err
			}
			_ = conn.Close()
			return err
		}
	}
}

// NewGotifyPluginInstance creates a plugin instance for a user context.
func NewGotifyPluginInstance(ctx plugin.UserContext) plugin.Plugin {
	return &MultiNotifierPlugin{}
}

func main() {
	panic("this should be built as go plugin")
}
