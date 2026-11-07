# 0.4.0 实施与验收记录

日期：2026-11-07。基线为本地 `17cf806`，当前改动留在工作区，未提交或发布 GitHub。
已按 `20261107fixplan-review.md` 完成 F0–F14 的本轮范围，版本更新为 0.4.0。
EPG、非 HTTP 直播及外部播放器位置回传沿用修订计划中的后续范围。

## 产物

- [macOS 通用 ZIP](../out/JSTV-Player-0.4.0-macos-universal.zip)
- [macOS 通用 DMG](../out/JSTV-Player-0.4.0-macos-universal.dmg)
- [保留的 App](<../out/.jstv-build.LPfKHz/dist/darwin-universal/JSTV Player.app>)
- [源码快照、产物摘要与验收清单](../out/JSTV-Player-0.4.0-build.json)

App 的 `CFBundleShortVersionString` 为 0.4.0，包含 arm64 与 x86_64。
`codesign --verify --deep --strict`、ZIP 完整性检查和 DMG 校验均通过。
签名为构建机 ad-hoc 签名，不是 Developer ID 公证。本轮未安装或替换已有 App。
原有 `out/JSTV-Player-0.3.9-macos-universal.zip` 保留。

| 文件 | SHA-256 |
|---|---|
| ZIP | `5455637a0b382e1b176aa933d458a0120c2f4bf7d275024ceb7a59a0c1fb5828` |
| DMG | `33a38bf464e937477420946c882b173bc1e87291e1768db9def392dd2f8749d3` |

源码快照摘要：`c9541691d1730d0a5a40887caf9dc9f194550fb33caca4a41c1f32b74a63210e`。
清单覆盖 124 个实现、前端、资源、测试与构建文件；逐文件摘要保存在上述 JSON。

## 逐项完成依据

| 状态 | 计划项 | 实现与验收依据 |
|---|---|---|
| [x] | F0 可执行配置信任 | `catvod.Source` 同时记录脚本、配置及完整清单摘要；配置新增/修改/删除均重新确认。`TestExecutableConfigChangesRequireApproval` |
| [x] | F4 配置 URL | Path 推导保留 query，扩展名大小写兼容。`TestConfigURLPreservesQueryAndCaseInsensitiveExtension` |
| [x] | F5 完整版本发布/回退 | 不可变版本目录，启动成功后才切换 `current.json`；配置失败保留原版本；旧版回退显式确认。`TestConfigFailureRetainsCompletePublishedVersion`、`TestFailedStartRetainsWorkingVersionAndSupportsExplicitRollback` |
| [x] | F1 配置页链路 | RPC 增加 OpenConfigPage，配置入口不依赖故障站点 init；配置卡片统一转换到本应用包装页。能力 URL 仅代理已核查路由；不直接开放整个 `/website` 管理面 |
| [x] | F13 隔离/LAN | opaque-origin sandbox、登录会话与源代际绑定的 30 分钟能力，剥离应用凭据，禁止重定向和执行台/文件管理；LAN 二次确认。`TestConfigurationCapabilitiesScopeRoutesAndRevoke`、`TestConfigurationBrowserReadWriteAndIsolation` |
| [x] | F2 错误归一化 | HTTP、网络、非 JSON、业务错误和超限使用结构化诊断；原始详情经日志脱敏，UI 有全失败汇总。Go ProtocolError 测试、前端全失败/正常空结果区分及最终浏览器流式搜索失败/恢复场景 |
| [x] | F3 空状态 | 已识别的插件直播站点提供“配置直播源”和“刷新站点”；原生直播无频道时返回格式/网络诊断，不假装已成功加载；前端专门验证配置按钮与刷新动作 |
| [x] | F6 响应上限 | 协议响应按 32 MiB + 1 检测溢出，包含错误体。`TestProtocolResponseLimitAppliesToSuccessAndErrorBodies` |
| [x] | F7/F8 生命周期 | 等锁前取消旧代际；下载、启动、配置与协议请求可取消；列表、进度、授权与配置能力撤销；旧清单不能重新签发分片。下载/启动取消、旧列表与代理 in-flight 测试 |
| [x] | F9 原子文件 | 共享 atomicfile 模块及 Windows 替换；settings/history/favorites 私有原子写；损坏历史保留原始备份。`TestReadersSeeOnlyCompleteVersions`、`TestHistoryCorruptionIsPreservedBeforeRecording` |
| [x] | F10 代理继承 | 默认关闭的独立继承开关，不继承其他秘密；全环境兼容模式仍显式选择。`TestProxyOnlyInheritanceDoesNotIncludeOtherSecrets` |
| [x] | F11 原生直播 | M3U/TXT 分组、台标、相对 URL、UA/Referer、URL 尾 headers、TVBox lives 和参考 JPEG/Base64 包装；保留结构化 URL，拒绝把 HLS 清单当频道目录。真实浏览器与外部解码验收 |
| [x] | F11 长时直播 | 活跃直播按空闲期限续期；同会话重复清单复用资源；停止立即撤销。`TestLiveLeaseRenewsButOrdinaryLeaseDoesNot`、重复清单及旧请求撤销测试，使用可控时间状态验证，无需等待两小时 |
| [x] | F12 缓存/刷新 | 启动缓存重算 SHA-256；条件刷新分别检查脚本与配置；强制下载忽略标记；损坏缓存隔离修复；Home/Category 按代际、筛选和分页缓存，配置保存后失效。条件刷新/修复/缓存复制隔离测试 |
| [x] | F14 收藏/续播/健康 | 原生频道收藏跨代际；内置/网页进度令牌与十秒合并，暂停/结束限频刷新；搜索近期失败站点后置、成功恢复。Go library/health 测试及浏览器收藏/暂停/续播 |
| [x] | RPC 防遗漏 | `TestFrontendLiteralCallsHaveWebAdapters` 扫描前端 literal call/rpc，与显式 HTTP 方法表核对；CI 默认 Go 测试包含此项 |
| [x] | CI 回归 | 新增 browser-test 作构建门槛，安装固定 Playwright 1.65.0，运行配置隔离、布局及播放端到端；旧发布源没有新脚本时保留兼容。此处只完成工作流代码，未触发远端 Actions |

实现入口见 `internal/app/`、`internal/catvod/`、`internal/live/`、`internal/atomicfile/` 和 `internal/webserver/configuration.go`。
`mygo.ts` 已用生成器更新，文档与版本元数据一致。

## 实际运行的门槛

| 检查 | 结果 |
|---|---|
| `go vet ./...` | 通过 |
| `go test ./...` | 全包通过 |
| `go test -race ./... -json` | 151 个测试项通过，2 个有意跳过，0 失败 |
| `node --test web/*.test.cjs`，提供 CHROME | 33 通过，0 跳过 |
| `bash scripts/build.test.sh` | 通过，包括失败归档不覆盖旧产物的负向 fixture |
| macOS universal build | Go 1.27.3 + MyGo 0.8.1，真实编译、签名、ZIP/DMG 校验通过 |
| Windows amd64 / Linux amd64 | 当前源码交叉编译通过；未在对应系统执行 |
| 配置页真实 Chromium | 读取→保存→重读通过；opaque origin、DOM/RPC 隔离、无应用 Cookie 通过 |
| 外部播放器 | 实际经过 Player.PlayEpisodes→Open→自定义 FFmpeg 程序，并解码出 H.264 视频帧 |
| 最终 App 可执行文件端到端 | M3U 导入、PHP HLS 解码、直播收藏、插件信任审批、全站搜索失败归一化与恢复、MP4 播放和位置续播均通过 |

Go 的两个跳过项是：默认不联网的 `TestPublicReferenceSubscription`（另已显式联网运行通过），以及本机不适用的平台分支检查 `TestFindSenPlayerOnlyOnDarwin`。
测试使用 Node v26.11.1、Playwright 1.65.0 / Chromium headless shell 158。

最终包的[浏览器结果 JSON](../.tmp/e2e-package-final/results.json)记录 4 组场景，零页面异常，12 次已验证媒体请求。
[直播截图](../.tmp/e2e-package-final/native-live.png)与[续播截图](../.tmp/e2e-package-final/vod-resume.png)中的蓝色视频是项目自产测试素材，不是公共频道画面。
逐项 Go 竞态结果位于 `.tmp/final-race.jsonl`；这些本机测试产物目录已加入忽略规则。

## 参考输入核验

显式对用户提供的参考订阅运行 `TestPublicReferenceSubscription`：
成功解码 JPEG + marker + Base64 配置，解析 **2632 个频道、14 个分组**。
两个外部列表当时不可用，不会清空其他可用频道；运行时显示逐源提示。
这是实际列表数据核验，没有执行该配置引用的点播 JS/JAR，也没有逐台验证全部公共频道播放。

配置代理的路由清单依据实际下载的参考 JS bundle 静态核查：
SHA-256 `6fc3f25c0d9a8fb19119cf9bc2bf29500da480cf45ec70282c418975b719d402`，
MD5 `a5532c5b2672ffc535735d0c22a8fc56`，与原计划的字节基准一致。
先前网页抓取工具返回的另一 MD5 不能代表本次下载字节。
确认直播 API 是注册前缀下的 `/website/live`，不是直接根路径 `/live`；页面资源、站点配置、云盘登录及播放配置均据实际注册路径列入允许表。
本实现未复制或翻译 GPL 项目的解析源码，仓库许可维持 MIT。

## 使用与边界

设置→订阅可选择“自动识别”“JS 插件”“M3U/TXT/TVBox 直播”。
原生直播无需 Node；此通用包未额外内置 Node，JS 插件沿用系统运行时。
“启动缓存”不查询远端；“重新加载”检查脚本与配置；设置内的“强制下载刷新”忽略服务器缓存标记。

仅支持 HTTP(S) 媒体，浏览器实际可播放性仍受编码和封装限制。
未增加 EPG、UDP/RTSP 播放或外部播放器位置反馈；未承诺 TVBox 所有私有加密/表达式配置。
配置沙箱不开放脚本执行台、上传、数据库和文件管理；其他 bundle 可能需新增经核查的配置路由。
Windows/Linux 为交叉编译检查，第三方真实频道没有逐台播放；macOS 原生窗口及每种 GUI 外部播放器没有逐一人工操作。
