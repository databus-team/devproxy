# Protocol Repair Diagnostics Suite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the protocol repair diagnostics suite: plugin chain tracing, structured repair reports, log redaction, passive SSE validation, OpenAI tool-call stream repair, LiteLLM-oriented recipes, compatibility docs, and regression fixtures.

**Architecture:** Keep the existing MITM proxy and plugin architecture. Add small shared diagnostics helpers in `pkg/proxy`, wire them through `server.go`, then update existing plugins and add one focused OpenAI tool-call stream fixer. The SSE validator is built into the response path under verbose mode and remains passive.

**Tech Stack:** Go 1.25.6, `net/http`, `io.Pipe`, `bufio`, `encoding/json`, `testing`, YAML examples, Markdown docs.

---

## Scope

This plan implements protocol repair and diagnostics only. It must not add a general AI gateway, provider routing, usage dashboard, quota system, `/v1/models` aggregation, or OTel/Langfuse/Phoenix integration.

## File Structure

- Create `pkg/proxy/diagnostics.go`: redaction, plugin trace records, structured repair reports, body diff helpers.
- Create `pkg/proxy/diagnostics_test.go`: unit tests for redaction, report formatting, trace formatting, and safe body snippets.
- Create `pkg/proxy/sse_validator.go`: passive SSE parser and state validators for Anthropic Messages, OpenAI Chat Completions, and OpenAI Responses.
- Create `pkg/proxy/sse_validator_test.go`: valid stream, malformed JSON, unknown event, out-of-order event, missing terminal event, and pass-through tests.
- Create `pkg/proxy/plugin_openai_tool_calls_fix.go`: request/response plugin that repairs OpenAI-compatible Chat Completions tool-call stream deltas without becoming a gateway.
- Create `pkg/proxy/plugin_openai_tool_calls_fix_test.go`: stream repair tests and pass-through tests.
- Modify `pkg/proxy/plugin.go`: register the new plugin and support `:diagnose` parameters where useful.
- Modify `pkg/proxy/server.go`: collect plugin chain trace, attach repair recorder to request context, log trace in verbose mode, and wrap final SSE responses with the passive validator.
- Modify `pkg/proxy/plugin_codex.go`: report model/content repairs using the shared diagnostics helper.
- Modify `pkg/proxy/plugin_force_stream.go`: report stream field repair.
- Modify `pkg/proxy/plugin_responses_api.go`: report request/response bridge repairs.
- Modify `pkg/proxy/plugin_anthropic_messages_fix.go`: report thinking/tool-use repairs.
- Modify `pkg/proxy/plugin_anthropic_thinking.go`: report signature drops and synthesized lifecycle events.
- Create `examples/recipes/codex-litellm.yaml`: Codex through LiteLLM recipe.
- Create `examples/recipes/claude-code-litellm.yaml`: Claude Code through LiteLLM recipe.
- Create `examples/recipes/opencode-openai-compatible.yaml`: opencode through OpenAI-compatible provider recipe.
- Create `docs/compatibility-matrix.md`: client/upstream/problem/plugin matrix.
- Create `pkg/proxy/testdata/sse/*.sse`: regression corpus for representative broken and valid streams.

## Task 1: Shared Diagnostics Foundation

**Files:**
- Create: `pkg/proxy/diagnostics.go`
- Create: `pkg/proxy/diagnostics_test.go`

- [ ] **Step 1: Write failing diagnostics tests**

Create tests that verify:

```go
func TestRedactSensitiveText(t *testing.T)
func TestRepairReportString(t *testing.T)
func TestPluginTraceString(t *testing.T)
func TestJSONShapeDiff(t *testing.T)
func TestTruncateSnippet(t *testing.T)
```

Expected behaviors:

- `Authorization: Bearer sk-abc` becomes `Authorization: <redacted>`.
- JSON keys `api_key`, `access_token`, `refresh_token`, and `password` are redacted.
- Repair reports format as `[repair] plugin=codex-fix request=/v1/chat/completions actions=content_array_to_string:2`.
- Plugin trace records rule name, plugin name, phase, error state, and modification labels.
- JSON shape diff reports type/key-level changes without dumping full body values.
- Snippets are truncated to the configured maximum and redacted before logging.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./pkg/proxy -run 'TestRedactSensitiveText|TestRepairReportString|TestPluginTraceString|TestJSONShapeDiff|TestTruncateSnippet' -v`

Expected: FAIL because diagnostics helpers do not exist yet.

- [ ] **Step 3: Implement diagnostics helpers**

Implement:

```go
type RepairAction struct { Name string; Count int; Detail string }
type RepairReport struct { Plugin string; Request string; Actions []RepairAction; Notes []string }
type PluginTraceEntry struct { Rule string; Phase string; Plugin string; Modified []string; Err error }
type PluginTrace struct { Entries []PluginTraceEntry }

func RedactSensitiveText(s string) string
func TruncateSnippet(s string, max int) string
func RedactedSnippet(s string, max int) string
func (r RepairReport) String() string
func (t PluginTrace) String() string
func JSONShapeDiff(before, after []byte) []string
```

Keep all helpers deterministic and free of request mutation.

- [ ] **Step 4: Run tests and verify pass**

Run: `go test ./pkg/proxy -run 'TestRedactSensitiveText|TestRepairReportString|TestPluginTraceString|TestJSONShapeDiff|TestTruncateSnippet' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/proxy/diagnostics.go pkg/proxy/diagnostics_test.go
git commit -m "feat: add protocol diagnostics helpers"
```

## Task 2: Plugin Chain Trace and Repair Report Wiring

**Files:**
- Modify: `pkg/proxy/server.go`
- Modify: `pkg/proxy/plugin_codex.go`
- Modify: `pkg/proxy/plugin_force_stream.go`
- Modify: `pkg/proxy/plugin_responses_api.go`
- Modify: `pkg/proxy/plugin_anthropic_messages_fix.go`
- Modify: `pkg/proxy/plugin_anthropic_thinking.go`
- Test: existing plugin tests plus new focused trace test in `pkg/proxy/diagnostics_test.go`

- [ ] **Step 1: Write failing trace/report tests**

Add tests that verify a `PluginTrace` entry is produced for request and response phases, errors are recorded, and repair reports do not include unredacted API keys.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./pkg/proxy -run 'TestPluginTrace|TestRepairReport' -v`

Expected: FAIL until server and plugins use the diagnostics helpers.

- [ ] **Step 3: Wire trace in server**

In `server.go`:

- Before running request plugins, create a trace object for the request.
- Compare request path/body/header state before and after each plugin using safe structural labels.
- Log trace in verbose mode with `s.Logger.Printf("[trace] %s", trace.String())`.
- Do the same for response plugins, including wrapping labels like `wrapped_sse`, `status_unchanged`, or `error`.
- Keep existing plugin interfaces unchanged.

- [ ] **Step 4: Add repair reports to existing plugins**

Add report logs in verbose mode for concrete repairs:

- `codex-fix`: model substitution and assistant content array flattening.
- `force-stream`: inserted `stream:true` and `Accept:text/event-stream`.
- `responses-api`: request path/body bridge and response stream/non-stream bridge.
- `anthropic-messages-fix`: thinking stripping/keeping and XML tool call extraction.
- `anthropic-thinking-fix`: dropped `signature_delta`, synthesized `content_block_stop`, `message_delta`, and `message_stop`.

- [ ] **Step 5: Run full proxy tests**

Run: `go test ./pkg/proxy -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/proxy/server.go pkg/proxy/plugin_codex.go pkg/proxy/plugin_force_stream.go pkg/proxy/plugin_responses_api.go pkg/proxy/plugin_anthropic_messages_fix.go pkg/proxy/plugin_anthropic_thinking.go pkg/proxy/diagnostics_test.go
git commit -m "feat: trace protocol repair plugins"
```

## Task 3: Passive SSE Stream Validator

**Files:**
- Create: `pkg/proxy/sse_validator.go`
- Create: `pkg/proxy/sse_validator_test.go`
- Modify: `pkg/proxy/server.go`
- Create: `pkg/proxy/testdata/sse/anthropic-valid.sse`
- Create: `pkg/proxy/testdata/sse/openai-chat-valid.sse`
- Create: `pkg/proxy/testdata/sse/openai-responses-valid.sse`
- Create: `pkg/proxy/testdata/sse/openai-responses-missing-terminal.sse`

- [ ] **Step 1: Write failing SSE validator tests**

Tests must cover:

- valid Anthropic Messages stream;
- valid OpenAI Chat Completions stream;
- valid OpenAI Responses stream;
- malformed JSON in an SSE frame;
- unknown event in a known family;
- out-of-order lifecycle event;
- missing terminal event at EOF;
- pass-through bytes unchanged.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./pkg/proxy -run 'TestSSE' -v`

Expected: FAIL because validator does not exist.

- [ ] **Step 3: Implement passive validator**

Implement a validating `io.ReadCloser` wrapper that:

- forwards bytes unchanged;
- parses complete SSE frames;
- detects protocol family from documented event names and documented payload shapes;
- validates lifecycle ordering only;
- logs anomalies through a provided logger callback;
- logs no sensitive headers;
- validates EOF terminal state;
- never mutates, blocks, synthesizes, or repairs events.

- [ ] **Step 4: Wire validator after response plugins**

In `server.go`, after configured response plugins run and only when `s.Verbose` is true and `Content-Type` contains `text/event-stream`, wrap `resp.Body` with the validator.

- [ ] **Step 5: Run tests**

Run: `go test ./pkg/proxy -run 'TestSSE' -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/proxy/sse_validator.go pkg/proxy/sse_validator_test.go pkg/proxy/server.go pkg/proxy/testdata/sse
git commit -m "feat: validate sse protocol streams"
```

## Task 4: OpenAI Tool Calls Stream Fix Plugin

**Files:**
- Create: `pkg/proxy/plugin_openai_tool_calls_fix.go`
- Create: `pkg/proxy/plugin_openai_tool_calls_fix_test.go`
- Modify: `pkg/proxy/plugin.go`

- [ ] **Step 1: Write failing plugin tests**

Tests must verify:

- plugin only touches Chat Completions SSE responses when its request marker was set;
- missing `tool_calls[].index` is filled using stable position;
- non-string function arguments are JSON-encoded into strings;
- missing tool call id/type is carried forward when later chunks provide it;
- non-tool chunks pass through unchanged;
- output remains valid SSE.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./pkg/proxy -run 'TestOpenAIToolCallsFix' -v`

Expected: FAIL because plugin does not exist.

- [ ] **Step 3: Implement plugin**

Implement `openai-tool-calls-fix` as a request and response plugin:

- request phase marks eligible `/v1/chat/completions` requests and disables compression;
- response phase wraps only successful SSE responses for marked requests;
- stream loop parses Chat Completions chunks;
- repair is limited to tool-call delta shape problems;
- plugin emits structured repair reports in verbose mode.

- [ ] **Step 4: Register plugin**

Register in `plugin.go` for request and response plugin registries.

- [ ] **Step 5: Run tests**

Run: `go test ./pkg/proxy -run 'TestOpenAIToolCallsFix|TestPluginRegistry' -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/proxy/plugin_openai_tool_calls_fix.go pkg/proxy/plugin_openai_tool_calls_fix_test.go pkg/proxy/plugin.go
git commit -m "feat: repair openai tool call streams"
```

## Task 5: Recipes and Compatibility Matrix

**Files:**
- Create: `examples/recipes/codex-litellm.yaml`
- Create: `examples/recipes/claude-code-litellm.yaml`
- Create: `examples/recipes/opencode-openai-compatible.yaml`
- Create: `docs/compatibility-matrix.md`

- [ ] **Step 1: Add recipe files**

Each recipe must include only current proxy concepts: `match`, `upstream`, `plugins`, `response_plugins`, `rules`, `verbose`, and `log-file`. Recipes must not configure provider routing, models aggregation, dashboards, or quota systems.

- [ ] **Step 2: Add compatibility matrix**

Document:

- Codex + LiteLLM Chat Completions only → `responses-api`.
- Claude Code + LiteLLM Anthropic-compatible endpoint → `anthropic-messages-fix`.
- Anthropic thinking stream with bad signatures/lifecycle → `anthropic-thinking-fix`.
- OpenAI-compatible tool-call stream shape problems → `openai-tool-calls-fix`.
- Missing `stream:true` when `stream_options` is present → `force-stream`.
- Passive debugging for SSE lifecycle → verbose built-in SSE validator.

- [ ] **Step 3: Validate YAML parsing**

Run: `go test ./pkg/config -v`

Expected: PASS. The config package should continue parsing existing config shapes.

- [ ] **Step 4: Commit**

```bash
git add examples/recipes docs/compatibility-matrix.md
git commit -m "docs: add protocol repair recipes"
```

## Task 6: Full Verification

**Files:**
- All modified files

- [ ] **Step 1: Run full test suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 2: Inspect product boundary**

Run: `rg -n "models|gateway|routing|quota|dashboard|Langfuse|Phoenix|OpenTelemetry|otel" pkg docs examples AGENTS.md`

Expected: only boundary docs, rejected non-goals, or recipe-neutral text; no implementation of general gateway responsibilities.

- [ ] **Step 3: Inspect git history**

Run: `git log --oneline --decorate -8`

Expected: task commits exist on `feat/protocol-repair-suite`.

- [ ] **Step 4: Commit any verification docs if needed**

Only commit if verification required doc corrections.

## Self-Review Notes

- Every proposal selected as valuable is covered: repair reports, plugin chain trace, SSE validator, LiteLLM recipes, OpenAI tool-call stream fix, compatibility matrix, regression corpus, redaction, and structured diagnostics.
- The plan keeps protocol conversion limited to concrete compatibility patches.
- No task adds a general gateway, provider router, quota system, dashboard, or model aggregation.
