# Changelog

## 0.6.1

- macOS、Windows 和 Linux 上用 mpv 播放时，节目标题会选用系统中已有的中文字体，避免 mpv 的 OSD 把中文片名显示成方框。

## 0.6.0

- 直播地址会区分频道列表和 HLS 播放清单。带 `#EXTINF:-1`、`group-title` 或分组行的 M3U 仍按频道解析，自定义的 `#EXT-X-` 标记不再把整份列表拒掉。`#EXT-X-STREAM-INF`、`#EXT-X-TARGETDURATION` 这类媒体或主播放列表则把该直播地址本身收成一个频道，名字用直播源的名称。`#EXTINF` 上的 `http-user-agent` 会带去播放。
- 订阅地址里的国际化域名会先转成 punycode。配置下载先用 TV 客户端的标识；如果返回的是网页，再换浏览器标识重试。没有 `url`、只有嵌套 `channels` 或 `proxy://do=live` 的直播源也会展开。
- `csp_XBPQ`、`csp_XYQHiker`（含 `csp_XYQHikerAL`、`csp_XYQBiu`）和 `csp_AppYs` / `csp_AppYsV2` 用 ext 里的公开规则在桌面和网页模式打开分类、详情和播放。其余 `csp_`、JAR 和 DEX 仍然只展示原因：这些类依赖 Android 接口，桌面 JVM 加载不了 spider JAR，DEX 也不能在桌面执行。
- Windows 上替换正在被读取的文件时，会把「拒绝访问」和共享冲突多试几次，避免偶发的原子写入失败。

## 0.5.0

- TVBox 点播配置可以加载。支持 BOM、注释、Base64、图片外壳和 `$#key#$` 解密；type 0 XML 与 type 1/4 JSON 采集；`csp_`、JAR 和 Python 站点只展示原因。`urls` 多仓配置先选择一条线路，最多跟随 4 层。`hosts` 改写连接地址，`rules` 去掉匹配的 HLS 广告分片。HTTP-FLV 在有 ffmpeg 时转成 fMP4。
- 返回网页的播放地址（`parse:1`、`jx` 或 HTML）会先从 video/source、MacCMS `player_` 对象、iframe 和媒体链接里提取直链，再按 flag 尝试配置里的 type 0/1 解析接口。解析出的地址进入连播队列，桌面和网页模式都可用。
- 桌面模式在静态解析失败后，用隐藏的无痕 WebView 嗅探页面里的媒体请求，并在本播放器里播放。12 秒内没有结果会给出明确错误。网页模式不做这一步。
- type 3 的 JavaScript 站点先在内置 QuickJS 里运行。脚本从用户配置的地址加载，不随应用打包。QuickJS 会展开 `import`/`export`（含 `export default`）并提供 `print`。脚本地址返回网页时拒绝执行；401/403 会换一套常见请求头再试一次。QuickJS 无法运行时，如果本机有 Node，仍走原来的 Node 路径，并把脚本里的远程 `import` 下载到本地再加载。
- 同组同名直播频道合并为一条，保留多条线路。`#EXTHTTP`、`proxy://do=live` 和嵌套分组可以读取。`proxy://do=live` 会展开成直播列表，不再把列表地址本身当作播放地址。内置播放器和网页播放器在出错或 15 秒没有画面时自动换下一条线路。

## 0.4.2

- 插件已经打印 Fastify 的 `Server listening at` 但没有打印 `JSTV_READY` 时，启动仍能认出监听端口。`/config` 不再被 3 秒客户端超时打断；慢响应可以一直等到本次启动的总时限。超时错误会带上端口（如果已经监听到）和最后一行插件日志。

## 0.4.1

- 同一集返回多个清晰度时，选中一个清晰度后仍会连播。内置播放器和网页播放器会在当前集结束后自动播放下一集，上一集和下一集可以在整条线路里切换。后面的每一集等到真正播放时才向插件解析，并优先使用刚才选中的清晰度；这一集没有该清晰度时改用第一条可播放地址。
- mpv、IINA、PotPlayer 和自定义播放器会收到从当前集开始的本地 M3U，每一条在播放器请求时再解析。播放器自己的上一个、下一个在这份队列里切换。
- SenPlayer 和系统播放器的启动方式只能传入一个地址，选中清晰度后仍只播放这一集。

## 0.4.0

- 原生导入 M3U/TXT 和 TVBox lives；支持频道分组、请求头、图片包装配置与缓存启动，直播无需 Node。网页和内置播放器按实际响应识别 HLS，支持无后缀/PHP 播放地址。
- 脚本与可执行配置以完整 SHA-256 清单审批，保存不可变版本；配置下载失败或启动失败不覆盖最后成功版本。新增显式缓存启动与回退确认。
- 修复带 query 的配置 URL、加载/切换取消和连播代际隔离；限制协议响应大小，归一化站点错误并保留脱敏诊断日志。
- 网页配置页使用沙箱和绑定登录会话/订阅代际的短期访问凭证；只开放已核查的配置路由。LAN 访问需要二次确认，代理变量继承有独立开关。
- 新增收藏、内置/网页播放器位置续播、失败站点搜索后置和目录响应缓存。设置、历史和收藏使用原子写入；损坏历史保留备份。
- 增加真实 Chromium 配置隔离与播放端到端测试。EPG、非 HTTP 直播和外部播放器进度回传不属于本版能力。

## 0.3.9

- 桌面框架从 MyGo v0.5.5 升级到 v0.8.1（https://github.com/egoist/mygo/releases/tag/v0.8.1）。Go 工具链和 Docker 构建镜像改为 1.27.3。
- Linux 包名固定为 `jstv-player`。发布出去的安装包文件名仍由 CI 按原规则命名。
- 配置页弹窗规则不变：本机地址留在应用窗口，公网 http(s) 交给系统浏览器，其它地址忽略。
- 生成的 TypeScript 客户端补上已有的配置页和网页播放方法。页面仍通过 `window.mygo.call` 调用。

## 0.3.8

- 目录里标成文件夹的条目会再请求一次分类并列出其中的内容。这类条目不再直接打开详情。

## 0.3.7

- 点击名为「配置」的站点，或地址本身就是本机配置页的条目，会打开该页面。配置页上的方形码在列表和详情里完整显示；页面可以滚动，指向本机的新窗口留在应用内。

## 0.3.6

- Add a web deployment mode (`--web` or `JSTV_MODE=web`) with username/password login, session cookies, and in-browser HLS/MP4 playback through hls.js. Docker Compose publishes the HTTP port and keeps settings, subscriptions, and credentials in a data volume. The desktop client does not require login.
- The desktop client can select the same built-in HTML5 video + hls.js player, alongside mpv, SenPlayer, and a custom player. Episodes play in a window through the existing media proxy, with previous, next, and close. External player launch paths are unchanged. Linux packages depend on GStreamer plugins (`gstreamer1.0-plugins-good`, `gstreamer1.0-plugins-bad`, `gstreamer1.0-libav`) so WebKitGTK can decode that HLS.

## 0.3.5

- macOS can use SenPlayer. Detection checks `/Applications/SenPlayer.app`,
  `~/Applications/SenPlayer.app`, then Spotlight for bundle id
  `com.wuziqi.SenPlayer`. Playback runs `open -b` with SenPlayer's
  `senplayer://x-callback-url/play?url=` scheme so the proxied address is one
  argument. That scheme plays a single URL; continuous play falls back to the
  selected episode instead of the local M3U. SenPlayer was not launched on a
  Mac in this change.
- Settings → Playback can select a custom player on every platform. A native
  file dialog picks a macOS `.app`, a Windows `.exe`, or a Linux executable.
  The path and optional arguments are stored in `settings.json`. `{url}` is
  replaced in the argument list, or the proxied URL / playlist is appended.
  The path must exist and be launchable. Arguments are argv, not a shell
  command. A previously saved player is left as it was.

## 0.3.4

- Accept CatVod category filters, including numeric option values and a
  repeated blank choice, and apply a filter's initial value when a category
  opens.
- Keep the query string and fragment on a plugin script address.
- Keep reading plugin output after an oversized log line so the Node process
  cannot stall.
- Recognize BOM-prefixed and extensionless HLS playlists in the local playback
  proxy. Playback and preflight errors no longer repeat the stream address.
  A browser User-Agent is sent only when the plugin does not provide one, and
  compressed upstream bytes are decoded before they reach the player.
- Stop an all-site search when the user leaves the results, and let protocol
  calls observe cancellation.
- Keep a pending plugin trust decision when saving the pin fails, so approval
  can be retried.
- Scope watch history to the active subscription, and keep a continuous
  playlist on the subscription that created it.
- Rewrite stale plugin poster URLs, and avoid copying long signed media paths
  into local proxy filenames.
- Serialize settings and subscription saves so one write cannot drop the other.
- A recommendation card that searches every site also clears the current-site
  search hint.
- The built-in continuous player starts from the resolved playback address.
  Its failure hint names a player available on the current system.

## 0.3.3

- Treat nil and empty stream headers as the same playback grant identity after
  the frontend JSON round trip, while retaining exact checks for nonempty
  headers.
- Restrict automatic episode selection to the HTTP(S) URL forms accepted by
  proxy grant registration, and report when an episode has no supported
  continuous-play address.
- These fixes address confirmed code paths; they do not establish the cause
  or resolution of any individual playback incident.

## 0.3.2

- The macOS release helper builds into a unique, isolated output directory and
  validates the ZIP and DMG before publishing either artifact.
- After successful validation and publication, it unregisters and removes only
  the app bundle created by that invocation. Existing `dist/` apps and prior
  artifacts are left untouched. `--keep-app` preserves the isolated build
  directory and reports its exact path.
- Failed builds or archive validation retain the isolated app for recovery and
  do not replace prior release archives. No installation, global Launch
  Services refresh, or Spotlight reset is performed.
- The script source was the original untracked `scripts/build.sh` (SHA-256
  `c28226eb8dbb11b93f70cdc55d999d135aa2615366377c65caf16dcfc2218d13`); its
  Windows target, `all` selector, Go/cache overrides, and fallback behavior
  are retained. The original file in the Downloads worktree was not modified.
- `LSApplicationCategoryType` remains entertainment metadata; this change does
  not claim it controls App-list categorization or guarantees refresh timing.
- Added a read-only macOS diagnostic for the installed and project `dist/`
  bundle paths; it does not alter Launch Services or system indexes.

## 0.3.1

- Set the macOS Launch Services application category to Entertainment in the
  generated bundle's `Info.plist`.
- Keep the stable bundle identifier `com.freelxs.jstvplayer`; this metadata
  change does not create a new application identity.
- Added an opt-in macOS installation helper that verifies the source bundle,
  asks before replacing `/Applications/JSTV Player.app`, preserves the prior
  app as a verified ZIP rollback archive, and registers only the installed
  target path.
- Does not force a Spotlight/Launchpad database rebuild. DMG drag-and-drop has
  no post-install hook, and visibility may refresh asynchronously.
- The duplicate icon source is confirmed as the installed
  `/Applications/JSTV Player.app` plus the extracted development bundle at
  `dist/darwin-universal/JSTV Player.app`.
  This release does not remove or unregister either copy.
- No GUI/app-list refresh or real playback verification is claimed. The repo
  does not configure Developer ID signing or notarization; the isolated app
  built for this check carries an ad-hoc signature, which does not authenticate
  its publisher.

## 0.3.0

This release adds security and playback behavior changes. Application/package
version is defined by `mygo.json`; CI derives artifact filenames from that
field. The Go module/toolchain requirements in `go.mod` are unchanged.

### P0 — lifecycle, downloads, and network exposure

- Improved app/plugin shutdown and cleanup, including initialization retry and
  host-start failure handling.
- Script/config downloads are bounded and streamed, validated before atomic
  replacement, with strict MD5 mode available. MD5 remains the publisher/source
  protocol check; it does not authenticate the publisher.
- Added secret redaction to application-managed logs. Arbitrary third-party
  plugin output may contain formats the app cannot recognize, so complete
  redaction is not guaranteed.
- LAN plugin serving and LAN media access require explicit settings and carry
  an exposure warning; the default remains local-only.

### P1 — proxy and plugin trust

- Replaced client-selected arbitrary proxy targets with opaque, server-managed
  stream IDs and scoped proxy mappings. HLS variants/keys, byte ranges,
  redirects, legitimate plugin loopback targets, and explicitly authorized LAN
  media are supported by the proxy flow.
- Added first-use and content-change SHA-256 trust prompts for downloaded
  plugin files. Existing source MD5 behavior is retained. This TOFU pin is a
  local change-detection/approval aid, not publisher authentication; changing
  the source URL requires a new trust decision.
- Node permission support remains a help/flag capability check only. A
  permission-restricted execution mode is not implemented, and Node's
  permission model is not treated as a security sandbox.
- The existing cross-platform CI test workflow is retained and includes Go,
  Node, and Linux race test jobs.

### P2 — filters and optional logs

- Added category-aware Home filters for the documented metadata format, with
  safe fallback for missing or malformed metadata and request-token handling
  for stale responses.
- Added optional bounded persistent, redacted logs. Persistence is off by
  default; the UI exposes clearing and retention information. Logs are limited
  by age and total size, so capacity pressure may evict entries earlier.
- Added per-row age filtering and cleanup so expired log lines do not remain
  visible merely because an active file was recently modified.

### Migration and verification notes

- Existing settings continue to migrate through the current settings schema;
  existing SHA-256 pins are retained. A first trust prompt is required where a
  source has no matching pin or its content/source URL has changed. Declining
  the prompt prevents the changed script from running.
- Persistent logs remain disabled after upgrade unless explicitly enabled.
  Clearing logs does not clear trust pins, playback history, or plugin config.
- GUI/manual playback was not verified for this release. Mock requests and
  automated tests do not establish successful playback against real media.
- The repo does not configure Developer ID signing or notarization. Locally
  built app bundles may carry an ad-hoc signature, which does not authenticate
  their publisher.
