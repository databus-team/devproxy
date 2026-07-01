package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/elazarl/goproxy"
	"golang.org/x/net/http2"
)

// ProxyRule 定义一组匹配规则及其对应的重写动作
type ProxyRule struct {
	Name            string
	Matchers        []URLMatcher
	Rewriters       []*HeaderRewriter
	Plugins         []RequestPlugin
	ResponsePlugins []ResponsePlugin
}

// ProxyServer MITM 代理服务器
type ProxyServer struct {
	Port          int
	UpstreamProxy string
	Rules         []*ProxyRule
	Verbose       bool
	VVerbose      bool // 极详细日志
	DumpTraffic   bool
	Logger        *log.Logger
	proxy         *goproxy.ProxyHttpServer
	server        *http.Server
	transport     *http.Transport // 优化的 Transport
	defaultRule   *ProxyRule
	// MITM 索引优化（2.1）
	mitmHosts     map[string]bool  // 预计算的 MITM 域名集合
	mitmPatterns  []*StringMatcher // 路径模式匹配器
	hasRegexRule  bool             // 是否有正则规则
	hasGlobalRule bool             // 是否有全局规则
}

// NewProxyServer 创建新的代理服务器（优化版：带连接池）
func NewProxyServer(port int, upstream string, verbose bool, logger *log.Logger) *ProxyServer {
	if logger == nil {
		logger = log.Default()
	}
	defaultRule := &ProxyRule{Name: "default"}
	s := &ProxyServer{
		Port:          port,
		UpstreamProxy: upstream,
		Verbose:       verbose,
		Logger:        logger,
		Rules:         []*ProxyRule{defaultRule},
		defaultRule:   defaultRule,
		mitmHosts:     make(map[string]bool),
	}
	s.rebuildMITMIndex() // 初始化索引

	// 2.2 Transport 连接池优化（高并发配置）
	transport := &http.Transport{
		// 代理配置
		Proxy: http.ProxyFromEnvironment,

		// 拨号配置
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,

		// 连接池配置（高并发场景）
		MaxIdleConns:        1000,
		MaxIdleConnsPerHost: 100,
		MaxConnsPerHost:     5000,
		IdleConnTimeout:     90 * time.Second,

		// TLS 配置
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},

		// 超时配置
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 180 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,

		// HTTP/2 支持
		ForceAttemptHTTP2: true,

		// 禁用压缩（让插件处理）
		DisableCompression: true,
	}

	// 配置 HTTP/2
	h2t, err := http2.ConfigureTransports(transport)
	if err == nil {
		// HTTP/2 特定优化
		h2t.MaxHeaderListSize = 10 << 20       // 10MB 头部限制
		h2t.MaxReadFrameSize = 32768           // 32KB 帧大小
		h2t.ReadIdleTimeout = 30 * time.Second // 健康检查间隔
		h2t.PingTimeout = 10 * time.Second     // PING 超时
	}

	s.transport = transport
	return s
}

// AddRule 添加一个新的规则组
func (s *ProxyServer) AddRule(rule *ProxyRule) {
	s.Rules = append(s.Rules, rule)
	s.rebuildMITMIndex() // 重新构建索引
}

// rebuildMITMIndex 重建 MITM 索引（O(n) 预处理，O(1) 查询）
func (s *ProxyServer) rebuildMITMIndex() {
	s.mitmHosts = make(map[string]bool)
	s.mitmPatterns = nil
	s.hasRegexRule = false
	s.hasGlobalRule = false

	for _, rule := range s.Rules {
		// 如果规则没有 Matcher 但有 Rewriter 或 Plugin，说明是全局规则，必须 MITM
		if len(rule.Matchers) == 0 && (len(rule.Rewriters) > 0 || len(rule.Plugins) > 0) {
			s.hasGlobalRule = true
			continue
		}

		for _, matcher := range rule.Matchers {
			// 如果是正则表达式匹配，由于无法预测，默认开启 MITM 以确保规则生效
			if _, ok := matcher.(*RegexMatcher); ok {
				s.hasRegexRule = true
				return // 有正则规则，默认全部 MITM
			}

			// 如果是字符串匹配
			if sm, ok := matcher.(*StringMatcher); ok {
				pattern := sm.Pattern
				// 纯 host 匹配（不包含 /），加入快速查找表
				if !strings.Contains(pattern, "/") {
					s.mitmHosts[pattern] = true
				} else {
					// 路径模式，加入列表
					s.mitmPatterns = append(s.mitmPatterns, sm)
				}
			}
		}
	}
}

// AddMatcher 添加全局 URL 匹配器 (添加到默认规则)
func (s *ProxyServer) AddMatcher(matcher URLMatcher) {
	s.defaultRule.Matchers = append(s.defaultRule.Matchers, matcher)
	s.rebuildMITMIndex()
}

// AddRewriter 添加请求头重写器 (添加到默认规则)
func (s *ProxyServer) AddRewriter(rewriter *HeaderRewriter) {
	s.defaultRule.Rewriters = append(s.defaultRule.Rewriters, rewriter)
	s.rebuildMITMIndex()
}

// AddPlugin 添加请求插件 (添加到默认规则)
func (s *ProxyServer) AddPlugin(plugin RequestPlugin) {
	s.defaultRule.Plugins = append(s.defaultRule.Plugins, plugin)
	s.rebuildMITMIndex()
}

// AddResponsePlugin 添加响应插件 (添加到默认规则)
func (s *ProxyServer) AddResponsePlugin(plugin ResponsePlugin) {
	s.defaultRule.ResponsePlugins = append(s.defaultRule.ResponsePlugins, plugin)
	s.rebuildMITMIndex()
}

// Start 启动代理服务器
func (s *ProxyServer) Start() error {
	s.proxy = goproxy.NewProxyHttpServer()
	// 重定向 goproxy 的日志到我们的 Logger
	s.proxy.Logger = s.Logger
	// 禁用 goproxy 内部过度详细且难以阅读的日志
	s.proxy.Verbose = false

	// 使用优化的 Transport
	s.proxy.Tr = s.transport

	// 设置上游代理
	if s.UpstreamProxy != "" {
		upstreamURL, err := url.Parse(s.UpstreamProxy)
		if err != nil {
			return fmt.Errorf("无效的上游代理地址：%w", err)
		}
		s.proxy.Tr.Proxy = http.ProxyURL(upstreamURL)
		s.proxy.ConnectDial = s.proxy.NewConnectDialToProxy(s.UpstreamProxy)
	}

	// 设置 HTTPS MITM
	s.proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		if s.ShouldMITM(host) {
			if s.Verbose {
				s.Logger.Printf("[CONNECT:MITM] %s", host)
			}
			return goproxy.MitmConnect, host
		}

		if s.Verbose {
			s.Logger.Printf("[CONNECT:PASSTHROUGH] %s", host)
		}
		return goproxy.OkConnect, host
	})

	// 设置请求拦截和修改
	s.proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		reqURL := req.URL.String()
		if req.URL.Host == "" && req.Host != "" {
			scheme := "http"
			if req.TLS != nil {
				scheme = "https"
			}
			reqURL = fmt.Sprintf("%s://%s%s", scheme, req.Host, req.URL.Path)
		}

		// 1. 标准化 URL 用于匹配
		matchURL := NormalizeURL(reqURL)

		// 2. 预判是否需要读取/缓存 Body
		var matchedRule *ProxyRule
		for _, rule := range s.Rules {
			matched := false
			if len(rule.Matchers) == 0 {
				if len(rule.Rewriters) > 0 || len(rule.Plugins) > 0 {
					matched = true
				}
			} else {
				for _, matcher := range rule.Matchers {
					if matcher.Match(matchURL) {
						matched = true
						break
					}
				}
			}
			if matched {
				matchedRule = rule
				break
			}
		}

		needBody := false
		if matchedRule != nil && len(matchedRule.Plugins) > 0 {
			needBody = true
		}

		// 3. 按需读取并恢复 Body (使用 Buffer Pool 减少内存分配)
		if needBody && req.Body != nil && req.Body != http.NoBody {
			buf := GetBuffer()
			defer PutBuffer(buf)

			_, err := io.Copy(buf, req.Body)
			if err == nil {
				body := buf.Bytes()
				req.GetBody = func() (io.ReadCloser, error) {
					return io.NopCloser(bytes.NewReader(body)), nil
				}
				req.Body, _ = req.GetBody()

				req.ContentLength = int64(len(body))
				req.Header.Del("Transfer-Encoding")
				req.Header.Del("Expect")
			}
		}

		// 4. 日志记录
		if s.DumpTraffic && req.Method != http.MethodConnect {
			dump, err := httputil.DumpRequest(req, needBody)
			if err == nil {
				s.Logger.Printf("[REQUEST DUMP] %s %s\n%s", req.Method, reqURL, string(dump))
			} else {
				s.Logger.Printf("[REQUEST DUMP ERROR] %s %s: %v", req.Method, reqURL, err)
			}
		} else if s.Verbose {
			s.Logger.Printf("[REQUEST] %s %s (Headers: %d, Length: %d)", req.Method, reqURL, len(req.Header), req.ContentLength)
		}

		// 5. 执行规则逻辑
		if matchedRule != nil {
			ctx.UserData = matchedRule
			if s.Verbose {
				s.Logger.Printf("[RULE:%s MATCHED] %s", matchedRule.Name, matchURL)
			}

			for _, plugin := range matchedRule.Plugins {
				err := plugin.ProcessRequest(req, s.Verbose)
				if s.Verbose {
					if err != nil {
						s.Logger.Printf("[RULE:%s PLUGIN ERROR] %s: %v", matchedRule.Name, plugin.Name(), err)
					} else {
						s.Logger.Printf("[RULE:%s PLUGIN APPLY] %s applied on %s", matchedRule.Name, plugin.Name(), matchURL)
					}
				}
			}

			for _, rewriter := range matchedRule.Rewriters {
				rewriter.Rewrite(req)
				if s.Verbose {
					s.Logger.Printf("[RULE:%s REWRITE] %s -> %s", matchedRule.Name, rewriter.HeaderName, rewriter.HeaderValue)
				}
			}
		}

		return req, nil
	})

	// 设置响应拦截和日志
	s.proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		reqURL := ctx.Req.URL.String()
		if resp != nil {
			matchURL := NormalizeURL(reqURL)

			var matchedRule *ProxyRule
			// 1. 优先从 UserData 获取在 Request 阶段匹配到的规则
			if r, ok := ctx.UserData.(*ProxyRule); ok {
				matchedRule = r
			}

			// 2. 如果没有，则重新尝试匹配
			if matchedRule == nil {
				for _, rule := range s.Rules {
					matched := false
					if len(rule.Matchers) == 0 {
						if len(rule.ResponsePlugins) > 0 {
							matched = true
						}
					} else {
						for _, matcher := range rule.Matchers {
							if matcher.Match(matchURL) {
								matched = true
								break
							}
						}
					}
					if matched {
						matchedRule = rule
						break
					}
				}
			}

			if matchedRule != nil && len(matchedRule.ResponsePlugins) > 0 {
				for _, plugin := range matchedRule.ResponsePlugins {
					err := plugin.ProcessResponse(resp, ctx, s.Verbose, s.VVerbose)
					if err != nil {
						s.Logger.Printf("[RULE:%s RESPONSE PLUGIN ERROR] %s: %v", matchedRule.Name, plugin.Name(), err)
					} else if s.Verbose {
						s.Logger.Printf("[RULE:%s RESPONSE PLUGIN APPLY] %s applied on %s", matchedRule.Name, plugin.Name(), matchURL)
					}
				}
			}

			if resp.StatusCode == http.StatusUnauthorized {
				dump, err := httputil.DumpResponse(resp, true)
				if err == nil {
					s.Logger.Printf("[DIAGNOSTIC] 401 UNAUTHORIZED DETAIL (%s):\n%s", reqURL, string(dump))
				}
			}

			if s.DumpTraffic && ctx.Req.Method != http.MethodConnect {
				contentType := resp.Header.Get("Content-Type")
				if strings.Contains(contentType, "text/event-stream") {
					s.Logger.Printf("[RESPONSE STREAM] %s %s -> %s (流式响应，跳过 dump)", ctx.Req.Method, reqURL, resp.Status)
				} else {
					dump, err := httputil.DumpResponse(resp, true)
					if err == nil {
						s.Logger.Printf("[RESPONSE DUMP] %s %s -> %s\n%s", ctx.Req.Method, reqURL, resp.Status, string(dump))
					} else {
						s.Logger.Printf("[RESPONSE DUMP ERROR] %s %s: %v", ctx.Req.Method, reqURL, err)
					}
				}
			} else if s.Verbose {
				s.Logger.Printf("[RESPONSE] %s %s -> %s", ctx.Req.Method, reqURL, resp.Status)
			}
		} else {
			if s.Verbose {
				s.Logger.Printf("[RESPONSE ERROR] %s %s: 响应为空", ctx.Req.Method, reqURL)
			}
		}
		return resp
	})

	s.server = &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", s.Port),
		Handler:           s.proxy,
		ErrorLog:          s.Logger,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	s.Logger.Printf("代理服务器启动在 http://127.0.0.1:%d", s.Port)
	if s.UpstreamProxy != "" {
		s.Logger.Printf("上游代理：%s", s.UpstreamProxy)
	}

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.Logger.Printf("代理服务器错误：%v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)
	return nil
}

// ShouldMITM 判断是否需要对该 host 进行 MITM（优化版：O(1) 查找）
func (s *ProxyServer) ShouldMITM(host string) bool {
	// 剥离端口号
	domain := host
	if pos := strings.Index(host, ":"); pos != -1 {
		domain = host[:pos]
	}

	// O(1) 查找：预计算的 host 集合
	if s.mitmHosts[domain] {
		return true
	}

	// O(1) 判断：全局规则
	if s.hasGlobalRule {
		return true
	}

	// O(1) 判断：正则规则
	if s.hasRegexRule {
		return true
	}

	// O(n) 遍历：路径模式匹配（n 通常很小）
	// 注意：这里需要检查域名是否匹配模式中的 host 部分
	for _, sm := range s.mitmPatterns {
		// sm.Pattern 可能是完整 URL 如 "https://host/path" 或纯路径 "/path"
		pattern := sm.Pattern
		if strings.HasPrefix(pattern, "https://") || strings.HasPrefix(pattern, "http://") {
			// 提取模式中的 host 部分进行匹配
			prefixLen := 8
			if strings.HasPrefix(pattern, "http://") {
				prefixLen = 7
			}
			rest := pattern[prefixLen:]
			slashIdx := strings.Index(rest, "/")
			if slashIdx != -1 {
				patternHost := rest[:slashIdx]
				// 检查域名是否匹配（精确匹配或后缀匹配）
				if domain == patternHost || strings.HasSuffix(domain, "."+patternHost) || strings.HasSuffix(patternHost, "."+domain) {
					return true
				}
			}
		} else if strings.HasPrefix(pattern, "/") {
			// 纯路径模式，无法在 CONNECT 阶段判断，默认开启 MITM
			return true
		} else {
			// 其他模式，尝试子串匹配
			if strings.Contains(domain, pattern) || strings.Contains(pattern, domain) {
				return true
			}
		}
	}

	return false
}

// Stop 停止代理服务器
func (s *ProxyServer) Stop() error {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.server.Shutdown(ctx)
	}
	return nil
}
