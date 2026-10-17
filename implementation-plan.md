# JSTV Player 改进实施记录与后续计划

本文件对应 `review.md` 的证据审查。用户随后授权在当前 `jstv-82de` worktree 实施全部 P0/P1；本轮已改代码、测试、UI 与文档，**未实施 P2**。以下估算是最初实施前的粗略工程量范围，不是对实际耗时或交付期限的承诺；任务可以拆批执行，按风险与测试结果排序，不要求一次性落地。

## 阶段状态与估算

| 阶段 | 状态 | 初始粗估 | 说明 |
|---|---|---:|---|
| P0 | 已实施 | 5.5–9.5 人日 | 明细上限 3+1+2+1.5+2=9.5；下限 2+0.5+1+1+1=5.5。跨平台行为仍有未验证项。 |
| P1 | 已实施 | 8–13 人日 | 明细上下限分别为 3+2+2+1=8、5+3+3+2=13。浏览器、真实播放、Node 18+ 和 Windows 运行尚未完成验证。 |
| P2 | 未实施 | 5–10 人日以上 | UI 筛选、日志落盘、macOS 签名/公证评估；公证还依赖外部账号与发行条件。 |
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

## P1：安全约束与更新信任（已实施）

### P1.1 本地代理 opaque ID 与播放授权映射

- **文件/函数**：`internal/catvod/proxy.go` `Grant`/`WrapGranted`/`ServeHTTP`/`RewritePlaylist`；`internal/catvod/client.go` 本地地址校验；`service.go` `Player.Play`/`Open`/`StopSource`；`web` 无客户端可控上游代理参数。
- **方案与结果**：移除 `u/h` 任意目标入口；只有当前插件 `play` 返回并由服务端校验登记的 URL/header pair 才能映射为高熵 opaque ID。HLS variant/key/segment 子资源在同一播放 session 下逐个映射；转发 Range，跟随重定向；订阅停止/切换清空 grant 和资源，单 ID 两小时过期并设容量上限。插件明确返回的 loopback 播放地址可授权；当前插件自身的 `/spider`/`/proxy`/`/website` 仍由本地化逻辑处理；LAN 媒体需独立设置明确授权。公网 CDN 动态主机变化通过重定向跟随支持，但每个新清单资源仍受本地地址策略限制。没有保留旧任意代理双协议默认后门。
- **验收**：本地 `httptest` fixture 覆盖伪造/未知 ID、query 拒绝、失效、Range、HLS key/variant、动态 CDN 重定向、本地 loopback 例外、用户 LAN 授权和停止后撤销。mock 成功只验证请求链路，不等于真实播放器播放。
- **兼容与回滚**：代理 URL 内部格式从可控参数变为应用生成 ID；UI/协议未要求外部保存旧代理 URL。对合法插件当前 `/spider` 等路由和显式 play loopback 做例外。关闭 LAN media 会拒绝被分类为私网的播放地址。回滚旧 `u/h` 协议会恢复任意目标代理风险，不作为默认兼容方案。
- **边界**：随机端口、Origin、CORS、浏览器 PNA/LNA 与 WebView 可达性未实测；DNS 检查并非 DNS rebinding 的强保证；有效 ID 若被同源恶意代码/本机进程窃取，可在失效前访问已授权资源。
- **依赖/初始粗估**：核心 Go 无新增依赖；3–5 人日（真实浏览器矩阵另需现成/可用环境）。

### P1.2 Node 环境白名单与权限探测边界

- **文件/函数**：`internal/catvod/host.go` `nodeEnvironment`/`Host.StartContext`；`service.go` `ProbeNodePermissions`；`web/index.html`/`app.js` 高级设置；`docs/SECURITY.md`。
- **方案与结果**：默认仅传必要 PATH、平台、语言、CA 环境变量；高级兼容选项允许用户显式继承完整父环境（可能含代理凭据等秘密）。Node 权限只做可选 `--help` 能力标志探测，不启用权限参数、不作为恶意脚本沙箱。
- **验收**：Go 测试核对默认环境白名单；前端 Node 测试覆盖能力展示/设置流程。未跑完整 Node 18+ 权限行为矩阵，实际插件环境兼容仍需用户/CI 版本矩阵验证。
- **兼容与回滚**：Node 18+ 默认启动参数不变；代理/自定义证书环境可用高级完整继承开关。若白名单导致某插件启动失败，可启用该选项；回滚默认全继承会重新暴露父环境秘密。权限标志探测显示不等同执行策略。
- **依赖/初始粗估**：无新增依赖；2–3 人日，Node 版本矩阵另计。

### P1.3 SHA-256 首次信任及更新审批

- **文件/函数**：`internal/catvod/source.go` SHA-256；新增 `trust.go` 与平台原子 pin 替换；`service.go` `LoadSource`/`ApproveSource`；`web/app.js`/`web/index.html` 确认 UI；`docs/SECURITY.md`。
- **方案与结果**：按订阅保存 URL+SHA-256 pin。首次使用、内容变化或 URL 更换都先停止源并展示确认；批准 pin 后再由用户显式重载；拒绝不执行新包。旧 pin 文件损坏时失败关闭。
- **验收**：`subscriptions_test.go` 与前端 Node 测试覆盖首次确认、拒绝、内容变化、URL 变化和重新批准；Go 测试通过。
- **兼容与回滚**：旧用户首次升级进入确认流程；原发布者 MD5 仍按协议读取和严格/提示语义工作。SHA-256 TOFU 只表示本机接受某字节内容，不能认证发布者，无法保护首次下载。删除 pin 可恢复再次确认而非静默信任旧包。
- **依赖/初始粗估**：Go 标准库；2–3 人日，含前端与状态迁移。

### P1.4 测试与 CI

- **文件/函数**：保留 `.github/workflows/build.yml` 原三平台 vet/test/build jobs，增加 Node test 和 Linux race；新增与修改 `*_test.go`、`web/*.test.cjs`。
- **方案与结果**：CI 原有配置保留，未把它误报为缺失。Go 1.27.2 本地工具链按已有 Homebrew 安装获得，未改 `go.mod` 版本；未访问真实在线源或运行第三方订阅脚本。已本地运行 `go test -count=1 ./...`、`go vet ./...`、`go test -race -count=1 ./...`、`node --test web/*.test.cjs`；还对 Windows 与 Linux 做 Go 测试二进制交叉编译。命令 stdout/stderr 原文保存在 `test-logs/`。
- **验收与限制**：本地命令结果见 `test-logs/`；没有查询远程 Actions 运行，故不宣称 CI job 已通过。交叉编译不等于 Windows/Linux 实际运行。mock 插件/本地 fixture 的请求成功不等于真实媒体播放。
- **兼容与回滚**：Go/Node 版本采用既有配置；race job 仅 Linux runner 执行。CI 新增步骤可单独回滚，不影响运行时。
- **依赖/初始粗估**：Go/Node 工具链；1–2 人日。

## P2 后续项（未实施）

### P2.1 筛选 UI

- **文件/函数**：`web/app.js` 首页/分类渲染；`web/index.html` 筛选容器；`web/style.css` 控件；`service.go` `Category`；`internal/catvod/client.go` `Home`/`Category`。
- **方案**：依据仓库协议/mock 实际 filters 元数据渲染筛选项，切换分类清理不兼容选择，分页保留筛选；样例不足时补本地 fixture。
- **验收**：空/单/多 filters、分页保留、切站切类清理、请求字段与键盘/窄屏交互。
- **兼容/回滚**：协议字段不变；可独立回滚 UI。
- **依赖/工作量**：无新增依赖；2–4 人日。

### P2.2 日志落盘

- **文件/函数**：`service.go` `Player.log`/`Logs`；新 writer 与日志设置 UI。
- **方案**：仅落盘脱敏后日志；限制单文件与保留量，安全权限、轮转、关闭/清除入口。未知插件自定义秘密无法保证完全脱敏，须继续提示用户。
- **验收**：轮转、并发、写失败、清除、权限、总量上限、重启读取与脱敏。
- **兼容/回滚**：保留现有内存 Logs API；可关闭写盘，回滚不自动删用户日志。
- **依赖/工作量**：标准库；1–2 人日。

### P2.3 macOS 签名与公证评估

- **文件/函数**：构建/发行 workflow、README 下载说明与制品路径（需先核对）。
- **方案**：确认 Apple Developer 身份、证书/secret 管理与发行流程后制定签名、公证、staple 计划；不凭源码推断已有发行权限。
- **验收**：对实际下载制品做 Gatekeeper、公证票据与首次启动验证。
- **兼容/回滚**：可回退未签名制品并同步说明；证书依赖外部发行条件。
- **依赖/工作量**：Apple 账号与 macOS runner；实施约 2–4 人日，外部等待另计。

## 明确暂缓

- **DASH**：需要独立处理 MPD XML、模板/时间线、Range、相对引用、加密和播放器兼容；当前没有实现或播放验证。
- **嵌入 Node**：牵涉三平台打包体积、许可、CVE 更新责任；本轮不做。
- **崩溃自动重启**：先根据线上/用户证据定义重启条件、退避和防循环；本轮只保证启动失败清理与错误返回。
- **预检缓存**：签名 URL/headers 可能改变或过期；无性能数据前不做。

## 尚未验证

- 真实第三方插件、在线订阅、真实 CDN、音视频解码和真实播放器端到端播放均未运行。
- 浏览器/WebView 的 PNA/LNA、Origin 与随机 loopback 端口策略，主机防火墙及 LAN 可达性未实测。
- Node 18+ 各具体小版本与插件的环境兼容矩阵、权限能力行为未完整验证。
- Windows 实际运行/IPC 与原子替换未执行；仅 Windows/Linux 测试二进制交叉编译通过。macOS 实际 GUI 未启动，未访问用户应用数据，也无截图。
- GitHub Actions 的本分支运行记录未查询；本地测试成功不能替代远程 CI 结果。
