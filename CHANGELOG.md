# 更新日志

## [0.6.1](https://github.com/databus-team/devproxy/compare/v0.6.0...v0.6.1) (2026-07-01)


### Bug Fixes

* **proxy:** 增加响应头超时时间至180秒以改善连接稳定性 ([6dc4b94](https://github.com/databus-team/devproxy/commit/6dc4b94fc40e1d86559705423272163741f2884a))

## [0.5.0](https://github.com/databus-team/devproxy/compare/v0.4.0...v0.5.0) (2026-06-24)


### Features

* **ci:** 添加 release-please 自动化版本和 changelog 管理 ([019f833](https://github.com/databus-team/devproxy/commit/019f8335cf8fcb99b81e6cd7570c0eb12cc5b9b8))
* **codex:** 支持 model 字段自动化替换，增加插件参数解析机制，解决 401 权限报错 ([2e3d322](https://github.com/databus-team/devproxy/commit/2e3d32222bce4248e41a9f643970d36e93e98809))
* **docs:** 更新 README 和 README_CN，增强对 OpenAI Responses API 的支持描述 ([6dd0dcd](https://github.com/databus-team/devproxy/commit/6dd0dcd035cca5d996e859ee19b913367bc1c3a6))
* **proxy:** add ResponsesAPIPlugin for OpenAI Responses API adaptation ([503d488](https://github.com/databus-team/devproxy/commit/503d4883b5add7c06ffc44903bc54ea5f1fd2c3a))
* **proxy:** 优化 codex-fix 插件，仅展平 assistant 消息内容 ([f110afc](https://github.com/databus-team/devproxy/commit/f110afcf19eb4f5a4fbf0faccadf86ca02d6971c))
* 增强交互式应用支持、选择性 MITM 及日志清洗功能 ([5d74b73](https://github.com/databus-team/devproxy/commit/5d74b73504da7e3ea9a158abf8ad0a1234fb2486))
* 完善插件系统抽象，增强证书管理及启动流程优化 ([4ba8c38](https://github.com/databus-team/devproxy/commit/4ba8c38342d89f35bf0cdf9d88a77613869bb08a))
* 引入基于 YAML 的配置文件管理功能并提供示例。 ([6c82001](https://github.com/databus-team/devproxy/commit/6c82001e0371661227aacfbb3bbb0c5fbf214172))
* 扩展 Makefile，新增 Docker 构建与运行命令，并集成 lint 和 format 工具。 ([4580b12](https://github.com/databus-team/devproxy/commit/4580b12f2893122ec06fa9c88e4f22d8390f0b4a))
* 支持 Windows 交互式 PTY (ConPTY) ([fad2024](https://github.com/databus-team/devproxy/commit/fad2024fcee4682c8e53b72b1777af41d7722399))
* 支持多级配置文件模式及成组匹配规则 ([df3afca](https://github.com/databus-team/devproxy/commit/df3afca5782a2e4d5c7b69f3742d29477793c3b4))
* 支持跨平台交叉编译 (Linux/Windows/macOS) ([f1aa7cc](https://github.com/databus-team/devproxy/commit/f1aa7cc60be751fc9f2869c5b649daebe4609782))
* 新增 vverbose 极详细日志模式并清理调试残留 ([50e1acb](https://github.com/databus-team/devproxy/commit/50e1acbc1a3d8137046115cc9766f8b27d9d0d44))
* 添加版本号支持并支持通过 ldflags 动态注入 ([efbe9e4](https://github.com/databus-team/devproxy/commit/efbe9e4e00750ceecd9f9c06fa2e98579baf26d7))


### Bug Fixes

* **ci:** 降级 Go 版本至 1.24 以兼容 golangci-lint ([a72daca](https://github.com/databus-team/devproxy/commit/a72daca74801828392dd93a97780dfe5fb56896c))
* **codex:** 修复 Codex 插件的 502/400/401 错误，适配 input 协议，并消除 Passthrough 干扰 ([65d4ef5](https://github.com/databus-team/devproxy/commit/65d4ef564ba53c805ad54c9ac8bd8c8ffeea4603))
* **docs:** 更新 README 和 README_CN 中的项目链接为 devproxy ([7b1bcf9](https://github.com/databus-team/devproxy/commit/7b1bcf9fc9cd36978bdd5ee49480d89914af68ba))
* **proxy:** reduce SSE buffer from 1MB to 4KB and add client disconnect detection ([bba0df6](https://github.com/databus-team/devproxy/commit/bba0df6579bfa695d95b52788861cd324f66628c))
* **proxy:** 修复 anthropic-thinking-fix 中嵌套 JSON 字段引发的工具调用错误拆分与截断 Bug ([bac684c](https://github.com/databus-team/devproxy/commit/bac684cf090e727b0a38f5fd4cad31aeb27f3c6c))
* **proxy:** 修复 Responses API 插件的流式协议兼容性与工具调用转换 ([15bdbbc](https://github.com/databus-team/devproxy/commit/15bdbbc9f58d5f614f9e29aac775f57b7e33c39a))
* **proxy:** 修复 ShouldMITM 路径模式匹配回归问题 ([0c47881](https://github.com/databus-team/devproxy/commit/0c47881c033c46eec0484221cdbc3f81499486e6))
* **proxy:** 修复非标 Responses API 格式兼容性及工具调用识别问题 ([6b49de1](https://github.com/databus-team/devproxy/commit/6b49de1ddebcf418422efa7ccc125bb15f3efcce))
* **proxy:** 减少 Header 强制干预，增加 401 诊断日志，让 net/http 标准库处理 Content-Length ([0835e28](https://github.com/databus-team/devproxy/commit/0835e287d7f700efcff8057a7eabacc5043c4b77))
* **proxy:** 在 ProcessRequest 中过滤消息历史里的 thinking 块以防止上游验证报错 ([1424f1d](https://github.com/databus-team/devproxy/commit/1424f1da5ac93a1eb9e5c77ab9e0ba86aba9899e))
* **proxy:** 支持在 anthropic-messages-fix 中解析标准的 &lt;think&gt; 标签，并修复切片越界 Bug ([bbc6817](https://github.com/databus-team/devproxy/commit/bbc6817da6e01abcb85f60bc28d8cac51b9adce8))
* **proxy:** 更新思维链检测逻辑，避免正文中的 "thinking" 被误判为思维链开始，并调整相关测试用例 ([0c08115](https://github.com/databus-team/devproxy/commit/0c0811571c5d9091e28a10447aace055495b9076))
* **proxy:** 解决 Electron/Chromium 环境下的 401/502 错误，加固 Body/Header 同步逻辑 ([32c3177](https://github.com/databus-team/devproxy/commit/32c3177a442d51e12a87b93845e934e682a213c9))
* resolve stream disconnection by handling compression and improving SSE formatting ([bd590a3](https://github.com/databus-team/devproxy/commit/bd590a3df257e131140cd30dbb202317b15fe6de))
* **responses-api:** convert Codex tools to Chat API format and filter unsupported tool types ([ac43551](https://github.com/databus-team/devproxy/commit/ac43551c4c3af55b681ab22d7e7d8bac382fe7c5))
* **thinking-fix:** 修复合并在同一个index下的畸形并行工具调用流 ([bb41e4f](https://github.com/databus-team/devproxy/commit/bb41e4fb3125cd42413032cd3f85bff37419e081))
* **thinking-fix:** 引入自动发送ping的保活机制以防御超长Prefill超时 ([13d7d31](https://github.com/databus-team/devproxy/commit/13d7d3141ffeb30a1f2218d9726a9bcf1c81b8f3))
* **thinking-fix:** 自动补全tool_use中缺失的必填属性input ([a7f1067](https://github.com/databus-team/devproxy/commit/a7f1067e78ca2c4dd22c97c91d51d26bebb006ef))
* 修复多进程架构重构后的编译错误和清理逻辑 ([d6e6ec5](https://github.com/databus-team/devproxy/commit/d6e6ec556fff386bea2c23c41279f879c778e163))
* 修复子进程退出时的 "read fatal" 错误 ([7ea17c7](https://github.com/databus-team/devproxy/commit/7ea17c7cf75b0350b22d0966ac9f0dad3ace2fe0))
* 劫持全局日志输出，彻底防止 goproxy 报错泄露到终端 UI ([f275cd7](https://github.com/databus-team/devproxy/commit/f275cd7ced6adc7d2db59d3c23e6ae1706868d26))
* 更新代理服务器的请求处理逻辑。 ([9660038](https://github.com/databus-team/devproxy/commit/9660038b0501eccd7f0bf5209791e081d68f43c8))
* 采用系统调用级重定向劫持 FD 2，从架构层面彻底杜绝日志泄露到终端 ([94488bf](https://github.com/databus-team/devproxy/commit/94488bff143b9609fcc50fe766d502df17c0f08d))
* 重定向 goproxy 和 http.Server 日志，防止报错干扰交互式应用 UI ([3c4f970](https://github.com/databus-team/devproxy/commit/3c4f970372b31dfce729886d4a291ec29eac0c7b))


### Performance Improvements

* **proxy:** 优化 codex-fix 插件，仅在模型不一致时进行替换 ([31e7667](https://github.com/databus-team/devproxy/commit/31e76672e3cd8211f3f3246fd5da18e5883069fb))
* **proxy:** 第一阶段性能优化 - Buffer Pooling、Header 缓存、URL 规范化 ([7c0e38f](https://github.com/databus-team/devproxy/commit/7c0e38f053644c76e010b19060ba37b6ec2b82be))
* **proxy:** 第二阶段架构优化 - MITM 索引、Transport 连接池、TLS 证书缓存 ([813ab8f](https://github.com/databus-team/devproxy/commit/813ab8fa7f0f5363db99bc5f73ec17303e0984ed))
* 优化编译大小和执行性能 ([2dea56a](https://github.com/databus-team/devproxy/commit/2dea56ae80dd2c5ee3396b11fba77c0c9e335e30))

## [0.4.0](https://github.com/daydaychen/devproxy/compare/v0.3.1...v0.4.0) (2026-05-24)


### Features

* **ci:** 添加 release-please 自动化版本和 changelog 管理 ([019f833](https://github.com/daydaychen/devproxy/commit/019f8335cf8fcb99b81e6cd7570c0eb12cc5b9b8))
* **codex:** 支持 model 字段自动化替换，增加插件参数解析机制，解决 401 权限报错 ([2e3d322](https://github.com/daydaychen/devproxy/commit/2e3d32222bce4248e41a9f643970d36e93e98809))
* **docs:** 更新 README 和 README_CN，增强对 OpenAI Responses API 的支持描述 ([6dd0dcd](https://github.com/daydaychen/devproxy/commit/6dd0dcd035cca5d996e859ee19b913367bc1c3a6))
* **proxy:** add ResponsesAPIPlugin for OpenAI Responses API adaptation ([503d488](https://github.com/daydaychen/devproxy/commit/503d4883b5add7c06ffc44903bc54ea5f1fd2c3a))
* **proxy:** 优化 codex-fix 插件，仅展平 assistant 消息内容 ([f110afc](https://github.com/daydaychen/devproxy/commit/f110afcf19eb4f5a4fbf0faccadf86ca02d6971c))
* 增强交互式应用支持、选择性 MITM 及日志清洗功能 ([5d74b73](https://github.com/daydaychen/devproxy/commit/5d74b73504da7e3ea9a158abf8ad0a1234fb2486))
* 完善插件系统抽象，增强证书管理及启动流程优化 ([4ba8c38](https://github.com/daydaychen/devproxy/commit/4ba8c38342d89f35bf0cdf9d88a77613869bb08a))
* 引入基于 YAML 的配置文件管理功能并提供示例。 ([6c82001](https://github.com/daydaychen/devproxy/commit/6c82001e0371661227aacfbb3bbb0c5fbf214172))
* 扩展 Makefile，新增 Docker 构建与运行命令，并集成 lint 和 format 工具。 ([4580b12](https://github.com/daydaychen/devproxy/commit/4580b12f2893122ec06fa9c88e4f22d8390f0b4a))
* 支持 Windows 交互式 PTY (ConPTY) ([fad2024](https://github.com/daydaychen/devproxy/commit/fad2024fcee4682c8e53b72b1777af41d7722399))
* 支持多级配置文件模式及成组匹配规则 ([df3afca](https://github.com/daydaychen/devproxy/commit/df3afca5782a2e4d5c7b69f3742d29477793c3b4))
* 支持跨平台交叉编译 (Linux/Windows/macOS) ([f1aa7cc](https://github.com/daydaychen/devproxy/commit/f1aa7cc60be751fc9f2869c5b649daebe4609782))
* 添加版本号支持并支持通过 ldflags 动态注入 ([efbe9e4](https://github.com/daydaychen/devproxy/commit/efbe9e4e00750ceecd9f9c06fa2e98579baf26d7))


### Bug Fixes

* **ci:** 降级 Go 版本至 1.24 以兼容 golangci-lint ([a72daca](https://github.com/daydaychen/devproxy/commit/a72daca74801828392dd93a97780dfe5fb56896c))
* **codex:** 修复 Codex 插件的 502/400/401 错误，适配 input 协议，并消除 Passthrough 干扰 ([65d4ef5](https://github.com/daydaychen/devproxy/commit/65d4ef564ba53c805ad54c9ac8bd8c8ffeea4603))
* **docs:** 更新 README 和 README_CN 中的项目链接为 devproxy ([7b1bcf9](https://github.com/daydaychen/devproxy/commit/7b1bcf9fc9cd36978bdd5ee49480d89914af68ba))
* **proxy:** reduce SSE buffer from 1MB to 4KB and add client disconnect detection ([bba0df6](https://github.com/daydaychen/devproxy/commit/bba0df6579bfa695d95b52788861cd324f66628c))
* **proxy:** 修复 Responses API 插件的流式协议兼容性与工具调用转换 ([15bdbbc](https://github.com/daydaychen/devproxy/commit/15bdbbc9f58d5f614f9e29aac775f57b7e33c39a))
* **proxy:** 修复 ShouldMITM 路径模式匹配回归问题 ([0c47881](https://github.com/daydaychen/devproxy/commit/0c47881c033c46eec0484221cdbc3f81499486e6))
* **proxy:** 修复非标 Responses API 格式兼容性及工具调用识别问题 ([6b49de1](https://github.com/daydaychen/devproxy/commit/6b49de1ddebcf418422efa7ccc125bb15f3efcce))
* **proxy:** 减少 Header 强制干预，增加 401 诊断日志，让 net/http 标准库处理 Content-Length ([0835e28](https://github.com/daydaychen/devproxy/commit/0835e287d7f700efcff8057a7eabacc5043c4b77))
* **proxy:** 解决 Electron/Chromium 环境下的 401/502 错误，加固 Body/Header 同步逻辑 ([32c3177](https://github.com/daydaychen/devproxy/commit/32c3177a442d51e12a87b93845e934e682a213c9))
* resolve stream disconnection by handling compression and improving SSE formatting ([bd590a3](https://github.com/daydaychen/devproxy/commit/bd590a3df257e131140cd30dbb202317b15fe6de))
* **thinking-fix:** 修复合并在同一个index下的畸形并行工具调用流 ([bb41e4f](https://github.com/daydaychen/devproxy/commit/bb41e4fb3125cd42413032cd3f85bff37419e081))
* **thinking-fix:** 引入自动发送ping的保活机制以防御超长Prefill超时 ([13d7d31](https://github.com/daydaychen/devproxy/commit/13d7d3141ffeb30a1f2218d9726a9bcf1c81b8f3))
* **thinking-fix:** 自动补全tool_use中缺失的必填属性input ([a7f1067](https://github.com/daydaychen/devproxy/commit/a7f1067e78ca2c4dd22c97c91d51d26bebb006ef))
* 修复多进程架构重构后的编译错误和清理逻辑 ([d6e6ec5](https://github.com/daydaychen/devproxy/commit/d6e6ec556fff386bea2c23c41279f879c778e163))
* 修复子进程退出时的 "read fatal" 错误 ([7ea17c7](https://github.com/daydaychen/devproxy/commit/7ea17c7cf75b0350b22d0966ac9f0dad3ace2fe0))
* 劫持全局日志输出，彻底防止 goproxy 报错泄露到终端 UI ([f275cd7](https://github.com/daydaychen/devproxy/commit/f275cd7ced6adc7d2db59d3c23e6ae1706868d26))
* 更新代理服务器的请求处理逻辑。 ([9660038](https://github.com/daydaychen/devproxy/commit/9660038b0501eccd7f0bf5209791e081d68f43c8))
* 采用系统调用级重定向劫持 FD 2，从架构层面彻底杜绝日志泄露到终端 ([94488bf](https://github.com/daydaychen/devproxy/commit/94488bff143b9609fcc50fe766d502df17c0f08d))
* 重定向 goproxy 和 http.Server 日志，防止报错干扰交互式应用 UI ([3c4f970](https://github.com/daydaychen/devproxy/commit/3c4f970372b31dfce729886d4a291ec29eac0c7b))


### Performance Improvements

* **proxy:** 优化 codex-fix 插件，仅在模型不一致时进行替换 ([31e7667](https://github.com/daydaychen/devproxy/commit/31e76672e3cd8211f3f3246fd5da18e5883069fb))
* **proxy:** 第一阶段性能优化 - Buffer Pooling、Header 缓存、URL 规范化 ([7c0e38f](https://github.com/daydaychen/devproxy/commit/7c0e38f053644c76e010b19060ba37b6ec2b82be))
* **proxy:** 第二阶段架构优化 - MITM 索引、Transport 连接池、TLS 证书缓存 ([813ab8f](https://github.com/daydaychen/devproxy/commit/813ab8fa7f0f5363db99bc5f73ec17303e0984ed))
* 优化编译大小和执行性能 ([2dea56a](https://github.com/daydaychen/devproxy/commit/2dea56ae80dd2c5ee3396b11fba77c0c9e335e30))

## [未发布] - 2026-01-30

### 修复
- **交互式应用支持** - 修复了启动交互式应用（如 vim、bash）时无响应的问题
  - 移除了 `Setpgid` 进程组设置，确保子进程能正确接收和处理终端输入输出
  - 简化了进程终止逻辑，使用标准的 SIGTERM 信号
  - 子进程现在可以正常与终端交互

### 技术细节
**问题原因**：
之前的实现中设置了 `Setpgid: true`，这会将子进程放到新的进程组中。虽然这对于进程管理有好处，但会导致交互式应用无法正确处理终端的输入输出和信号。

**修复方案**：
1. 移除 `SysProcAttr.Setpgid` 设置
2. 保持 stdin/stdout/stderr 连接到父进程（已有）
3. 简化 Stop() 方法，直接使用 SIGTERM 信号而不是进程组终止

**影响范围**：
- ✅ 交互式应用（vim、bash、python等）现在可以正常工作
- ✅ 非交互式应用仍然正常工作
- ✅ 进程清理仍然可靠（使用 SIGTERM + Kill 组合）

## [初始版本] - 2026-01-30

### 新增
- HTTPS MITM 代理支持
- URL 匹配和请求头重写
- 上游代理转发
- 随机端口分配
- 进程隔离代理
- 详细日志输出
