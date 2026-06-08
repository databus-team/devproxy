package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/elazarl/goproxy"
)

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ResponsesAPIPlugin adapts the OpenAI Responses API (/v1/responses) to the Chat Completions API (/v1/chat/completions).
type ResponsesAPIPlugin struct {
	KeepReasoning bool
}

func (p *ResponsesAPIPlugin) Name() string {
	return "responses-api"
}

// ResponsesAPIRequest represents the incoming request body for /v1/responses
type ResponsesAPIRequest struct {
	Model           string        `json:"model"`
	Instructions    string        `json:"instructions,omitempty"`
	Input           []interface{} `json:"input,omitempty"`
	ResponseFormat  *ResponseFmt  `json:"response_format,omitempty"`
	Stream          bool          `json:"stream,omitempty"`
	MaxOutputTokens int           `json:"max_output_tokens,omitempty"`
	Temperature     *float64      `json:"temperature,omitempty"`
	TopP            *float64      `json:"top_p,omitempty"`
	N               *int          `json:"n,omitempty"`
	Tools           []interface{} `json:"tools,omitempty"`
	ToolChoice      interface{}   `json:"tool_choice,omitempty"`
}

type ResponseFmt struct {
	Type       string                 `json:"type"`
	JSONSchema map[string]interface{} `json:"json_schema,omitempty"`
}

// ChatCompletionRequest represents the standard OpenAI Chat Completion request
type ChatCompletionRequest struct {
	Model               string              `json:"model"`
	Messages            []interface{}       `json:"messages"`
	ResponseFormat      *ChatResponseFormat `json:"response_format,omitempty"`
	Stream              bool                `json:"stream,omitempty"`
	MaxTokens           int                 `json:"max_tokens,omitempty"`
	MaxCompletionTokens int                 `json:"max_completion_tokens,omitempty"`
	Temperature         *float64            `json:"temperature,omitempty"`
	TopP                *float64            `json:"top_p,omitempty"`
	N                   *int                `json:"n,omitempty"`
	Tools               []interface{}       `json:"tools,omitempty"`
	ToolChoice          interface{}         `json:"tool_choice,omitempty"`
}

type ChatResponseFormat struct {
	Type       string                 `json:"type"`
	JSONSchema map[string]interface{} `json:"json_schema,omitempty"`
}

// Shared types for response handling
type gzipReadCloser struct {
	io.Reader
	orig io.ReadCloser
}

func (g *gzipReadCloser) Close() error {
	return g.orig.Close()
}

type ChatCompletionChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role      string      `json:"role,omitempty"`
			Content   string      `json:"content,omitempty"`
			ToolCalls []ChunkTool `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage map[string]interface{} `json:"usage,omitempty"`
}

type ChunkTool struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   map[string]interface{} `json:"usage,omitempty"`
}

type ChatCompletionChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason *string     `json:"finish_reason,omitempty"`
}

type ChatMessage struct {
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	ToolCalls []interface{} `json:"tool_calls,omitempty"`
}

type ResponsesAPIEvent struct {
	Type             string                `json:"type"`
	ResponseID       string                `json:"response_id,omitempty"`
	ItemID           string                `json:"item_id,omitempty"`
	Item             *Item                 `json:"item,omitempty"`
	ContentPart      *ContentPart          `json:"content_part,omitempty"`
	ContentPartIndex int                   `json:"content_part_index,omitempty"`
	Delta            string                `json:"delta,omitempty"`
	SequenceNumber   int                   `json:"sequence_number,omitempty"`
	OutputIndex      int                   `json:"output_index"`
	Usage            interface{}           `json:"usage,omitempty"`
	Response         *ResponsesAPIResponse `json:"response,omitempty"`
}

type ContentPart struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Text  string `json:"text,omitempty"`
}

type ResponsesAPIResponse struct {
	ID        string      `json:"id"`
	Object    string      `json:"object"`
	CreatedAt int64       `json:"created_at"`
	Model     string      `json:"model"`
	Output    []Item      `json:"output"`
	Usage     interface{} `json:"usage,omitempty"`
}

type Item struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Status    string    `json:"status,omitempty"`
	Role      string    `json:"role,omitempty"`
	Content   []Content `json:"content,omitempty"`
	Name      string    `json:"name,omitempty"`
	Arguments string    `json:"arguments,omitempty"`
	CallID    string    `json:"call_id,omitempty"`
}

type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (p *ResponsesAPIPlugin) ProcessRequest(req *http.Request) error {
	// Only intercept requests ending with /v1/responses
	path := req.URL.Path
	if !strings.HasSuffix(path, "/v1/responses") {
		return nil
	}

	// 1. Rewrite path: /v1/responses -> /v1/chat/completions
	req.URL.Path = strings.Replace(path, "/v1/responses", "/v1/chat/completions", 1)

	// 2. Read body
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return fmt.Errorf("responses-api: failed to read request body: %w", err)
	}
	req.Body.Close()

	if len(bodyBytes) == 0 {
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		return nil
	}

	// 3. Unmarshal Responses API request
	var respReq ResponsesAPIRequest
	if err := json.Unmarshal(bodyBytes, &respReq); err != nil {
		log.Printf("[responses-api] Warning: failed to parse request body: %v", err)
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		return nil
	}

	// 4. Transform to Chat Completion Request
	messages := p.transformMessages(respReq.Input)

	// Prepend instructions as a developer/system message if present
	if respReq.Instructions != "" {
		instrMsg := map[string]interface{}{
			"role":    "developer",
			"content": respReq.Instructions,
		}
		messages = append([]interface{}{instrMsg}, messages...)
	}

	chatReq := ChatCompletionRequest{
		Model:       respReq.Model,
		Messages:    messages,
		Stream:      respReq.Stream,
		Temperature: respReq.Temperature,
		TopP:        respReq.TopP,
		N:           respReq.N,
		Tools:       p.transformTools(respReq.Tools),
		ToolChoice:  p.transformToolChoice(respReq.ToolChoice),
	}

	// Map max_output_tokens -> max_completion_tokens (preferred for newer models)
	if respReq.MaxOutputTokens > 0 {
		chatReq.MaxCompletionTokens = respReq.MaxOutputTokens
		// Also set max_tokens for backward compatibility
		chatReq.MaxTokens = respReq.MaxOutputTokens
	}

	// Map response_format
	if respReq.ResponseFormat != nil {
		chatReq.ResponseFormat = &ChatResponseFormat{
			Type:       respReq.ResponseFormat.Type,
			JSONSchema: respReq.ResponseFormat.JSONSchema,
		}
	}

	// 5. Marshal new body
	newBodyBytes, err := json.Marshal(chatReq)
	if err != nil {
		return fmt.Errorf("responses-api: failed to marshal new request body: %w", err)
	}

	req.Body = io.NopCloser(bytes.NewReader(newBodyBytes))
	req.ContentLength = int64(len(newBodyBytes))
	req.Header.Set("Content-Length", fmt.Sprint(len(newBodyBytes)))

	// Ensure we ask for identity encoding to handle response body easily
	req.Header.Set("Accept-Encoding", "identity")

	// Mark this request as a Responses API request so ProcessResponse can handle it
	req.Header.Set("X-DevProxy-Responses-API", "true")

	log.Printf("[responses-api] Rewrote request /v1/responses -> /v1/chat/completions (model: %s)", respReq.Model)
	return nil
}

func (p *ResponsesAPIPlugin) transformMessages(input []interface{}) []interface{} {
	if input == nil {
		return nil
	}
	output := make([]interface{}, 0, len(input))
	for _, m := range input {
		item, ok := m.(map[string]interface{})
		if !ok {
			output = append(output, m)
			continue
		}

		itemType, _ := item["type"].(string)

		switch itemType {
		case "message":
			newMsg := make(map[string]interface{})
			// Standard Chat Completion roles: user, assistant, system, developer
			if role, ok := item["role"].(string); ok {
				newMsg["role"] = role
			}
			if content := item["content"]; content != nil {
				newMsg["content"] = p.transformContent(content)
			}
			// Copy other relevant fields (like name)
			for k, v := range item {
				if k != "type" && k != "role" && k != "content" && k != "phase" {
					newMsg[k] = v
				}
			}
			output = append(output, newMsg)

		case "function_call":
			// Map to assistant message with tool_calls for standard Chat Completion
			newMsg := map[string]interface{}{
				"role": "assistant",
				"tool_calls": []interface{}{
					map[string]interface{}{
						"id":   item["call_id"],
						"type": "function",
						"function": map[string]interface{}{
							"name":      item["name"],
							"arguments": item["arguments"],
						},
					},
				},
			}
			output = append(output, newMsg)

		case "function_call_output":
			// Map to tool message
			newMsg := map[string]interface{}{
				"role":         "tool",
				"tool_call_id": item["call_id"],
				"content":      item["output"],
			}
			output = append(output, newMsg)

		default:
			// Fallback: If it has role, assume it's already a standard message
			if _, hasRole := item["role"]; hasRole {
				newMsg := make(map[string]interface{})
				for k, v := range item {
					if k == "content" {
						newMsg[k] = p.transformContent(v)
					} else {
						newMsg[k] = v
					}
				}
				output = append(output, newMsg)
			} else {
				output = append(output, item)
			}
		}
	}
	return output
}

func (p *ResponsesAPIPlugin) stripThinkBlock(s string) string {
	for {
		startIdx := strings.Index(s, "<think>")
		if startIdx == -1 {
			break
		}
		endIdx := strings.Index(s, "</think>")
		if endIdx != -1 && endIdx > startIdx {
			s = s[:startIdx] + s[endIdx+8:]
		} else {
			// If there is a <think> tag but no closing tag (truncated), strip from <think> to end
			s = s[:startIdx]
			break
		}
	}
	return strings.TrimSpace(s)
}

func (p *ResponsesAPIPlugin) transformContent(content interface{}) interface{} {
	switch v := content.(type) {
	case string:
		return p.stripThinkBlock(v)
	case []interface{}:
		newContent := make([]interface{}, len(v))
		for i, pVal := range v {
			part, ok := pVal.(map[string]interface{})
			if !ok {
				newContent[i] = pVal
				continue
			}
			newPart := make(map[string]interface{})
			for pk, pv := range part {
				// Responses API uses input_text/output_text, Chat uses text
				if pk == "type" && (pv == "input_text" || pv == "output_text") {
					newPart[pk] = "text"
				} else if pk == "text" {
					if textStr, ok := pv.(string); ok {
						newPart[pk] = p.stripThinkBlock(textStr)
					} else {
						newPart[pk] = pv
					}
				} else {
					newPart[pk] = pv
				}
			}
			newContent[i] = newPart
		}
		return newContent
	default:
		return content
	}
}

func (p *ResponsesAPIPlugin) transformTools(tools []interface{}) []interface{} {
	if tools == nil {
		return nil
	}

	result := make([]interface{}, 0, len(tools))
	for _, t := range tools {
		tool, ok := t.(map[string]interface{})
		if !ok {
			// Pass through non-map tools
			result = append(result, t)
			continue
		}

		toolType, _ := tool["type"].(string)

		switch toolType {
		case "function":
			// Codex sends flat format: {"type": "function", "name": "...", "description": "...", "parameters": {...}}
			// Chat API expects nested: {"type": "function", "function": {"name": "...", "description": "...", "parameters": {...}}}
			newTool := map[string]interface{}{
				"type": "function",
			}
			funcObj := make(map[string]interface{})

			// Extract function properties
			if name, ok := tool["name"].(string); ok {
				funcObj["name"] = name
			}
			if desc, ok := tool["description"].(string); ok {
				funcObj["description"] = desc
			}
			if params, ok := tool["parameters"]; ok {
				funcObj["parameters"] = p.cleanParameters(params)
			}

			newTool["function"] = funcObj
			result = append(result, newTool)

		case "namespace":
			// Namespace tools contain nested tools - extract them
			if nestedTools, ok := tool["tools"].([]interface{}); ok {
				result = append(result, p.transformTools(nestedTools)...)
			}

		default:
			// Skip non-function tool types (web_search, image_generation, code_interpreter, etc.)
			// Chat API only supports "function" type tools
		}
	}

	return result
}

func (p *ResponsesAPIPlugin) extractToolProperties(tool map[string]interface{}) map[string]interface{} {
	props := make(map[string]interface{})
	for k, v := range tool {
		if k != "type" && k != "function" {
			props[k] = v
		}
	}
	return props
}

func (p *ResponsesAPIPlugin) cleanParameters(params interface{}) interface{} {
	m, ok := params.(map[string]interface{})
	if !ok {
		return params
	}

	newM := make(map[string]interface{})
	for k, v := range m {
		// Strip additionalProperties as many providers don't support it in tool parameters
		if k == "additionalProperties" {
			continue
		}

		// Recursively clean properties if it's a map
		if k == "properties" {
			if props, ok := v.(map[string]interface{}); ok {
				newProps := make(map[string]interface{})
				for pk, pv := range props {
					newProps[pk] = p.cleanParameters(pv)
				}
				newM[k] = newProps
				continue
			}
		}

		// Also clean items for arrays
		if k == "items" {
			newM[k] = p.cleanParameters(v)
			continue
		}

		newM[k] = v
	}
	return newM
}

func (p *ResponsesAPIPlugin) transformToolChoice(choice interface{}) interface{} {
	if choice == nil {
		return nil
	}
	// If it's a string like "auto", "none", "required", return as is
	if _, ok := choice.(string); ok {
		return choice
	}

	// If it's an object {"type": "function", "function": {"name": "..."}}
	m, ok := choice.(map[string]interface{})
	if !ok {
		return choice
	}

	// If it has name at top level (Responses API might support this in some versions)
	if name, ok := m["name"].(string); ok && m["function"] == nil {
		return map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name": name,
			},
		}
	}

	return choice
}

func (p *ResponsesAPIPlugin) ProcessResponse(resp *http.Response, ctx *goproxy.ProxyCtx, verbose bool) error {
	if resp.Request == nil {
		return nil
	}

	// Detection logic:
	// 1. Check for our internal marker header
	isResponsesAPI := resp.Request.Header.Get("X-DevProxy-Responses-API") == "true"

	// 2. Fallback: Check if it's a chat/completions request (which we might have rewritten from /v1/responses)
	// We only do this if we are SURE it was our plugin that did the rewrite.
	// Since we are now using matchedRule in server.go, this plugin will only be called if the rule matched.
	// Therefore, if it's a chat/completions request, it's highly likely it needs conversion back to responses format.
	if !isResponsesAPI && strings.HasSuffix(resp.Request.URL.Path, "/chat/completions") {
		isResponsesAPI = true
	}

	if !isResponsesAPI {
		return nil
	}

	if verbose {
		log.Printf("[responses-api] Processing response for %s (Status: %d)", resp.Request.URL.Path, resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		if verbose {
			log.Printf("[responses-api] Non-200 status code: %d", resp.StatusCode)
		}
		return nil
	}

	contentType := resp.Header.Get("Content-Type")

	// Handle Gzip
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(resp.Body)
		if err == nil {
			resp.Body = &gzipReadCloser{Reader: gr, orig: resp.Body}
			resp.Header.Del("Content-Encoding")
			resp.Header.Del("Content-Length")
			resp.ContentLength = -1
		}
	}

	if strings.Contains(contentType, "application/json") {
		return p.handleJSON(resp, verbose)
	}

	if strings.Contains(contentType, "text/event-stream") {
		return p.handleStream(resp, verbose)
	}

	return nil
}

func (p *ResponsesAPIPlugin) parseContent(contentStr string) []Content {
	if !p.KeepReasoning {
		// 如果不需要保留 reasoning，则直接使用 stripThinkBlock 剥离思考块并过滤它，只把正文作为唯一的 output_text 传回
		stripped := p.stripThinkBlock(contentStr)
		if len(stripped) == 0 {
			return []Content{}
		}
		return []Content{
			{
				Type: "output_text",
				Text: stripped,
			},
		}
	}

	var parts []Content
	s := contentStr

	for {
		startIdx := strings.Index(s, "<think>")
		if startIdx == -1 {
			if len(s) > 0 {
				parts = append(parts, Content{Type: "output_text", Text: s})
			}
			break
		}

		if startIdx > 0 {
			parts = append(parts, Content{Type: "output_text", Text: s[:startIdx]})
		}

		s = s[startIdx+7:]
		endIdx := strings.Index(s, "</think>")
		if endIdx != -1 {
			reasoningText := s[:endIdx]
			if len(reasoningText) > 0 {
				parts = append(parts, Content{Type: "reasoning", Text: reasoningText})
			}
			s = s[endIdx+8:]
		} else {
			if len(s) > 0 {
				parts = append(parts, Content{Type: "reasoning", Text: s})
			}
			break
		}
	}

	if len(parts) == 0 {
		return []Content{}
	}
	return parts
}

func (p *ResponsesAPIPlugin) handleJSON(resp *http.Response, verbose bool) error {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("responses-api: failed to read response body: %w", err)
	}
	resp.Body.Close()

	if len(bodyBytes) == 0 {
		resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		return nil
	}

	var chatResp ChatCompletionResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		if verbose {
			log.Printf("[responses-api] Warning: failed to parse response body: %v", err)
		}
		resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		return nil
	}

	// Transform
	resResp := ResponsesAPIResponse{
		ID:        strings.Replace(chatResp.ID, "chatcmpl-", "resp_", 1),
		Object:    "response",
		CreatedAt: chatResp.Created,
		Model:     chatResp.Model,
		Usage:     chatResp.Usage,
		Output:    make([]Item, 0, len(chatResp.Choices)),
	}

	for _, choice := range chatResp.Choices {
		tcInfos, cleanedContent := parseXMLToolCalls(choice.Message.Content)

		msgItem := Item{
			ID:      fmt.Sprintf("msg_%s_%d", resResp.ID, len(resResp.Output)),
			Type:    "message",
			Status:  "completed",
			Role:    choice.Message.Role,
			Content: p.parseContent(cleanedContent),
		}
		resResp.Output = append(resResp.Output, msgItem)

		for _, tc := range tcInfos {
			toolIdx := len(resResp.Output)
			toolItem := Item{
				ID:        fmt.Sprintf("msg_%s_%d", resResp.ID, toolIdx),
				Type:      "function_call",
				Status:    "completed",
				Name:      tc.Name,
				Arguments: tc.Arguments,
				CallID:    fmt.Sprintf("call_%s_%d", resResp.ID, toolIdx),
			}
			resResp.Output = append(resResp.Output, toolItem)
		}
	}

	newBodyBytes, err := json.Marshal(resResp)
	if err != nil {
		return fmt.Errorf("responses-api: failed to marshal new response body: %w", err)
	}

	resp.Body = io.NopCloser(bytes.NewReader(newBodyBytes))
	resp.ContentLength = int64(len(newBodyBytes))
	resp.Header.Set("Content-Length", fmt.Sprint(len(newBodyBytes)))

	return nil
}

func (p *ResponsesAPIPlugin) handleStream(resp *http.Response, verbose bool) error {
	reader, writer := io.Pipe()
	originalBody := resp.Body
	resp.Body = reader
	resp.ContentLength = -1
	resp.Header.Del("Content-Length")
	resp.Header.Set("Cache-Control", "no-cache")
	resp.Header.Set("Connection", "keep-alive")
	resp.Header.Set("X-Accel-Buffering", "no")
	// Ensure Content-Type is set for SSE
	resp.Header.Set("Content-Type", "text/event-stream")

	go func() {
		defer originalBody.Close()
		defer writer.Close()

		// Use default 4KB buffer for SSE streaming to minimize TTFB latency.
		// 1MB buffer causes data to accumulate before being read, violating SSE real-time semantics.
		scanner := bufio.NewScanner(originalBody)
		var responseID string
		var createdAt int64
		var model string

		if verbose {
			log.Printf("[responses-api] handleStream: started with 4KB buffer")
		}

		// State for message (Item 0)
		var messageItemID string
		var messageAdded bool
		var messageDone bool

		var pendingText string
		var inThinkBlock bool
		var inXMLToolBlock bool
		var xmlBuffer strings.Builder

		var reasoningContent strings.Builder
		var outputTextContent strings.Builder
		
		var messageSeqNum int
		
		// To track which parts we've added
		var reasoningPartAdded bool
		var reasoningPartDone bool
		var outputTextPartAdded bool
		var outputTextPartDone bool
		var outputTextStarted bool

		// State for tool calls (Items 1, 2, ...)
		type toolCallState struct {
			ID        string
			Name      string
			Arguments strings.Builder
			Added     bool
			Done      bool
		}
		toolCalls := make(map[int]*toolCallState)

		var completedSent bool
		var hasError bool

		// writeEventWrapper wraps writeEvent and logs errors for client disconnect detection
		writeEventWrapper := func(eventType string, data interface{}) bool {
			if verbose {
				log.Printf("[responses-api] Writing event: %s", eventType)
			}
			if err := p.writeEvent(writer, eventType, data); err != nil {
				if verbose {
					log.Printf("[responses-api] Client disconnected while writing event %s: %v", eventType, err)
				}
				hasError = true
				return false
			}
			return true
		}

		getOutputTextIndex := func() int {
			if p.KeepReasoning && reasoningPartAdded {
				return 1
			}
			return 0
		}

		startThink := func() bool {
			inThinkBlock = true
			if !reasoningPartAdded {
				if p.KeepReasoning {
					if !writeEventWrapper("response.content_part.added", ResponsesAPIEvent{
						Type: "response.content_part.added", ResponseID: responseID, ItemID: messageItemID,
						ContentPart: &ContentPart{Type: "reasoning", Index: 0},
					}) {
						return false
					}
				}
				reasoningPartAdded = true
			}
			return true
		}

		emitThink := func(text string) bool {
			if !reasoningPartAdded {
				if !startThink() {
					return false
				}
			}
			reasoningContent.WriteString(text)
			if p.KeepReasoning {
				messageSeqNum++
				if !writeEventWrapper("response.reasoning.delta", ResponsesAPIEvent{
					Type: "response.reasoning.delta", ResponseID: responseID, ItemID: messageItemID,
					Delta: text, SequenceNumber: messageSeqNum,
				}) {
					return false
				}
			}
			return true
		}

		endThink := func() bool {
			inThinkBlock = false
			if reasoningPartAdded && !reasoningPartDone {
				if p.KeepReasoning {
					if !writeEventWrapper("response.reasoning.done", ResponsesAPIEvent{
						Type: "response.reasoning.done", ResponseID: responseID, ItemID: messageItemID,
						ContentPartIndex: 0, ContentPart: &ContentPart{Type: "reasoning", Index: 0, Text: reasoningContent.String()},
					}) {
						return false
					}
					if !writeEventWrapper("response.content_part.done", ResponsesAPIEvent{
						Type: "response.content_part.done", ResponseID: responseID, ItemID: messageItemID,
						ContentPartIndex: 0, ContentPart: &ContentPart{Type: "reasoning", Index: 0, Text: reasoningContent.String()},
					}) {
						return false
					}
				}
				reasoningPartDone = true
			}
			return true
		}

		startOutput := func() bool {
			if inThinkBlock {
				if !endThink() {
					return false
				}
			}
			if reasoningPartAdded && !reasoningPartDone {
				if !endThink() {
					return false
				}
			}

			if !outputTextPartAdded {
				idx := getOutputTextIndex()
				if !writeEventWrapper("response.content_part.added", ResponsesAPIEvent{
					Type: "response.content_part.added", ResponseID: responseID, ItemID: messageItemID,
					ContentPart: &ContentPart{Type: "output_text", Index: idx},
				}) {
					return false
				}
				outputTextPartAdded = true
			}
			return true
		}

		emitOutput := func(text string) bool {
			if !outputTextPartAdded {
				if !startOutput() {
					return false
				}
			}

			if !outputTextStarted {
				trimmed := strings.TrimLeft(text, " \t\r\n")
				if len(trimmed) == 0 {
					return true
				}
				text = trimmed
				outputTextStarted = true
			}

			outputTextContent.WriteString(text)
			messageSeqNum++
			if !writeEventWrapper("response.output_text.delta", ResponsesAPIEvent{
				Type: "response.output_text.delta", ResponseID: responseID, ItemID: messageItemID,
				Delta: text, SequenceNumber: messageSeqNum,
			}) {
				return false
			}
			return true
		}

		endOutput := func() bool {
			if outputTextPartAdded && !outputTextPartDone {
				idx := getOutputTextIndex()
				if !writeEventWrapper("response.output_text.done", ResponsesAPIEvent{
					Type: "response.output_text.done", ResponseID: responseID, ItemID: messageItemID,
					ContentPartIndex: idx, ContentPart: &ContentPart{Type: "output_text", Index: idx, Text: outputTextContent.String()},
				}) {
					return false
				}
				if !writeEventWrapper("response.content_part.done", ResponsesAPIEvent{
					Type: "response.content_part.done", ResponseID: responseID, ItemID: messageItemID,
					ContentPartIndex: idx, ContentPart: &ContentPart{Type: "output_text", Index: idx, Text: outputTextContent.String()},
				}) {
					return false
				}
				outputTextPartDone = true
			}
			return true
		}

		specialPrefixes := []string{"<think>", "<invoke", "<minimax:tool_call>", "</minimax:tool_call>"}

		getMaxKeepLen := func(s string) int {
			maxLen := 0
			for _, prefix := range specialPrefixes {
				for i := 1; i < len(prefix); i++ {
					if len(s) >= i {
						suffix := s[len(s)-i:]
						prefPart := prefix[:i]
						if suffix == prefPart {
							if i > maxLen {
								maxLen = i
							}
						}
					}
				}
			}
			return maxLen
		}

		// Helper to close the message item
		finishMessage := func() {
			if messageAdded && !messageDone && !hasError {
				if inXMLToolBlock && xmlBuffer.Len() > 0 {
					emitOutput(xmlBuffer.String())
					xmlBuffer.Reset()
					inXMLToolBlock = false
				}

				if len(pendingText) > 0 {
					if inThinkBlock {
						emitThink(pendingText)
					} else {
						emitOutput(pendingText)
					}
					pendingText = ""
				}

				if inThinkBlock {
					endThink()
				}
				if outputTextPartAdded && !outputTextPartDone {
					endOutput()
				}

				var finalContents []Content
				if p.KeepReasoning && reasoningPartAdded {
					finalContents = append(finalContents, Content{Type: "reasoning", Text: reasoningContent.String()})
				}
				if outputTextPartAdded {
					finalContents = append(finalContents, Content{Type: "output_text", Text: outputTextContent.String()})
				}

				writeEventWrapper("response.output_item.done", ResponsesAPIEvent{
					Type: "response.output_item.done", ResponseID: responseID, OutputIndex: 0,
					Item: &Item{ID: messageItemID, Type: "message", Status: "completed", Role: "assistant", Content: finalContents},
				})
				messageDone = true
			}
		}

		// Helper to close a tool call item
		finishToolCall := func(idx int, tc *toolCallState) {
			if tc.Added && !tc.Done && !hasError {
				itemIdx := 0
				if messageAdded {
					itemIdx = 1
				}
				itemIdx += idx
				tItemID := fmt.Sprintf("msg_%s_%d", responseID, itemIdx)

				writeEventWrapper("response.output_item.done", ResponsesAPIEvent{
					Type: "response.output_item.done", ResponseID: responseID, OutputIndex: itemIdx,
					Item: &Item{ID: tItemID, Type: "function_call", Status: "completed", Name: tc.Name, Arguments: tc.Arguments.String(), CallID: tc.ID},
				})
				tc.Done = true
			}
		}

		sendFinalEvents := func(usage interface{}) {
			if completedSent || hasError {
				return
			}
			finishMessage()
			if hasError {
				return
			}

			finalItems := make([]Item, 0)
			if messageAdded {
				var finalContents []Content
				if p.KeepReasoning && reasoningPartAdded {
					finalContents = append(finalContents, Content{Type: "reasoning", Text: reasoningContent.String()})
				}
				if outputTextPartAdded {
					finalContents = append(finalContents, Content{Type: "output_text", Text: outputTextContent.String()})
				}
				finalItems = append(finalItems, Item{
					ID: messageItemID, Type: "message", Status: "completed", Role: "assistant",
					Content: finalContents,
				})
			}

			// Finish all tool calls in order
			for i := 0; i < len(toolCalls); i++ {
				if tc, ok := toolCalls[i]; ok {
					finishToolCall(i, tc)
					if hasError {
						return
					}
					itemIdx := len(finalItems)
					tItemID := fmt.Sprintf("msg_%s_%d", responseID, itemIdx)
					finalItems = append(finalItems, Item{
						ID: tItemID, Type: "function_call", Status: "completed", Name: tc.Name, Arguments: tc.Arguments.String(), CallID: tc.ID,
					})
				}
			}

			if !hasError {
				p.writeEvent(writer, "response.completed", ResponsesAPIEvent{
					Type: "response.completed", ResponseID: responseID,
					Response: &ResponsesAPIResponse{ID: responseID, Object: "response", CreatedAt: createdAt, Model: model, Usage: usage, Output: finalItems},
				})
			}
			completedSent = true
		}

		for scanner.Scan() {
			line := scanner.Text()

			if verbose {
				logLen := len(line)
				if logLen > 50 {
					logLen = 50
				}
				log.Printf("[responses-api] Read line: %s", line[:logLen])
			}

			if line == "" {
				if _, err := writer.Write([]byte("\n")); err != nil {
					if verbose {
						log.Printf("[responses-api] Client disconnected: %v", err)
					}
					return
				}
			} else {
				if strings.HasPrefix(line, "data: ") {
					data := strings.TrimPrefix(line, "data: ")
					if data == "[DONE]" {
						if verbose {
							log.Printf("[responses-api] Received [DONE], sending final events")
						}
						sendFinalEvents(nil)
						if _, err := writer.Write([]byte("data: [DONE]\n\n")); err != nil {
							if verbose {
								log.Printf("[responses-api] Client disconnected: %v", err)
							}
							return
						}
					} else {
						var chunk ChatCompletionChunk
						if errUnmarshal := json.Unmarshal([]byte(data), &chunk); errUnmarshal == nil && len(chunk.ID) > 0 {
							if verbose {
								log.Printf("[responses-api] Parsed chunk: ID=%s, Choices=%d", chunk.ID, len(chunk.Choices))
							}
							if responseID == "" {
								responseID = strings.Replace(chunk.ID, "chatcmpl-", "resp_", 1)
								createdAt = chunk.Created
								model = chunk.Model
								writeEventWrapper("response.created", ResponsesAPIEvent{
									Type: "response.created", ResponseID: responseID,
									Response: &ResponsesAPIResponse{ID: responseID, Object: "response", CreatedAt: createdAt, Model: model},
								})
							}

							if len(chunk.Choices) > 0 {
								choice := chunk.Choices[0]
								delta := choice.Delta

								// 1. Process Text Content
								if delta.Content != "" {
									if !messageAdded {
										messageItemID = fmt.Sprintf("msg_%s_0", responseID)
										if !writeEventWrapper("response.output_item.added", ResponsesAPIEvent{
											Type: "response.output_item.added", ResponseID: responseID, OutputIndex: 0,
											Item: &Item{ID: messageItemID, Type: "message", Status: "in_progress", Role: "assistant"},
										}) {
											return
										}
										messageAdded = true
									}

									pendingText += delta.Content

									for {
										if !inThinkBlock && !inXMLToolBlock {
											idx := strings.Index(pendingText, "<think>")
											if idx != -1 {
												before := pendingText[:idx]
												if len(before) > 0 {
													if !emitOutput(before) {
														return
													}
												}
												if !startThink() {
													return
												}
												pendingText = pendingText[idx+7:]
												continue
											}

											invokeIdx := strings.Index(pendingText, "<invoke")
											minimaxIdx := strings.Index(pendingText, "<minimax:tool_call")

											targetIdx := -1
											if invokeIdx != -1 && (minimaxIdx == -1 || invokeIdx < minimaxIdx) {
												targetIdx = invokeIdx
											} else if minimaxIdx != -1 {
												targetIdx = minimaxIdx
											}

											if targetIdx != -1 {
												before := pendingText[:targetIdx]
												if len(before) > 0 {
													if !emitOutput(before) {
														return
													}
												}
												inXMLToolBlock = true
												xmlBuffer.WriteString(pendingText[targetIdx:])
												pendingText = ""
												break
											}

											keepLen := getMaxKeepLen(pendingText)

											if len(pendingText) > keepLen {
												toEmit := pendingText[:len(pendingText)-keepLen]
												if !emitOutput(toEmit) {
													return
												}
												pendingText = pendingText[len(pendingText)-keepLen:]
											}
											break
										} else if inXMLToolBlock {
											xmlBuffer.WriteString(pendingText)
											pendingText = ""

											xmlStr := xmlBuffer.String()
											if strings.Contains(xmlStr, "</invoke>") {
												tcInfos, cleaned := parseXMLToolCalls(xmlStr)
												if len(tcInfos) > 0 {
													for _, tc := range tcInfos {
														idx := len(toolCalls)
														callID := fmt.Sprintf("call_%s_%d", responseID, idx)
														tItemID := fmt.Sprintf("msg_%s_%d", responseID, idx+1)

														finishMessage()
														if hasError {
															return
														}

														if !writeEventWrapper("response.output_item.added", ResponsesAPIEvent{
															Type: "response.output_item.added", ResponseID: responseID, OutputIndex: idx+1,
															Item: &Item{ID: tItemID, Type: "function_call", Status: "in_progress", Name: tc.Name, CallID: callID},
														}) {
															return
														}

														if !writeEventWrapper("response.output_item.done", ResponsesAPIEvent{
															Type: "response.output_item.done", ResponseID: responseID, OutputIndex: idx+1,
															Item: &Item{ID: tItemID, Type: "function_call", Status: "completed", Name: tc.Name, Arguments: tc.Arguments, CallID: callID},
														}) {
															return
														}

														tcState := &toolCallState{
															ID:    callID,
															Name:  tc.Name,
															Added: true,
															Done:  true,
														}
														tcState.Arguments.WriteString(tc.Arguments)
														toolCalls[idx] = tcState
													}
												}

												pendingText = cleaned
												xmlBuffer.Reset()
												inXMLToolBlock = false
												continue
											}
											break
										} else {
											idx := strings.Index(pendingText, "</think>")
											if idx != -1 {
												before := pendingText[:idx]
												if len(before) > 0 {
													if !emitThink(before) {
														return
													}
												}
												if !endThink() {
													return
												}
												pendingText = pendingText[idx+8:]
												continue
											}

											keepLen := 0
											for i := 1; i <= 7; i++ {
												if len(pendingText) >= i {
													suffix := pendingText[len(pendingText)-i:]
													prefix := "</think>"[:i]
													if suffix == prefix {
														keepLen = i
														break
													}
												}
											}

											if len(pendingText) > keepLen {
												toEmit := pendingText[:len(pendingText)-keepLen]
												if !emitThink(toEmit) {
													return
												}
												pendingText = pendingText[len(pendingText)-keepLen:]
											}
											break
										}
									}
								}

								// 2. Process Tool Calls
								if len(delta.ToolCalls) > 0 {
									// If we were sending text, finish that item first to keep order
									finishMessage()
									if hasError {
										return
									}

									for _, tcChunk := range delta.ToolCalls {
										tc, ok := toolCalls[tcChunk.Index]
										if !ok {
											tc = &toolCallState{}
											toolCalls[tcChunk.Index] = tc
										}
										if tcChunk.ID != "" {
											tc.ID = tcChunk.ID
										}
										if tcChunk.Function.Name != "" {
											tc.Name = tcChunk.Function.Name
										}
										if tcChunk.Function.Arguments != "" {
											tc.Arguments.WriteString(tcChunk.Function.Arguments)
										}

										// Announce tool call once we have basic info (ID/Name) or some arguments
										if !tc.Added && (tc.ID != "" || tc.Name != "" || tc.Arguments.Len() > 0) {
											itemIdx := 0
											if messageAdded {
												itemIdx = 1
											}
											itemIdx += tcChunk.Index
											tItemID := fmt.Sprintf("msg_%s_%d", responseID, itemIdx)

											if !writeEventWrapper("response.output_item.added", ResponsesAPIEvent{
												Type: "response.output_item.added", ResponseID: responseID, OutputIndex: itemIdx,
												Item: &Item{ID: tItemID, Type: "function_call", Status: "in_progress", Name: tc.Name, CallID: tc.ID},
											}) {
												return
											}
											tc.Added = true
										}
									}
								}

								if choice.FinishReason != nil {
									sendFinalEvents(chunk.Usage)
								}
							}
						} else {
							if verbose {
								log.Printf("[responses-api] Failed to parse chunk or empty ID: %v, data: %s", errUnmarshal, line[:min(100, len(line))])
							}
							// Forward non-JSON or unparseable data as-is
							if _, err := writer.Write([]byte(line + "\n\n")); err != nil {
								if verbose {
									log.Printf("[responses-api] Client disconnected: %v", err)
								}
								return
							}
						}
					}
				} else {
					if _, err := writer.Write([]byte(line + "\n")); err != nil {
						if verbose {
							log.Printf("[responses-api] Client disconnected: %v", err)
						}
						return
					}
				}
			}
		}
		// After loop: check scanner errors
		if err := scanner.Err(); err != nil {
			log.Printf("[responses-api] Scanner error: %v", err)
		}
	}()

	return nil
}

func (p *ResponsesAPIPlugin) writeEvent(w io.Writer, eventType string, data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("writeEvent: failed to marshal JSON: %w", err)
	}

	// Use strings.Builder to reduce allocations compared to fmt.Sprintf
	prefix := "event: "
	suffix := "\n\ndata: "
	end := "\n\n"

	var builder strings.Builder
	builder.Grow(len(prefix) + len(eventType) + len(suffix) + len(payload) + len(end))
	builder.WriteString(prefix)
	builder.WriteString(eventType)
	builder.WriteString(suffix)
	builder.Write(payload)
	builder.WriteString(end)

	_, err = w.Write([]byte(builder.String()))
	if err != nil {
		return fmt.Errorf("writeEvent: failed to write: %w", err)
	}
	return nil
}

var invokeRegex = regexp.MustCompile(`(?s)<invoke\s+name="([^"]+)">([\s\S]*?)</invoke>`)
var paramRegex = regexp.MustCompile(`<parameter\s+name="([^"]+)">([^<]*)</parameter>`)
var minimaxCallRegex = regexp.MustCompile(`(?s)</?minimax:tool_call\s*>`)

// XMLToolCallInfo 保存解析出的工具调用
type XMLToolCallInfo struct {
	Name      string
	Arguments string // JSON string
	RawText   string // 被替换的原始 XML 文本
}

func parseXMLToolCalls(s string) ([]XMLToolCallInfo, string) {
	var toolCalls []XMLToolCallInfo
	
	// 首先找到所有的 invoke 块
	matches := invokeRegex.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return nil, s
	}

	for _, match := range matches {
		rawText := s[match[0]:match[1]]
		name := s[match[2]:match[3]]
		innerParams := s[match[4]:match[5]]
		
		// 解析参数
		params := make(map[string]string)
		paramMatches := paramRegex.FindAllStringSubmatch(innerParams, -1)
		for _, pm := range paramMatches {
			pName := pm[1]
			pValue := pm[2]
			params[pName] = pValue
		}
		
		argBytes, _ := json.Marshal(params)
		
		toolCalls = append(toolCalls, XMLToolCallInfo{
			Name:      name,
			Arguments: string(argBytes),
			RawText:   rawText,
		})
	}
	
	// 从正文中删除这些 XML，并清理 minimax:tool_call 标签
	cleaned := s
	for _, tc := range toolCalls {
		cleaned = strings.ReplaceAll(cleaned, tc.RawText, "")
	}
	cleaned = minimaxCallRegex.ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)
	
	return toolCalls, cleaned
}
