# PROJECT KNOWLEDGE BASE

**Generated:** 2026-05-23
**Commit:** bb41e4f
**Branch:** main

## OVERVIEW

Go-based MITM proxy for AI developers that intercepts, modifies, and adapts LLM API requests. Supports OpenAI Responses API → Chat Completions conversion, Anthropic thinking event fix, Codex content format fix, and streaming protocol enhancement. Bypasses TLS verification intentionally — NOT for production use.

## PRODUCT BOUNDARY

devproxy is a local AI protocol repair and diagnostics layer, not a general AI gateway.

Keep the product centered on fixing real client/provider protocol incompatibilities in local developer workflows, especially for Codex, Claude Code, opencode, and similar agent tools. Common examples include:

- A client requires the OpenAI Responses API, but the chosen provider only exposes Chat Completions.
- A provider emits malformed or incomplete SSE lifecycle events.
- A client rejects provider-specific content shapes, thinking deltas, signatures, or tool-call stream details.
- A developer needs verbose protocol diagnostics to identify where an upstream response deviates from the expected format.

Protocol conversion is in scope when it is a compatibility patch for a concrete client/provider mismatch, such as Responses API ↔ Chat Completions bridging for Codex. It should remain narrow, explicit, and testable.

Do not evolve devproxy into a LiteLLM/New API/One API replacement. In particular, avoid adding platform-gateway responsibilities such as provider marketplace management, unified `/v1/models` aggregation, multi-provider routing and fallback, quota/billing systems, dashboards, multi-user key management, or OpenTelemetry/Langfuse/Phoenix observability as core product directions.

Assume LiteLLM Proxy or similar upstream gateways handle general provider aggregation, routing, fallback, cost tracking, and model management. devproxy may sit in front of or behind those systems to repair and diagnose protocol behavior, but should not duplicate their gateway role.

## ARCHITECTURE

The system uses a **two-process model**:
- **Parent process**: CLI entry point (Cobra), config loading, spawns proxy worker + target command
- **Proxy worker process** (`__internal_proxy_worker`): Runs the MITM proxy server in an isolated subprocess for log isolation

**Request flow:**
1. Target process sends HTTP/HTTPS request (via injected env vars: `HTTP_PROXY`, `HTTPS_PROXY`, etc.)
2. Proxy receives request → URL normalization → rule matching → execute `RequestPlugin.ProcessRequest()` → header rewriting → forward to upstream
3. Upstream responds → rule matching (from `ctx.UserData` or re-match) → execute `ResponsePlugin.ProcessResponse()` → return to client

**Key architectural decisions:**
- Process isolation: proxy worker runs as a child subprocess, stdout/stderr physically redirected
- goproxy library provides MITM foundation; devproxy adds rule engine, plugins, and streaming transforms
- `io.Pipe()` pattern for streaming response transformation (goroutine reads original, writes transformed)
- Buffer pool (`sync.Pool`) for request body buffering to reduce GC pressure
- MITM host index (`mitmHosts` map) for O(1) CONNECT-stage decisions

## STRUCTURE

```
devproxy/
├── main.go                  # Entry point (delegates to cmd/root.go)
├── cmd/
│   └── root.go              # Cobra CLI: config loading, process orchestration
│   └── AGENTS.md            # CLI-specific notes
├── pkg/
│   ├── config/
│   │   └── config.go        # YAML config loading (RuleConfig struct)
│   ├── proxy/               # MITM proxy server + plugins + matching
│   │   ├── plugin.go                  # RequestPlugin/ResponsePlugin interfaces & registries
│   │   ├── plugin_responses_api.go    # Responses API → Chat Completions (bidirectional)
│   │   ├── plugin_openai_responses.go # Chat Completions → Responses API (reverse direction)
│   │   ├── plugin_codex.go            # Codex content format fix + model substitution
│   │   ├── plugin_anthropic_thinking.go # Anthropic thinking event lifecycle fix
│   │   ├── plugin_force_stream.go     # Force stream:true when stream_options present
│   │   ├── server.go                  # ProxyServer core (MITM, rule engine, request/response hooks)
│   │   ├── matcher.go                 # URLMatcher interface, RegexMatcher, StringMatcher
│   │   ├── rewriter.go                # HeaderRewriter with pre-computed key/value bytes
│   │   ├── buffer_pool.go             # sync.Pool-based buffer recycling
│   │   ├── cert_cache.go              # LRU+TTL TLS certificate cache
│   │   └── *_test.go                  # Tests alongside source
│   ├── process/
│   │   ├── launcher.go      # ProcessLauncher: env var injection, subprocess lifecycle
│   │   └── certs.go         # Export goproxy CA cert to temp file
│   └── util/
│       ├── version.go       # Version string (set via -ldflags)
│       ├── port.go          # Random port allocation
│       ├── ansi.go          # ANSI escape code stripping
│       ├── io.go            # FD-level stream hijacking (dup2)
│       ├── sys_*.go         # Platform-specific dup/dup2 wrappers
│       └── trust_cert_mac.sh # macOS cert trust script
├── examples/                # Sample configs and test scripts
├── docs/                    # Architecture docs and plans
├── scripts/                 # Benchmarks and test scripts
├── Makefile                 # Build, release, cross-build, clean
├── AGENTS.md                # This file
├── README.md                # English README
├── README_CN.md             # Chinese README
└── go.mod                   # Go 1.25.6, deps: goproxy, cobra, yaml, x/net
```

## WHERE TO LOOK

| Task | Location | Notes |
|------|----------|-------|
| Add new CLI flag | `cmd/root.go` init() | Uses Cobra CLI framework |
| Add new plugin | `pkg/proxy/plugin_*.go` + register in `plugin.go` init() | Implement RequestPlugin/ResponsePlugin interface |
| Modify URL matching | `pkg/proxy/matcher.go` | RegexMatcher/StringMatcher + URL normalization |
| Modify MITM decision logic | `pkg/proxy/server.go` ShouldMITM() | O(1) host lookup via mitmHosts map |
| Modify request interception | `pkg/proxy/server.go` OnRequest handler | Rule matching, plugin execution, header rewriting |
| Modify response interception | `pkg/proxy/server.go` OnResponse handler | ResponsePlugin execution, logging |
| Configuration loading | `cmd/root.go` loadConfig() | 4 sources: global/dir/explicit/CLI, merged sequentially |
| Config struct | `pkg/config/config.go` | RuleConfig, YAML fields |
| Process launching | `pkg/process/launcher.go` | Env var injection, TLS bypass vars |
| Certificate management | `pkg/process/certs.go` | Export goproxy CA to temp file |
| Terminal I/O hijacking | `pkg/util/io.go` | FD-level stream redirection via dup2 |
| Streaming transform pattern | `pkg/proxy/plugin_*_responses.go` handleStream() | io.Pipe() + goroutine pattern |

## PLUGIN SYSTEM

### Interfaces

```go
type RequestPlugin interface {
    Name() string
    ProcessRequest(req *http.Request) error
}

type ResponsePlugin interface {
    Name() string
    ProcessResponse(resp *http.Response, ctx *goproxy.ProxyCtx, verbose bool) error
}
```

### Registration

Plugins are registered in `plugin.go` init() using two global maps:
- `RequestPluginRegistry` (map[string]RequestPlugin)
- `ResponsePluginRegistry` (map[string]ResponsePlugin)

Lookup via `GetPlugin(name)` / `GetResponsePlugin(name)` supports `"name:param"` format (currently only `codex-fix:model_name` uses params).

### Built-in Plugins

| Plugin Name | Type(s) | Purpose |
|-------------|---------|---------|
| `responses-api` | Request + Response | Bidirectional `/v1/responses` ↔ `/v1/chat/completions` conversion |
| `openai-responses` | Request + Response | Reverse direction: Chat Completions → Responses API format |
| `codex-fix` | Request | Flatten content arrays for Codex; optional model substitution (`codex-fix:target_model`) |
| `anthropic-thinking-fix` | Request + Response | Fix Anthropic streaming: drop signature_delta, patch missing lifecycle events, handle parallel tool call bug |
| `force-stream` | Request | Force `stream:true` when request contains `stream_options` but missing `stream` field |

### Adding a New Plugin

1. Create `pkg/proxy/plugin_<name>.go`
2. Implement `RequestPlugin` (and optionally `ResponsePlugin`)
3. Register in `plugin.go` init(): `RegisterPlugin(&YourPlugin{})` and/or `RegisterResponsePlugin(&YourPlugin{})`
4. Add tests in `pkg/proxy/plugin_<name>_test.go`

## STREAMING PROTOCOL DETAILS

All streaming plugins use the `io.Pipe()` pattern:
- `reader, writer := io.Pipe()` — replace `resp.Body` with reader
- Goroutine reads original body line-by-line, transforms, writes to pipe writer
- SSE buffer size: 4KB (minimized for low TTFB; previous 1MB caused latency)
- Client disconnect detection: check `writer.Write()` errors, bail early

**Responses API streaming event sequence:**
`response.created` → `response.output_item.added` → `response.content_part.added` → `response.output_text.delta` (repeated) → `response.output_text.done` → `response.content_part.done` → `response.output_item.done` → `response.completed`

**Anthropic streaming fix specifics:**
- Drops `signature_delta` events (fake signatures cause client-side validation failures)
- Patches missing `message_delta`/`message_stop`/`content_block_stop` events
- Handles parallel tool call bug: when multiple tool calls arrive on same `index`, splits them into separate `content_block_start/stop` pairs with incremented indices

## CONFIGURATION SYSTEM

### YAML Config Format

```yaml
# Global settings
verbose: true
upstream: "http://127.0.0.1:7890"
port: 8080
log-file: "./proxy.log"

# Global match/overwrite (applied to default rule)
match:
  - "google.com"
overwrite:
  User-Agent: "devproxy/1.0"

# Global plugins
plugins:
  - "codex-fix"
  - "codex-fix:deepseek-chat"  # parametric plugin
response-plugins:
  - "openai-responses"

# Named rule groups (higher priority, separate matching)
rules:
  - name: "adapter-to-deepseek"
    match: ["https://api.openai.com/v1/responses"]
    plugins:
      - "responses-api"
    overwrite:
      Authorization: "Bearer sk-xxx"
  - name: "fix-codex-content"
    match: ["/chat/completions"]
    plugins:
      - "codex-fix"
```

### Config Source Priority (low → high)

1. Global: `~/.config/devproxy/global.yaml`
2. Directory: `devproxy.yaml` or `.devproxy.yaml` in cwd
3. Explicit: `--config <path>`
4. CLI flags: `--match`, `--overwrite`, etc.

Lists (match, overwrite, plugins, rules) are **accumulated** across sources. Single values (port, upstream, verbose) use last-wins.

## CODE MAP

| Symbol | Type | Location | Role |
|--------|------|----------|------|
| `ProxyServer` | struct | `server.go` | MITM proxy core with rule engine |
| `ProxyRule` | struct | `server.go` | Rule group: matchers + rewriters + plugins |
| `ShouldMITM` | func | `server.go` | O(1) MITM decision per host |
| `rebuildMITMIndex` | func | `server.go` | Rebuild host/pattern/regex index after rule changes |
| `URLMatcher` | interface | `matcher.go` | URL matching contract |
| `NormalizeURL` | func | `matcher.go` | Strip default ports (443/80), cached |
| `HeaderRewriter` | struct | `rewriter.go` | Pre-computed header key/value rewrite |
| `RequestPlugin` | interface | `plugin.go` | Request modification contract |
| `ResponsePlugin` | interface | `plugin.go` | Response modification contract |
| `RequestPluginRegistry` | var | `plugin.go` | Global request plugin map |
| `ResponsePluginRegistry` | var | `plugin.go` | Global response plugin map |
| `RegisterPlugin` | func | `plugin.go` | Register request plugin |
| `GetPlugin` | func | `plugin.go` | Lookup by name, supports "name:param" |
| `ResponsesAPIPlugin` | struct | `plugin_responses_api.go` | /v1/responses → /v1/chat/completions |
| `OpenAIResponsesPlugin` | struct | `plugin_openai_responses.go` | Chat Completions → Responses API |
| `CodexFixPlugin` | struct | `plugin_codex.go` | Content flattening + model swap |
| `AnthropicThinkingFixPlugin` | struct | `plugin_anthropic_thinking.go` | Anthropic stream lifecycle fix |
| `ForceStreamPlugin` | struct | `plugin_force_stream.go` | Force stream:true |
| `ProcessLauncher` | struct | `launcher.go` | Target subprocess lifecycle |
| `ExportProxyCA` | func | `certs.go` | Export goproxy CA to temp PEM |
| `HijackStandardStreams` | func | `io.go` | FD-level stdout/stderr redirection |

## CONVENTIONS

- **Chinese comments**: Code comments mix Chinese/English; log messages are primarily Chinese
- **Test location**: Tests live alongside source files (`*_test.go`)
- **Go version**: Requires Go 1.25.6+
- **No internal/**: All packages in `pkg/` (public by design)
- **Version injection**: `-X devproxy/pkg/util.Version=` via Makefile LDFLAGS
- **Buffer pool pattern**: Use `GetBuffer()`/`PutBuffer()` for request body reads, not raw `bytes.NewBuffer`
- **Pipe pattern for streaming**: All streaming plugins use `io.Pipe()` with goroutine — never buffer full response
- **Plugin dual registration**: Plugins that handle both request and response (e.g., responses-api) register in both registries

## ANTI-PATTERNS (THIS PROJECT)

1. **TLS bypass**: 12+ env vars disable certificate verification (`launcher.go:57-84`)
2. **Production warning**: README explicitly warns "仅适用于开发和测试环境"
3. **Fatal errors**: `log.Fatalf` used throughout `cmd/root.go`
4. **Global registries**: Plugin registries are package-level `map[string]` vars — not concurrent-safe for dynamic registration
5. **Hardcoded paths**: Makefile installs to `$(HOME)/.local/bin/` without override

## BUILD & TEST

```bash
make build          # Standard build with version injection
make build-opt      # Optimized build (-s -w flags, smaller binary)
make cross-build    # Cross-compile for linux/darwin/windows (amd64/arm64)
make release        # Build-opt + install to ~/.local/bin/
make clean          # Remove binaries and build dir

# Tests
go test ./pkg/proxy/   # Run proxy package tests
go test ./...           # Run all tests

# Lint
make lint               # Run golangci-lint (if configured)
```

## UNIQUE FEATURES

- **Plugin system**: Extensible request/response transformation with dual registries
- **Interactive app support**: Raw terminal mode, ANSI stripping
- **Multi-level config**: 4 config sources merged (global/dir/explicit/CLI)
- **Log isolation**: Proxy worker in separate subprocess, FD-level stream redirection
- **Dynamic MITM**: `ShouldMITM` with O(1) host lookup + pattern fallback
- **URL normalization**: Default ports (443/80) handled correctly with caching
- **SSE streaming**: io.Pipe pattern with 4KB buffer for real-time event transformation
- **Anthropic thinking fix**: Handles parallel tool call index collision bug
- **Responses API bidirectional**: Both forward (responses→chat) and reverse (chat→responses) conversion
