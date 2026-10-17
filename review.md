# 改进建议证据审查

审查范围：实施前基线 commit `40c7345819b8` 的 worktree 源码、仓库测试、`README.md`、`docs/PROTOCOL.md` 与外部建议文件。本文所有源文件行号均固定指该基线版本，避免实施后行号移动造成误引；现状/修复后的验证以 `implementation-plan.md` 和 `docs/SECURITY.md` 为准。判断状态含义：**保留**=方向有依据；**修正**=问题真实但原论断/方案过强；**延期**=可做但不是当前优先项；**删除**=该条具体论据或方案不成立。置信度针对本地代码事实；环境/浏览器行为无法在此复现时另行注明。

## 逐条结论

| 建议 | 状态 / 置信度 | 代码证据与审查结论 |
|---|---|---|
| Node `--permission` 限制插件 | **修正 / 高** | `internal/catvod/host.go@40c7345819b8:111` 用 `exec.Command` 直接启动 Node；113–120 将完整 `os.Environ()`（基线；现版本默认白名单并提供显式高级继承选项） 继承给子进程，再追加端口、`NODE_PATH`、配置等。Node 权限模型最多是纵深防御/减少误操作，不是恶意代码安全沙箱：进程仍以用户身份运行，现有环境变量可能含凭据；仅加 `--permission` 不能抹掉它们。README 声明支持 Node 18+（`README.md@40c7345819b8:28`），建议的权限选项在不同 Node 版本中的可用性、稳定性和语法不同；Node 26 实测不能外推到最低支持版本。协议又明确插件需要完整内置模块（`docs/PROTOCOL.md@40c7345819b8:19-27`），白名单可能破坏插件。应先做版本能力探测、最小环境变量方案和兼容矩阵；不可宣称已验证可作为安全边界。 |
| 本地代理 token + Origin，称任意网页能读内网 | **修正 / 高** | `internal/catvod/proxy.go@40c7345819b8:27-34` 监听随机 loopback 端口；132–163 接受带 `u=` 的任意 HTTP(S) 目标且反射响应 CORS `*`，故本地代理确实缺少请求授权/目标限制。但“任意网页必然可读内网”过强：攻击者通常还要发现随机端口，浏览器对 loopback/私网访问受 Origin、PNA/LNA/权限提示与浏览器版本策略影响；CORS `*` 只放宽响应读取，不保证请求能发出或目标可达。Origin 校验对非浏览器客户端可伪造；固定 allowlist 对任意目标代理有帮助但插件域名、重定向、DNS 重绑定和媒体 CDN 会增加复杂性；随机 bearer token 会被同源恶意代码、插件/日志/本机进程泄漏时滥用，也不替代目标限制。先做威胁模型、限制方法/地址空间/重定向，并验证 webview 与主流浏览器。 |
| 本地记住 SHA-256（TOFU） | **修正 / 高** | `internal/catvod/source.go@40c7345819b8:59-70` 获取发布者 `.md5` 并计算本地 MD5，当前只检查一致性；`service.go@40c7345819b8:272-276` 默认仅提示，可配置严格拒绝。发布者同时控制脚本与 `.md5`，不能靠客户端更换算法修协议。客户端 SHA-256 pin 可检测同一安装之后内容发生变化、触发审批；首次下载的 TOFU 仍接受当时下载的恶意内容，不能认证发布者，也不能防首次 MITM/源站被攻陷。应明确首次信任与更新确认语义，并考虑可信重置/订阅 URL 变更时 pin 迁移。 |
| 停止调用插件 `stop()` / SIGTERM 优雅退出 | **修正 / 高** | `internal/catvod/bootstrap.js@40c7345819b8:58-72` 只调用 `start(config)`；stdin end 直接 `process.exit(0)`，没有调用导出 `stop()`。Go `internal/catvod/host.go@40c7345819b8:182-197` 关闭 stdin，1.5 秒后 Kill。建议方向成立，但 SIGTERM 是否被 Windows、插件及运行时统一支持不能假定；优先用 IPC/shutdown hook 调用 stop，再限时等待，超时强制终止并跨平台覆盖。 |
| 插件崩溃后自动重启一次/心跳 | **延期 / 高** | `internal/catvod/host.go@40c7345819b8:145-167` 有 wait goroutine，但进程退出后仅在 Start 等待阶段报错；成功后 `Base()` 只检查 `cmd != nil`（172–180），故可返回死进程端口。问题真实，自动重启会与订阅切换、进程退出、init 状态及用户触发请求竞态。先做退出状态传播/健康错误，再评估重启；避免未经请求的循环重启。 |
| LAN 开关服务暴露局域网 | **修正 / 高** | `internal/catvod/host.go@40c7345819b8:86-93` LAN=true 绑定 `0.0.0.0`；`internal/catvod/bootstrap.js@40c7345819b8:11-24` 将插件 HTTP 服务监听于该地址；`service.go@40c7345819b8:37-40,149-157,279` 有 `allowLan` 设置并在切换时重载。协议含 `/config`、站点 API 和插件代理路由，未见认证中间件。因此是实质暴露面，应默认关闭并在设置中清楚显示/提示；token 可降低未授权访问，但会让局域网网页/设备的 CORS、共享 URL 使用更复杂，不能只加提示就视为安全。现有默认关闭是有效缓解。 |
| `init` 失败后重试 | **保留 / 高** | `service.go@40c7345819b8:305-319` 在调用前即将 `inited[api]` 置 true，Init 错误仅写日志并仍返回 client；本次和后续请求都不会再次 Init。应仅在成功后标记，或缓存失败并提供有界重试，区分暂时网络失败与确定性错误。 |
| 下载流式写盘，严格检测 256 MB 上限 | **修正 / 高** | `internal/catvod/source.go@40c7345819b8:62,92-107` `get` 用 `io.ReadAll(io.LimitReader(...))`，脚本最多读 256 MiB、配置最多 64 MiB；但读满 limit 后不检测是否还有多余字节，超限资源可能被静默截断。Fetch 在读取完整 body 后直接覆盖 `source/index.js`（71–78），中断/写失败可能破坏缓存；配置同理（82–87）。边下边 hash/写临时文件、超限多读一个字节检测、成功后原子替换是真实改进。 |
| 日志落盘轮转 | **延期 / 高** | `service.go@40c7345819b8:105-119` 只保留内存最近 500 行；`internal/catvod/host.go@40c7345819b8:128-143` 转发插件 stdout/stderr。落盘有排错价值，但 `service.go@40c7345819b8:267,436-469` 已记录订阅 URL、播放 URL、header map 和错误文本，URL 可能含 Basic Auth/签名参数，header 可能含 Cookie。先做敏感数据脱敏/保留期限/权限，再谈持久化；当前日志隐私风险比丢失日志更紧急。 |
| 未签名/未公证，做 macOS 公证 | **延期 / 中** | `README.md@40c7345819b8:34-51` 明确写无代码签名及首次打开说明。当前仓库证据不能确认发布流水线、Apple Developer 身份、制品来源或公证是否可用；这属于发布/运维决策而非源码安全修复。先审查发布流程和密钥条件，再排序。 |
| 筛选 UI | **保留 / 高** | README `README.md@40c7345819b8:102-106` 仍写暂不支持筛选 UI；协议 `docs/PROTOCOL.md@40c7345819b8:40-48` 定义 filters，且 `internal/catvod/client.go@40c7345819b8:106-122` 已传递/接收筛选结构。建议真实，但属于功能项，列 P2。 |
| DASH `.mpd` | **延期 / 中** | `internal/catvod/proxy.go@40c7345819b8:101-129,148-189` 只识别并重写 HLS m3u8；协议也只承诺 HLS 重整（`docs/PROTOCOL.md@40c7345819b8:71-75`）。DASH 需要解析 XML、模板/时间线、Range、相对 URL、加密与请求头，不是扩展名替换；应以后按用户需求和播放器矩阵另立方案。 |
| SHA 固定 GitHub commit raw URL | **删除 / 高** | commit SHA 标识 Git 对象/提交内容，不等于下载文件 SHA-256，也不证明作者身份、审查或仓库账号未被攻陷。固定不可变提交可减少跟随分支漂移，但依赖正确取得 commit 标识且需核对响应对象；不可描述为强校验/发布者认证。 |
| 预检缓存首字节结果 | **延期 / 中** | `service.go@40c7345819b8:443-470` 与 `internal/catvod/bridge.go@40c7345819b8:313-339` 当前确有预检，请求 Range 0–1023，默认 20 秒；`playlist.go@40c7345819b8:321-352` 连播开始前也会预检。缓存对签名 URL、Cookie/headers、过期时间、Range 支持以及同 URL 不同身份难以正确，先收集延迟/失败数据，暂不做。 |
| 播放记录/README 矛盾 | **修正 / 高** | README `README.md@40c7345819b8:18` 已描述记住上次集数，`README.md@40c7345819b8:106` 又称无播放记录；代码 `playlist.go@40c7345819b8:390-430` 实际以 `history.json` 记录最近 2000 部剧的上次线路/集数/时间。应以代码为准；本轮已把 README 限制修为“不支持筛选 UI 和收藏”，注明继续播放记录存在。 |
| CI 已覆盖三平台 go vet/test，补 race 与真实插件冒烟 | **修正 / 高** | 更正：原始目录 `/Users/sandy/Downloads/jstv-player/.github/workflows/build.yml` 存在 `test` job（ubuntu/macOS/Windows，Go 由 `go.mod` 决定，Node 20，执行 `go vet ./...`、`go test ./...`）及依赖 test 的三平台 build job。该文件在原始目录与当前 worktree 都由 Git 跟踪，blob 均为 `f1346417221fa2283196d49f58fe69eda4638d15`；不是未跟踪或被忽略文件。此前“检索未发现 CI workflow”的结论错误，已修正。CI 配置存在不等于远程 CI 通过；本轮未查询 GitHub Actions 运行记录。基线测试文件包括 `internal/catvod/catvod_test.go@40c7345819b8:73-123`。实施后已通过 Go 1.27.2 本地运行约定测试；原始命令输出在 `test-logs/`。这只说明本地命令成功，不代表远程 CI 通过或真实媒体播放成功。README 要求 Go 1.27+（`README.md@40c7345819b8:53-63`），本地测试使用 Go 1.27.2，CI 使用 `go-version-file: go.mod`。 |

### Git 状态核对（本次修订）

- 原始目录 `/Users/sandy/Downloads/jstv-player` 在 `main` 分支，`git status --short --branch` 显示 `ahead 4`，另有未跟踪 `.DS_Store`、`out/`、`scripts/`；`.github/workflows/build.yml` 是 tracked 文件（blob `f1346417221fa2283196d49f58fe69eda4638d15`），`git check-ignore` 未匹配，故不属于未跟踪或被忽略文件。
- 当前 worktree `/Users/sandy/.lorca/worktrees/jstv-player-7553ca/jstv-82de` 在 `jstv-82de` 分支。初次审查时 workflow tracked、blob 与原始目录相同且 `git check-ignore` 未匹配；本轮在该 tracked 文件上增加 Node 与 Linux race 步骤，当前变更状态应以最新 `git status` 为准。
- 原始建议文档引用的行号有过时/错位风险；本文表格引用均使用显式 `path@40c7345819b8:line` 格式，且仅指实施前基线；当前实现的对应证据请看 `implementation-plan.md` 与 `docs/SECURITY.md`，不能把基线行号用来解释当前文件。旧建议中的代理行号曾把 `proxy.go:135` 误当成 CORS（该基线行实际解析目标 URL）；请以 `proxy.go@40c7345819b8:...` 的路径/提交锚点为准。

## 关键边界

- **端口竞态（基线问题，已修复）**：`internal/catvod/host.go@40c7345819b8:107-124` 曾先关闭临时 listener 再启动插件，存在端口抢占窗口。当前实现让插件绑定端口 0 并经 `JSTV_READY` 回报实际端口；当前方案和跨平台验证边界见实施计划。

- **MD5**：发布方同源提供的校验和只提供传输一致性线索，不是可信认证；协议兼容要求不应通过客户端改算法伪装解决。
- **本地代理**：其通用目标转发 + `Access-Control-Allow-Origin: *` 是代码事实；“任意网页必然读取内网”不是。需要把随机端口可发现性、浏览器 Origin、PNA/LNA、webview 实际策略、目标重定向和本机非浏览器进程纳入验证。当前未做浏览器实验，具体可利用性置信度低。
- **Node**：实施前基线会全继承父环境（`internal/catvod/host.go@40c7345819b8:113-120`）；当前默认白名单，另提供显式完整继承高级选项。权限模型在 Node 18+兼容性/各权限开关版本差异尚未验证；本审查未运行权限实验。未验证项不能写成“本机实测可行”。
- **测试**：源码中有 Go 测试，但最初审查时容器尚无 Go；授权实施阶段后用 Homebrew 安装 Go 1.27.2 并完成测试。Node 测试也已运行；详细新鲜运行日志见 `test-logs/`。
- **mock 与真实播放**：`TestHostWithMockSpider`（`internal/catvod/catvod_test.go@40c7345819b8:73-123`）启动仓库内 mock 插件并断言 HTTP `/config`、init、home、category、detail、play、search 请求及返回数据成功；它不启动媒体播放器，也不验证真实 CDN、授权、音视频解码或端到端播放。代理用例（基线 `internal/catvod/catvod_test.go@40c7345819b8:129-191,279-314`）以及新增测试都使用本地 `httptest` fixture 验证请求、HLS 重写与响应；它们不代表真实播放验证。
- **LAN**：开关默认为 false（Go bool 零值），用户开启后确实把插件服务绑定所有接口；路由可操作插件并可能访问上游。实际网络可达性还受主机防火墙/网络配置影响，未做 LAN 实测。
