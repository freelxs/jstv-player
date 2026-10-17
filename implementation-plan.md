# JSTV Player 改进实施记录与后续计划

本文件对应 `review.md` 的证据审查。P0/P1 已在此前批次实施；本轮按授权仅实施 P2.1 筛选 UI 与 P2.2 可选脱敏日志持久化。Node 权限执行模式仍延期；P2.3 签名/公证/App Store 相关工作明确排除。以下初始估算是粗略工程量范围，不是对实际耗时或交付期限的承诺；任务可拆批推进。

## 阶段状态与估算

| 阶段 | 状态 | 初始粗估 | 说明 |
|---|---|---:|---|
| P0 | 已实施 | 5.5–9.5 人日 | 明细上限 3+1+2+1.5+2=9.5；下限 2+0.5+1+1+1=5.5。跨平台行为仍有未验证项。 |
| P1 | 核心已实施；Node 权限执行模式延期 | 8–13 人日（初始粗估） | 明细上下限分别为 3+2+2+1=8、5+3+3+2=13。该估算不代表当前全部验收完成；Node 权限执行模式未实施、未计入增量估算。浏览器、真实播放、Node 18+ 和 Windows 运行尚未完成验证。 |
| P2.1/P2.2 | 已实施 | 初始估算 3–6 人日 | 本轮只覆盖筛选 UI 与可选日志持久化；按要求排除 P2.3 发行工作。 |
| P2.3 | 明确排除 | 未估 | macOS 签名/公证/App Store 均不在本次授权范围，不实施、不评估发行账号或工作流。 |
| 暂缓 | 未实施 | 未估 | DASH、嵌入 Node、自动重启、预检缓存。需另行验证需求和兼容性，不属于本轮范围。 |

## P0：可靠性与立即可见的风险（已实施）

### P0.1 Host 生命周期、bootstrap IPC 与失败清理

- **文件/函数**：`internal/catvod/host.go` `Host.Start`/`StartContext`/`Stop`/`Base`；`internal/catvod/bootstrap.js` shutdown IPC；`service.go` `Player.LoadSource`/`StopSource`。
- **方案与结果**：用 stdin JSON shutdown 消息在 Windows/macOS/Linux 共享关闭路径；bootstrap 幂等调用插件 `stop()`，EOF 兼容父进程关闭；限时等待后强杀并等待回收。启动支持 context/timeout；失败时清理子进程与 Host 状态。端口由插件绑定 0 并经 `JSTV_READY` 回报，避免临时端口释放后的抢占窗口。
- **验收**：Go 测试覆盖 mock 插件协议、stop 调用、启动异常清理、context 取消；Windows/Linux 做了交叉编译，未在 Windows/Linux 实际运行。
- **兼容与回滚**：旧调用保留 `Start(timeout)` 包装；插件仍从 `DEV_HTTP_PORT` 获得端口，需遵循本仓库 bootstrap 的 `JSTV_READY` 约定。回滚可恢复原启动路径，但也恢复端口竞争/清理缺陷。优雅 stop 不保证第三方插件自定义资源一定释放。
- **依赖/初始粗估**：Go/Node 标准能力，无新增依赖；2–3 人日。

### P0.2 init 单飞并允许失败后重试

- **文件/函数**：`service.go` `Player.initSite`/`client`。
- **方案与结果**：按 API 串行 init；仅成功后记录已初始化，并校验 Host 身份，避免切源期间旧请求写入新源状态。
- **验收**：`service_init_test.go` 覆盖并发首请求单飞、失败后重试成功及切源状态检查；纳入 Go 全量测试和 race。
- **兼容与回滚**：不改变插件协议；失败请求现在显式失败并允许下次用户请求重试。回滚只恢复失败也被缓存的旧行为。
- **依赖/初始粗估**：无新增依赖；0.5–1 人日。

### P0.3 下载流式、限额、原子替换与 MD5 先验收

- **文件/函数**：`internal/catvod/source.go` `FetchWithOptions`/`downloadTemp`；`replace_{unix,windows}.go`。
- **方案与结果**：脚本与配置流式落入同目录临时文件，`limit+1` 检出超限，同步计算 MD5/SHA-256；严格 MD5 在缓存发布前校验；单文件经平台替换函数原子发布，临时文件失败清理。脚本 256 MiB、配置 64 MiB。
- **验收**：本地 HTTP fixture 覆盖成功/哈希、严格 MD5 失败保留旧包、超限及临时文件清理。原子性是**单文件级**；脚本与可选配置不构成跨文件事务，磁盘/替换错误下不能承诺二者成对回滚。
- **兼容与回滚**：发布方 MD5 协议不变；默认仍可提示不一致，严格模式拒绝。回滚可恢复直接覆盖行为，但会失去失败时保留缓存的性质。
- **依赖/初始粗估**：Go 标准库；1–2 人日。

### P0.4 日志脱敏

- **文件/函数**：新增 `log_sanitize.go`；`service.go` `Player.log`/`Open`/`LoadSource`；`internal/catvod/host.go` 日志转发。
- **方案与结果**：写入内存日志前遮盖常见 URL userinfo、token 类 query、Cookie、Authorization 与常见签名键，并限制单行长度。未持久化日志。
- **验收**：`log_sanitize_test.go` 断言常见凭据样式不会原样进入日志；Go 测试通过。
- **兼容与回滚**：保留现有内存日志 API/500 行上限；脱敏规则可能漏掉未知插件自定义字段或格式，不能保证所有插件日志完全脱敏。回滚可恢复原文日志，但会重新暴露常见秘密。
- **依赖/初始粗估**：无新增依赖；1–1.5 人日。

### P0.5 LAN 插件服务显式警示与关闭失败安全

- **文件/函数**：`service.go` `Settings`/`SaveSettings`/`StopSource`；`internal/catvod/host.go` `listenHost`/启动；`web/index.html`、`web/app.js` 设置流程。
- **方案与结果**：`allowLan` 默认 false；UI 明确提示绑定所有接口及插件代理能力。切换设置时先与 `LoadSource` 共用加载锁串行，再同步停止旧进程；这样即使设置变更碰到正在启动的旧配置，也会在保存调用返回前停止该进程。随后由 UI 重载；重载失败不会遗留旧的 all-interface listener。LAN 插件 API 与 LAN 媒体授权分成两个开关。
- **验收**：本地 service 测试覆盖关闭后旧 Host 已停止及重载失败路径；未实测物理 LAN、防火墙或 IPv6 可达性。
- **兼容与回滚**：继续读取已有 `allowLan` 字段，不静默改用户选择；改动失败时服务保持关闭。回滚会失去关闭旧 listener 的同步保证。
- **依赖/初始粗估**：无新增依赖；1–2 人日（覆盖跨平台 LAN 行为验证）。

## P1：安全约束与更新信任（核心已实施；Node 权限执行模式延期）

### P1.1 本地代理 opaque ID 与播放授权映射

- **文件/函数**：`internal/catvod/proxy.go` `Grant`/`WrapGranted`/`ServeHTTP`/`RewritePlaylist`；`internal/catvod/client.go` 本地地址校验；`service.go` `Player.Play`/`Open`/`StopSource`；`web` 无客户端可控上游代理参数。
- **方案与结果**：移除 `u/h` 任意目标入口；只有当前插件 `play` 返回并由服务端校验登记的 URL/header pair 才能映射为高熵 opaque ID。HLS variant/key/segment 子资源在同一播放 session 下逐个映射；转发 Range，跟随重定向；订阅停止/切换清空 grant 和资源，单 ID 两小时过期并设容量上限。插件明确返回的 loopback 播放地址可授权；当前插件自身的 `/spider`/`/proxy`/`/website` 仍由本地化逻辑处理；LAN 媒体需独立设置明确授权。公网 CDN 动态主机变化通过重定向跟随支持，但每个新清单资源仍受本地地址策略限制。没有保留旧任意代理双协议默认后门。
- **验收**：本地 `httptest` fixture 覆盖伪造/未知 ID、query 拒绝、失效、Range、HLS key/variant、动态 CDN 重定向、本地 loopback 例外、用户 LAN 授权和停止后撤销。mock 成功只验证请求链路，不等于真实播放器播放。
- **兼容与回滚**：代理 URL 内部格式从可控参数变为应用生成 ID；UI/协议未要求外部保存旧代理 URL。对合法插件当前 `/spider` 等路由和显式 play loopback 做例外。关闭 LAN media 会拒绝被分类为私网的播放地址。回滚旧 `u/h` 协议会恢复任意目标代理风险，不作为默认兼容方案。
- **边界**：随机端口、Origin、CORS、浏览器 PNA/LNA 与 WebView 可达性未实测；DNS 检查并非 DNS rebinding 的强保证；有效 ID 若被同源恶意代码/本机进程窃取，可在失效前访问已授权资源。
- **依赖/初始粗估**：核心 Go 无新增依赖；3–5 人日（真实浏览器矩阵另需现成/可用环境）。

### P1.2 Node 环境白名单与权限模型评估（权限执行模式延期）

- **文件/函数**：`internal/catvod/host.go` `nodeEnvironment`/`Host.StartContext`；`service.go` `ProbeNodePermissions`；`web/index.html`/`app.js` 高级设置；`docs/SECURITY.md`。
- **状态**：环境变量默认白名单、高级完整继承选项和标志探测按钮已实施；可选权限执行模式**未实现，明确延期**。不能将 P1.2 或 P1 全部表述为完成。
- **方案与结果**：默认仅传必要 PATH、平台、语言、CA 环境变量；高级兼容选项允许用户显式继承完整父环境（可能含代理凭据等秘密）。当前按钮只读所选 Node 的 `--help` 标志，不执行带权限参数的插件进程，也不代表安全边界。
- **延期原因**：Node 18+ 的权限标志、稳定性与行为有版本差异；权限模式可能拒绝插件文件/网络/子进程能力，且无法把 Node 权限模型当作恶意脚本沙箱。没有完成最低支持版本矩阵及真实插件兼容测试前，不新增 opt-in 执行模式，以免把不兼容或无效限制包装成安全功能。
- **后续验收**：先建立 Node 18 至当前版本矩阵，以仓库 mock 和明确授权的本地 fixture 验证文件读写、证书、DNS/网络、子进程需求；若决定实施，再新增明确标注实验性的 opt-in 模式、独立兼容设置及失败回退 UX，并证明关闭选项时 Node 18+ 启动行为不变。完成矩阵与兼容方案后再单独评估/排期；当前提交不包含权限执行模式。
- **兼容与回滚**：当前 Node 18+ 默认启动参数不变；代理/自定义证书环境可用高级完整继承开关。白名单导致插件启动失败时可选完整继承；回滚白名单会重新暴露父环境秘密。未来执行模式须默认关闭、独立开关、可立刻回退为无权限启动；能力标志探测不能替代执行测试。
- **依赖/初始粗估**：当前环境收敛/探测无新增依赖；2–3 人日原估仅覆盖本条计划范围，Node 权限执行模式的实现、兼容矩阵和 UI 变更需另估。

### P1.3 SHA-256 首次信任及更新审批

- **文件/函数**：`internal/catvod/source.go` SHA-256；新增 `trust.go` 与平台原子 pin 替换；`service.go` `LoadSource`/`ApproveSource`；`web/app.js`/`web/index.html` 确认 UI；`docs/SECURITY.md`。
- **方案与结果**：按订阅保存 URL+SHA-256 pin。首次使用、内容变化或 URL 更换都先停止源并展示确认；批准 pin 后再由用户显式重载；拒绝不执行新包。旧 pin 文件损坏时失败关闭。
- **验收**：`subscriptions_test.go` 与前端 Node 测试覆盖首次确认、拒绝、内容变化、URL 变化和重新批准；Go 测试通过。
- **兼容与回滚**：旧用户首次升级进入确认流程；原发布者 MD5 仍按协议读取和严格/提示语义工作。SHA-256 TOFU 只表示本机接受某字节内容，不能认证发布者，无法保护首次下载。删除 pin 可恢复再次确认而非静默信任旧包。
- **依赖/初始粗估**：Go 标准库；2–3 人日，含前端与状态迁移。

### P1.4 测试与 CI

- **文件/函数**：保留 `.github/workflows/build.yml` 原三平台 vet/test/build jobs，增加 Node test 和 Linux race；新增与修改 `*_test.go`、`web/*.test.cjs`。
- **方案与结果**：CI 原有配置保留，未把它误报为缺失。Go 1.27.2 本地工具链按已有 Homebrew 安装获得，未改 `go.mod` 版本；未访问真实在线源或运行第三方订阅脚本。Go 测试使用显式临时 `GOMODCACHE`/`GOCACHE` 且 `GOPROXY=off`，避免依赖下载。已本地运行 `go test -count=1 ./...`、`go vet ./...`、`go test -race -count=1 ./...`、`node --test web/*.test.cjs`；还对 Windows 与 Linux 做 Go 测试二进制交叉编译。带命令、版本、环境和退出码的运行记录保存在 `test-logs/`。
- **验收与限制**：完整命令、Go/Node 路径和版本、缓存环境及退出码均记录在 `test-logs/`。本次模块缓存位于 `/private/tmp/jstv-player-gopath/pkg/mod`；若只设 `GOPROXY=off` 却使用默认 Go module cache，当前环境找不到 `github.com/egoist/mygo@v0.5.5`，因而会如复跑者所见失败。没有查询远程 Actions 运行，故不宣称 CI job 已通过。交叉编译不等于 Windows/Linux 实际运行。mock 插件/本地 fixture 的请求成功不等于真实媒体播放。
- **兼容与回滚**：Go/Node 版本采用既有配置；race job 仅 Linux runner 执行。CI 新增步骤可单独回滚，不影响运行时。
- **依赖/初始粗估**：Go/Node 工具链；1–2 人日。

## P2：本轮授权实施项

### P2.1 筛选 UI（已实施）

- **文件/函数**：`web/app.js` 首页/分类渲染；`web/index.html` 筛选容器；`web/style.css` 控件；`service.go` `Category`；`internal/catvod/client.go` `Home`/`Category`。
- **方案与结果**：支持 `home.filters[categoryID]` 为数组，元素形如 `{key,label,options:[{value,label}]}`；每个筛选独立单选下拉框，选择调用分类 API 并重置页码，分页沿用当前 map；站点、分类和订阅源切换清空选择。格式缺失、畸形、重复 key 或超限则隐藏该分类全部控件，并以 `{}` 请求。仓库 mock 已补一组本地地区筛选 fixture；不声称支持任意插件的单/多选协议变体。
- **验收**：`web/app-async.test.cjs` 覆盖格式校验、畸形退化、UI 选择请求、页码重置、分页保留、分类/站点切换；请求使用既有 token 丢弃过期结果。原生 `<select>` 支持键盘，CSS 为窄屏改为纵向控件；未启动真实桌面 WebView，视觉效果未实测。
- **兼容/回滚**：`Home`/`Category` 网络字段兼容既有协议；旧设置无需迁移。单独回滚前端容器与 JS/CSS 可恢复原无 UI 行为，mock/doc同步回滚。
- **依赖/粗估**：无新增依赖；初始粗估 2–4 人日，不是承诺。

### P2.2 日志持久化（已实施）

- **文件/函数**：`service.go` `Player.log`/`Logs`/`ClearLogs`/`SaveSettings`；`logs_persist.go`；设置/日志 UI；README 与 `docs/SECURITY.md`。
- **方案与结果**：新增 `persistLogs`，旧设置缺字段时默认 false。仅在既有 sanitizer 之后写盘；RFC3339 UTC 行时间戳、单行含换行最多 4096 字节、每文件 1 MiB、4 个文件总计 4 MiB。读取时仅返回近 7×24 小时行并清理过期行，启动清理一次，追加时每小时最多清理一次；容量上限可以提前淘汰。关闭且应用闲置时磁盘清理推迟到启动/读取，但 UI 不呈现过期数据。串行 mutex 保证并发写、切换关闭和清除；目录 0700、文件 0600（平台支持时）。轮转失败或磁盘错误不向播放调用传播。日志页可清除内存及固定名称日志文件；不删除 pin/history/plugin 配置。未知插件秘密仍可能漏脱敏，UI 与文档明示。
- **验收**：Go tempdir 测试覆盖默认关闭、脱敏、权限、轮转/总量、并发、清除边界、设置关闭后无后续写入、磁盘错误不影响 `log()`、新 mtime 文件中过期行过滤、重启只读清理、关闭时不展示磁盘日志、UTF-8 与换行总长上限；Node tests 覆盖 UI/API。文件清除/权限在 macOS 测试；Windows 权限语义未实机验证。重启读取由新 Player 实例读取临时日志目录模拟，未启动完整桌面 app 验证。
- **兼容/回滚**：新增 JSON bool 默认关闭，旧设置可读取；内存 `Logs` API 保留。回滚代码不自动删除 `logs/` 数据；再次运行旧版不会继续追加日志。
- **依赖/粗估**：Go 标准库；初始粗估 1–2 人日，不是承诺。

## 明确暂缓

- **DASH**：需要独立处理 MPD XML、模板/时间线、Range、相对引用、加密和播放器兼容；当前没有实现或播放验证。
- **嵌入 Node**：牵涉三平台打包体积、许可、CVE 更新责任；本轮不做。
- **崩溃自动重启**：先根据线上/用户证据定义重启条件、退避和防循环；本轮只保证启动失败清理与错误返回。
- **预检缓存**：签名 URL/headers 可能改变或过期；无性能数据前不做。
- **P2.3 签名/公证/App Store**：用户明确排除，本轮未触碰发行流程、凭证或工作流。

## 尚未验证

- 真实第三方插件、在线订阅、真实 CDN、音视频解码和真实播放器端到端播放均未运行。
- 浏览器/WebView 的 PNA/LNA、Origin 与随机 loopback 端口策略，主机防火墙及 LAN 可达性未实测。
- Node 18+ 各具体小版本与插件的环境兼容矩阵、权限能力行为未完整验证。
- Windows 实际运行/IPC 与原子替换未执行；仅 Windows/Linux 测试二进制交叉编译通过。macOS 实际 GUI 未启动，未访问用户应用数据，也无截图。
- GitHub Actions 的本分支运行记录未查询；本地测试成功不能替代远程 CI 结果。
- P2 本地基线与实现后复验的命令、工具版本、缓存环境和退出码分别记录在 `test-logs/p2-baseline-*.log`、`test-logs/p2-final-*.log`；HTML/WebView 的视觉 GUI 未启动，未生成截图。
