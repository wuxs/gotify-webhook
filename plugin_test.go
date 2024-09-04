package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockStorageHandler is a mock for the StorageHandler interface
type MockStorageHandler struct {
	mock.Mock
}

func (m *MockStorageHandler) GetString(key string) (string, error) {
	args := m.Called(key)
	return args.String(0), args.Error(1)
}

func (m *MockStorageHandler) GetInt(key string) (int, error) {
	args := m.Called(key)
	return args.Int(0), args.Error(1)
}

func (m *MockStorageHandler) GetBool(key string) (bool, error) {
	args := m.Called(key)
	return args.Bool(0), args.Error(1)
}

func (m *MockStorageHandler) SetString(key string, value string) error {
	args := m.Called(key, value)
	return args.Error(0)
}

func (m *MockStorageHandler) SetInt(key string, value int) error {
	args := m.Called(key, value)
	return args.Error(0)
}

func (m *MockStorageHandler) SetBool(key string, value bool) error {
	args := m.Called(key, value)
	return args.Error(0)
}

func TestMultiNotifierPlugin_ValidateAndSetConfig(t *testing.T) {
	tests := []struct {
		name        string
		config      *Config
		expectError bool
		expectHooks int
	}{
		{
			name: "Valid configuration",
			config: &Config{
				ClientToken: "test-token",
				HostServer:  "ws://localhost:8080",
				WebHooks: []*WebHook{
					{
						Url:    "http://example.com",
						Method: "POST",
						Body:   "{\"message\": \"{{.message}}\"}",
					},
				},
			},
			expectError: false,
			expectHooks: 1,
		},
		{
			name: "Empty webhook URL",
			config: &Config{
				ClientToken: "test-token",
				HostServer:  "ws://localhost:8080",
				WebHooks: []*WebHook{
					{
						Method: "POST",
						Body:   "{\"message\": \"{{.message}}\"}",
					},
				},
			},
			expectError: true,
			expectHooks: 0,
		},
		{
			name: "Invalid webhook URL",
			config: &Config{
				ClientToken: "test-token",
				HostServer:  "ws://localhost:8080",
				WebHooks: []*WebHook{
					{
						Url:    "example.com",
						Method: "POST",
						Body:   "{\"message\": \"{{.message}}\"}",
					},
				},
			},
			expectError: true,
			expectHooks: 0,
		},
		{
			name: "Empty webhook body be allowed",
			config: &Config{
				ClientToken: "test-token",
				HostServer:  "ws://localhost:8080",
				WebHooks: []*WebHook{
					{
						Url:    "http://example.com",
						Method: "POST",
					},
				},
			},
			expectError: false,
			expectHooks: 1,
		},
		{
			name: "Default method and content type",
			config: &Config{
				ClientToken: "test-token",
				HostServer:  "ws://localhost:8080",
				WebHooks: []*WebHook{
					{
						Url:  "http://example.com",
						Body: "{\"message\": \"{{.message}}\"}",
					},
				},
			},
			expectError: false,
			expectHooks: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := &MultiNotifierPlugin{}
			err := plugin.ValidateAndSetConfig(tt.config)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectHooks, len(plugin.config.WebHooks), "Unexpected number of webhooks")

				if len(plugin.config.WebHooks) > 0 {
					assert.Equal(t, "POST", plugin.config.WebHooks[0].Method, "Default method should be POST")
					assert.Equal(t, "text/plain", plugin.config.WebHooks[0].Header["Content-Type"], "Default Content-Type should be text/plain")
				}
			}
		})
	}
}

func TestReceiveMessages(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	// Create a mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		// Send a test message
		msg := MessageExternal{
			ID:            1,
			ApplicationID: 1,
			Message:       "Test message",
			Title:         "Test title",
			Priority:      1,
			Date:          time.Now(),
		}
		msgJSON, _ := json.Marshal(msg)
		c.WriteMessage(websocket.TextMessage, msgJSON)

		time.Sleep(100 * time.Millisecond)

		c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}))
	defer server.Close()

	// Set up httpmock responder
	httpmock.RegisterResponder("POST", "http://example.com", httpmock.NewStringResponder(200, "OK"))

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Create a plugin instance
	plugin := &MultiNotifierPlugin{
		config: &Config{
			ClientToken: "test-token",
			HostServer:  wsURL,
			WebHooks: []*WebHook{
				{
					Url:    "http://example.com",
					Method: "POST",
					Body:   "{{.title}} - {{.message}}",
				},
			},
		},
	}

	// Run the receiveMessages method
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := plugin.receiveMessages(ctx, wsURL)

	assert.Error(t, err, "Expected an error from receiveMessages")
	assert.Equal(t, "read message error: websocket: close 1000 (normal)", err.Error(),
		"Unexpected error message: %v", err)

	// Check if the HTTP request was made
	callCount := httpmock.GetTotalCallCount()
	assert.Equal(t, 1, callCount, "Expected 1 HTTP call, got %d", callCount)

	// Check the details of the HTTP request
	calls := httpmock.GetCallCountInfo()
	assert.Equal(t, 1, calls["POST http://example.com"], "Expected 1 POST call to http://example.com")
}

func TestMultiNotifierPlugin_SendMessage(t *testing.T) {
	plugin := &MultiNotifierPlugin{
		config: &Config{
			WebHooks: []*WebHook{
				{
					Url:    "http://example.com",
					Method: "POST",
					Body:   "{\"message\": \"{{.message}}\"}",
					Apps:   []uint{1, 2},
				},
			},
		},
	}

	// Create a test server to handle the webhook request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "Test message", body["message"])

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Update the webhook URL to use the test server
	plugin.config.WebHooks[0].Url = server.URL

	msg := &MessageExternal{
		ID:            1,
		ApplicationID: 1,
		Message:       "Test message",
		Title:         "Test title",
		Priority:      1,
		Date:          time.Now(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should pass
	errs := plugin.sendMessage(ctx, msg, plugin.config.WebHooks)
	assert.Empty(t, errs)

	// Test with a non-matching app ID
	msg.ApplicationID = 3
	errs = plugin.sendMessage(ctx, msg, plugin.config.WebHooks)
	assert.Empty(t, errs)

	// Should fail when context is canceled
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel() // Immediately cancel the context
	errs = plugin.sendMessage(canceledCtx, msg, plugin.config.WebHooks)
	assert.NotEmpty(t, errs)
}

func TestMultiNotifierPlugin_Enable(t *testing.T) {
	plugin := &MultiNotifierPlugin{
		config: &Config{
			ClientToken: "test-token",
			HostServer:  "ws://localhost:8080",
		},
	}

	err := plugin.Enable()
	assert.NoError(t, err)
	assert.NotNil(t, plugin.cancel)

	// Clean up
	plugin.Disable()
}

func TestMultiNotifierPlugin_Disable(t *testing.T) {
	plugin := &MultiNotifierPlugin{}

	ctx, cancel := context.WithCancel(context.Background())
	plugin.cancel = cancel

	err := plugin.Disable()
	assert.NoError(t, err)

	assert.Error(t, ctx.Err(), "Context should have been cancelled")

	err = plugin.Disable()
	assert.NoError(t, err, "Calling Disable multiple times should not produce an error")
}

func TestMultiNotifierPlugin_DefaultConfig(t *testing.T) {
	plugin := &MultiNotifierPlugin{}
	config := plugin.DefaultConfig()

	assert.IsType(t, &Config{}, config)
	defaultConfig := config.(*Config)
	assert.Equal(t, "CrMo3UaAQG1H37G", defaultConfig.ClientToken)
	assert.Equal(t, "ws://localhost", defaultConfig.HostServer)
}

func TestProcessTemplateString(t *testing.T) {
	testCases := []struct {
		name     string
		template string
		msg      *MessageExternal
		expected string
	}{
		{
			name:     "Simple text template",
			template: "Title: {{.title}}, Message: {{.message}}",
			msg: &MessageExternal{
				Title:   "Test Title",
				Message: "Test Message",
			},
			expected: "Title: Test Title, Message: Test Message",
		},
		{
			name:     "Template with JSON-like content",
			template: "{\"title\": \"{{.title}}\", \"message\": \"{{.message}}\"}",
			msg: &MessageExternal{
				Title:   "JSON Title",
				Message: "JSON Message",
			},
			expected: "{\"title\": \"JSON Title\", \"message\": \"JSON Message\"}",
		},
		{
			name:     "Template with actual JSON message",
			template: "Title: {{.title}}, Content: {{.message}}",
			msg: &MessageExternal{
				Title:   "JSON Content",
				Message: "{\"key1\": \"value1\", \"key2\": \"value2\"}",
			},
			expected: "Title: JSON Content, Content: {\"key1\": \"value1\", \"key2\": \"value2\"}",
		},
		// {{.message}} without being quoted will be processed as a plain text template
		{
			name:     "JSON template with raw JSON message",
			template: "{\"title\": \"{{.title}}\", \"content\": {{.message}}}",
			msg: &MessageExternal{
				Title:   "Raw JSON",
				Message: "{\"key1\": \"value1\", \"key2\": \"value2\"}",
			},
			expected: "{\"title\": \"Raw JSON\", \"content\": {\"key1\": \"value1\", \"key2\": \"value2\"}}",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := processTemplateString(tc.template, tc.msg)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestProcessJSONRecursive(t *testing.T) {
	testCases := []struct {
		name     string
		template string
		msg      *MessageExternal
		expected string
	}{
		{
			name:     "Simple JSON template",
			template: "{\"title\": \"{{.title}}\", \"message\": \"{{.message}}\"}",
			msg: &MessageExternal{
				Title:   "Test Title",
				Message: "Test Message",
			},
			expected: "{\"message\":\"Test Message\",\"title\":\"Test Title\"}",
		},
		{
			name:     "JSON template with escaped message",
			template: "{\"title\": \"{{.title}}\", \"content\": \"{{.message}}\"}",
			msg: &MessageExternal{
				Title:   "JSON Content",
				Message: "{\"key1\": \"value1\", \"key2\": \"value2\"}",
			},
			expected: "{\"content\":\"{\\\"key1\\\": \\\"value1\\\", \\\"key2\\\": \\\"value2\\\"}\",\"title\":\"JSON Content\"}",
		},
		{
			name:     "JSON template with escaped message in array",
			template: "{\"title\": \"{{.title}}\", \"content\": [\"{{.message}}\"]}",
			msg: &MessageExternal{
				Title:   "JSON Content",
				Message: "{\"key1\": \"value1\", \"key2\": \"value2\"}",
			},
			expected: "{\"content\":[\"{\\\"key1\\\": \\\"value1\\\", \\\"key2\\\": \\\"value2\\\"}\"],\"title\":\"JSON Content\"}",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var jsonBody map[string]interface{}
			isJSON := json.Unmarshal([]byte(tc.template), &jsonBody) == nil
			assert.True(t, isJSON, "Template should be JSON formated.")
			err := processJSONRecursive(jsonBody, tc.msg)
			assert.NoError(t, err)
			newBody, err := json.Marshal(jsonBody)
			assert.Equal(t, tc.expected, string(newBody))
		})
	}
}
