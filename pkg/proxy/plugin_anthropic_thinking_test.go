package proxy

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
)

// runPlugin 执行插件并把输出解析成事件切片
func runPlugin(t *testing.T, lines []string) []map[string]interface{} {
	t.Helper()

	plugin := &AnthropicThinkingFixPlugin{}
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

func findEvent(events []map[string]interface{}, pred func(map[string]interface{}) bool) int {
	for i, ev := range events {
		if pred(ev) {
			return i
		}
	}
	return -1
}

// collectTextDeltas 收集指定 index 上所有 text_delta 文本，验证最终拼接结果
func collectTextDeltas(events []map[string]interface{}, index float64) string {
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

// TestAnthropicThinkingFix_ThinkingPassthrough 验证 thinking 块原样透传，
// 不再被改写为 text 块；仅补齐缺失的收尾事件。
func TestAnthropicThinkingFix_ThinkingPassthrough(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"deepseek-v4-pro","role":"assistant","id":"msg_123","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" user is just saying hi"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hi"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":1}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runPlugin(t, stream)

	// 1) thinking 块原样透传，content_block 类型仍是 thinking
	startIdx := findEvent(events, func(ev map[string]interface{}) bool {
		return ev["type"] == "content_block_start" && ev["index"].(float64) == 0
	})
	if startIdx < 0 {
		t.Fatalf("找不到 index=0 的 content_block_start\n%s", dumpEvents(events))
	}
	cb0, _ := events[startIdx]["content_block"].(map[string]interface{})
	if cb0["type"] != "thinking" {
		t.Errorf("index=0 应保留为 thinking 块，实际 %v", cb0["type"])
	}

	// 2) thinking_delta 保留
	hasThinkingDelta := findEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_delta" || ev["index"].(float64) != 0 {
			return false
		}
		d, _ := ev["delta"].(map[string]interface{})
		return d != nil && d["type"] == "thinking_delta"
	}) >= 0
	if !hasThinkingDelta {
		t.Errorf("thinking_delta 应保留\n%s", dumpEvents(events))
	}

	// 3) 不应再注入 <think> 包裹的 text_delta
	merged0 := collectTextDeltas(events, 0)
	if strings.Contains(merged0, "<think>") {
		t.Errorf("不应再注入 <think> 包裹文本，实际: %q", merged0)
	}

	// 4) index=1 的 text 仍然完整
	merged1 := collectTextDeltas(events, 1)
	if merged1 != "Hi" {
		t.Errorf("index=1 的文本应为 \"Hi\"，实际: %q", merged1)
	}

	// 5) message_delta 在 message_stop 之前
	mdIdx := findEvent(events, func(ev map[string]interface{}) bool {
		return ev["type"] == "message_delta"
	})
	msIdx := findEvent(events, func(ev map[string]interface{}) bool {
		return ev["type"] == "message_stop"
	})
	if mdIdx < 0 || msIdx < 0 || mdIdx >= msIdx {
		t.Errorf("message_delta(%d) 必须在 message_stop(%d) 之前", mdIdx, msIdx)
	}
}

// TestAnthropicThinkingFix_ThinkingPlusToolUse 验证 thinking + tool_use 流：
// thinking 原样透传，tool_use 保留独立 index，stop_reason=tool_use。
func TestAnthropicThinkingFix_ThinkingPlusToolUse(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","role":"assistant","id":"msg_456","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" user wants README"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_1","name":"Read","input":{}}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"/\"}"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":1}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runPlugin(t, stream)

	// thinking 透传
	cbStart0 := findEvent(events, func(ev map[string]interface{}) bool {
		return ev["type"] == "content_block_start" && ev["index"].(float64) == 0
	})
	cb0, _ := events[cbStart0]["content_block"].(map[string]interface{})
	if cb0["type"] != "thinking" {
		t.Errorf("index=0 应保留为 thinking 块，实际 %v", cb0["type"])
	}

	// tool_use 保留独立 index，id 不改写
	toolStart := findEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_start" {
			return false
		}
		cb, _ := ev["content_block"].(map[string]interface{})
		return cb != nil && cb["type"] == "tool_use"
	})
	if toolStart < 0 {
		t.Fatalf("tool_use content_block_start 未保留\n%s", dumpEvents(events))
	}
	if events[toolStart]["index"].(float64) != 1 {
		t.Errorf("tool_use index 应为 1，实际 %v", events[toolStart]["index"])
	}
	cb1, _ := events[toolStart]["content_block"].(map[string]interface{})
	if cb1["id"] != "call_1" {
		t.Errorf("tool_use id 不应被改写，实际 %v", cb1["id"])
	}

	// input_json_delta 保留
	hasInputDelta := findEvent(events, func(ev map[string]interface{}) bool {
		if ev["type"] != "content_block_delta" {
			return false
		}
		d, _ := ev["delta"].(map[string]interface{})
		return d != nil && d["type"] == "input_json_delta"
	}) >= 0
	if !hasInputDelta {
		t.Errorf("input_json_delta 未保留\n%s", dumpEvents(events))
	}

	// message_delta 注入，stop_reason=tool_use
	mdIdx := findEvent(events, func(ev map[string]interface{}) bool {
		return ev["type"] == "message_delta"
	})
	if mdIdx < 0 {
		t.Fatalf("缺少 message_delta\n%s", dumpEvents(events))
	}
	d, _ := events[mdIdx]["delta"].(map[string]interface{})
	if d["stop_reason"] != "tool_use" {
		t.Errorf("含 tool_use 时 stop_reason 应为 tool_use，实际 %v", d["stop_reason"])
	}
}

// TestAnthropicThinkingFix_TruncatedToolUseStream 模拟上游真实断流：
// 发完 thinking + tool_use 的 input_json 后直接断流，不发收尾事件。
// 插件必须补齐 content_block_stop(1) + message_delta + message_stop。
func TestAnthropicThinkingFix_TruncatedToolUseStream(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","model":"deepseek-v4-flash","role":"assistant","id":"msg_x","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" reading README"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_abc","name":"Read","input":{}}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\": \"/README.md\"}"}}`,
	}

	events := runPlugin(t, stream)

	if len(events) == 0 || events[len(events)-1]["type"] != "message_stop" {
		t.Fatalf("断流时未补 message_stop\n%s", dumpEvents(events))
	}
	md := events[len(events)-2]
	if md["type"] != "message_delta" {
		t.Errorf("倒数第二应为 message_delta，实际 %v", md["type"])
	}
	d, _ := md["delta"].(map[string]interface{})
	if d["stop_reason"] != "tool_use" {
		t.Errorf("断流的 tool_use 流 stop_reason 应为 tool_use，实际 %v", d["stop_reason"])
	}
	stopIdx := findEvent(events, func(ev map[string]interface{}) bool {
		return ev["type"] == "content_block_stop" && ev["index"].(float64) == 1
	})
	if stopIdx < 0 {
		t.Errorf("断流时未补 content_block_stop(1)\n%s", dumpEvents(events))
	}
}

// TestAnthropicThinkingFix_SignatureDeltaDropped 验证上游 signature_delta 会被
// 丢弃 —— 一旦客户端收到 signature 就会去校验，没有真签名就会丢掉后续内容。
func TestAnthropicThinkingFix_SignatureDeltaDropped(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","role":"assistant","id":"msg_s","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"abc123"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runPlugin(t, stream)

	for _, ev := range events {
		if ev["type"] != "content_block_delta" {
			continue
		}
		d, _ := ev["delta"].(map[string]interface{})
		if d != nil && d["type"] == "signature_delta" {
			t.Errorf("signature_delta 应被丢弃\n%s", dumpEvents(events))
		}
	}
}

// TestAnthropicThinkingFix_TextOnlyPassthrough 验证不含 thinking 的纯 text 流
// 不会被插件干扰。
func TestAnthropicThinkingFix_TextOnlyPassthrough(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","role":"assistant","id":"msg_t","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runPlugin(t, stream)

	merged := collectTextDeltas(events, 0)
	if merged != "Hello" {
		t.Errorf("纯 text 流不应被改写：期望 \"Hello\"，实际 %q", merged)
	}
	if strings.Contains(merged, "<think>") {
		t.Errorf("纯 text 流不应被插入 <think> 标签")
	}
}

// TestAnthropicThinkingFix_MalformedParallelToolUse 验证遇到合并在同一个 index 下的并行工具调用 Bug 时，
// 插件能正确将其拆分成符合 Anthropic 规范的多个 content_block。
func TestAnthropicThinkingFix_MalformedParallelToolUse(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","role":"assistant","id":"msg_t","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_abc","name":"Bash","input":{}}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\n  \"command\": \"cat README.md\",\n  \"description\": \"Read README\"\n}"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\n  \"command\": \"cat README_CN.md\",\n  \"description\": \"Read README_CN\"\n}"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":1}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runPlugin(t, stream)

	var starts []map[string]interface{}
	var deltas []map[string]interface{}
	var stops []map[string]interface{}

	for _, ev := range events {
		switch ev["type"] {
		case "content_block_start":
			starts = append(starts, ev)
		case "content_block_delta":
			deltas = append(deltas, ev)
		case "content_block_stop":
			stops = append(stops, ev)
		}
	}

	if len(starts) != 2 {
		t.Fatalf("应该有 2 个 content_block_start，实际有 %d\n%s", len(starts), dumpEvents(events))
	}
	if len(deltas) != 2 {
		t.Fatalf("应该有 2 个 content_block_delta，实际有 %d\n%s", len(deltas), dumpEvents(events))
	}
	if len(stops) != 2 {
		t.Fatalf("应该有 2 个 content_block_stop，实际有 %d\n%s", len(stops), dumpEvents(events))
	}

	// 校验第一个工具调用
	cb1 := starts[0]["content_block"].(map[string]interface{})
	if cb1["id"] != "call_abc" || cb1["name"] != "Bash" || starts[0]["index"].(float64) != 1 {
		t.Errorf("第一个工具 start 不正确: %v", starts[0])
	}
	d1 := deltas[0]["delta"].(map[string]interface{})
	if deltas[0]["index"].(float64) != 1 || !strings.Contains(d1["partial_json"].(string), "README.md") {
		t.Errorf("第一个工具 delta 不正确: %v", deltas[0])
	}
	if stops[0]["index"].(float64) != 1 {
		t.Errorf("第一个工具 stop 不正确: %v", stops[0])
	}

	// 校验第二个工具调用（被成功拆分）
	cb2 := starts[1]["content_block"].(map[string]interface{})
	if cb2["id"] != "call_abc_1" || cb2["name"] != "Bash" || starts[1]["index"].(float64) != 2 {
		t.Errorf("第二个工具 start 不正确: %v", starts[1])
	}
	d2 := deltas[1]["delta"].(map[string]interface{})
	if deltas[1]["index"].(float64) != 2 || !strings.Contains(d2["partial_json"].(string), "README_CN.md") {
		t.Errorf("第二个工具 delta 不正确: %v", deltas[1])
	}
	if stops[1]["index"].(float64) != 2 {
		t.Errorf("第二个工具 stop 不正确: %v", stops[1])
	}
}

// TestAnthropicThinkingFix_ToolUseMissingInput 验证当 content_block_start 里的 tool_use 缺失必填属性 input 时，
// 插件能自动为它补齐为空的 map 对象。
func TestAnthropicThinkingFix_ToolUseMissingInput(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","role":"assistant","id":"msg_t","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool-ee07f2fb4e20426eaa66a402d5f72372","name":"Bash"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runPlugin(t, stream)

	startIdx := findEvent(events, func(ev map[string]interface{}) bool {
		return ev["type"] == "content_block_start"
	})
	if startIdx < 0 {
		t.Fatalf("找不到 content_block_start\n%s", dumpEvents(events))
	}

	cb := events[startIdx]["content_block"].(map[string]interface{})
	input, exists := cb["input"]
	if !exists {
		t.Fatalf("未成功补齐缺失的 input 属性\n%s", dumpEvents(events))
	}

	inputMap, ok := input.(map[string]interface{})
	if !ok || len(inputMap) != 0 {
		t.Errorf("补齐的 input 属性应为空的 map[string]interface{}，实际: %v", input)
	}
}

type slowReader struct {
	readCount int
}

func (s *slowReader) Read(p []byte) (n int, err error) {
	if s.readCount == 0 {
		s.readCount++
		// 第一个数据：message_start
		data := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"type\":\"message\",\"role\":\"assistant\",\"id\":\"msg_t\",\"content\":[]}}\n\n"
		copy(p, data)
		return len(data), nil
	} else if s.readCount == 1 {
		s.readCount++
		// 故意休眠 4500 毫秒，模拟 Prefill 延迟
		time.Sleep(4500 * time.Millisecond)
		data := "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		copy(p, data)
		return len(data), nil
	}
	return 0, io.EOF
}

func (s *slowReader) Close() error {
	return nil
}

// TestAnthropicThinkingFix_KeepAlive 验证自动保活 ping 事件的分发。
func TestAnthropicThinkingFix_KeepAlive(t *testing.T) {
	plugin := &AnthropicThinkingFixPlugin{}
	ctx := &goproxy.ProxyCtx{}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       &slowReader{},
	}
	resp.Header.Set("Content-Type", "text/event-stream")

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

	// 期望在 message_start 和 message_stop 之间，至少收到了 1 个 ping 事件
	hasPing := false
	for _, ev := range events {
		if ev["type"] == "ping" {
			hasPing = true
			break
		}
	}

	if !hasPing {
		t.Errorf("由于休眠了 4.5 秒，理应发送至少 1 次 ping 保活事件，实际事件流为:\n%s", dumpEvents(events))
	}
}

func TestAnthropicThinkingFix_NestedJSONNotTruncated(t *testing.T) {
	stream := []string{
		"event: message_start",
		`data: {"type":"message_start","message":{"type":"message","role":"assistant","id":"msg_t","content":[]}}`,
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_abc","name":"Bash","input":{}}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\n  \"command\": \"run_cmd\",\n  \"options\": "}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\n}"}}`,
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":1}`,
		"event: message_stop",
		`data: {"type":"message_stop"}`,
	}

	events := runPlugin(t, stream)

	var starts []map[string]interface{}
	for _, ev := range events {
		if ev["type"] == "content_block_start" {
			starts = append(starts, ev)
		}
	}

	// 嵌套 JSON 不应该被拆分为多个 tool call，因此 content_block_start 应该只有 1 个
	if len(starts) != 1 {
		t.Fatalf("对于含有嵌套空 JSON 的工具参数，不应该拆分工具调用，期望 starts 数量为 1，实际为 %d\n%s", len(starts), dumpEvents(events))
	}
}

func dumpEvents(events []map[string]interface{}) string {
	var b strings.Builder
	for i, ev := range events {
		buf, _ := json.Marshal(ev)
		b.WriteString("  ")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(": ")
		b.Write(buf)
		b.WriteByte('\n')
	}
	return b.String()
}

