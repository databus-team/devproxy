package proxy

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/elazarl/goproxy"
)

func TestOpenAIToolCallsFix_ProcessRequestMarksChatCompletions(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://api.example.com/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}

	plugin := &OpenAIToolCallsFixPlugin{}
	if err := plugin.ProcessRequest(req, true); err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	if req.Header.Get("X-DevProxy-OpenAI-Tool-Calls-Fix") != "true" {
		t.Fatalf("request was not marked")
	}
	if req.Header.Get("Accept-Encoding") != "identity" {
		t.Fatalf("request should disable compression")
	}
}

func TestOpenAIToolCallsFix_StreamRepairsToolCallShape(t *testing.T) {
	input := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"type":"function","function":{"name":"search","arguments":{"q":"devproxy"}}}]}}]}`,
		``,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"arguments":"!"}}]}}]}`,
		``,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"?"}}]}}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	out := runOpenAIToolCallsFixResponse(t, input)

	for _, want := range []string{
		`"index":0`,
		`"arguments":"{\"q\":\"devproxy\"}"`,
		`"id":"call_1"`,
		`"type":"function"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("fixed stream missing %q:\n%s", want, out)
		}
	}
}

func TestOpenAIToolCallsFix_NonToolChunksPassThrough(t *testing.T) {
	input := "data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n" +
		"data: [DONE]\n\n"

	out := runOpenAIToolCallsFixResponse(t, input)

	if out != input {
		t.Fatalf("non-tool stream changed:\nwant=%q\n got=%q", input, out)
	}
}

func TestOpenAIToolCallsFix_IgnoresUnmarkedResponses(t *testing.T) {
	resp := newOpenAIToolCallsFixResponse("data: [DONE]\n\n")
	resp.Request.Header.Del("X-DevProxy-OpenAI-Tool-Calls-Fix")

	plugin := &OpenAIToolCallsFixPlugin{}
	if err := plugin.ProcessResponse(resp, &goproxy.ProxyCtx{}, true, false); err != nil {
		t.Fatalf("ProcessResponse: %v", err)
	}

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "data: [DONE]\n\n" {
		t.Fatalf("unmarked response changed: %q", string(out))
	}
}

func runOpenAIToolCallsFixResponse(t *testing.T, input string) string {
	t.Helper()
	resp := newOpenAIToolCallsFixResponse(input)
	plugin := &OpenAIToolCallsFixPlugin{}
	if err := plugin.ProcessResponse(resp, &goproxy.ProxyCtx{}, true, false); err != nil {
		t.Fatalf("ProcessResponse: %v", err)
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(out)
}

func newOpenAIToolCallsFixResponse(body string) *http.Response {
	req, _ := http.NewRequest(http.MethodPost, "https://api.example.com/v1/chat/completions", nil)
	req.Header.Set("X-DevProxy-OpenAI-Tool-Calls-Fix", "true")
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
		},
		Body:    io.NopCloser(strings.NewReader(body)),
		Request: req,
	}
}
