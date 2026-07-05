package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/elazarl/goproxy"
)

// AnthropicThinkingFixPlugin 把上游缺少收尾事件的 Anthropic 风格流式事件补齐。
//
// 关键策略：上游 thinking 块没有真实加密 signature。只要客户端不收到
// signature_delta 就不会触发签名校验，能照常把 thinking 渲染成思考块；一旦
// 收到 signature_delta（哪怕是空的），客户端会去校验，校验失败就静默丢弃
// 后续的 text/tool_use。所以插件只做最小干预：
//   - thinking 块原样透传，不做任何改写
//   - 丢弃上游所有 signature_delta
//   - 补齐缺失的 message_delta / message_stop / content_block_stop
type AnthropicThinkingFixPlugin struct{}

func (p *AnthropicThinkingFixPlugin) Name() string {
	return "anthropic-thinking-fix"
}

func (p *AnthropicThinkingFixPlugin) ProcessRequest(req *http.Request, verbose bool) error {
	req.Header.Set("Accept-Encoding", "identity")
	return nil
}

func (p *AnthropicThinkingFixPlugin) ProcessResponse(resp *http.Response, ctx *goproxy.ProxyCtx, verbose bool, vverbose bool) error {
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return nil
	}

	reader, writer := io.Pipe()
	originalBody := resp.Body
	resp.Body = reader

	resp.ContentLength = -1
	resp.Header.Del("Content-Length")
	resp.Header.Set("Cache-Control", "no-cache")
	resp.Header.Set("Connection", "keep-alive")
	resp.Header.Set("X-Accel-Buffering", "no")

	go p.rewrite(originalBody, writer, verbose, vverbose)
	return nil
}

type toolUseInfo struct {
	id           string
	name         string
	origIndex    int
	currIndex    int
	count        int
	bufferedJSON string
}

type thinkingFixState struct {
	openIndex       int
	hasOpenBlock    bool
	sawToolUse      bool
	sawMessageDelta bool
	sawMessageStop  bool

	activeTool   *toolUseInfo
	maxIndexSent int
}

func (p *AnthropicThinkingFixPlugin) rewrite(src io.ReadCloser, dst *io.PipeWriter, verbose bool, vverbose bool) {
	defer src.Close()
	defer dst.Close()

	br := bufio.NewReader(src)
	state := &thinkingFixState{}

	var pendingEventType string

	// 启动自动保活 goroutine，避免上游在超长 Prefill 时静默导致客户端或网关超时
	keepAliveStop := make(chan struct{})
	defer close(keepAliveStop)

	var activeMu sync.Mutex
	lastActive := time.Now()

	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-keepAliveStop:
				return
			case <-ticker.C:
				activeMu.Lock()
				idle := time.Since(lastActive)
				activeMu.Unlock()

				// 如果已经超过 3 秒没有任何读取活动，则主动发送 ping 帧
				if idle >= 3*time.Second {
					p.writeEvent(dst, "ping", map[string]interface{}{
						"type": "ping",
					})
					if vverbose {
						log.Printf("[%s] 自动发送 ping 保活事件 (已静默 %v)", p.Name(), idle.Round(time.Second))
					}
					// 主动刷新活跃时间，避免在依然没有真实数据时过于高频写入
					activeMu.Lock()
					lastActive = time.Now()
					activeMu.Unlock()
				}
			}
		}
	}()

	for {
		line, err := br.ReadString('\n')

		// 每次读操作完成后，立即刷新活跃时间
		activeMu.Lock()
		lastActive = time.Now()
		activeMu.Unlock()

		if err != nil && err != io.EOF {
			if verbose {
				log.Printf("[%s] 读取响应体出错: %v", p.Name(), err)
			}
			p.flushTrailing(dst, state, verbose, vverbose)
			return
		}

		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(trimmed, "event: "):
			pendingEventType = strings.TrimPrefix(trimmed, "event: ")
		case strings.HasPrefix(trimmed, "data: "):
			data := strings.TrimPrefix(trimmed, "data: ")
			p.dispatch(dst, state, pendingEventType, data, verbose, vverbose)
			pendingEventType = ""
		case trimmed == "":
			// 丢弃上游空行；writeEvent / writeRawEvent 会自动写 \n\n 分隔
		default:
			_, _ = dst.Write([]byte(line))
		}

		if err == io.EOF {
			p.flushTrailing(dst, state, verbose, vverbose)
			return
		}
	}
}

func (p *AnthropicThinkingFixPlugin) dispatch(dst io.Writer, state *thinkingFixState, eventType, data string, verbose bool, vverbose bool) {
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		p.writeRawEvent(dst, eventType, data)
		return
	}

	if t, _ := event["type"].(string); eventType == "" && t != "" {
		eventType = t
	}

	indexVal, _ := event["index"].(float64)
	index := int(indexVal)

	switch eventType {
	case "message_start":
		p.fixMessageStart(event)
		p.writeEvent(dst, eventType, event)

	case "content_block_start":
		state.openIndex = index
		state.hasOpenBlock = true
		cb, _ := event["content_block"].(map[string]interface{})
		blockType, _ := cb["type"].(string)

		if blockType == "tool_use" || blockType == "server_tool_use" {
			state.sawToolUse = true
			if cb != nil {
				if _, hasInput := cb["input"]; !hasInput {
					cb["input"] = map[string]interface{}{}
				}
			}
			id, _ := cb["id"].(string)
			name, _ := cb["name"].(string)
			state.activeTool = &toolUseInfo{
				id:        id,
				name:      name,
				origIndex: index,
				currIndex: index,
				count:     0,
			}
			if index > state.maxIndexSent {
				state.maxIndexSent = index
			}
		} else {
			state.activeTool = nil
		}
		p.writeEvent(dst, eventType, event)

	case "content_block_delta":
		delta, _ := event["delta"].(map[string]interface{})
		deltaType, _ := delta["type"].(string)

		if deltaType == "signature_delta" {
			if vverbose {
				log.Printf("[%s] 丢弃 signature_delta(index=%d)", p.Name(), index)
			}
			if verbose {
				log.Print(RepairReport{
					Plugin: p.Name(),
					Actions: []RepairAction{
						{Name: "drop_signature_delta", Count: 1, Detail: fmt.Sprintf("index=%d", index)},
					},
				}.String())
			}
			return
		}

		if deltaType == "input_json_delta" && state.activeTool != nil {
			partialJSON, _ := delta["partial_json"].(string)

			// 检查在处理当前 partialJSON 之前，当前缓冲区中的内容是否已经构成了一个完整非空的 JSON 对象。
			// 如果已经是完整的 JSON 对象，说明上一个工具调用的所有参数已经全部传输完毕。
			var temp map[string]interface{}
			trimmedBuf := strings.TrimSpace(state.activeTool.bufferedJSON)
			isPrevCompleteJSON := len(trimmedBuf) > 0 &&
				strings.HasPrefix(trimmedBuf, "{") &&
				json.Unmarshal([]byte(trimmedBuf), &temp) == nil

			// 检查新的 partial_json 是否以 "{" 开头，如果是，说明这是一个新的 JSON 对象的开始。
			isNewStart := strings.HasPrefix(strings.TrimSpace(partialJSON), "{")

			state.activeTool.count++

			if isPrevCompleteJSON && isNewStart {
				// 1. 发送 content_block_stop 结束上一个 currIndex
				p.writeEvent(dst, "content_block_stop", map[string]interface{}{
					"type":  "content_block_stop",
					"index": state.activeTool.currIndex,
				})

				// 2. 递增最大 index 和生成新的 id
				state.maxIndexSent++
				newIndex := state.maxIndexSent
				newID := fmt.Sprintf("%s_%d", state.activeTool.id, state.activeTool.count-1)

				// 3. 更新 activeTool 状态
				state.activeTool.currIndex = newIndex
				state.activeTool.bufferedJSON = partialJSON

				// 4. 发送新的 content_block_start
				p.writeEvent(dst, "content_block_start", map[string]interface{}{
					"type":  "content_block_start",
					"index": newIndex,
					"content_block": map[string]interface{}{
						"type":  "tool_use",
						"id":    newID,
						"name":  state.activeTool.name,
						"input": map[string]interface{}{},
					},
				})

				// 5. 修改当前 event 里的 index
				event["index"] = newIndex
				p.writeEvent(dst, eventType, event)

				if verbose {
					log.Printf("[%s] 检测到并行工具调用 Bug，已自动拆分为新工具调用: index=%d, id=%s", p.Name(), newIndex, newID)
					log.Print(RepairReport{
						Plugin: p.Name(),
						Actions: []RepairAction{
							{Name: "split_parallel_tool_call", Count: 1, Detail: fmt.Sprintf("index=%d", newIndex)},
						},
					}.String())
				}
				return
			}

			// 如果没有拆分，就把新的 partialJSON 追加到当前缓冲中
			state.activeTool.bufferedJSON += partialJSON
		}

		if state.activeTool != nil && state.activeTool.currIndex != index {
			event["index"] = state.activeTool.currIndex
			p.writeEvent(dst, eventType, event)
			return
		}

		p.writeRawEvent(dst, eventType, data)

	case "content_block_stop":
		state.hasOpenBlock = false
		if state.activeTool != nil && state.activeTool.currIndex != index {
			event["index"] = state.activeTool.currIndex
			p.writeEvent(dst, eventType, event)
			state.activeTool = nil
			return
		}
		state.activeTool = nil
		p.writeRawEvent(dst, eventType, data)

	case "message_delta":
		state.sawMessageDelta = true
		p.writeRawEvent(dst, eventType, data)

	case "message_stop":
		p.closeLingeringBlock(dst, state, verbose, vverbose)
		if !state.sawMessageDelta {
			p.writeMessageDelta(dst, state)
			state.sawMessageDelta = true
			if vverbose {
				log.Printf("[%s] message_stop 前补发 message_delta", p.Name())
			}
			if verbose {
				log.Print(RepairReport{
					Plugin: p.Name(),
					Actions: []RepairAction{
						{Name: "synthesize_message_delta", Count: 1},
					},
				}.String())
			}
		}
		state.sawMessageStop = true
		p.writeRawEvent(dst, eventType, data)

	default:
		// ping / error / 未来新事件类型：透传
		p.writeRawEvent(dst, eventType, data)
	}
}

func (p *AnthropicThinkingFixPlugin) fixMessageStart(event map[string]interface{}) {
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

func (p *AnthropicThinkingFixPlugin) closeLingeringBlock(dst io.Writer, state *thinkingFixState, verbose bool, vverbose bool) {
	if !state.hasOpenBlock {
		return
	}
	idx := state.openIndex
	if state.activeTool != nil {
		idx = state.activeTool.currIndex
	}
	p.writeEvent(dst, "content_block_stop", map[string]interface{}{
		"type":  "content_block_stop",
		"index": idx,
	})
	state.hasOpenBlock = false
	if vverbose {
		log.Printf("[%s] 收尾时补发 content_block_stop(index=%d)", p.Name(), idx)
	}
	if verbose {
		log.Print(RepairReport{
			Plugin: p.Name(),
			Actions: []RepairAction{
				{Name: "synthesize_content_block_stop", Count: 1, Detail: fmt.Sprintf("index=%d", idx)},
			},
		}.String())
	}
}

func (p *AnthropicThinkingFixPlugin) flushTrailing(dst io.Writer, state *thinkingFixState, verbose bool, vverbose bool) {
	if state.sawMessageStop {
		return
	}
	p.closeLingeringBlock(dst, state, verbose, vverbose)
	if !state.sawMessageDelta {
		p.writeMessageDelta(dst, state)
		state.sawMessageDelta = true
	}
	p.writeEvent(dst, "message_stop", map[string]interface{}{
		"type": "message_stop",
	})
	state.sawMessageStop = true
	if vverbose {
		log.Printf("[%s] 上游未发 message_stop，补发收尾事件", p.Name())
	}
	if verbose {
		log.Print(RepairReport{
			Plugin: p.Name(),
			Actions: []RepairAction{
				{Name: "synthesize_message_stop", Count: 1},
			},
		}.String())
	}
}

func (p *AnthropicThinkingFixPlugin) writeMessageDelta(dst io.Writer, state *thinkingFixState) {
	stopReason := "end_turn"
	if state.sawToolUse {
		stopReason = "tool_use"
	}
	p.writeEvent(dst, "message_delta", map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]interface{}{
			"output_tokens": 0,
		},
	})
}

func (p *AnthropicThinkingFixPlugin) writeEvent(dst io.Writer, eventType string, event interface{}) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	if eventType != "" {
		fmt.Fprintf(dst, "event: %s\n", eventType)
	}
	fmt.Fprintf(dst, "data: %s\n\n", payload)
}

func (p *AnthropicThinkingFixPlugin) writeRawEvent(dst io.Writer, eventType, rawData string) {
	if eventType != "" {
		fmt.Fprintf(dst, "event: %s\n", eventType)
	}
	fmt.Fprintf(dst, "data: %s\n\n", rawData)
}
