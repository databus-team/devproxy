package proxy

import (
	"io"
	"strings"
	"testing"
)

func TestSSEValidator_AnthropicValid(t *testing.T) {
	input := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"content\":[]}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	out, logs := readValidatedSSE(t, input)

	if out != input {
		t.Fatalf("validator changed stream bytes")
	}
	if len(logs) != 0 {
		t.Fatalf("valid stream produced logs: %#v", logs)
	}
}

func TestSSEValidator_OpenAIChatValid(t *testing.T) {
	input := "data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
		"data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: [DONE]\n\n"

	out, logs := readValidatedSSE(t, input)

	if out != input {
		t.Fatalf("validator changed stream bytes")
	}
	if len(logs) != 0 {
		t.Fatalf("valid chat stream produced logs: %#v", logs)
	}
}

func TestSSEValidator_OpenAIResponsesValid(t *testing.T) {
	input := "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
		"event: response.output_item.added\n" +
		"data: {\"type\":\"response.output_item.added\",\"item_id\":\"msg_1\",\"output_index\":0}\n\n" +
		"event: response.content_part.added\n" +
		"data: {\"type\":\"response.content_part.added\",\"item_id\":\"msg_1\",\"content_part_index\":0}\n\n" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_1\",\"content_part_index\":0,\"delta\":\"hi\"}\n\n" +
		"event: response.output_text.done\n" +
		"data: {\"type\":\"response.output_text.done\",\"item_id\":\"msg_1\",\"content_part_index\":0}\n\n" +
		"event: response.content_part.done\n" +
		"data: {\"type\":\"response.content_part.done\",\"item_id\":\"msg_1\",\"content_part_index\":0}\n\n" +
		"event: response.output_item.done\n" +
		"data: {\"type\":\"response.output_item.done\",\"item_id\":\"msg_1\",\"output_index\":0}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n"

	out, logs := readValidatedSSE(t, input)

	if out != input {
		t.Fatalf("validator changed stream bytes")
	}
	if len(logs) != 0 {
		t.Fatalf("valid responses stream produced logs: %#v", logs)
	}
}

func TestSSEValidator_MalformedJSON(t *testing.T) {
	_, logs := readValidatedSSE(t, "event: message_start\ndata: {bad-json}\n\n")

	assertLogContains(t, logs, "malformed_json")
}

func TestSSEValidator_UnknownEventInKnownFamily(t *testing.T) {
	input := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"content\":[]}}\n\n" +
		"event: vendor_weird\n" +
		"data: {\"type\":\"vendor_weird\"}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	_, logs := readValidatedSSE(t, input)

	assertLogContains(t, logs, "unknown_event")
}

func TestSSEValidator_OutOfOrderLifecycle(t *testing.T) {
	input := "event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"

	_, logs := readValidatedSSE(t, input)

	assertLogContains(t, logs, "invalid_order")
}

func TestSSEValidator_MissingTerminalAtEOF(t *testing.T) {
	input := "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"

	_, logs := readValidatedSSE(t, input)

	assertLogContains(t, logs, "missing_terminal")
}

func readValidatedSSE(t *testing.T, input string) (string, []string) {
	t.Helper()
	var logs []string
	validator := NewSSEValidatorReadCloser(io.NopCloser(strings.NewReader(input)), "/v1/test", func(msg string) {
		logs = append(logs, msg)
	})
	out, err := io.ReadAll(validator)
	if err != nil {
		t.Fatalf("read validator: %v", err)
	}
	if err := validator.Close(); err != nil {
		t.Fatalf("close validator: %v", err)
	}
	return string(out), logs
}

func assertLogContains(t *testing.T, logs []string, want string) {
	t.Helper()
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, want) {
		t.Fatalf("logs missing %q: %#v", want, logs)
	}
}
