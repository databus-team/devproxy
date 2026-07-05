package proxy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type RepairAction struct {
	Name   string
	Count  int
	Detail string
}

type RepairReport struct {
	Plugin  string
	Request string
	Actions []RepairAction
	Notes   []string
}

type PluginTraceEntry struct {
	Rule     string
	Phase    string
	Plugin   string
	Modified []string
	Err      error
}

type PluginTrace struct {
	Entries []PluginTraceEntry
}

var (
	authHeaderPattern = regexp.MustCompile(`(?im)^(Authorization|Proxy-Authorization|Cookie|Set-Cookie):\s*.*$`)
	jsonSecretPattern = regexp.MustCompile(`(?i)"(api_key|access_token|refresh_token|password|authorization|secret|token)"\s*:\s*"[^"]*"`)
	bearerPattern     = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]+`)
)

func RedactSensitiveText(s string) string {
	if s == "" {
		return s
	}

	redacted := authHeaderPattern.ReplaceAllStringFunc(s, func(line string) string {
		name := line
		if idx := strings.Index(line, ":"); idx >= 0 {
			name = line[:idx]
		}
		return name + ": <redacted>"
	})
	redacted = bearerPattern.ReplaceAllString(redacted, "Bearer <redacted>")
	redacted = jsonSecretPattern.ReplaceAllStringFunc(redacted, func(match string) string {
		idx := strings.Index(match, ":")
		if idx < 0 {
			return match
		}
		return match[:idx+1] + `"<redacted>"`
	})
	return redacted
}

func TruncateSnippet(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func RedactedSnippet(s string, max int) string {
	return TruncateSnippet(RedactSensitiveText(s), max)
}

func (r RepairReport) String() string {
	var b strings.Builder
	b.WriteString("[repair]")
	if r.Plugin != "" {
		b.WriteString(" plugin=")
		b.WriteString(r.Plugin)
	}
	if r.Request != "" {
		b.WriteString(" request=")
		b.WriteString(r.Request)
	}
	if len(r.Actions) > 0 {
		parts := make([]string, 0, len(r.Actions))
		for _, action := range r.Actions {
			count := action.Count
			if count <= 0 {
				count = 1
			}
			part := fmt.Sprintf("%s:%d", action.Name, count)
			if action.Detail != "" {
				part += "(" + RedactSensitiveText(action.Detail) + ")"
			}
			parts = append(parts, part)
		}
		b.WriteString(" actions=")
		b.WriteString(strings.Join(parts, ","))
	}
	if len(r.Notes) > 0 {
		notes := make([]string, 0, len(r.Notes))
		for _, note := range r.Notes {
			notes = append(notes, RedactSensitiveText(note))
		}
		b.WriteString(" notes=")
		b.WriteString(strings.Join(notes, ";"))
	}
	return b.String()
}

func (t *PluginTrace) Add(entry PluginTraceEntry) {
	if t == nil {
		return
	}
	t.Entries = append(t.Entries, entry)
}

func (t PluginTrace) String() string {
	if len(t.Entries) == 0 {
		return "no plugin activity"
	}

	parts := make([]string, 0, len(t.Entries))
	for _, entry := range t.Entries {
		var b strings.Builder
		if entry.Rule != "" {
			b.WriteString("rule=")
			b.WriteString(entry.Rule)
			b.WriteString(" ")
		}
		if entry.Phase != "" {
			b.WriteString("phase=")
			b.WriteString(entry.Phase)
			b.WriteString(" ")
		}
		b.WriteString("plugin=")
		b.WriteString(entry.Plugin)
		if len(entry.Modified) > 0 {
			b.WriteString(" modified=")
			b.WriteString(strings.Join(entry.Modified, ","))
		}
		if entry.Err != nil {
			b.WriteString(" error=")
			b.WriteString(RedactSensitiveText(entry.Err.Error()))
		}
		parts = append(parts, strings.TrimSpace(b.String()))
	}
	return strings.Join(parts, " | ")
}

func JSONShapeDiff(before, after []byte) []string {
	var beforeVal interface{}
	var afterVal interface{}
	if err := json.Unmarshal(before, &beforeVal); err != nil {
		return nil
	}
	if err := json.Unmarshal(after, &afterVal); err != nil {
		return nil
	}

	var diffs []string
	collectShapeDiff("", beforeVal, afterVal, &diffs)
	return diffs
}

func collectShapeDiff(path string, before, after interface{}, diffs *[]string) {
	beforeKind := jsonShapeKind(before)
	afterKind := jsonShapeKind(after)
	if beforeKind != afterKind {
		*diffs = append(*diffs, fmt.Sprintf("changed %s %s -> %s", path, beforeKind, afterKind))
		return
	}

	switch beforeTyped := before.(type) {
	case map[string]interface{}:
		afterTyped, _ := after.(map[string]interface{})
		keys := make(map[string]bool, len(beforeTyped)+len(afterTyped))
		for key := range beforeTyped {
			keys[key] = true
		}
		for key := range afterTyped {
			keys[key] = true
		}
		sorted := make([]string, 0, len(keys))
		for key := range keys {
			sorted = append(sorted, key)
		}
		sort.Strings(sorted)
		for _, key := range sorted {
			childPath := joinJSONPath(path, key)
			beforeChild, beforeOK := beforeTyped[key]
			afterChild, afterOK := afterTyped[key]
			switch {
			case !beforeOK:
				*diffs = append(*diffs, fmt.Sprintf("added %s %s", childPath, jsonShapeKind(afterChild)))
			case !afterOK:
				*diffs = append(*diffs, fmt.Sprintf("removed %s %s", childPath, jsonShapeKind(beforeChild)))
			default:
				collectShapeDiff(childPath, beforeChild, afterChild, diffs)
			}
		}
	case []interface{}:
		afterTyped, _ := after.([]interface{})
		limit := len(beforeTyped)
		if len(afterTyped) < limit {
			limit = len(afterTyped)
		}
		for i := 0; i < limit; i++ {
			collectShapeDiff(fmt.Sprintf("%s[%d]", path, i), beforeTyped[i], afterTyped[i], diffs)
		}
		if len(beforeTyped) != len(afterTyped) {
			*diffs = append(*diffs, fmt.Sprintf("changed %s length %d -> %d", path, len(beforeTyped), len(afterTyped)))
		}
	default:
		if path != "" && !jsonPrimitiveEqual(before, after) {
			*diffs = append(*diffs, fmt.Sprintf("changed %s %s", path, beforeKind))
		}
	}
}

func joinJSONPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func jsonShapeKind(v interface{}) string {
	switch v.(type) {
	case map[string]interface{}:
		return "object"
	case []interface{}:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case nil:
		return "null"
	default:
		return "unknown"
	}
}

func jsonPrimitiveEqual(a, b interface{}) bool {
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case nil:
		return b == nil
	default:
		return false
	}
}
