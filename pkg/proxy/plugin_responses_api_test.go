package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesAPIPlugin_ProcessRequest(t *testing.T) {
	plugin := &ResponsesAPIPlugin{}

	reqBody := ResponsesAPIRequest{
		Model: "gpt-4o",
		Input: []interface{}{
			map[string]interface{}{"role": "user", "content": "Hello"},
		},
		MaxOutputTokens: 100,
		Stream:          true,
		ResponseFormat: &ResponseFmt{
			Type: "json_schema",
			JSONSchema: map[string]interface{}{
				"name": "foo",
			},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(bodyBytes))

	if err := plugin.ProcessRequest(req); err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}

	if req.URL.Path != "/v1/chat/completions" {
		t.Errorf("Path not rewritten, got %s", req.URL.Path)
	}

	if req.Header.Get("X-DevProxy-Responses-API") != "true" {
		t.Errorf("Header X-DevProxy-Responses-API not set")
	}

	// Check body
	newBodyBytes, _ := io.ReadAll(req.Body)
	var chatReq ChatCompletionRequest
	if err := json.Unmarshal(newBodyBytes, &chatReq); err != nil {
		t.Fatalf("Failed to unmarshal chat request: %v", err)
	}

	if chatReq.Model != "gpt-4o" {
		t.Errorf("Model mismatch: %s", chatReq.Model)
	}
	if chatReq.MaxTokens != 100 {
		t.Errorf("MaxTokens mismatch: %d", chatReq.MaxTokens)
	}
	if !chatReq.Stream {
		t.Errorf("Stream mismatch")
	}
	if chatReq.ResponseFormat == nil || chatReq.ResponseFormat.Type != "json_schema" {
		t.Errorf("ResponseFormat mismatch")
	}
}

func TestResponsesAPIPlugin_ProcessResponse_JSON(t *testing.T) {
	plugin := &ResponsesAPIPlugin{}

	// Mock request with marker header
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-DevProxy-Responses-API", "true")

	// Construct response body using a map to avoid anonymous struct issues
	chatRespMap := map[string]interface{}{
		"id":      "chatcmpl-123",
		"object":  "chat.completion",
		"created": 1234567890,
		"model":   "gpt-4o",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "Hi there",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{
			"total_tokens": 10,
		},
	}
	respBody, _ := json.Marshal(chatRespMap)

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "application/json")

	if err := plugin.ProcessResponse(resp, nil, true); err != nil {
		t.Fatalf("ProcessResponse failed: %v", err)
	}

	// Check response body
	newBytes, _ := io.ReadAll(resp.Body)
	var resResp ResponsesAPIResponse
	if err := json.Unmarshal(newBytes, &resResp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if resResp.ID != "resp_123" {
		t.Errorf("ID mismatch: %s", resResp.ID)
	}
	if resResp.Object != "response" {
		t.Errorf("Object mismatch: %s", resResp.Object)
	}
	if len(resResp.Output) != 1 {
		t.Fatalf("Expected 1 output, got %d", len(resResp.Output))
	}
	if resResp.Output[0].Content[0].Text != "Hi there" {
		t.Errorf("Content mismatch: %s", resResp.Output[0].Content[0].Text)
	}
}

func TestResponsesAPIPlugin_ProcessResponse_Ignored(t *testing.T) {
	plugin := &ResponsesAPIPlugin{}

	// Request to a DIFFERENT endpoint (not chat/completions, not responses)
	req := httptest.NewRequest("POST", "/v1/completions", nil)

	chatRespMap := map[string]interface{}{
		"id": "cmpl-123",
	}
	respBody, _ := json.Marshal(chatRespMap)

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "application/json")

	if err := plugin.ProcessResponse(resp, nil, true); err != nil {
		t.Fatalf("ProcessResponse failed: %v", err)
	}

	// Body should be unchanged
	newBytes, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(newBytes, []byte("cmpl-123")) {
		t.Errorf("Response should not have been transformed")
	}
}

func TestResponsesAPIPlugin_ProcessResponse_Stream(t *testing.T) {
	plugin := &ResponsesAPIPlugin{}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-DevProxy-Responses-API", "true")

	// Mock Stream Data
	chunks := []string{
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677649420,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677649420,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`data: [DONE]`,
	}
	bodyStr := strings.Join(chunks, "\n\n") + "\n\n"

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(bodyStr)),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "text/event-stream")

	if err := plugin.ProcessResponse(resp, nil, true); err != nil {
		t.Fatalf("ProcessResponse failed: %v", err)
	}

	// Read stream
	reader := io.Reader(resp.Body)
	buf := new(bytes.Buffer)
	buf.ReadFrom(reader)
	output := buf.String()

	if !strings.Contains(output, "event: response.created") {
		t.Errorf("Missing response.created event")
	}
	if !strings.Contains(output, "event: response.output_text.delta") {
		t.Errorf("Missing response.output_text.delta event")
	}
	// Since we mock [DONE] but maybe not enough chunks to trigger completion if we implemented full logic?
	// Our `handleStream` implementation sends completion events when it sees `finish_reason` OR when `[DONE]` (if not sent yet).
	// In the mock chunks above, `finish_reason` is null. So it should trigger on `[DONE]`.
	if !strings.Contains(output, "event: response.completed") {
		t.Errorf("Missing response.completed event")
	}
}

func TestResponsesAPIPlugin_ProcessResponse_JSON_WithThink(t *testing.T) {
	plugin := &ResponsesAPIPlugin{KeepReasoning: true}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-DevProxy-Responses-API", "true")

	chatRespMap := map[string]interface{}{
		"id":      "chatcmpl-123",
		"object":  "chat.completion",
		"created": 1234567890,
		"model":   "deepseek-r1",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "<think>This is reasoning.</think>This is answer.",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{
			"total_tokens": 10,
		},
	}
	respBody, _ := json.Marshal(chatRespMap)

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "application/json")

	if err := plugin.ProcessResponse(resp, nil, true); err != nil {
		t.Fatalf("ProcessResponse failed: %v", err)
	}

	newBytes, _ := io.ReadAll(resp.Body)
	var resResp ResponsesAPIResponse
	if err := json.Unmarshal(newBytes, &resResp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(resResp.Output) != 1 {
		t.Fatalf("Expected 1 output, got %d", len(resResp.Output))
	}

	content := resResp.Output[0].Content
	if len(content) != 2 {
		t.Fatalf("Expected 2 content parts, got %d", len(content))
	}

	if content[0].Type != "reasoning" || content[0].Text != "This is reasoning." {
		t.Errorf("First part mismatch: type=%s, text=%s", content[0].Type, content[0].Text)
	}

	if content[1].Type != "output_text" || content[1].Text != "This is answer." {
		t.Errorf("Second part mismatch: type=%s, text=%s", content[1].Type, content[1].Text)
	}
}

func TestResponsesAPIPlugin_ProcessResponse_Stream_WithThink(t *testing.T) {
	plugin := &ResponsesAPIPlugin{KeepReasoning: true}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-DevProxy-Responses-API", "true")

	// Split "<think>reason</think>answer" across chunk boundaries
	chunks := []string{
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677649420,"model":"deepseek-r1","choices":[{"index":0,"delta":{"content":"<th"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677649420,"model":"deepseek-r1","choices":[{"index":0,"delta":{"content":"ink>reason</th"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677649420,"model":"deepseek-r1","choices":[{"index":0,"delta":{"content":"ink>ans"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677649420,"model":"deepseek-r1","choices":[{"index":0,"delta":{"content":"wer"},"finish_reason":null}]}`,
		`data: [DONE]`,
	}
	bodyStr := strings.Join(chunks, "\n\n") + "\n\n"

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(bodyStr)),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "text/event-stream")

	if err := plugin.ProcessResponse(resp, nil, true); err != nil {
		t.Fatalf("ProcessResponse failed: %v", err)
	}

	reader := io.Reader(resp.Body)
	buf := new(bytes.Buffer)
	buf.ReadFrom(reader)
	output := buf.String()

	// Verify events
	if !strings.Contains(output, "event: response.content_part.added") {
		t.Errorf("Missing response.content_part.added event")
	}
	if !strings.Contains(output, "event: response.reasoning.delta") {
		t.Errorf("Missing response.reasoning.delta event")
	}
	if !strings.Contains(output, "event: response.reasoning.done") {
		t.Errorf("Missing response.reasoning.done event")
	}
	if !strings.Contains(output, "event: response.output_text.delta") {
		t.Errorf("Missing response.output_text.delta event")
	}
	if !strings.Contains(output, "event: response.output_text.done") {
		t.Errorf("Missing response.output_text.done event")
	}

	// Verify the parsed text matches
	if !strings.Contains(output, `"delta":"reason"`) {
		t.Errorf("Missing or incorrect reasoning delta")
	}
	if !strings.Contains(output, `"delta":"ans"`) || !strings.Contains(output, `"delta":"wer"`) {
		t.Errorf("Missing or incorrect output delta")
	}
}

func TestResponsesAPIPlugin_ProcessRequest_StripsThink(t *testing.T) {
	plugin := &ResponsesAPIPlugin{}

	reqBody := ResponsesAPIRequest{
		Model: "gpt-4o",
		Input: []interface{}{
			map[string]interface{}{
				"role":    "assistant",
				"content": "Before think.<think>This is reasoning process.</think>After think.",
				"type":    "message",
			},
		},
		Stream: true,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(bodyBytes))

	if err := plugin.ProcessRequest(req); err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}

	newBodyBytes, _ := io.ReadAll(req.Body)
	var chatReq ChatCompletionRequest
	if err := json.Unmarshal(newBodyBytes, &chatReq); err != nil {
		t.Fatalf("Failed to unmarshal chat request: %v", err)
	}

	if len(chatReq.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(chatReq.Messages))
	}

	msg := chatReq.Messages[0].(map[string]interface{})
	content := msg["content"].(string)

	expected := "Before think.After think."
	if content != expected {
		t.Errorf("Expected stripped content to be %q, got %q", expected, content)
	}
}

func TestResponsesAPIPlugin_ProcessResponse_JSON_StripThink(t *testing.T) {
	// 默认 KeepReasoning 为 false
	plugin := &ResponsesAPIPlugin{KeepReasoning: false}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-DevProxy-Responses-API", "true")

	chatRespMap := map[string]interface{}{
		"id":      "chatcmpl-123",
		"object":  "chat.completion",
		"created": 1234567890,
		"model":   "deepseek-r1",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "<think>This is reasoning.</think>This is answer.",
				},
				"finish_reason": "stop",
			},
		},
	}
	respBody, _ := json.Marshal(chatRespMap)

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "application/json")

	if err := plugin.ProcessResponse(resp, nil, true); err != nil {
		t.Fatalf("ProcessResponse failed: %v", err)
	}

	newBytes, _ := io.ReadAll(resp.Body)
	var resResp ResponsesAPIResponse
	if err := json.Unmarshal(newBytes, &resResp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(resResp.Output) != 1 {
		t.Fatalf("Expected 1 output, got %d", len(resResp.Output))
	}

	content := resResp.Output[0].Content
	// 当 KeepReasoning 为 false 时，应该只有 1 个 content part
	if len(content) != 1 {
		t.Fatalf("Expected 1 content part (reasoning should be stripped), got %d", len(content))
	}

	if content[0].Type != "output_text" || content[0].Text != "This is answer." {
		t.Errorf("Content mismatch: type=%s, text=%s", content[0].Type, content[0].Text)
	}
}

func TestResponsesAPIPlugin_ProcessResponse_Stream_StripThink(t *testing.T) {
	// 默认 KeepReasoning 为 false
	plugin := &ResponsesAPIPlugin{KeepReasoning: false}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-DevProxy-Responses-API", "true")

	// Split "<think>reason</think>answer" across chunk boundaries
	chunks := []string{
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677649420,"model":"deepseek-r1","choices":[{"index":0,"delta":{"content":"<th"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677649420,"model":"deepseek-r1","choices":[{"index":0,"delta":{"content":"ink>reason</th"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677649420,"model":"deepseek-r1","choices":[{"index":0,"delta":{"content":"ink>ans"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677649420,"model":"deepseek-r1","choices":[{"index":0,"delta":{"content":"wer"},"finish_reason":null}]}`,
		`data: [DONE]`,
	}
	bodyStr := strings.Join(chunks, "\n\n") + "\n\n"

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(bodyStr)),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "text/event-stream")

	if err := plugin.ProcessResponse(resp, nil, true); err != nil {
		t.Fatalf("ProcessResponse failed: %v", err)
	}

	reader := io.Reader(resp.Body)
	buf := new(bytes.Buffer)
	buf.ReadFrom(reader)
	output := buf.String()

	// Verify events
	if !strings.Contains(output, "event: response.content_part.added") {
		t.Errorf("Missing response.content_part.added event")
	}
	// 不应该包含 reasoning 事件
	if strings.Contains(output, "event: response.reasoning.delta") {
		t.Errorf("Should not contain response.reasoning.delta event")
	}
	if strings.Contains(output, "event: response.reasoning.done") {
		t.Errorf("Should not contain response.reasoning.done event")
	}
	if !strings.Contains(output, "event: response.output_text.delta") {
		t.Errorf("Missing response.output_text.delta event")
	}
	if !strings.Contains(output, "event: response.output_text.done") {
		t.Errorf("Missing response.output_text.done event")
	}

	// Verify the reasoning delta was indeed stripped
	if strings.Contains(output, `"delta":"reason"`) {
		t.Errorf("Reasoning delta should have been stripped")
	}
	// Verify output_text is still intact
	if !strings.Contains(output, `"delta":"ans"`) || !strings.Contains(output, `"delta":"wer"`) {
		t.Errorf("Missing or incorrect output delta")
	}
}

func TestResponsesAPIPlugin_ProcessResponse_JSON_XMLToolCall(t *testing.T) {
	plugin := &ResponsesAPIPlugin{KeepReasoning: false}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-DevProxy-Responses-API", "true")

	contentWithTool := "• 最后验证没有残留引用：\n" +
		"<invoke name=\"rg\">\n" +
		"  <parameter name=\"args\">decision_tracker|DECISION_LOG_KEY</parameter>\n" +
		"  <parameter name=\"workdir\">/Users/chentt/Projects/databus-pilot-aegra</parameter>\n" +
		"</invoke>\n" +
		"</minimax:tool_call>\n"

	chatRespMap := map[string]interface{}{
		"id":      "chatcmpl-123",
		"object":  "chat.completion",
		"created": 1234567890,
		"model":   "deepseek-r1",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": contentWithTool,
				},
				"finish_reason": "stop",
			},
		},
	}
	respBody, _ := json.Marshal(chatRespMap)

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "application/json")

	if err := plugin.ProcessResponse(resp, nil, true); err != nil {
		t.Fatalf("ProcessResponse failed: %v", err)
	}

	newBytes, _ := io.ReadAll(resp.Body)
	var resResp ResponsesAPIResponse
	if err := json.Unmarshal(newBytes, &resResp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// 最终的 Output 应该有 2 项：1 个 message, 1 个 function_call
	if len(resResp.Output) != 2 {
		t.Fatalf("Expected 2 outputs, got %d", len(resResp.Output))
	}

	// 验证 message item 只有洗干净后的正文
	msgItem := resResp.Output[0]
	if msgItem.Type != "message" {
		t.Errorf("First output type mismatch: %s", msgItem.Type)
	}
	if len(msgItem.Content) != 1 || msgItem.Content[0].Text != "• 最后验证没有残留引用：" {
		t.Errorf("First output content mismatch: %+v", msgItem.Content)
	}

	// 验证 function_call item
	toolItem := resResp.Output[1]
	if toolItem.Type != "function_call" {
		t.Errorf("Second output type mismatch: %s", toolItem.Type)
	}
	if toolItem.Name != "rg" {
		t.Errorf("Tool name mismatch: %s", toolItem.Name)
	}
	
	var args map[string]string
	if err := json.Unmarshal([]byte(toolItem.Arguments), &args); err != nil {
		t.Fatalf("Failed to parse tool arguments: %v", err)
	}
	if args["args"] != "decision_tracker|DECISION_LOG_KEY" || args["workdir"] != "/Users/chentt/Projects/databus-pilot-aegra" {
		t.Errorf("Tool arguments mismatch: %+v", args)
	}
}

func TestResponsesAPIPlugin_ProcessResponse_Stream_XMLToolCall(t *testing.T) {
	plugin := &ResponsesAPIPlugin{KeepReasoning: false}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-DevProxy-Responses-API", "true")

	// 模拟流式吐出包含 XML 工具调用的 delta
	chunks := []string{
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677649420,"model":"deepseek-r1","choices":[{"index":0,"delta":{"content":"验证：\n<inv"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677649420,"model":"deepseek-r1","choices":[{"index":0,"delta":{"content":"oke name=\"rg\">\n  <parameter name=\"args\">de"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677649420,"model":"deepseek-r1","choices":[{"index":0,"delta":{"content":"cision_tracker|DECISION_LOG_KEY</parameter>\n"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677649420,"model":"deepseek-r1","choices":[{"index":0,"delta":{"content":"  <parameter name=\"workdir\">/Users/chentt</parameter>\n</invoke>\n</minimax:tool_call>\n结束"},"finish_reason":null}]}`,
		`data: [DONE]`,
	}
	bodyStr := strings.Join(chunks, "\n\n") + "\n\n"

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(bodyStr)),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "text/event-stream")

	if err := plugin.ProcessResponse(resp, nil, true); err != nil {
		t.Fatalf("ProcessResponse failed: %v", err)
	}

	reader := io.Reader(resp.Body)
	buf := new(bytes.Buffer)
	buf.ReadFrom(reader)
	output := buf.String()

	// 验证包含工具调用的 output_item.added 还有 output_item.done 事件
	if !strings.Contains(output, "event: response.output_item.added") {
		t.Errorf("Missing response.output_item.added event")
	}
	if !strings.Contains(output, `"type":"function_call"`) {
		t.Errorf("Missing function_call event payload")
	}
	if !strings.Contains(output, `"name":"rg"`) {
		t.Errorf("Missing tool name 'rg' in payload")
	}
	if !strings.Contains(output, `decision_tracker|DECISION_LOG_KEY`) {
		t.Errorf("Missing tool arguments in payload")
	}
	if !strings.Contains(output, `结束`) {
		// 验证工具结束后的后续正文能够正确吐出
		t.Errorf("Missing trailing text delta")
	}
}


