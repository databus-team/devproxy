package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/elazarl/goproxy"
)

type OpenAIToolCallsFixPlugin struct {
	Diagnose bool
}

func (p *OpenAIToolCallsFixPlugin) Name() string {
	return "openai-tool-calls-fix"
}

func (p *OpenAIToolCallsFixPlugin) ProcessRequest(req *http.Request, verbose bool) error {
	if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/v1/chat/completions") {
		return nil
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("X-DevProxy-OpenAI-Tool-Calls-Fix", "true")
	return nil
}

func (p *OpenAIToolCallsFixPlugin) ProcessResponse(resp *http.Response, ctx *goproxy.ProxyCtx, verbose bool, vverbose bool) error {
	if resp == nil || resp.Request == nil {
		return nil
	}
	if resp.Request.Header.Get("X-DevProxy-OpenAI-Tool-Calls-Fix") != "true" {
		return nil
	}
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

	go p.rewrite(originalBody, writer, verbose)
	return nil
}

type openAIToolCallState struct {
	ID   string
	Type string
	Name string
}

func (p *OpenAIToolCallsFixPlugin) rewrite(src io.ReadCloser, dst *io.PipeWriter, verbose bool) {
	defer src.Close()
	defer dst.Close()

	states := make(map[string]*openAIToolCallState)
	scanner := bufio.NewScanner(src)
	for scanner.Scan() {
		line := scanner.Text()
		output := line
		if strings.HasPrefix(line, "data: ") && strings.TrimSpace(strings.TrimPrefix(line, "data: ")) != "[DONE]" {
			fixed, actions, changed := p.fixDataLine(strings.TrimPrefix(line, "data: "), states)
			if changed {
				if !p.Diagnose {
					output = "data: " + fixed
				}
				if verbose {
					if p.Diagnose {
						for i := range actions {
							actions[i].Name = "would_" + actions[i].Name
						}
					}
					log.Print(RepairReport{
						Plugin:  p.Name(),
						Actions: actions,
					}.String())
				}
			}
		}
		if _, err := io.WriteString(dst, output+"\n"); err != nil {
			return
		}
	}
	if err := scanner.Err(); err != nil && verbose {
		log.Printf("[%s] scanner error: %v", p.Name(), err)
	}
}

func (p *OpenAIToolCallsFixPlugin) fixDataLine(data string, states map[string]*openAIToolCallState) (string, []RepairAction, bool) {
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return data, nil, false
	}

	changed := false
	actionCounts := map[string]int{}

	choices, _ := payload["choices"].([]interface{})
	for _, choiceVal := range choices {
		choice, ok := choiceVal.(map[string]interface{})
		if !ok {
			continue
		}
		choiceIndex := intFromPayload(choice, "index")
		delta, _ := choice["delta"].(map[string]interface{})
		toolCalls, _ := delta["tool_calls"].([]interface{})
		for position, callVal := range toolCalls {
			call, ok := callVal.(map[string]interface{})
			if !ok {
				continue
			}
			callIndex, hasIndex := numberAsInt(call["index"])
			if !hasIndex {
				callIndex = position
				call["index"] = callIndex
				changed = true
				actionCounts["insert_tool_call_index"]++
			}

			key := fmt.Sprintf("%d:%d", choiceIndex, callIndex)
			state := states[key]
			if state == nil {
				state = &openAIToolCallState{}
				states[key] = state
			}

			if id, ok := call["id"].(string); ok && id != "" {
				state.ID = id
			} else if state.ID != "" {
				call["id"] = state.ID
				changed = true
				actionCounts["carry_tool_call_id"]++
			}

			if callType, ok := call["type"].(string); ok && callType != "" {
				state.Type = callType
			} else if state.Type != "" {
				call["type"] = state.Type
				changed = true
				actionCounts["carry_tool_call_type"]++
			}

			function, _ := call["function"].(map[string]interface{})
			if function == nil {
				continue
			}
			if name, ok := function["name"].(string); ok && name != "" {
				state.Name = name
			} else if state.Name != "" {
				function["name"] = state.Name
				changed = true
				actionCounts["carry_tool_call_name"]++
			}
			if args, ok := function["arguments"]; ok {
				if _, isString := args.(string); !isString {
					encoded, err := json.Marshal(args)
					if err == nil {
						function["arguments"] = string(encoded)
						changed = true
						actionCounts["stringify_tool_call_arguments"]++
					}
				}
			}
		}
	}

	if !changed {
		return data, nil, false
	}

	fixed, err := json.Marshal(payload)
	if err != nil {
		return data, nil, false
	}
	actions := make([]RepairAction, 0, len(actionCounts))
	for _, name := range []string{
		"insert_tool_call_index",
		"stringify_tool_call_arguments",
		"carry_tool_call_id",
		"carry_tool_call_type",
		"carry_tool_call_name",
	} {
		if count := actionCounts[name]; count > 0 {
			actions = append(actions, RepairAction{Name: name, Count: count})
		}
	}
	return string(bytes.TrimSpace(fixed)), actions, true
}

func numberAsInt(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}
