package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

// ForceStreamPlugin 检查请求体，如果包含 stream_options 但未开启 stream，则强制设置 stream: true
type ForceStreamPlugin struct{}

func (p *ForceStreamPlugin) Name() string {
	return "force-stream"
}

func (p *ForceStreamPlugin) ProcessRequest(req *http.Request, verbose bool) error {
	if req.Method != http.MethodPost {
		return nil
	}

	// 读取 Body
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return nil // 读取失败则不处理
	}
	req.Body.Close()

	if len(bodyBytes) == 0 {
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		return nil
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		// 不是 JSON 或解析失败，原样回写
		if verbose {
			log.Printf("[%s] JSON 解析失败: %v, Body: %s", p.Name(), err, string(bodyBytes))
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		return nil
	}

	// 检查逻辑：如果缺失 stream 字段，则补全为 true；如果已有（无论是 true 还是 false），则跳过
	_, hasStream := payload["stream"]

	if !hasStream {
		payload["stream"] = true

		newBodyBytes, err := json.Marshal(payload)
		if err != nil {
			if verbose {
				log.Printf("[%s] 序列化失败: %v", p.Name(), err)
			}
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			return nil
		}

		// 调试日志：输出修改后的完整 payload
		if verbose {
			log.Printf("[%s] 修正后的 Payload: %s", p.Name(), string(newBodyBytes))
		}

		req.Body = io.NopCloser(bytes.NewReader(newBodyBytes))
		req.ContentLength = int64(len(newBodyBytes))
		req.Header.Set("Content-Length", fmt.Sprintf("%d", len(newBodyBytes)))

		// 同时确保 Accept 头包含 text/event-stream
		if req.Header.Get("Accept") == "" {
			req.Header.Set("Accept", "text/event-stream")
		}

		if verbose {
			log.Printf("[%s] 缺失 stream 字段，已强制补全为 true", p.Name())
		}
		return nil
	}

	// 如果已有 stream 字段，原样回写原始字节，跳过序列化步骤
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return nil
}
