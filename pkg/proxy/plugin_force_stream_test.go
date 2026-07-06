package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestForceStreamPlugin_ProcessRequest(t *testing.T) {
	plugin := &ForceStreamPlugin{}

	tests := []struct {
		name     string
		payload  map[string]interface{}
		modified bool
	}{
		{
			name: "Force stream when stream missing",
			payload: map[string]interface{}{
				"model": "gpt-4",
			},
			modified: true,
		},
		{
			name: "No change when stream is explicitly false",
			payload: map[string]interface{}{
				"model":  "gpt-4",
				"stream": false,
			},
			modified: false,
		},
		{
			name: "No change when stream is already true",
			payload: map[string]interface{}{
				"model":  "gpt-4",
				"stream": true,
			},
			modified: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes, _ := json.Marshal(tt.payload)
			req, _ := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(bodyBytes))

			err := plugin.ProcessRequest(req, true)
			if err != nil {
				t.Fatalf("ProcessRequest failed: %v", err)
			}

			newBodyBytes, _ := io.ReadAll(req.Body)
			var newPayload map[string]interface{}
			json.Unmarshal(newBodyBytes, &newPayload)

			if tt.modified {
				if newPayload["stream"] != true {
					t.Errorf("Expected stream: true, got %v", newPayload["stream"])
				}
				if req.Header.Get("Accept") != "text/event-stream" {
					t.Errorf("Expected Accept header to be set")
				}
			} else {
				// 应该保持不变
				if len(newBodyBytes) != len(bodyBytes) {
					t.Errorf("Payload should not have been modified")
				}
			}
		})
	}
}

func TestForceStreamPlugin_DiagnoseDoesNotModifyRequest(t *testing.T) {
	body := `{"model":"gpt-4","stream_options":{"include_usage":true}}`
	req, err := http.NewRequest(http.MethodPost, "https://api.example.com/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	plugin := &ForceStreamPlugin{Diagnose: true}
	if err := plugin.ProcessRequest(req, true); err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("diagnose mode changed body:\nwant=%s\n got=%s", body, string(got))
	}
	if req.Header.Get("Accept") != "" {
		t.Fatalf("diagnose mode changed Accept header: %q", req.Header.Get("Accept"))
	}
}
