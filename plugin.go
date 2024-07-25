package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
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

// EchoPlugin is the gotify plugin instance.
type MultiNotifierPlugin struct {
	msgHandler     plugin.MessageHandler
	storageHandler plugin.StorageHandler
	config         *Config
	basePath       string
	done           chan struct{}
}

func (p *MultiNotifierPlugin) TestSocket(serverUrl string) (err error) {
	_, _, err = websocket.DefaultDialer.Dial(serverUrl, nil)
	if err != nil {
		log.Println("Test dial error : ", err)
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
	log.Println("echo plugin enabled")
	serverUrl := p.config.HostServer + "/stream?token=" + p.config.ClientToken
	log.Println("Websocket url : ", serverUrl)
	go p.ReceiveMessages(serverUrl)
	return nil
}

// Disable disables the plugin.
func (p *MultiNotifierPlugin) Disable() error {
	log.Println("echo plugin disbled")
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

type WebHook struct {
	Url    string            `yaml:"url"`
	Method string            `yaml:"method"`
	Body   string            `yaml:"body"`
	Header map[string]string `yaml:"header"`
	Tags   []string          `yaml:"tags"`
}

// Config defines the plugin config scheme
type Config struct {
	ClientToken string     `yaml:"client_token" validate:"required"`
	HostServer  string     `yaml:"host_server" validate:"required"`
	Debug  		bool       `yaml:"debug" validate:"required"`
	WebHooks    []*WebHook `yaml:"web_hooks"`
}

// DefaultConfig implements plugin.Configurer
func (p *MultiNotifierPlugin) DefaultConfig() interface{} {
	c := &Config{
		ClientToken: "CrMo3UaAQG1H37G",
		HostServer:  "ws://localhost",
		Debug: false
	}
	return c
}

// ValidateAndSetConfig implements plugin.Configurer
func (p *MultiNotifierPlugin) ValidateAndSetConfig(config interface{}) error {
	p.config = config.(*Config)
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
		body: "{\"wxid\":\"xxxxxxxx\",\"msg\":\"$title\n$message\"}"
	  - url: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxxxxx"
		method: "POST"
		body: "{\"msgtype\":\"text\",\"text\":{\"content\":\"$title\n$message\"}}"

	注：请在更改后重新启用插件。
	`
	return message
}

func (p *MultiNotifierPlugin) SendMessage(msg plugin.Message, webhook *WebHook) (err error) {
    var msgTag = ""
    var matchTag = false
    if val, ok := msg.Extras["tag"]; ok {
        msgTag = val.(string)
    }
	if p.config.Debug {
		log.Printf("msgTag : %v", msgTag)
	}
    for _, tag := range webhook.Tags {
		if p.config.Debug {
			log.Printf("tag : %v", tag)
		}
        if msgTag != "" && msgTag == tag {
            matchTag = true
            break
        }
    }
    if !matchTag {
        log.Printf("tag dont match, skip")
        return nil
    }
    if webhook.Url == "" {
        return errors.New("webhook url is empty")
    }
    if webhook.Method == "" {
        webhook.Method = "POST"
    }
    if webhook.Header == nil {
        webhook.Header = map[string]string{
            "Content-Type": "application/json",
        }
    }
    if webhook.Body == "" {
        webhook.Body = "{\"msg\":\"$title\n$message\"}"
    }
    body := webhook.Body
    body = strings.Replace(body, "$title", msg.Title, -1)
    body = strings.Replace(body, "$message", msg.Message, -1)
    log.Printf("webhook body : %s", body)
    payload := strings.NewReader(body)
    req, err := http.NewRequest(webhook.Method, webhook.Url, payload)
    if err != nil {
        log.Printf("NewRequest error : %v ", err)
        return err
    }
    for k, v := range webhook.Header {
        req.Header.Add(k, v)
    }
    res, err := http.DefaultClient.Do(req)
    if err != nil {
        log.Printf("Do request error : %v ", err)
        return err
    }
    defer res.Body.Close()
    log.Printf("webhook response : %v ", res)
	return
}

func (p *MultiNotifierPlugin) ReceiveMessages(serverUrl string) {
	time.Sleep(1 * time.Second)

	err := p.receiveMessages(serverUrl)
	if err != nil {
		log.Println("read message error, retry after 1s")
	}
}

func (p *MultiNotifierPlugin) receiveMessages(serverUrl string) (err error) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	conn, _, err := websocket.DefaultDialer.Dial(serverUrl, nil)
	if err != nil {
		log.Println("Dial error : ", err)
		return err
	}
	log.Printf("Connected to %s", serverUrl)
	defer conn.Close()
	msg := plugin.Message{}
	go func() {
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Println("Websocket read message error :", err)
				return
			}
			if message[0] == '{' {
				if p.config.Debug {
					log.Println("Websocket read message :", message)
				}
				if err := json.Unmarshal(message, &msg); err != nil {
					log.Println("Json Unmarshal error :", err)
					continue
				}
				for _, webhook := range p.config.WebHooks {
                    err = p.SendMessage(msg, webhook)
                    if err != nil {
                        log.Printf("SendMessage error : %v ", err)
                    }
				}
			} else {
				log.Println("unsupported message format")
			}
		}
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.done:
			log.Println("plugin stopped")
			return
		case t := <-ticker.C:
			err := conn.WriteMessage(websocket.TextMessage, []byte(t.String()))
			if err != nil {
				log.Println("write:", err)
				return err
			}
			ticker.Reset(time.Second)
		case <-interrupt:
			log.Println("plugin interrupt")
			err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("write close:", err)
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
