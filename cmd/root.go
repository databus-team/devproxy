package cmd

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/daydaychen/devproxy/pkg/config"
	"github.com/daydaychen/devproxy/pkg/process"
	"github.com/daydaychen/devproxy/pkg/proxy"
	"github.com/daydaychen/devproxy/pkg/util"

	"github.com/spf13/cobra"
)

func loadConfig(cmd *cobra.Command) {
	// 收集所有配置源
	var configFiles []string

	// 1. 全局配置 (~/.config/devproxy/global.yaml)
	home, err := os.UserHomeDir()
	if err == nil {
		globalPath := fmt.Sprintf("%s/.config/devproxy/global.yaml", home)
		if _, err := os.Stat(globalPath); err == nil {
			configFiles = append(configFiles, globalPath)
		}
	}

	// 2. 目录级别配置 (优先级高于全局)
	// 如果命令行指定了 --config，则只使用该文件作为“目录级/显式”配置
	if configFile != "" {
		configFiles = append(configFiles, configFile)
	} else {
		// 检查默认本地配置文件
		defaults := []string{"devproxy.yaml", ".devproxy.yaml"}
		for _, d := range defaults {
			if _, err := os.Stat(d); err == nil {
				configFiles = append(configFiles, d)
				break
			}
		}
	}

	// 按顺序加载并合并
	for _, path := range configFiles {
		cfg, err := config.LoadConfig(path)
		if err != nil {
			// 如果是显式指定的配置文件加载失败，则报错退出
			if configFile != "" && path == configFile {
				log.Fatalf("加载指定的配置文件失败: %v", err)
			}
			// 否则仅打印警告
			fmt.Printf("警告: 加载配置文件 %s 失败: %v\n", path, err)
			continue
		}

		fmt.Printf("集成配置文件: %s\n", path)

		// 合并单值配置 (如果命令行没指定，后面的文件会覆盖前面的)
		if !cmd.Flags().Changed("upstream") && cfg.Upstream != "" {
			upstreamProxy = cfg.Upstream
		}
		if !cmd.Flags().Changed("port") && cfg.Port > 0 {
			port = cfg.Port
		}
		if !cmd.Flags().Changed("verbose") {
			// 如果配置文件中明确设置了 verbose，则应用它
			// 注意：这里默认是 false，所以我们只在配置文件为 true 时更新
			if cfg.Verbose {
				verbose = true
			}
		}
		if !cmd.Flags().Changed("vverbose") {
			if cfg.VVerbose {
				vverbose = true
			}
		}
		if !cmd.Flags().Changed("dump") {
			if cfg.DumpTraffic {
				dumpTraffic = true
			}
		}
		if !cmd.Flags().Changed("log-file") && cfg.LogFile != "" {
			logFile = cfg.LogFile
		}

		// 合并列表配置 (累加)
		if len(cfg.Match) > 0 {
			matchPatterns = append(matchPatterns, cfg.Match...)
		}
		if len(cfg.Overwrite) > 0 {
			for k, v := range cfg.Overwrite {
				overwriteRules = append(overwriteRules, fmt.Sprintf("%s=%s", k, v))
			}
		}
		if len(cfg.Plugins) > 0 {
			pluginNames = append(pluginNames, cfg.Plugins...)
		}
		if len(cfg.ResponsePlugins) > 0 {
			responsePluginNames = append(responsePluginNames, cfg.ResponsePlugins...)
		}
		if len(cfg.Rules) > 0 {
			configRules = append(configRules, cfg.Rules...)
		}
	}
}

var (
	matchPatterns       []string
	overwriteRules      []string
	configRules         []config.RuleConfig
	pluginNames         []string
	responsePluginNames []string
	upstreamProxy       string
	port                int
	verbose             bool
	vverbose            bool
	dumpTraffic         bool
	logFile             string
	configFile          string
)

var rootCmd = &cobra.Command{
	Use:   "devproxy [flags] -- <command> [args...]",
	Short: "Smart Proxy - 智能MITM代理工具",
	Long: `Smart Proxy 是一个智能的MITM代理工具，可以拦截并修改HTTP/HTTPS请求。
它只代理启动的子进程流量，不影响系统其他进程。

示例:
  # 使用配置文件
  devproxy --config config.yaml -- node server.js

  # 基本用法
  devproxy --match "domain.com/v1/api" --overwrite useragent=CustomUA -- node server.js

  # 带上游代理
  devproxy --match "*.example.com" --overwrite useragent=Bot --upstream http://127.0.0.1:7890 -- node app.js

  # 指定端口和详细日志
  devproxy --port 8888 --match "/api/" --overwrite useragent=Test --verbose -- npm start

  # 运行交互式应用（如 vim）时，建议将 verbose 日志输出到文件
  devproxy --verbose --log-file ./proxy.log -- vim test.js`,
	Args: cobra.MinimumNArgs(1),
	Run:  run,
}

func init() {
	rootCmd.Version = util.Version
	rootCmd.SetVersionTemplate("devproxy version {{.Version}}\n")

	rootCmd.Flags().StringArrayVar(&matchPatterns, "match", []string{}, "URL匹配规则 (可指定多次)")
	rootCmd.Flags().StringArrayVar(&overwriteRules, "overwrite", []string{}, "请求头重写规则 (格式: header=value, 可指定多次)")
	rootCmd.Flags().StringArrayVar(&pluginNames, "plugin", []string{}, "请求处理插件 (如 codex-fix, 可指定多次)")
	rootCmd.Flags().StringArrayVar(&responsePluginNames, "response-plugin", []string{}, "响应处理插件 (可指定多次)")
	rootCmd.Flags().StringVar(&upstreamProxy, "upstream", "", "上游代理地址 (例: http://127.0.0.1:7890)")
	rootCmd.Flags().IntVar(&port, "port", 0, "代理服务器端口 (默认随机分配)")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "V", false, "详细日志输出")
	rootCmd.Flags().BoolVar(&vverbose, "vverbose", false, "极详细日志输出 (记录上游事件流数据)")
	rootCmd.Flags().BoolVar(&dumpTraffic, "dump", false, "输出完整的请求/响应头和正文 (用于详细调试)")
	rootCmd.Flags().StringVar(&logFile, "log-file", "", "日志文件路径 (用于避免干扰交互式应用，如vim)")
	rootCmd.Flags().StringVarP(&configFile, "config", "c", "", "配置文件路径 (支持 YAML 格式)")
	// 添加 -v 作为 --version 的缩写
	rootCmd.Flags().BoolP("version", "v", false, "查看版本号")

	rootCmd.AddCommand(proxyWorkerCmd)
}

var proxyWorkerCmd = &cobra.Command{
	Use:    "__internal_proxy_worker",
	Short:  "Internal proxy worker (do not use directly)",
	Hidden: true,
	Run:    runProxyWorker,
}

func runProxyWorker(cmd *cobra.Command, args []string) {
	// 从环境变量加载配置
	vStr := os.Getenv("SMART_PROXY_VERBOSE")
	isVerbose := vStr == "true"
	vvStr := os.Getenv("SMART_PROXY_VVERBOSE")
	isVVerbose := vvStr == "true"
	pStr := os.Getenv("SMART_PROXY_PORT")
	pPort, _ := strconv.Atoi(pStr)
	uProxy := os.Getenv("SMART_PROXY_UPSTREAM")
	dStr := os.Getenv("SMART_PROXY_DUMP")
	isDump := dStr == "true"

	// 重新加载配置（为了匹配规则）
	loadConfig(cmd)

	// 创建代理服务器
	log.SetPrefix("[PROXY-WORKER] ")
	ps := proxy.NewProxyServer(pPort, uProxy, isVerbose, nil)
	ps.VVerbose = isVVerbose
	ps.DumpTraffic = isDump

	// 添加匹配器
	for _, pattern := range matchPatterns {
		ps.AddMatcher(proxy.NewStringMatcher(pattern))
	}
	for _, rule := range overwriteRules {
		headerName, headerValue := parseOverwriteRule(rule)
		ps.AddRewriter(proxy.NewHeaderRewriter(headerName, headerValue))
	}
	for _, name := range pluginNames {
		// 尝试作为请求插件加载
		p, err := proxy.GetPlugin(name)
		if err == nil {
			ps.AddPlugin(p)
			continue
		}

		// 尝试作为响应插件加载
		rp, err := proxy.GetResponsePlugin(name)
		if err == nil {
			ps.AddResponsePlugin(rp)
			continue
		}

		log.Printf("加载全局插件 %s 失败: 未在请求或响应插件注册表中找到", name)
	}

	for _, name := range responsePluginNames {
		rp, err := proxy.GetResponsePlugin(name)
		if err != nil {
			log.Printf("加载全局响应插件 %s 失败: %v", name, err)
		} else {
			ps.AddResponsePlugin(rp)
		}
	}

	// 添加成组规则
	for _, ruleCfg := range configRules {
		pRule := &proxy.ProxyRule{Name: ruleCfg.Name}
		for _, pattern := range ruleCfg.Match {
			pRule.Matchers = append(pRule.Matchers, proxy.NewStringMatcher(pattern))
		}
		for k, v := range ruleCfg.Overwrite {
			hN, hV := parseOverwriteRule(fmt.Sprintf("%s=%s", k, v))
			pRule.Rewriters = append(pRule.Rewriters, proxy.NewHeaderRewriter(hN, hV))
		}
		for _, name := range ruleCfg.Plugins {
			// 尝试作为请求插件加载
			p, err := proxy.GetPlugin(name)
			if err == nil {
				pRule.Plugins = append(pRule.Plugins, p)
				continue
			}

			// 尝试作为响应插件加载
			rp, err := proxy.GetResponsePlugin(name)
			if err == nil {
				pRule.ResponsePlugins = append(pRule.ResponsePlugins, rp)
				continue
			}

			log.Printf("规则 %s 加载插件 %s 失败: 未在请求或响应插件注册表中找到", ruleCfg.Name, name)
		}
		for _, name := range ruleCfg.ResponsePlugins {
			rp, err := proxy.GetResponsePlugin(name)
			if err != nil {
				log.Printf("规则 %s 加载响应插件 %s 失败: %v", ruleCfg.Name, name, err)
			} else {
				pRule.ResponsePlugins = append(pRule.ResponsePlugins, rp)
			}
		}
		ps.AddRule(pRule)
	}

	if err := ps.Start(); err != nil {
		log.Fatalf("代理工作进程启动失败: %v", err)
	}

	// 内存优化：初始化完成后强制回收
	debug.FreeOSMemory()

	// 阻塞等待
	select {}
}

func run(cmd *cobra.Command, args []string) {
	// 加载配置文件
	loadConfig(cmd)

	var logFileWriter *os.File

	if logFile != "" {
		var err error
		logFileWriter, err = os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Fatalf("打开日志文件失败: %v", err)
		}
		defer logFileWriter.Close()

		fmt.Printf("详细日志将输出到文件: %s (已过滤终端控制符)\n", logFile)

		// 创建一个带剥离 ANSI 功能的 Writer
		strippedWriter := &util.AnsiStripper{Writer: logFileWriter}
		// 设置全局默认 Logger 的输出，拦截主进程自身的 log.Printf
		log.SetOutput(strippedWriter)

		log.Printf("=== Smart Proxy 启动 (PID: %d) ===", os.Getpid())
	}

	// 分配端口
	var proxyPort int
	var err error
	if port > 0 {
		proxyPort = port
	} else {
		proxyPort, err = util.GetRandomPort()
		if err != nil {
			log.Fatalf("分配随机端口失败: %v", err)
		}
	}

	// 架构级修复：启动一个独立的子进程来运行代理服务器
	// 这提供了绝对的日志隔离，因为代理子进程的标准输出/错误在创建时就被重定向了
	proxyWorkerCmd := exec.Command(os.Args[0], "__internal_proxy_worker")
	proxyWorkerCmd.Env = append(os.Environ(),
		fmt.Sprintf("SMART_PROXY_PORT=%d", proxyPort),
		fmt.Sprintf("SMART_PROXY_UPSTREAM=%s", upstreamProxy),
		fmt.Sprintf("SMART_PROXY_VERBOSE=%v", verbose),
		fmt.Sprintf("SMART_PROXY_VVERBOSE=%v", vverbose),
		fmt.Sprintf("SMART_PROXY_DUMP=%v", dumpTraffic),
	)

	// 关键：将代理进程的输出物理重定向到日志文件
	if logFile != "" {
		proxyWorkerCmd.Stdout = logFileWriter
		proxyWorkerCmd.Stderr = logFileWriter
	} else if verbose || vverbose || dumpTraffic {
		// 如果开启了详细日志或流量转储，且未指定日志文件，则输出到终端
		proxyWorkerCmd.Stdout = os.Stdout
		proxyWorkerCmd.Stderr = os.Stderr
	} else {
		// 如果没指定日志文件且非 verbose 模式，代理的输出被静默，以防干扰 UI
		proxyWorkerCmd.Stdout = nil
		proxyWorkerCmd.Stderr = nil
	}

	if err := proxyWorkerCmd.Start(); err != nil {
		log.Fatalf("启动代理工作进程失败: %v", err)
	}
	defer func() {
		if proxyWorkerCmd.Process != nil {
			proxyWorkerCmd.Process.Kill()
		}
	}()

	// 内存优化：主进程不再需要这些大数组，因为它们已经传递给（或会被重新加载到）工作进程
	matchPatterns = nil
	overwriteRules = nil
	configRules = nil
	pluginNames = nil

	// 创建进程启动器
	targetCommand := args[0]
	targetArgs := args[1:]
	launcher := process.NewProcessLauncher(targetCommand, targetArgs, proxyPort, verbose)

	// 启动子进程
	launcher.Stdout = os.Stdout
	launcher.Stderr = os.Stderr
	launcher.Stdin = os.Stdin

	// 启动子进程
	if err := launcher.Start(); err != nil {
		log.Fatalf("启动子进程失败: %v", err)
	}

	// 内存优化：主进程启动完所有组件后释放不再需要的初始化内存
	debug.FreeOSMemory()

	// 处理系统信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 等待信号
	go func() {
		sig := <-sigChan
		log.Printf("\n收到终止信号 (%v)，正在清理...", sig)
		launcher.Stop()
		if proxyWorkerCmd.Process != nil {
			proxyWorkerCmd.Process.Kill()
		}
		if logFileWriter != nil {
			logFileWriter.Sync()
		}
		os.Exit(0)
	}()

	// 等待子进程结束
	err = launcher.Wait()

	var exitCode int
	if err != nil {
		log.Printf("子进程运行出错: %v", err)
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = 1
		}
	} else {
		log.Println("子进程正常结束")
	}

	log.Println("正在清理并退出...")
	if proxyWorkerCmd.Process != nil {
		proxyWorkerCmd.Process.Kill()
	}

	// 在退出前给 log 库一点时间处理最后的写入
	if logFileWriter != nil {
		log.Println("=== Smart Proxy 结束 ===")
		logFileWriter.Sync()
		// 稍微等待确保 OS 刷写完成
		time.Sleep(100 * time.Millisecond)
	}

	os.Exit(exitCode)
}

// Execute 执行命令
func Execute() {
	// 针对命令行工具优化：设置更积极的 GC (默认 100 降至 50)
	// 这有助于在低交互期间更快回收内存
	debug.SetGCPercent(50)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseOverwriteRule(rule string) (string, string) {
	parts := strings.SplitN(rule, "=", 2)
	if len(parts) != 2 {
		log.Fatalf("无效的重写规则: %s (格式应为 header=value)", rule)
	}

	headerName := parts[0]
	headerValue := parts[1]

	// 处理常见的简写
	switch strings.ToLower(headerName) {
	case "useragent", "ua":
		headerName = "User-Agent"
	case "referer":
		headerName = "Referer"
	case "origin":
		headerName = "Origin"
	}

	return headerName, headerValue
}
