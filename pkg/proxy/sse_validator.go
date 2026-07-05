package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

type SSEValidatorLogFunc func(string)

type sseValidatorReadCloser struct {
	src    io.ReadCloser
	reader *io.PipeReader
	writer *io.PipeWriter
	once   sync.Once
	done   chan struct{}
	err    error
}

func NewSSEValidatorReadCloser(src io.ReadCloser, requestPath string, logf SSEValidatorLogFunc) io.ReadCloser {
	reader, writer := io.Pipe()
	v := &sseValidatorReadCloser{
		src:    src,
		reader: reader,
		writer: writer,
		done:   make(chan struct{}),
	}
	go v.run(requestPath, logf)
	return v
}

func (v *sseValidatorReadCloser) Read(p []byte) (int, error) {
	return v.reader.Read(p)
}

func (v *sseValidatorReadCloser) Close() error {
	v.once.Do(func() {
		_ = v.src.Close()
		_ = v.reader.Close()
		<-v.done
	})
	return v.err
}

func (v *sseValidatorReadCloser) run(requestPath string, logf SSEValidatorLogFunc) {
	defer close(v.done)
	defer v.writer.Close()
	defer v.src.Close()

	validator := newSSEProtocolValidator(requestPath, logf)
	br := bufio.NewReader(v.src)
	frame := sseFrame{}

	for {
		line, err := br.ReadString('\n')
		if line != "" {
			if _, writeErr := v.writer.Write([]byte(line)); writeErr != nil {
				v.err = writeErr
				return
			}
			trimmed := strings.TrimRight(line, "\r\n")
			if trimmed == "" {
				validator.observe(frame)
				frame = sseFrame{}
			} else {
				frame.addLine(trimmed)
			}
		}
		if err != nil {
			if err != io.EOF {
				v.err = err
			}
			if frame.hasData() {
				validator.observe(frame)
			}
			validator.finish()
			return
		}
	}
}

type sseFrame struct {
	event string
	data  []string
}

func (f *sseFrame) addLine(line string) {
	if strings.HasPrefix(line, ":") {
		return
	}
	if strings.HasPrefix(line, "event:") {
		f.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		return
	}
	if strings.HasPrefix(line, "data:") {
		f.data = append(f.data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
	}
}

func (f sseFrame) dataString() string {
	return strings.Join(f.data, "\n")
}

func (f sseFrame) hasData() bool {
	return f.event != "" || len(f.data) > 0
}

type sseProtocolFamily string

const (
	sseFamilyUnknown   sseProtocolFamily = ""
	sseFamilyAnthropic sseProtocolFamily = "anthropic_messages"
	sseFamilyChat      sseProtocolFamily = "openai_chat_completions"
	sseFamilyResponses sseProtocolFamily = "openai_responses"
)

type sseProtocolValidator struct {
	requestPath string
	logf        SSEValidatorLogFunc
	sequence    int
	family      sseProtocolFamily

	anthropic sseAnthropicState
	chat      sseChatState
	responses sseResponsesState
}

type sseAnthropicState struct {
	sawStart   bool
	sawStop    bool
	openBlocks map[int]bool
}

type sseChatState struct {
	sawChunk bool
	sawDone  bool
}

type sseResponsesState struct {
	sawCreated bool
	sawDone    bool
	parts      map[string]bool
	items      map[string]bool
}

func newSSEProtocolValidator(requestPath string, logf SSEValidatorLogFunc) *sseProtocolValidator {
	return &sseProtocolValidator{
		requestPath: requestPath,
		logf:        logf,
		anthropic:   sseAnthropicState{openBlocks: make(map[int]bool)},
		responses:   sseResponsesState{parts: make(map[string]bool), items: make(map[string]bool)},
	}
}

func (v *sseProtocolValidator) observe(frame sseFrame) {
	if !frame.hasData() {
		return
	}
	v.sequence++
	data := frame.dataString()
	if data == "[DONE]" {
		v.setFamily(sseFamilyChat)
		v.chat.sawDone = true
		return
	}

	var payload map[string]interface{}
	if data != "" {
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			v.log("malformed_json", frame.event, "payload="+RedactedSnippet(data, 160))
			return
		}
	}

	eventName := frame.event
	if eventName == "" {
		eventName, _ = payload["type"].(string)
	}

	if v.family == sseFamilyUnknown {
		v.family = detectSSEFamily(eventName, payload)
	}

	switch v.family {
	case sseFamilyAnthropic:
		v.observeAnthropic(eventName, payload)
	case sseFamilyResponses:
		v.observeResponses(eventName, payload)
	case sseFamilyChat:
		v.observeChat(eventName, payload)
	default:
		v.log("unknown_family", eventName, "unable to classify stream")
	}
}

func (v *sseProtocolValidator) setFamily(family sseProtocolFamily) {
	if v.family == sseFamilyUnknown {
		v.family = family
	}
}

func detectSSEFamily(eventName string, payload map[string]interface{}) sseProtocolFamily {
	if strings.HasPrefix(eventName, "response.") {
		return sseFamilyResponses
	}
	if isAnthropicEvent(eventName) {
		return sseFamilyAnthropic
	}
	if _, ok := payload["choices"].([]interface{}); ok {
		return sseFamilyChat
	}
	if object, _ := payload["object"].(string); object == "chat.completion.chunk" {
		return sseFamilyChat
	}
	return sseFamilyUnknown
}

func (v *sseProtocolValidator) observeAnthropic(eventName string, payload map[string]interface{}) {
	if !isAnthropicEvent(eventName) {
		v.log("unknown_event", eventName, "family=anthropic_messages")
		return
	}

	index := intFromPayload(payload, "index")
	switch eventName {
	case "message_start":
		if v.anthropic.sawStart {
			v.log("invalid_order", eventName, "duplicate message_start")
		}
		v.anthropic.sawStart = true
	case "content_block_start":
		if !v.anthropic.sawStart {
			v.log("invalid_order", eventName, "expected message_start before content_block_start")
		}
		if v.anthropic.openBlocks[index] {
			v.log("invalid_order", eventName, "content block already open index="+strconv.Itoa(index))
		}
		v.anthropic.openBlocks[index] = true
	case "content_block_delta":
		if !v.anthropic.sawStart || !v.anthropic.openBlocks[index] {
			v.log("invalid_order", eventName, "expected open content block index="+strconv.Itoa(index))
		}
	case "content_block_stop":
		if !v.anthropic.openBlocks[index] {
			v.log("invalid_order", eventName, "expected open content block before stop index="+strconv.Itoa(index))
		}
		delete(v.anthropic.openBlocks, index)
	case "message_delta":
		if !v.anthropic.sawStart {
			v.log("invalid_order", eventName, "expected message_start before message_delta")
		}
		if len(v.anthropic.openBlocks) > 0 {
			v.log("invalid_order", eventName, "content block still open")
		}
	case "message_stop":
		if !v.anthropic.sawStart {
			v.log("invalid_order", eventName, "expected message_start before message_stop")
		}
		if len(v.anthropic.openBlocks) > 0 {
			v.log("invalid_order", eventName, "content block still open")
		}
		v.anthropic.sawStop = true
	case "ping", "error":
	}
}

func (v *sseProtocolValidator) observeChat(eventName string, payload map[string]interface{}) {
	if eventName != "" && eventName != "error" {
		v.log("unknown_event", eventName, "family=openai_chat_completions")
	}
	choices, ok := payload["choices"].([]interface{})
	if !ok {
		v.log("invalid_shape", eventName, "expected ChatCompletionChunk choices array")
		return
	}
	v.chat.sawChunk = true
	for _, choice := range choices {
		choiceMap, ok := choice.(map[string]interface{})
		if !ok {
			v.log("invalid_shape", eventName, "choice must be object")
			continue
		}
		if _, ok := choiceMap["delta"].(map[string]interface{}); !ok {
			if _, hasFinish := choiceMap["finish_reason"]; !hasFinish {
				v.log("invalid_shape", eventName, "choice missing delta")
			}
		}
	}
}

func (v *sseProtocolValidator) observeResponses(eventName string, payload map[string]interface{}) {
	if !strings.HasPrefix(eventName, "response.") {
		v.log("unknown_event", eventName, "family=openai_responses")
		return
	}
	if !isKnownResponsesEvent(eventName) {
		v.log("unknown_event", eventName, "family=openai_responses")
		return
	}

	itemID, _ := payload["item_id"].(string)
	partKey := itemID + ":" + strconv.Itoa(intFromPayload(payload, "content_part_index"))
	switch eventName {
	case "response.created":
		v.responses.sawCreated = true
	case "response.output_item.added":
		if !v.responses.sawCreated {
			v.log("invalid_order", eventName, "expected response.created before output_item.added")
		}
		v.responses.items[itemID] = true
	case "response.content_part.added":
		if !v.responses.sawCreated {
			v.log("invalid_order", eventName, "expected response.created before content_part.added")
		}
		v.responses.parts[partKey] = true
	case "response.output_text.delta", "response.reasoning.delta":
		if !v.responses.sawCreated {
			v.log("invalid_order", eventName, "expected response.created before delta")
		}
		if itemID != "" && !v.responses.parts[partKey] {
			v.log("invalid_order", eventName, "expected content_part.added before delta")
		}
	case "response.output_text.done", "response.reasoning.done", "response.content_part.done":
		if itemID != "" && !v.responses.parts[partKey] {
			v.log("invalid_order", eventName, "expected content_part.added before done")
		}
	case "response.output_item.done":
		if itemID != "" && !v.responses.items[itemID] {
			v.log("invalid_order", eventName, "expected output_item.added before output_item.done")
		}
	case "response.completed", "response.failed", "response.incomplete":
		if !v.responses.sawCreated {
			v.log("invalid_order", eventName, "expected response.created before terminal event")
		}
		v.responses.sawDone = true
	}
}

func (v *sseProtocolValidator) finish() {
	switch v.family {
	case sseFamilyAnthropic:
		if v.anthropic.sawStart && !v.anthropic.sawStop {
			v.log("missing_terminal", "message_stop", "stream ended before message_stop")
		}
	case sseFamilyChat:
		if v.chat.sawChunk && !v.chat.sawDone {
			v.log("missing_terminal", "[DONE]", "stream ended before [DONE]")
		}
	case sseFamilyResponses:
		if v.responses.sawCreated && !v.responses.sawDone {
			v.log("missing_terminal", "response.completed", "stream ended before terminal response event")
		}
	}
}

func (v *sseProtocolValidator) log(kind, eventName, detail string) {
	if v.logf == nil {
		return
	}
	v.logf(fmt.Sprintf("[sse-validator] kind=%s family=%s request=%s seq=%d event=%s detail=%s",
		kind, v.family, v.requestPath, v.sequence, eventName, RedactSensitiveText(detail)))
}

func isAnthropicEvent(eventName string) bool {
	switch eventName {
	case "message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop", "ping", "error":
		return true
	default:
		return false
	}
}

func isKnownResponsesEvent(eventName string) bool {
	switch eventName {
	case "response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.output_item.done",
		"response.content_part.added",
		"response.content_part.done",
		"response.output_text.delta",
		"response.output_text.done",
		"response.reasoning.delta",
		"response.reasoning.done",
		"response.completed",
		"response.failed",
		"response.incomplete":
		return true
	default:
		return false
	}
}

func intFromPayload(payload map[string]interface{}, key string) int {
	switch value := payload[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}
