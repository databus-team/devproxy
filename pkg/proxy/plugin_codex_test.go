package proxy

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCodexFixPlugin(t *testing.T) {
	plugin := &CodexFixPlugin{}

	t.Run("Ignore non-POST", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
		err := plugin.ProcessRequest(req, true)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
	})

	t.Run("Ignore non-JSON", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "http://example.com", nil)
		req.Header.Set("Content-Type", "text/plain")
		err := plugin.ProcessRequest(req, true)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
	})

	t.Run("Codex output_text type array", func(t *testing.T) {
		reqBody := []byte(`{
			"messages": [
				{
					"role": "assistant",
					"content": [
						{"type": "output_text", "text": "thought... "},
						{"type": "output_text", "text": "answer"}
					]
				}
			]
		}`)

		req, _ := http.NewRequest(http.MethodPost, "http://example.com", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")

		err := plugin.ProcessRequest(req, true)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		if req.GetBody == nil {
			t.Fatal("Expected GetBody to be set")
		}

		modifiedBody, _ := io.ReadAll(req.Body)
		sBody := strings.ReplaceAll(string(modifiedBody), " ", "")
		sBody = strings.ReplaceAll(sBody, "\n", "")

		if !strings.Contains(sBody, `"content":"thought...answer"`) {
			t.Errorf("Expected combined content, got: %s", string(modifiedBody))
		}
	})

	t.Run("Input field support", func(t *testing.T) {
		reqBody := []byte(`{
			"input": [
				{
					"role": "assistant",
					"content": [{"type": "text", "text": "I am thinking"}]
				}
			],
			"model": "test-model"
		}`)

		req, _ := http.NewRequest(http.MethodPost, "http://example.com", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")

		err := plugin.ProcessRequest(req, true)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		modifiedBody, _ := io.ReadAll(req.Body)
		if !bytes.Contains(modifiedBody, []byte(`"content":"I am thinking"`)) {
			t.Errorf("Input field content was not processed: %s", string(modifiedBody))
		}
	})

	t.Run("Nested input support", func(t *testing.T) {
		reqBody := []byte(`{
			"input": {
				"messages": [
					{
						"role": "assistant",
						"type": "internal",
						"content": [{"type": "output_text", "text": "result"}]
					}
				]
			}
		}`)

		req, _ := http.NewRequest(http.MethodPost, "http://example.com", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")

		err := plugin.ProcessRequest(req, true)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		modifiedBody, _ := io.ReadAll(req.Body)
		if !bytes.Contains(modifiedBody, []byte(`"content":"result"`)) {
			t.Errorf("Nested message content was not flattened: %s", string(modifiedBody))
		}
	})

	t.Run("Model replacement support", func(t *testing.T) {
		p := &CodexFixPlugin{TargetModel: "minimax-m21-ifind"}
		reqBody := []byte(`{"model":"gpt-5.1-codex-mini","messages":[{"role":"user","content":"hello"}]}`)
		req, _ := http.NewRequest(http.MethodPost, "http://example.com", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")

		err := p.ProcessRequest(req, true)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		modifiedBody, _ := io.ReadAll(req.Body)
		if !bytes.Contains(modifiedBody, []byte(`"model":"minimax-m21-ifind"`)) {
			t.Errorf("Model was not replaced: %s", string(modifiedBody))
		}
	})

	t.Run("Ignore non-assistant role content", func(t *testing.T) {
		reqBody := []byte(`{
			"messages": [
				{
					"role": "user",
					"content": [
						{"type": "text", "text": "hello"},
						{"type": "image_url", "image_url": {"url": "http://..."}}
					]
				}
			]
		}`)

		req, _ := http.NewRequest(http.MethodPost, "http://example.com", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")

		err := plugin.ProcessRequest(req, true)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		modifiedBody, _ := io.ReadAll(req.Body)
		sBody := strings.ReplaceAll(string(modifiedBody), " ", "")
		sBody = strings.ReplaceAll(sBody, "\n", "")
		sBody = strings.ReplaceAll(sBody, "\t", "")

		// 验证内容没有被修改为字符串，仍然保持数组形式
		if strings.Contains(sBody, `"content":"hello"`) {
			t.Errorf("User role content should not be flattened: %s", string(modifiedBody))
		}
		if !strings.Contains(sBody, `"type":"image_url"`) {
			t.Errorf("User role content should remain an array: %s", string(modifiedBody))
		}
	})
}

func TestCodexFixPlugin_DiagnoseDoesNotModifyRequest(t *testing.T) {
	plugin := &CodexFixPlugin{TargetModel: "deepseek-chat", Diagnose: true}
	reqBody := []byte(`{"model":"gpt-5","messages":[{"role":"assistant","content":[{"type":"output_text","text":"answer"}]}]}`)
	req, _ := http.NewRequest(http.MethodPost, "http://example.com/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	if err := plugin.ProcessRequest(req, true); err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(reqBody) {
		t.Fatalf("diagnose mode changed body:\nwant=%s\n got=%s", string(reqBody), string(got))
	}
}
