package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/elazarl/goproxy"
)

// dumpReadCloser dumps all read data to a file for debugging
type dumpReadCloser struct {
	src  io.ReadCloser
	dump io.Writer
}

func (d *dumpReadCloser) Read(p []byte) (int, error) {
	n, err := d.src.Read(p)
	if n > 0 {
		d.dump.Write(p[:n])
	}
	return n, err
}

func (d *dumpReadCloser) Close() error {
	return d.src.Close()
}

// AnthropicMessagesFixPlugin 修复上游 /v1/messages 端点返回的流式事件中的问题：
//
//  1. 将  thinking... 思考 /  thinking...  response 从 text_delta 中剥离：
//     - 默认模式（KeepReasoning=false）：丢弃思考内容，只保留正文
//     - KeepReasoning=true：将思考转为标准 Anthropic thinking content_block 事件
//       （content_block_start thinking → thinking_delta → content_block_stop →
//       content_block_start text → ...），参考 anthropic-thinking-fix 的透传格式
//
//  2. 将 <invoke>/<minimax:tool_call> XML 工具调用从 text_delta 中提取，
//     转换为符合 Anthropic 规范的 tool_use content_block
type AnthropicMessagesFixPlugin struct {
	KeepReasoning bool
}

func (p *AnthropicMessagesFixPlugin) Name() string {
	return "anthropic-messages-fix"
}

// ---------- Request plugin ----------

func (p *AnthropicMessagesFixPlugin) ProcessRequest(req *http.Request) error {
	if !strings.HasSuffix(req.URL.Path, "/v1/messages") {
		return nil
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("X-DevProxy-Messages-Fix", "true")
	return nil
}

// ---------- Response plugin ----------

func (p *AnthropicMessagesFixPlugin) ProcessResponse(resp *http.Response, ctx *goproxy.ProxyCtx, verbose bool) error {
	if resp.Request == nil {
		return nil
	}

	xFix := resp.Request.Header.Get("X-DevProxy-Messages-Fix")
	if xFix != "true" {
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return nil
	}

	if verbose {
		log.Printf("[%s] Processing SSE response for /v1/messages", p.Name())
	}

	reader, writer := io.Pipe()
	originalBody := resp.Body
	resp.Body = reader
	resp.ContentLength = -1
	resp.Header.Del("Content-Length")
	resp.Header.Set("Cache-Control", "no-cache")
	resp.Header.Set("Connection", "keep-alive")
	resp.Header.Set("X-Accel-Buffering", "no")

	go p.rewrite(originalBody, writer, verbose)
	return nil
}

// ---------- Stream state ----------

type messagesFixState struct {
	hasOpenBlock bool
	openIndex    int

	emittedThinkingBlock bool

	sawToolUse      bool
	maxIndexSent    int
	toolCallCounter int

	indexRemap map[int]int

	sawMessageDelta bool
	sawMessageStop  bool
}

// ---------- Rewrite loop ----------

func (p *AnthropicMessagesFixPlugin) rewrite(src io.ReadCloser, dst *io.PipeWriter, verbose bool) {
	defer src.Close()
	defer dst.Close()

	// DEBUG: dump upstream input
	fIn, _ := os.Create("./devproxy_messages_fix_input.log")
	if fIn != nil {
		defer fIn.Close()
		src = &dumpReadCloser{src: src, dump: fIn}
	}

	scanner := bufio.NewScanner(src)
	state := &messagesFixState{
		indexRemap: make(map[int]int),
	}

	var pendingEventType string
	var pendingText string
	var inThinkBlock bool
	var inXMLBlock bool
	var upstreamTextIndex int = -1

	knownTags := []string{
		" thinking", " 思考",
		" response", " 回答",
		"<invoke", "</invoke>",
		"<minimax:tool_call>", "</minimax:tool_call>",
		"<parameter", "</parameter>",
		" thinking", " response",
		"&lt;think&gt;", "&lt;/think&gt;",
		"<think>", "</think>",
	}
	knownPartialLen := func(s string) int {
		maxLen := 0
		for _, tag := range knownTags {
			for i := 1; i < len(tag); i++ {
				if len(s) >= i && s[len(s)-i:] == tag[:i] {
					if i > maxLen {
						maxLen = i
					}
				}
			}
		}
		return maxLen
	}

	// ---------- block helpers ----------

	closeCurrentBlock := func() {
		if !state.hasOpenBlock {
			return
		}
		p.writeRawJSON(dst, "content_block_stop", fmt.Sprintf(
			`{"type":"content_block_stop","index":%d}`, state.openIndex,
		))
		state.hasOpenBlock = false
	}

	startNewTextBlock := func() {
		state.maxIndexSent++
		newIndex := state.maxIndexSent
		state.openIndex = newIndex
		state.hasOpenBlock = true
		state.emittedThinkingBlock = false

		p.writeEvent(dst, "content_block_start", map[string]interface{}{
			"type":  "content_block_start",
			"index": newIndex,
			"content_block": map[string]interface{}{
				"type": "text",
				"text": "",
			},
		})
	}

	startThinkingBlock := func() {
		if state.emittedThinkingBlock {
			return
		}
		closeCurrentBlock()

		state.maxIndexSent++
		newIndex := state.maxIndexSent
		state.openIndex = newIndex
		state.hasOpenBlock = true
		state.emittedThinkingBlock = true

		p.writeEvent(dst, "content_block_start", map[string]interface{}{
			"type":  "content_block_start",
			"index": newIndex,
			"content_block": map[string]interface{}{
				"type":      "thinking",
				"thinking":  "",
				"signature": "",
			},
		})
	}

	flushTextDelta := func(text string) {
		if len(text) == 0 || !state.hasOpenBlock {
			return
		}
		p.writeRawJSON(dst, "content_block_delta", fmt.Sprintf(
			`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%s}}`,
			state.openIndex, jsonString(text),
		))
	}

	flushThinkingDelta := func(text string) {
		if len(text) == 0 || !state.hasOpenBlock {
			return
		}
		p.writeRawJSON(dst, "content_block_delta", fmt.Sprintf(
			`{"type":"content_block_delta","index":%d,"delta":{"type":"thinking_delta","thinking":%s}}`,
			state.openIndex, jsonString(text),
		))
	}

	safeFlushText := func() {
		if inThinkBlock || inXMLBlock {
			return
		}
		if len(pendingText) == 0 {
			return
		}
		keepLen := knownPartialLen(pendingText)
		if len(pendingText) > keepLen {
			toFlush := pendingText[:len(pendingText)-keepLen]
			flushTextDelta(toFlush)
			pendingText = pendingText[len(pendingText)-keepLen:]
		}
	}

	// ---------- tool use emit ----------

	emitToolUse := func(tc XMLToolCallInfo) {
		closeCurrentBlock()

		state.toolCallCounter++
		state.maxIndexSent++
		newIndex := state.maxIndexSent
		toolID := fmt.Sprintf("toolu_%s_%d", tc.Name, state.toolCallCounter)
		state.sawToolUse = true

		p.writeEvent(dst, "content_block_start", map[string]interface{}{
			"type":  "content_block_start",
			"index": newIndex,
			"content_block": map[string]interface{}{
				"type":  "tool_use",
				"id":    toolID,
				"name":  tc.Name,
				"input": map[string]interface{}{},
			},
		})

		p.writeEvent(dst, "content_block_delta", map[string]interface{}{
			"type":  "content_block_delta",
			"index": newIndex,
			"delta": map[string]interface{}{
				"type":         "input_json_delta",
				"partial_json": tc.Arguments,
			},
		})

		p.writeEvent(dst, "content_block_stop", map[string]interface{}{
			"type":  "content_block_stop",
			"index": newIndex,
		})

		if verbose {
			log.Printf("[%s] XML tool_use: name=%s, args=%s, index=%d", p.Name(), tc.Name, tc.Arguments, newIndex)
		}
	}

	// ---------- processPending ----------

	// findThinkTag detects think block start markers.
	// Supports:
	//   " thinking" (literal XML tag from JSON-encoded upstream like hexin)
	//   " thinking" / " 思考" (marker-based format)
	//   "&lt;think&gt;" (HTML-entity format, some upstream variants)
	findThinkTag := func() (int, int) {
		for _, tag := range []string{
			" thinking",             // XML: <think>
			" 思考",
			"&lt;think&gt;",        // HTML entity format
			"<think>",             // standard think tag
			"thinking", "思考",
		} {
			idx := strings.Index(pendingText, tag)
			if idx != -1 {
				return idx, idx + len(tag)
			}
		}
		return -1, -1
	}

	// findResponseTag detects think block end markers.
	findResponseTag := func() (int, int) {
		for _, tag := range []string{
			" response",             // XML: </think>
			" 回答",
			"&lt;/think&gt;",       // HTML entity format
			"</think>",            // standard end think tag
			"response", "回答",
		} {
			idx := strings.Index(pendingText, tag)
			if idx != -1 {
				return idx, idx + len(tag)
			}
		}
		return -1, -1
	}

	processPending := func() {
		for loop := 0; loop < 100; loop++ {
			if !state.hasOpenBlock && !inThinkBlock && !inXMLBlock {
				startNewTextBlock()
			}

			if !inThinkBlock && !inXMLBlock {
				thinkIdx, _ := findThinkTag()
				invokeIdx := strings.Index(pendingText, "<invoke")
				minimaxOpenIdx := strings.Index(pendingText, "<minimax:tool_call")
				minimaxCloseIdx := strings.Index(pendingText, "</minimax:tool_call>")

				type marker struct {
					idx  int
					kind string
				}
				var markers []marker
				if thinkIdx != -1 {
					markers = append(markers, marker{thinkIdx, "think"})
				}
				if invokeIdx != -1 {
					markers = append(markers, marker{invokeIdx, "xml"})
				}
				if minimaxOpenIdx != -1 {
					markers = append(markers, marker{minimaxOpenIdx, "xml"})
				}
				if minimaxCloseIdx != -1 {
					markers = append(markers, marker{minimaxCloseIdx, "xml"})
				}

				if len(markers) == 0 {
					break
				}

				earliest := markers[0]
				for _, m := range markers[1:] {
					if m.idx < earliest.idx {
						earliest = m
					}
				}

				if earliest.idx > 0 {
					flushTextDelta(pendingText[:earliest.idx])
					pendingText = pendingText[earliest.idx:]
				}

				if earliest.kind == "think" {
					if p.KeepReasoning {
						startThinkingBlock()
					}
					_, thinkEnd := findThinkTag()
					pendingText = pendingText[thinkEnd:]
					inThinkBlock = true
				} else {
					inXMLBlock = true
				}
				continue
			}

			if inThinkBlock {
				endIdx, endLen := findResponseTag()
				if endIdx != -1 {
					if p.KeepReasoning {
						if endIdx > 0 {
							flushThinkingDelta(pendingText[:endIdx])
						}
						closeCurrentBlock()
					}
					pendingText = pendingText[endLen:]
					inThinkBlock = false
					continue
				}

				// partial match of end tag at tail
				keepLen := 0
				for _, tag := range []string{
					" response", " 回答", "response", "回答",
					" response", "&lt;/think&gt;",
					"</think>",
				} {
					for i := 1; i < len(tag); i++ {
						if len(pendingText) >= i && pendingText[len(pendingText)-i:] == tag[:i] {
							if i > keepLen {
								keepLen = i
							}
						}
					}
				}

				if len(pendingText) > keepLen {
					if p.KeepReasoning {
						flushThinkingDelta(pendingText[:len(pendingText)-keepLen])
					}
					pendingText = pendingText[len(pendingText)-keepLen:]
				}
				break
			}

			if inXMLBlock {
				invokeEnd := strings.Index(pendingText, "</invoke>")
				minimaxEnd := strings.Index(pendingText, "</minimax:tool_call>")

				if invokeEnd != -1 || minimaxEnd != -1 {
					var xmlEnd int
					endLen := 0
					if invokeEnd != -1 && minimaxEnd != -1 {
						if invokeEnd < minimaxEnd {
							xmlEnd = invokeEnd
							endLen = len("</invoke>")
						} else {
							xmlEnd = minimaxEnd
							endLen = len("</minimax:tool_call>")
						}
					} else if invokeEnd != -1 {
						xmlEnd = invokeEnd
						endLen = len("</invoke>")
					} else {
						xmlEnd = minimaxEnd
						endLen = len("</minimax:tool_call>")
					}

					xmlStr := pendingText[:xmlEnd+endLen]
					remaining := pendingText[xmlEnd+endLen:]

					tcInfos, cleaned := parseXMLToolCalls(xmlStr)

					if len(tcInfos) > 0 {
						for _, tc := range tcInfos {
							emitToolUse(tc)
						}
					}

					pendingText = minimaxCallRegex.ReplaceAllString(cleaned+remaining, "")
					inXMLBlock = false
					continue
				}

				keepLen := 0
				for _, tag := range []string{"</invoke>", "</minimax:tool_call>"} {
					for i := 1; i < len(tag); i++ {
						if len(pendingText) >= i && pendingText[len(pendingText)-i:] == tag[:i] {
							if i > keepLen {
								keepLen = i
							}
						}
					}
				}
				_ = keepLen
				break
			}
		}
	}

	// ---------- main loop ----------

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimRight(line, "\r\n")

		switch {
		case strings.HasPrefix(trimmed, "event: "):
			pendingEventType = strings.TrimPrefix(trimmed, "event: ")

		case strings.HasPrefix(trimmed, "data: "):
			data := strings.TrimPrefix(trimmed, "data: ")

			var event map[string]interface{}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				fmt.Fprintf(dst, "%s\n\n", line)
				pendingEventType = ""
				continue
			}

			eventType, _ := event["type"].(string)
			if eventType == "" && pendingEventType != "" {
				eventType = pendingEventType
			}

			switch eventType {
			case "message_start":
				p.fixMessageStart(event)
				p.writeEvent(dst, "message_start", event)

			case "content_block_start":
				closeCurrentBlock()

				origIndex := int(getFloat64(event, "index"))
				cb, _ := event["content_block"].(map[string]interface{})
				blockType, _ := cb["type"].(string)

				if blockType == "text" {
					upstreamTextIndex = origIndex
					state.openIndex = origIndex
					state.hasOpenBlock = true
					if state.openIndex > state.maxIndexSent {
						state.maxIndexSent = state.openIndex
					}
					pendingText = ""
					inThinkBlock = false
					inXMLBlock = false
					state.emittedThinkingBlock = false
					p.writeEvent(dst, "content_block_start", event)
				} else {
					state.sawToolUse = true
					if _, hasInput := cb["input"]; !hasInput {
						cb["input"] = map[string]interface{}{}
					}

					if origIndex <= state.maxIndexSent {
						state.maxIndexSent++
						newIndex := state.maxIndexSent
						state.indexRemap[origIndex] = newIndex
						event["index"] = float64(newIndex)
						state.openIndex = newIndex
					} else {
						state.openIndex = origIndex
						if origIndex > state.maxIndexSent {
							state.maxIndexSent = origIndex
						}
					}
					state.hasOpenBlock = true
					p.writeEvent(dst, "content_block_start", event)
				}

			case "content_block_delta":
				delta, _ := event["delta"].(map[string]interface{})
				deltaType, _ := delta["type"].(string)

				if deltaType == "text_delta" {
					text, _ := delta["text"].(string)
					if text == "" {
						p.writeEvent(dst, "content_block_delta", event)
						continue
					}

					pendingText += text
					processPending()
					safeFlushText()
				} else {
					origIdx := int(getFloat64(event, "index"))
					if newIdx, ok := state.indexRemap[origIdx]; ok {
						event["index"] = float64(newIdx)
					}
					p.writeEvent(dst, "content_block_delta", event)
				}

			case "content_block_stop":
				origIdx := int(getFloat64(event, "index"))

				if origIdx == upstreamTextIndex {
					// handle XML residue
					if inXMLBlock && len(pendingText) > 0 {
						tcInfos, cleaned := parseXMLToolCalls(pendingText)
						if len(tcInfos) > 0 {
							for _, tc := range tcInfos {
								emitToolUse(tc)
							}
						}
						pendingText = minimaxCallRegex.ReplaceAllString(cleaned, "")
						inXMLBlock = false
					}

					// handle thinking residue
					if inThinkBlock {
						if p.KeepReasoning && len(pendingText) > 0 {
							flushThinkingDelta(pendingText)
						}
						pendingText = ""
						inThinkBlock = false
					}

					// flush remaining text
					if !inThinkBlock && !inXMLBlock && len(pendingText) > 0 {
						flushTextDelta(pendingText)
					}
					inThinkBlock = false
					inXMLBlock = false
					pendingText = ""

					if state.hasOpenBlock && state.openIndex == origIdx {
						p.writeRawJSON(dst, "content_block_stop", fmt.Sprintf(
							`{"type":"content_block_stop","index":%d}`, origIdx,
						))
						state.hasOpenBlock = false
					}
				} else {
					if newIdx, ok := state.indexRemap[origIdx]; ok {
						event["index"] = float64(newIdx)
						delete(state.indexRemap, origIdx)
					}
					p.writeEvent(dst, "content_block_stop", event)
					state.hasOpenBlock = false
				}

			case "message_delta":
				state.sawMessageDelta = true
				if state.sawToolUse {
					delta, _ := event["delta"].(map[string]interface{})
					if delta != nil {
						delta["stop_reason"] = "tool_use"
					}
				}
				p.writeEvent(dst, "message_delta", event)

			case "message_stop":
				if state.hasOpenBlock {
					if !inThinkBlock && !inXMLBlock && len(pendingText) > 0 {
						flushTextDelta(pendingText)
						pendingText = ""
					}
					closeCurrentBlock()
				}

				if !state.sawMessageDelta {
					p.writeMessageDelta(dst, state)
					state.sawMessageDelta = true
					if verbose {
						log.Printf("[%s] message_stop补齐 message_delta", p.Name())
					}
				}
				state.sawMessageStop = true
				p.writeEvent(dst, "message_stop", event)

			default:
				fmt.Fprintf(dst, "%s\n\n", line)
			}

			pendingEventType = ""

		case trimmed == "":
			fmt.Fprintf(dst, "\n")

		default:
			fmt.Fprintf(dst, "%s\n", line)
		}
	}

	if err := scanner.Err(); err != nil {
		if verbose {
			log.Printf("[%s] Scanner error: %v", p.Name(), err)
		}
	}

	// complete trailing events
	if !state.sawMessageStop {
		if state.hasOpenBlock {
			if !inThinkBlock && !inXMLBlock && len(pendingText) > 0 {
				flushTextDelta(pendingText)
			}
			closeCurrentBlock()
		}
		if !state.sawMessageDelta {
			p.writeMessageDelta(dst, state)
		}
		p.writeRawJSON(dst, "message_stop", `{"type":"message_stop"}`)
		state.sawMessageStop = true
		if verbose {
			log.Printf("[%s] 上游未发 message_stop，补发收尾事件", p.Name())
		}
	}
}

// ---------- helpers ----------

func (p *AnthropicMessagesFixPlugin) fixMessageStart(event map[string]interface{}) {
	msg, ok := event["message"].(map[string]interface{})
	if !ok {
		return
	}
	if _, ok := msg["stop_reason"]; !ok {
		msg["stop_reason"] = nil
	}
	if _, ok := msg["stop_sequence"]; !ok {
		msg["stop_sequence"] = nil
	}
	if _, ok := msg["content"]; !ok {
		msg["content"] = []interface{}{}
	}
	if _, ok := msg["usage"]; !ok {
		msg["usage"] = map[string]interface{}{
			"input_tokens":  0,
			"output_tokens": 0,
		}
	}
}

func (p *AnthropicMessagesFixPlugin) writeMessageDelta(dst io.Writer, state *messagesFixState) {
	stopReason := "end_turn"
	if state.sawToolUse {
		stopReason = "tool_use"
	}
	p.writeRawJSON(dst, "message_delta", fmt.Sprintf(
		`{"type":"message_delta","delta":{"stop_reason":"%s","stop_sequence":null},"usage":{"output_tokens":0}}`,
		stopReason,
	))
}

func (p *AnthropicMessagesFixPlugin) writeEvent(dst io.Writer, eventType string, event interface{}) {
	payload, err := json.Marshal(event)
	if err != nil {
		log.Printf("[%s] writeEvent marshal error: %v", p.Name(), err)
		return
	}
	if eventType != "" {
		fmt.Fprintf(dst, "event: %s\n", eventType)
	}
	fmt.Fprintf(dst, "data: %s\n\n", payload)
}

func (p *AnthropicMessagesFixPlugin) writeRawJSON(dst io.Writer, eventType, rawJSON string) {
	if eventType != "" {
		fmt.Fprintf(dst, "event: %s\n", eventType)
	}
	fmt.Fprintf(dst, "data: %s\n\n", rawJSON)
}

func getFloat64(event map[string]interface{}, key string) float64 {
	v, ok := event[key]
	if !ok {
		return 0
	}
	f, _ := v.(float64)
	return f
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}