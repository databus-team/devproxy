# SSE Stream Validator Design

## Status

Idea accepted for design recording. Implementation plan has not been written yet.

## Goal

Add an internal SSE stream validator to help diagnose abnormal LLM streaming responses. The validator should make it faster to locate protocol problems in Anthropic Messages, OpenAI Chat Completions, and OpenAI Responses streams without changing client-visible behavior.

## User-Confirmed Scope

- Built in to the proxy response path, not configured as a user-facing plugin.
- Enabled only when `verbose: true` or equivalent verbose CLI behavior is active.
- Passive only: observe the final stream seen by the client, log diagnostics, and never modify, synthesize, block, or delay events intentionally.
- Automatically identify known streaming protocol families:
  - Anthropic Messages streaming.
  - OpenAI Chat Completions streaming.
  - OpenAI Responses API streaming.
- Validate protocol state machines, not deep field semantics.
- Use only official provider documentation as the standard source. Do not infer or invent events from current plugin behavior.

## Recommended Approach

Wrap `resp.Body` in the proxy response path after response plugins have run, but only when verbose mode is enabled and the response appears to be `text/event-stream`.

The wrapper should pass bytes through to the client as they are read. In parallel with that pass-through behavior, it should parse SSE frames, identify the protocol family from documented event names and payload shapes, update a protocol-specific state machine, and log anomalies.

This keeps validation as a diagnostic layer over the final response. It also avoids mixing observation logic into individual transform plugins.

## Protocol Standard Constraint

The validator must strictly follow official streaming event documentation:

- OpenAI Chat Completions streaming: validate the documented `ChatCompletionChunk` delta stream shape.
- OpenAI Responses API streaming: validate documented Responses streaming events such as `response.created`, `response.output_item.added`, `response.content_part.added`, `response.output_text.delta`, and `response.completed`.
- Anthropic Messages streaming: validate documented events such as `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, and `message_stop`.

If an event is not recognized from official documentation, v1 should log it as unknown and avoid applying strict state-machine assumptions to that event. Unknown events are diagnostic evidence, not grounds for mutation or synthetic repair.

## Data Flow

1. Upstream response returns to `server.go`.
2. Configured response plugins run normally.
3. If verbose is off, the response is returned unchanged.
4. If verbose is on and the response content type is SSE, the proxy wraps `resp.Body` with a validating reader.
5. As the client reads the stream, the validating reader forwards bytes and incrementally parses complete SSE frames.
6. The validator detects protocol family, checks the matching state machine, and logs warnings or errors with enough request context to debug the issue.
7. End-of-stream validation checks that required terminal events were observed for the detected protocol family.

## State-Machine Validation

v1 should validate event order and required lifecycle completion:

- Anthropic Messages: message lifecycle starts with `message_start`, content blocks use documented start/delta/stop sequencing, and the stream ends with `message_stop`.
- OpenAI Responses: response lifecycle starts with documented response creation events, output/content events appear in valid order, and the stream ends with a documented completion event.
- OpenAI Chat Completions: chunks must be parseable as documented chat completion chunks and the stream should terminate with the documented final chunk or `[DONE]` behavior.

v1 should not validate provider-specific field semantics unless the official docs require them for the stream lifecycle. Examples deferred from v1 include usage accounting, finish reason interpretation, tool-call argument completeness, or exact id/index consistency.

## Logging

Diagnostics should be concise and actionable:

- Protocol family detected.
- Request URL or normalized path.
- Event name and sequence position.
- Current state and expected next state when a violation occurs.
- Raw payload snippets should be truncated and must avoid logging sensitive headers.

The validator should log only anomalies and one low-noise summary when useful in verbose mode.

## Error Handling

Validation errors must not change the response body. Parser errors, unknown events, malformed JSON, invalid event order, or missing terminal events should be logged and then the stream should continue forwarding.

If validation itself fails internally, the proxy should log the validator failure in verbose mode and continue streaming bytes unchanged.

## Testing

Tests should cover:

- Valid Anthropic Messages stream.
- Valid OpenAI Chat Completions stream.
- Valid OpenAI Responses stream.
- Malformed JSON in an SSE frame.
- Unknown documented-family event.
- Out-of-order lifecycle event.
- Missing terminal event at EOF.
- Pass-through behavior proving output bytes are unchanged.

## Non-Goals

- No event repair.
- No synthetic lifecycle events.
- No client-visible diagnostic events.
- No blocking or failing client responses.
- No field-level semantic validator in v1.
- No offline log/file validator in v1.
