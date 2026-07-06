package proxy

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/elazarl/goproxy"
)

// RequestPlugin 定义了一类可以在请求发送到上游前修改 *http.Request 的插件
type RequestPlugin interface {
	// Name 返回插件的名字，用于配置匹配
	Name() string
	// ProcessRequest 拦截并修改请求体。返回 error 则中断代理。
	ProcessRequest(req *http.Request, verbose bool) error
}

// ResponsePlugin 定义了一类可以在响应返回客户端前修改 *http.Response 的插件
type ResponsePlugin interface {
	// Name 返回插件的名字
	Name() string
	// ProcessResponse 拦截并修改响应体。返回 error 则中断代理。
	ProcessResponse(resp *http.Response, ctx *goproxy.ProxyCtx, verbose bool, vverbose bool) error
}

// RequestPluginRegistry 请求插件注册表
var RequestPluginRegistry = map[string]RequestPlugin{}

// ResponsePluginRegistry 响应插件注册表
var ResponsePluginRegistry = map[string]ResponsePlugin{}

func init() {
	// 注册内置插件
	RegisterPlugin(&CodexFixPlugin{})

	responsesAPIPlugin := &ResponsesAPIPlugin{}
	RegisterPlugin(responsesAPIPlugin)
	RegisterResponsePlugin(responsesAPIPlugin)

	anthropicThinkingPlugin := &AnthropicThinkingFixPlugin{}
	RegisterPlugin(anthropicThinkingPlugin)
	RegisterResponsePlugin(anthropicThinkingPlugin)

	forceStreamPlugin := &ForceStreamPlugin{}
	RegisterPlugin(forceStreamPlugin)

	messagesFixPlugin := &AnthropicMessagesFixPlugin{}
	RegisterPlugin(messagesFixPlugin)
	RegisterResponsePlugin(messagesFixPlugin)

	openAIToolCallsFixPlugin := &OpenAIToolCallsFixPlugin{}
	RegisterPlugin(openAIToolCallsFixPlugin)
	RegisterResponsePlugin(openAIToolCallsFixPlugin)
}

// RegisterPlugin 注册一个请求插件
func RegisterPlugin(plugin RequestPlugin) {
	RequestPluginRegistry[plugin.Name()] = plugin
}

// RegisterResponsePlugin 注册一个响应插件
func RegisterResponsePlugin(plugin ResponsePlugin) {
	ResponsePluginRegistry[plugin.Name()] = plugin
}

// GetPlugin 根据名称获取插件实例，支持 "name:param" 格式
func GetPlugin(fullName string) (RequestPlugin, error) {
	name := fullName
	param := ""
	if strings.Contains(fullName, ":") {
		parts := strings.SplitN(fullName, ":", 2)
		name = parts[0]
		param = parts[1]
	}

	// 针对 codex-fix 的特殊处理：支持参数化实例
	if name == "codex-fix" {
		opts := parsePluginOptions(param)
		targetModel := firstNonEmpty(opts["model"], opts["target_model"], opts["_"])
		if pluginOptionBool(opts, "diagnose") {
			return &CodexFixPlugin{TargetModel: targetModel, Diagnose: true}, nil
		}
		if param != "" {
			return &CodexFixPlugin{TargetModel: targetModel}, nil
		}
	}

	if name == "responses-api" {
		keepReasoning := false
		if param == "keep-reasoning" {
			keepReasoning = true
		}
		return &ResponsesAPIPlugin{KeepReasoning: keepReasoning}, nil
	}

	if name == "anthropic-messages-fix" {
		keepReasoning := false
		if param == "keep-reasoning" {
			keepReasoning = true
		}
		return &AnthropicMessagesFixPlugin{KeepReasoning: keepReasoning}, nil
	}

	if name == "force-stream" && pluginOptionBool(parsePluginOptions(param), "diagnose") {
		return &ForceStreamPlugin{Diagnose: true}, nil
	}

	if name == "openai-tool-calls-fix" && pluginOptionBool(parsePluginOptions(param), "diagnose") {
		return &OpenAIToolCallsFixPlugin{Diagnose: true}, nil
	}

	p, ok := RequestPluginRegistry[name]
	if !ok {
		return nil, fmt.Errorf("请求插件 %s 未找到", name)
	}
	return p, nil
}

// GetResponsePlugin 根据名称获取响应插件实例
func GetResponsePlugin(fullName string) (ResponsePlugin, error) {
	name := fullName
	param := ""
	if strings.Contains(fullName, ":") {
		parts := strings.SplitN(fullName, ":", 2)
		name = parts[0]
		param = parts[1]
	}

	if name == "responses-api" {
		keepReasoning := false
		if param == "keep-reasoning" {
			keepReasoning = true
		}
		return &ResponsesAPIPlugin{KeepReasoning: keepReasoning}, nil
	}

	if name == "anthropic-messages-fix" {
		keepReasoning := false
		if param == "keep-reasoning" {
			keepReasoning = true
		}
		return &AnthropicMessagesFixPlugin{KeepReasoning: keepReasoning}, nil
	}

	if name == "openai-tool-calls-fix" && pluginOptionBool(parsePluginOptions(param), "diagnose") {
		return &OpenAIToolCallsFixPlugin{Diagnose: true}, nil
	}

	p, ok := ResponsePluginRegistry[name]
	if !ok {
		return nil, fmt.Errorf("响应插件 %s 未找到", name)
	}
	return p, nil
}

func parsePluginOptions(param string) map[string]string {
	opts := make(map[string]string)
	for _, part := range strings.Split(param, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "=") {
			if part == "diagnose" || part == "keep-reasoning" {
				opts[part] = "true"
			} else if opts["_"] == "" {
				opts["_"] = part
			}
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		opts[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return opts
}

func pluginOptionBool(opts map[string]string, key string) bool {
	switch strings.ToLower(opts[key]) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
