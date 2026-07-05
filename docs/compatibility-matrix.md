# Compatibility Matrix

devproxy is a local AI protocol repair and diagnostics layer. Use this matrix to choose a narrow compatibility patch for a concrete client/provider mismatch.

| Client | Upstream | Symptom | Plugin or Mode |
|---|---|---|---|
| Codex | LiteLLM or provider exposing only Chat Completions | Client sends `/v1/responses`, upstream only accepts `/v1/chat/completions` | `responses-api` request + response plugin |
| Claude Code | LiteLLM Anthropic-compatible endpoint | Messages stream includes non-standard thinking text or XML-style tool calls | `anthropic-messages-fix` request + response plugin |
| Claude Code | Anthropic-compatible stream shim | Stream includes `signature_delta`, missing lifecycle events, or parallel tool-call index collisions | `anthropic-thinking-fix` request + response plugin |
| opencode | OpenAI-compatible provider | Request contains `stream_options` but omits `stream:true` | `force-stream` request plugin |
| opencode / Codex-compatible clients | OpenAI-compatible provider | Chat Completions stream has malformed `tool_calls` deltas | `openai-tool-calls-fix` request + response plugin |
| Any streaming client | Any SSE upstream | Need to know whether the final stream follows Anthropic Messages, OpenAI Chat Completions, or OpenAI Responses lifecycle | Built-in SSE validator under `verbose: true` |

## Notes

- Recipes live in `examples/recipes/`.
- The built-in SSE validator is passive: it logs anomalies and never mutates, blocks, synthesizes, or repairs events.
- Protocol conversion stays in scope only when it fixes a concrete compatibility gap, such as Codex Responses API requests over a Chat Completions-only upstream.
- General provider aggregation, model listing, routing, fallback, quota, and dashboard features should stay in LiteLLM Proxy or a similar upstream gateway.
