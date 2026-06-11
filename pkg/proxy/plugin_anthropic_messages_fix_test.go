package proxy

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/elazarl/goproxy"
)

// ---------- Test helpers ----------

func runMessagesFixPlugin(t *testing.T, lines []string) []map[string]interface{} {
	t.Helper()
	return runMessagesFixPluginOpts(t, lines, false)
}

func runMessagesFixPluginWithKeepReasoning(t *testing.T, lines []string) []map[string]interface{} {
	t.Helper()
	return runMessagesFixPluginOpts(t, lines, true)
}

func runMessagesFixPluginOpts(t *testing.T, lines []string, keepReasoning bool) []map[string]interface{} {
	t.Helper()

	plugin := &AnthropicMessagesFixPlugin{KeepReasoning: keepReasoning}
	ctx := &goproxy.ProxyCtx{}

	var body strings.Builder
	for _, c := range lines {
		switch {
		case strings.HasPrefix(c, "event: "):
			body.WriteString(c + "\n")
		case strings.HasPrefix(c, "data: "):
			body.WriteString(c + "\n\n")
		}
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body.String())),
	}
	resp.Header.Set("Content-Type", "text/event-stream")
	resp.Request = &http.Request{
		Header: http.Header{"X-Devproxy-Messages-Fix": []string{"true"}},
	}

	if err := plugin.ProcessResponse(resp, ctx, false, false); err != nil {
		t.Fatalf("ProcessResponse 失败: %v", err)
	}

	reader := bufio.NewReader(resp.Body)
	var events []map[string]interface{}
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "data: ") {
			data := strings.TrimPrefix(trimmed, "data: ")
			var ev map[string]interface{}
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				events = append(events, ev)
			}
		}
	}
	return events
}

func findMessagesFixEvent(events []map[string]interface{}, pred func(map[string]interface{}) bool) int {
	for i, ev := range events {
		if pred(ev) {
			return i
		}
	}
	return -1
}

func collectTextDeltasMF(events []map[string]interface{}, index float64) string {
	var b strings.Builder
	for _, ev := range events {
		if ev["type"] != "content_block_delta" {
			continue
		}
		if ev["index"] != index {
			continue
		}
		d, _ := ev["delta"].(map[string]interface{})
		if d != nil && d["type"] == "text_delta" {
			if s, ok := d["text"].(string); ok {
				b.WriteString(s)
			}
		}
	}
	return b.String()
}

func collectThinkingDeltas(events []map[string]interface{}, index float64) string {
	var b strings.Builder
	for _, ev := range events {
		if ev["type"] != "content_block_delta" {
			continue
		}
		if ev["index"] != index {
			continue
		}
		d, _ := ev["delta"].(map[string]interface{})
		if d != nil && d["type"] == "thinking_delta" {
			if s, ok := d["thinking"].(string); ok {
				b.WriteString(s)
			}
		}
	}
	return b.String()
}

// ---------- Test: 纯 text 流透传 ----------

func TestMessagesFix_TextOnlyPassthrough(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","role":"assistant","id":"msg_1","content":[],"model":"test"}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello world"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runMessagesFixPlugin(t, stream)

	text := collectTextDeltasMF(events, 0)
	if text != "Hello world" {
		t.Errorf("纯文本应透传，期望 \"Hello world\"，实际 %q", text)
	}
}

// ---------- Test: thinking 剥离（默认模式）----------

func TestMessagesFix_ThinkingStripped(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"B2C-MiniMax-M2.5","role":"assistant","id":"msg_1","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" thinkingThe"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" user"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" wants"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" me"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" to"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" find"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" a file"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":".\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" response"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"\n\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello!"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runMessagesFixPlugin(t, stream)

	// 默认模式下不应该有 thinking content_block
	for _, ev := range events {
		if ev["type"] == "content_block_start" {
			cb, _ := ev["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "thinking" {
				t.Errorf("默认模式下不应有 thinking content_block\n%s", dumpEvents(events))
			}
		}
	}

	text := collectTextDeltasMF(events, 0)
	if text == "" {
		t.Fatalf("输出 text 不应为空\n%s", dumpEvents(events))
	}

	//  thinking 内容应被剥离
	if strings.Contains(text, "The user wants") {
		t.Errorf("thinking 内容应被剥离，实际: %q", text)
	}

	// 正文应保留
	if !strings.Contains(text, "Hello") {
		t.Errorf(" response 之后的正文应保留，实际: %q", text)
	}
}

// ---------- Test: KeepReasoning=true 转为标准 thinking 事件 ----------

func TestMessagesFix_KeepReasoning_ThinkingBlock(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"test","role":"assistant","id":"msg_1","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" thinkingI need to search"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" for files."}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" response\n\nLet me check."}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runMessagesFixPluginWithKeepReasoning(t, stream)

	// 应该有 thinking content_block_start（带 signature 字段）
	thinkStart := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_start" {
			return false
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		return cb != nil && cb["type"] == "thinking"
	})
	if thinkStart < 0 {
		t.Fatalf("KeepReasoning=true 时应生成 thinking content_block\n%s", dumpEvents(events))
	}

	cb, _ := events[thinkStart]["content_block"].(map[string]interface{})
	if _, hasSig := cb["signature"]; !hasSig {
		t.Errorf("thinking block 应包含 signature 字段\n%s", dumpEvents(events))
	}

	// thinking 块应该有 thinking_delta
	thinkIdx := events[thinkStart]["index"].(float64)
	thinkText := collectThinkingDeltas(events, thinkIdx)
	if !strings.Contains(thinkText, "I need to search") || !strings.Contains(thinkText, "for files") {
		t.Errorf("thinking_delta 应包含完整思考内容，实际: %q", thinkText)
	}

	// 应该有 thinking content_block_stop
	thinkStop := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		return ev["type"] == "content_block_stop" && ev["index"] == thinkIdx
	})
	if thinkStop < 0 {
		t.Errorf("thinking 块应有 content_block_stop\n%s", dumpEvents(events))
	}

	// 之后应有 text content_block（index 更大）
	textStart := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_start" {
			return false
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		if cb == nil || cb["type"] != "text" {
			return false
		}
		idx, _ := ev["index"].(float64)
		return idx > thinkIdx
	})
	if textStart < 0 {
		t.Fatalf("thinking 块之后应有 text content_block\n%s", dumpEvents(events))
	}

	// text 块应有正文
	textIdx := events[textStart]["index"].(float64)
	text := collectTextDeltas(events, textIdx)
	if !strings.Contains(text, "Let me check") {
		t.Errorf("text 块(index=%.0f)应包含正文，实际: %q\n%s", textIdx, text, dumpEvents(events))
	}
}

// ---------- Test: XML 工具调用转换为 tool_use ----------

func TestMessagesFix_XMLToolCallToToolUse(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"test","role":"assistant","id":"msg_1","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" response\n\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"\n<invoke name=\"Glob\">\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<parameter name=\"pattern\">"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"**/README.md</parameter>\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"</invoke>\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"</minimax:tool_call>"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runMessagesFixPlugin(t, stream)

	// 应该有 tool_use content_block_start
	toolStart := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_start" {
			return false
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		return cb != nil && cb["type"] == "tool_use"
	})
	if toolStart < 0 {
		t.Fatalf("未找到 tool_use content_block_start\n%s", dumpEvents(events))
	}

	cb, _ := events[toolStart]["content_block"].(map[string]interface{})
	if cb["name"] != "Glob" {
		t.Errorf("tool_use name 应为 \"Glob\"，实际 %v", cb["name"])
	}

	// 应该有 input_json_delta
	inputDelta := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_delta" {
			return false
		}
		d, _ := ev["delta"].(map[string]interface{})
		return d != nil && d["type"] == "input_json_delta"
	})
	if inputDelta < 0 {
		t.Fatalf("未找到 input_json_delta\n%s", dumpEvents(events))
	}

	d, _ := events[inputDelta]["delta"].(map[string]interface{})
	args, _ := d["partial_json"].(string)
	if !strings.Contains(args, "pattern") || !strings.Contains(args, "README.md") {
		t.Errorf("input_json_delta 应包含 pattern 参数，实际: %s", args)
	}

	// stop_reason 应为 tool_use
	mdIdx := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		return ev["type"] == "message_delta"
	})
	if mdIdx >= 0 {
		delta, _ := events[mdIdx]["delta"].(map[string]interface{})
		if delta["stop_reason"] != "tool_use" {
			t.Errorf("含 tool_use 时 stop_reason 应为 tool_use，实际 %v", delta["stop_reason"])
		}
	}

	// text 中不应残留 XML 标签
	text := collectTextDeltasMF(events, 0)
	if strings.Contains(text, "<invoke") || strings.Contains(text, "<minimax:tool_call") || strings.Contains(text, "</minimax:tool_call>") {
		t.Errorf("text 中不应残留 XML 标签，实际: %q", text)
	}
}

// ---------- Test: thinking + XML 工具调用组合 ----------

func TestMessagesFix_ThinkingPlusXMLToolCall(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"test","role":"assistant","id":"msg_1","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" thinkingLet me read"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" the file."}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" response\n\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<invoke name=\"Read\">\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<parameter name=\"file_path\">"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"/tmp/test.txt</parameter>\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"</invoke>"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runMessagesFixPlugin(t, stream)

	// thinking 内容不应出现在 text 中
	text0 := collectTextDeltas(events, 0)
	if strings.Contains(text0, "Let me read") {
		t.Errorf("thinking 内容应被剥离: %q", text0)
	}

	// 应有 tool_use
	toolStart := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_start" {
			return false
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		return cb != nil && cb["type"] == "tool_use"
	})
	if toolStart < 0 {
		t.Fatalf("应生成 tool_use content_block\n%s", dumpEvents(events))
	}

	last := events[len(events)-1]
	if last["type"] != "message_stop" {
		t.Errorf("最后事件应为 message_stop，实际 %v", last["type"])
	}
}

// ---------- Test: KeepReasoning + thinking + XML 组合 ----------

func TestMessagesFix_KeepReasoning_ThinkingPlusXML(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"test","role":"assistant","id":"msg_1","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" thinkingI need to read the file."}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" response\n\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<invoke name=\"Read\">\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<parameter name=\"file_path\">"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"/tmp/test.txt</parameter>\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"</invoke>"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runMessagesFixPluginWithKeepReasoning(t, stream)

	// 应该有 thinking block
	thinkStart := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_start" {
			return false
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		return cb != nil && cb["type"] == "thinking"
	})
	if thinkStart < 0 {
		t.Fatalf("KeepReasoning=true 时应生成 thinking block\n%s", dumpEvents(events))
	}

	// 应该有 tool_use block
	toolStart := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_start" {
			return false
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		return cb != nil && cb["type"] == "tool_use"
	})
	if toolStart < 0 {
		t.Fatalf("应生成 tool_use content_block\n%s", dumpEvents(events))
	}

	// thinking 内容不应出现在 text_delta 中
	for _, ev := range events {
		if ev["type"] == "content_block_delta" {
			d, _ := ev["delta"].(map[string]interface{})
			if d != nil && d["type"] == "text_delta" {
				text, _ := d["text"].(string)
				if strings.Contains(text, "I need to read the file") {
					t.Errorf("thinking 内容不应出现在 text_delta 中: %q", text)
				}
			}
		}
	}
}

// ---------- Test: 多个 XML 工具调用 ----------

func TestMessagesFix_MultipleXMLToolCalls(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"test","role":"assistant","id":"msg_1","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" response\n\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<invoke name=\"Glob\">\n<parameter name=\"pattern\">"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"*.go</parameter>\n</invoke>\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"\n<invoke name=\"Read\">\n<parameter name=\"file_path\">"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"/tmp/main.go</parameter>\n</invoke>"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runMessagesFixPlugin(t, stream)

	toolCount := 0
	for _, ev := range events {
		if ev["type"] != "content_block_start" {
			continue
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		if cb != nil && cb["type"] == "tool_use" {
			toolCount++
		}
	}
	if toolCount != 2 {
		t.Errorf("应有 2 个 tool_use content_block，实际 %d\n%s", toolCount, dumpEvents(events))
	}
}

// ---------- Test: 跨 chunk 的 thinking 标签 ----------

func TestMessagesFix_ThinkingAcrossChunks(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"test","role":"assistant","id":"msg_1","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"  thin"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"kingHmm"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"..."}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" resp"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"onse\n\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runMessagesFixPlugin(t, stream)

	text := collectTextDeltasMF(events, 0)

	if strings.Contains(text, "Hmm") {
		t.Errorf("thinking 内容应被剥离，实际: %q", text)
	}

	if !strings.Contains(text, "Hello") {
		t.Errorf(" response 之后的正文应保留，实际: %q", text)
	}
}

// ---------- Test: 跨 chunk 的 XML 标签 ----------

func TestMessagesFix_XMLAcrossChunks(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"test","role":"assistant","id":"msg_1","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" response\n\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<inv"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"oke name=\"B"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ash\">\n<paramet"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"er name=\"command\">"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ls</parameter>\n</invoke>"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runMessagesFixPlugin(t, stream)

	toolStart := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_start" {
			return false
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		return cb != nil && cb["type"] == "tool_use"
	})
	if toolStart < 0 {
		t.Fatalf("跨 chunk 的 XML 应正确解析并生成 tool_use\n%s", dumpEvents(events))
	}

	cb, _ := events[toolStart]["content_block"].(map[string]interface{})
	if cb["name"] != "Bash" {
		t.Errorf("tool_use name 应为 \"Bash\"，实际 %v", cb["name"])
	}
}

// ---------- Test: 断流补齐 ----------

func TestMessagesFix_TruncatedStream(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"test","role":"assistant","id":"msg_1","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
	}

	events := runMessagesFixPlugin(t, stream)

	if len(events) == 0 || events[len(events)-1]["type"] != "message_stop" {
		t.Fatalf("断流时未补 message_stop\n%s", dumpEvents(events))
	}

	md := events[len(events)-2]
	if md["type"] != "message_delta" {
		t.Errorf("倒数第二应为 message_delta，实际 %v", md["type"])
	}
}

// ---------- Test: minimax:tool_call 标签（无 invoke） ----------

func TestMessagesFix_MinimaxToolCallOnly(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"test","role":"assistant","id":"msg_1","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" response\n\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<minimax:tool_call>\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<invoke name=\"Glob\">\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<parameter name=\"pattern\">"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"*.md</parameter>\n</invoke>\n"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"</minimax:tool_call>"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runMessagesFixPlugin(t, stream)

	toolStart := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_start" {
			return false
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		return cb != nil && cb["type"] == "tool_use"
	})
	if toolStart < 0 {
		t.Fatalf("minimax:tool_call 中的 invoke 应被解析\n%s", dumpEvents(events))
	}

	text := collectTextDeltasMF(events, 0)
	if strings.Contains(text, "minimax:tool_call") {
		t.Errorf("text 中不应残留 minimax:tool_call 标签，实际: %q", text)
	}
}

// ---------- Test: 中文化 思考/回答 标签 ----------

func TestMessagesFix_ChineseThinkingTags(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"test","role":"assistant","id":"msg_1","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" 思考让我想想..."}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" 回答\n\n这是答案。"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runMessagesFixPlugin(t, stream)

	// 默认模式下思考内容应被剥离
	text := collectTextDeltasMF(events, 0)
	if strings.Contains(text, "让我想想") {
		t.Errorf("中文化思考内容应被剥离，实际: %q", text)
	}
	if !strings.Contains(text, "这是答案") {
		t.Errorf("中文化回答后的正文应保留，实际: %q", text)
	}
}

// ---------- Test: 中文化思考 + KeepReasoning ----------

func TestMessagesFix_KeepReasoning_ChineseThinking(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"test","role":"assistant","id":"msg_1","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" 思考让我想想..."}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" 回答\n\n这是答案。"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runMessagesFixPluginWithKeepReasoning(t, stream)

	// 应该有 thinking block
	thinkStart := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_start" {
			return false
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		return cb != nil && cb["type"] == "thinking"
	})
	if thinkStart < 0 {
		t.Fatalf("中文化思考也应生成 thinking block\n%s", dumpEvents(events))
	}

	thinkIdx := events[thinkStart]["index"].(float64)
	thinkText := collectThinkingDeltas(events, thinkIdx)
	if !strings.Contains(thinkText, "让我想想") {
		t.Errorf("thinking_delta 应包含中文化思考内容，实际: %q", thinkText)
	}

	// 之后应有 text block
	textStart := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_start" {
			return false
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		if cb == nil || cb["type"] != "text" {
			return false
		}
		idx, _ := ev["index"].(float64)
		return idx > thinkIdx
	})
	if textStart < 0 {
		t.Fatalf("中文化回答后应有 text block\n%s", dumpEvents(events))
	}

	textIdx := events[textStart]["index"].(float64)
	text := collectTextDeltas(events, textIdx)
	if !strings.Contains(text, "这是答案") {
		t.Errorf("text block 应包含中文化回答后的正文，实际: %q", text)
	}
}

// ---------- Test: 真实上游流端到端验证 ----------

func TestMessagesFix_RealUpstreamStream(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type": "message_start", "message": {"id": "msg_0cc952f0-c8a1-4f6f-b181-c73c31d156a4", "type": "message", "role": "assistant", "content": [], "model": "B2C-MiniMax-M2.5", "stop_reason": null, "stop_sequence": null, "usage": {"input_tokens": 0, "output_tokens": 0, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}}}`,
		"event: content_block_start",
		`data: {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " thinkingThe"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " user"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " wants"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " me"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " to"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " read"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " a"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " README"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": ".md"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " file"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "."}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " Let"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " me"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " first"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " check"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " where"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " this"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " file"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " might"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " be"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " located"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "."}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " Based"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " on"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " the"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " project"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " structure"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": ","}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " it"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " could"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " be"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " in"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " the"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " root"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " directory"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " of"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " the"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " project"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": ".\n"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " response"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "\n\n\n"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "\n<invoke name=\"Glob\">\n"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "<"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "parameter"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": " name"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "=\""}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "pattern"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "\">"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "**"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "/"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "README"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": ".md"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "</"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "parameter"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": ">\n"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "</"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "invoke"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": ">\n"}}`,
		"event: content_block_delta",
		`data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "</minimax:tool_call>"}}`,
		"event: content_block_stop",
		`data: {"type": "content_block_stop", "index": 0}`,
		"event: message_delta",
		`data: {"type": "message_delta", "delta": {"stop_reason": "end_turn"}, "usage": {"input_tokens": 6012, "output_tokens": 74}}`,
		"event: message_stop",
		`data: {"type": "message_stop"}`,
	}

	events := runMessagesFixPlugin(t, stream)

	// 验证 tool_use
	toolStart := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_start" {
			return false
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		return cb != nil && cb["type"] == "tool_use"
	})
	if toolStart < 0 {
		t.Fatalf("真实上游流应产生 tool_use content_block\n%s", dumpEvents(events))
	}

	cb, _ := events[toolStart]["content_block"].(map[string]interface{})
	if cb["name"] != "Glob" {
		t.Errorf("tool_use name 应为 Glob，实际 %v", cb["name"])
	}

	// 验证 stop_reason 被修正为 tool_use
	mdIdx := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		return ev["type"] == "message_delta"
	})
	if mdIdx >= 0 {
		delta, _ := events[mdIdx]["delta"].(map[string]interface{})
		if delta["stop_reason"] != "tool_use" {
			t.Errorf("含 tool_use 时 stop_reason 应为 tool_use，实际 %v", delta["stop_reason"])
		}
	}

	// 验证 text 中不残留 thinking 内容
	text := collectTextDeltasMF(events, 0)
	if strings.Contains(text, "The user wants") {
		t.Errorf("思考内容不应出现在正文中，实际: %q", text)
	}

	// 验证 text 中不残留 XML 标签
	if strings.Contains(text, "<invoke") || strings.Contains(text, "</minimax:tool_call>") {
		t.Errorf("XML 标签不应出现在正文中，实际: %q", text)
	}

	// 验证 text 中不残留 <parameter> 片段
	if strings.Contains(text, "</parameter>") || strings.Contains(text, "<parameter") {
		t.Errorf("parameter 标签不应出现在正文中，实际: %q", text)
	}
}

func TestMessagesFix_ThinkTagsStripped(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"B2C-MiniMax-M2.5","role":"assistant","id":"msg_1","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<think>The"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" user"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" is saying hi.</think>\n\nHi!"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runMessagesFixPlugin(t, stream)

	// 默认模式下不应该有 thinking content_block
	for _, ev := range events {
		if ev["type"] == "content_block_start" {
			cb, _ := ev["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "thinking" {
				t.Errorf("默认模式下不应有 thinking content_block\n%s", dumpEvents(events))
			}
		}
	}

	text := collectTextDeltasMF(events, 0)
	if text == "" {
		t.Fatalf("输出 text 不应为空\n%s", dumpEvents(events))
	}

	// think 内容应被剥离
	if strings.Contains(text, "The user") {
		t.Errorf("think 内容应被剥离，实际: %q", text)
	}

	// 正文应保留
	if !strings.Contains(text, "Hi!") {
		t.Errorf("think 之后的正文应保留，实际: %q", text)
	}
}

func TestMessagesFix_KeepReasoning_ThinkTags(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"B2C-MiniMax-M2.5","role":"assistant","id":"msg_1","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<think>The"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" user"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" is saying hi.</think>\n\nHi!"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runMessagesFixPluginWithKeepReasoning(t, stream)

	// 应该有 thinking content_block_start
	thinkStart := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_start" {
			return false
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		return cb != nil && cb["type"] == "thinking"
	})
	if thinkStart < 0 {
		t.Fatalf("KeepReasoning=true 时应生成 thinking content_block\n%s", dumpEvents(events))
	}

	thinkIdx := events[thinkStart]["index"].(float64)
	thinkText := collectThinkingDeltas(events, thinkIdx)
	if !strings.Contains(thinkText, "The user") || !strings.Contains(thinkText, "is saying hi") {
		t.Errorf("thinking_delta 应包含完整思考内容，实际: %q", thinkText)
	}

	// 应该有 thinking content_block_stop
	thinkStop := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		return ev["type"] == "content_block_stop" && ev["index"] == thinkIdx
	})
	if thinkStop < 0 {
		t.Errorf("thinking 块应有 content_block_stop\n%s", dumpEvents(events))
	}

	// 之后应有 text content_block
	textStart := findMessagesFixEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_start" {
			return false
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		if cb == nil || cb["type"] != "text" {
			return false
		}
		idx, _ := ev["index"].(float64)
		return idx > thinkIdx
	})
	if textStart < 0 {
		t.Fatalf("thinking 块之后应有 text content_block\n%s", dumpEvents(events))
	}

	textIdx := events[textStart]["index"].(float64)
	text := collectTextDeltas(events, textIdx)
	if !strings.Contains(text, "Hi!") {
		t.Errorf("text 块(index=%.0f)应包含正文，实际: %q\n%s", textIdx, text, dumpEvents(events))
	}
}

func TestMessagesFix_ProcessRequest_StripsThinking(t *testing.T) {
	plugin := &AnthropicMessagesFixPlugin{}

	reqJSON := `{
		"model": "B2C-MiniMax-M2.5",
		"messages": [
			{"role": "user", "content": "hi"},
			{
				"role": "assistant",
				"content": [
					{"type": "thinking", "thinking": "The user wants me to read the README.md file from the current project directory.\n"},
					{"type": "text", "text": "Ok, let me check."}
				]
			}
		]
	}`

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", strings.NewReader(reqJSON))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if err := plugin.ProcessRequest(req, true); err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("Failed to read body: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		t.Fatalf("Failed to parse output JSON: %v", err)
	}

	messagesVal, ok := payload["messages"]
	if !ok {
		t.Fatalf("messages field missing in rewritten request")
	}

	messages, ok := messagesVal.([]interface{})
	if !ok {
		t.Fatalf("messages is not a slice")
	}

	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	assistantMsg, ok := messages[1].(map[string]interface{})
	if !ok {
		t.Fatalf("assistant message is not a map")
	}

	content := assistantMsg["content"]
	if s, ok := content.(string); ok {
		if s != "Ok, let me check." {
			t.Errorf("expected content to be 'Ok, let me check.', got '%s'", s)
		}
	} else {
		t.Errorf("expected assistant content to be string, got %T: %v", content, content)
	}
}