package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// CodexFixPlugin 针对 Codex 客户端由于发出了包含对象数组的 messages 而导致的 validation errors 进行修复
// 它会将 messages 中的 content 对象数组拼接为纯文本字符串，并可选地替换 model 字段
type CodexFixPlugin struct {
	TargetModel string // 若不为空，则将请求中的 model 字段替换为此值
	Diagnose    bool   // 仅诊断会发生的修复，不修改请求
}

func (c *CodexFixPlugin) Name() string {
	return "codex-fix"
}

func (c *CodexFixPlugin) ProcessRequest(req *http.Request, verbose bool) error {
	// 只处理 POST 请求
	if req.Method != http.MethodPost {
		return nil
	}

	// 只处理 JSON 请求
	contentType := req.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		return nil
	}

	if req.Body == nil {
		return nil
	}

	// 1. 读取原始 Body
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return fmt.Errorf("codex-fix: 读取请求体失败: %w", err)
	}

	if verbose {
		log.Printf("[codex-fix] 收到请求, 原始 Body 长度: %d", len(bodyBytes))
	}

	// 关键修复：提供 GetBody 以支持重试，防止 502
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyBytes)), nil
	}
	req.Body, _ = req.GetBody()

	// 如果 body 太短或者不像是合法的 JSON，跳过
	if len(bodyBytes) == 0 {
		return nil
	}

	// 2. 解析 JSON
	var payload map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		if verbose {
			log.Printf("[codex-fix] JSON 解析失败: %v", err)
		}
		return nil
	}

	contentArrayCountBefore := countAssistantContentArrays(payload)
	modelBefore, _ := payload["model"].(string)
	if c.Diagnose {
		report := RepairReport{
			Plugin:  c.Name(),
			Request: req.URL.Path,
		}
		if contentArrayCountBefore > 0 {
			report.Actions = append(report.Actions, RepairAction{
				Name:  "would_content_array_to_string",
				Count: contentArrayCountBefore,
			})
		}
		if c.TargetModel != "" && modelBefore != "" && modelBefore != c.TargetModel {
			report.Actions = append(report.Actions, RepairAction{
				Name:   "would_model_substitution",
				Count:  1,
				Detail: fmt.Sprintf("%s -> %s", modelBefore, c.TargetModel),
			})
		}
		if verbose && len(report.Actions) > 0 {
			log.Print(report.String())
		}
		return nil
	}

	// 3. 寻找并处理消息容器 (递归或特定路径)
	modified := c.processPayload(payload, verbose)

	if !modified {
		// 如果顶层没找到，尝试在 input 或其他大字段内部找 (Codex 可能存在嵌套结构)
		for _, nestedField := range []string{"input", "input_data"} {
			if nested, ok := payload[nestedField].(map[string]interface{}); ok {
				if verbose {
					log.Printf("[codex-fix] 在嵌套字段 %s 中继续寻找...", nestedField)
				}
				if c.processPayload(nested, verbose) {
					payload[nestedField] = nested
					modified = true
				}
			}
		}
	}

	// 如果没有修改任何内容，直接返回
	if !modified {
		if verbose {
			log.Printf("[codex-fix] 遍历完成, 无需修改. 现有 Key: %v", getMapKeys(payload))
		}
		return nil
	}

	// 5. 将修改后的 payload 序列化写回 req.Body
	if verbose {
		log.Printf("[codex-fix] 即将重新序列化请求体 (Payload 已修改)")
	}
	newBodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("codex-fix: 重新序列化请求体失败: %w", err)
	}

	// 更新请求体和 ContentLength，并提供新的 GetBody
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(newBodyBytes)), nil
	}
	req.Body, _ = req.GetBody()

	// 设置 ContentLength 数值，但移除 Header 级的显式设置，让 net/http 传输层自动处理
	// 同时确保删除 Transfer-Encoding 以满足固定长度 Request 的规范
	req.ContentLength = int64(len(newBodyBytes))
	req.Header.Del("Transfer-Encoding")

	if verbose {
		log.Printf("[codex-fix] 请求已成功重写并重新包装 (新长度: %d)", req.ContentLength)
		report := RepairReport{
			Plugin:  c.Name(),
			Request: req.URL.Path,
		}
		contentArrayCountAfter := countAssistantContentArrays(payload)
		if contentArrayCountBefore > contentArrayCountAfter {
			report.Actions = append(report.Actions, RepairAction{
				Name:  "content_array_to_string",
				Count: contentArrayCountBefore - contentArrayCountAfter,
			})
		}
		if c.TargetModel != "" && modelBefore != "" && modelBefore != c.TargetModel {
			report.Actions = append(report.Actions, RepairAction{
				Name:   "model_substitution",
				Count:  1,
				Detail: fmt.Sprintf("%s -> %s", modelBefore, c.TargetModel),
			})
		}
		if len(report.Actions) == 0 {
			report.Actions = append(report.Actions, RepairAction{Name: "body_rewrite", Count: 1})
		}
		log.Print(report.String())
	}
	return nil
}

// processPayload 在 map 中寻找并处理已知格式的消息数组，以及可选的 model 替换
func (c *CodexFixPlugin) processPayload(p map[string]interface{}, verbose bool) bool {
	modified := false

	// 替换 model 字段
	if c.TargetModel != "" {
		if currentModel, ok := p["model"]; ok {
			if currentModel != c.TargetModel {
				if verbose {
					log.Printf("[codex-fix] 替换 model 字段: %v -> %s", currentModel, c.TargetModel)
				}
				p["model"] = c.TargetModel
				modified = true
			}
		}
	}

	// 展平 content 数组
	for _, field := range []string{"messages", "input", "history"} {
		if val, ok := p[field]; ok {
			if msgs, ok := val.([]interface{}); ok {
				if verbose {
					log.Printf("[codex-fix] 发现数组字段 %s (长度: %d), 正在处理...", field, len(msgs))
				}
				if c.processMessageArray(msgs, verbose) {
					p[field] = msgs
					modified = true
				}
			}
		}
	}
	return modified
}

// processMessageArray 处理消息数组（仅展平 content），返回是否有修改
func (c *CodexFixPlugin) processMessageArray(messages []interface{}, verbose bool) bool {
	modified := false

	for i, msgIntf := range messages {
		msg, ok := msgIntf.(map[string]interface{})
		if !ok {
			continue
		}

		// A. 处理 Content (如果是数组则展平)
		// 仅针对 role 为 assistant 的消息进行处理，以保留 user 消息可能存在的多模态数组
		role, _ := msg["role"].(string)
		if role != "assistant" {
			continue
		}

		contentIntf, ok := msg["content"]
		if !ok {
			continue
		}

		if contentArr, isArr := contentIntf.([]interface{}); isArr {
			var sb strings.Builder
			for _, itemIntf := range contentArr {
				if item, ok := itemIntf.(map[string]interface{}); ok {
					for _, key := range []string{"text", "output_text"} {
						if textVal, ok := item[key]; ok {
							if textStr, ok := textVal.(string); ok {
								sb.WriteString(textStr)
								break
							}
						}
					}
				} else if s, ok := itemIntf.(string); ok {
					sb.WriteString(s)
				}
			}

			newStr := strings.TrimRight(sb.String(), "\n")
			if verbose {
				log.Printf("[codex-fix] 消息 %d content 展平为文本 (前15字符: %s...)", i, truncateString(newStr, 15))
			}
			msg["content"] = newStr
			messages[i] = msg
			modified = true
		}
	}
	return modified
}

func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

func countAssistantContentArrays(payload map[string]interface{}) int {
	count := 0
	for _, field := range []string{"messages", "input", "history"} {
		messages, ok := payload[field].([]interface{})
		if !ok {
			continue
		}
		count += countAssistantContentArraysInMessages(messages)
	}
	for _, nestedField := range []string{"input", "input_data"} {
		nested, ok := payload[nestedField].(map[string]interface{})
		if ok {
			count += countAssistantContentArrays(nested)
		}
	}
	return count
}

func countAssistantContentArraysInMessages(messages []interface{}) int {
	count := 0
	for _, msgIntf := range messages {
		msg, ok := msgIntf.(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role != "assistant" {
			continue
		}
		if _, ok := msg["content"].([]interface{}); ok {
			count++
		}
	}
	return count
}
