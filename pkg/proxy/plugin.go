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
	ProcessRequest(req *http.Request) error
}

// ResponsePlugin 定义了一类可以在响应返回客户端前修改 *http.Response 的插件
type ResponsePlugin interface {
	// Name 返回插件的名字
	Name() string
	// ProcessResponse 拦截并修改响应体。返回 error 则中断代理。
	ProcessResponse(resp *http.Response, ctx *goproxy.ProxyCtx, verbose bool) error
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
	if name == "codex-fix" && param != "" {
		return &CodexFixPlugin{TargetModel: param}, nil
	}

	if name == "responses-api" {
		keepReasoning := false
		if param == "keep-reasoning" {
			keepReasoning = true
		}
		return &ResponsesAPIPlugin{KeepReasoning: keepReasoning}, nil
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

	p, ok := ResponsePluginRegistry[name]
	if !ok {
		return nil, fmt.Errorf("响应插件 %s 未找到", name)
	}
	return p, nil
}
