package proxy

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactSensitiveText(t *testing.T) {
	input := `Authorization: Bearer sk-secret
{"api_key":"sk-json","access_token":"access-secret","refresh_token":"refresh-secret","password":"password-secret","safe":"value"}`

	got := RedactSensitiveText(input)

	for _, secret := range []string{"sk-secret", "sk-json", "access-secret", "refresh-secret", "password-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted text still contains secret %q: %s", secret, got)
		}
	}
	for _, want := range []string{"Authorization: <redacted>", `"api_key":"<redacted>"`, `"safe":"value"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted text missing %q: %s", want, got)
		}
	}
}

func TestRepairReportString(t *testing.T) {
	report := RepairReport{
		Plugin:  "codex-fix",
		Request: "/v1/chat/completions",
		Actions: []RepairAction{
			{Name: "content_array_to_string", Count: 2},
			{Name: "model_substitution", Count: 1, Detail: "gpt-5 -> deepseek-chat"},
		},
		Notes: []string{"Authorization: Bearer sk-secret"},
	}

	got := report.String()

	if !strings.Contains(got, "[repair] plugin=codex-fix request=/v1/chat/completions") {
		t.Fatalf("unexpected report prefix: %s", got)
	}
	if !strings.Contains(got, "actions=content_array_to_string:2,model_substitution:1(gpt-5 -> deepseek-chat)") {
		t.Fatalf("unexpected action list: %s", got)
	}
	if strings.Contains(got, "sk-secret") {
		t.Fatalf("report leaked secret: %s", got)
	}
}

func TestPluginTraceString(t *testing.T) {
	trace := PluginTrace{}
	trace.Add(PluginTraceEntry{
		Rule:     "codex-to-litellm",
		Phase:    "request",
		Plugin:   "responses-api",
		Modified: []string{"path", "body"},
	})
	trace.Add(PluginTraceEntry{
		Rule:   "codex-to-litellm",
		Phase:  "response",
		Plugin: "responses-api",
		Err:    errors.New("boom"),
	})

	got := trace.String()

	for _, want := range []string{
		"rule=codex-to-litellm phase=request plugin=responses-api modified=path,body",
		"rule=codex-to-litellm phase=response plugin=responses-api error=boom",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("trace missing %q: %s", want, got)
		}
	}
}

func TestDiffRequestTraceIncludesJSONShapeDiff(t *testing.T) {
	before := requestTraceSnapshot{
		bodyShape: "before",
		body:      []byte(`{"messages":[{"role":"assistant","content":[{"type":"text","text":"hi"}]}]}`),
	}
	after := requestTraceSnapshot{
		bodyShape: "after",
		body:      []byte(`{"messages":[{"role":"assistant","content":"hi"}]}`),
	}

	got := strings.Join(diffRequestTrace(before, after), "\n")

	if !strings.Contains(got, "body") {
		t.Fatalf("expected body label: %s", got)
	}
	if !strings.Contains(got, "body:changed messages[0].content array -> string") {
		t.Fatalf("expected JSON shape diff label: %s", got)
	}
	if strings.Contains(got, "hi") {
		t.Fatalf("shape diff leaked body value: %s", got)
	}
}

func TestJSONShapeDiff(t *testing.T) {
	before := []byte(`{"model":"a","messages":[{"role":"assistant","content":[{"type":"text","text":"hi"}]}],"keep":true}`)
	after := []byte(`{"model":"b","messages":[{"role":"assistant","content":"hi"}],"stream":true}`)

	got := JSONShapeDiff(before, after)
	joined := strings.Join(got, "\n")

	for _, want := range []string{
		"changed model string",
		"changed messages[0].content array -> string",
		"removed keep bool",
		"added stream bool",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("shape diff missing %q: %#v", want, got)
		}
	}
	if strings.Contains(joined, "hi") {
		t.Fatalf("shape diff leaked body value: %s", joined)
	}
}

func TestTruncateSnippet(t *testing.T) {
	got := RedactedSnippet("Authorization: Bearer sk-secret\nabcdefghijklmnopqrstuvwxyz", 32)

	if strings.Contains(got, "sk-secret") {
		t.Fatalf("snippet leaked secret: %s", got)
	}
	if len(got) > 35 {
		t.Fatalf("snippet was not truncated: len=%d value=%q", len(got), got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncated snippet should end with ellipsis: %q", got)
	}
}

func TestDiagnosePluginParameters(t *testing.T) {
	codex, err := GetPlugin("codex-fix:deepseek-chat,diagnose=true")
	if err != nil {
		t.Fatal(err)
	}
	codexFix, ok := codex.(*CodexFixPlugin)
	if !ok {
		t.Fatalf("expected CodexFixPlugin, got %T", codex)
	}
	if !codexFix.Diagnose || codexFix.TargetModel != "deepseek-chat" {
		t.Fatalf("unexpected codex diagnose plugin: %#v", codexFix)
	}

	codexKeyValue, err := GetPlugin("codex-fix:model=glm-4.5,diagnose=true")
	if err != nil {
		t.Fatal(err)
	}
	codexKeyValueFix, ok := codexKeyValue.(*CodexFixPlugin)
	if !ok || !codexKeyValueFix.Diagnose || codexKeyValueFix.TargetModel != "glm-4.5" {
		t.Fatalf("unexpected key-value codex diagnose plugin: %#v", codexKeyValue)
	}

	force, err := GetPlugin("force-stream:diagnose=true")
	if err != nil {
		t.Fatal(err)
	}
	forceStream, ok := force.(*ForceStreamPlugin)
	if !ok || !forceStream.Diagnose {
		t.Fatalf("unexpected force-stream diagnose plugin: %#v", force)
	}

	toolReq, err := GetPlugin("openai-tool-calls-fix:diagnose")
	if err != nil {
		t.Fatal(err)
	}
	toolReqPlugin, ok := toolReq.(*OpenAIToolCallsFixPlugin)
	if !ok || !toolReqPlugin.Diagnose {
		t.Fatalf("unexpected request tool diagnose plugin: %#v", toolReq)
	}

	toolResp, err := GetResponsePlugin("openai-tool-calls-fix:diagnose")
	if err != nil {
		t.Fatal(err)
	}
	toolRespPlugin, ok := toolResp.(*OpenAIToolCallsFixPlugin)
	if !ok || !toolRespPlugin.Diagnose {
		t.Fatalf("unexpected response tool diagnose plugin: %#v", toolResp)
	}
}
