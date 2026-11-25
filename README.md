# XsPlayer（空播）

[![Listed on Awesome MyGo](https://awesomemygo.dev/badge.svg)](https://awesomemygo.dev/apps/jstv-player)

用 Go + [MyGo](https://github.com/egoist/mygo) 写的跨平台（macOS / Windows / Linux）桌面播放器外壳，实现 **CatPawOpen 兼容的 JS 插件接口**：加载用户自己提供的 JS 插件（`index.js`），浏览、搜索并播放插件返回的媒体地址。也可直接导入 M3U/TXT 和 TVBox lives 直播订阅，无需 Node。

> ⚠️ **免责声明**
>
> - 本项目只是一个通用的播放器外壳，**不包含、不提供、不推荐任何内容、插件或源地址**，与任何内容提供方无关。
> - 插件和源地址完全由用户自行提供。请只使用你有权使用的内容，并遵守你所在地区的法律法规。
> - 因使用本项目或第三方插件产生的任何后果，由使用者自行承担，维护者不承担任何责任。

## 功能

- **直播订阅**：支持 M3U/TXT 分组、台标、UA/Referer、`#EXTHTTP`、`proxy://do=live`、嵌套分组、TVBox lives 与图片包装配置。同名频道合并成多条线路，内置播放器和网页播放器在出错或 15 秒没有画面时自动换下一条。直播只支持 HTTP(S) 媒体，EPG 和其他传输协议尚未实现
- **TVBox 点播**：JSON 配置可含 BOM、注释、Base64、图片外壳和 `$#key#$` AES。type 0 XML、type 1/4 JSON 采集接口直接请求；type 3 的 `.js` 站点先用内置 QuickJS 运行（不打包 drpy2，脚本从配置地址加载），跑不了再回退到 Node。`csp_`、JAR 和 Python 站点只展示原因、不执行。`urls` 多仓配置先选择一条线路，最多跟随 4 层。`hosts` 改写连接地址，`rules` 去掉匹配的 HLS 广告分片。HTTP-FLV 在安装了 ffmpeg 时转成 fMP4。Docker 镜像带 ffmpeg
- **Kazumi 规则**：无需 Node，直接导入 Kazumi `index.json` 规则清单或单条规则 JSON，内置 HTML XPath 与 JSONPath 搜索与剧集线路提取；提供「推荐目录」（Bangumi 每日放送与热门番剧）并自动补齐封面、评分、演职员与剧情简介，支持在设置中填写本机私有的 Bangumi Access Token
- **多订阅**：在设置里添加、重命名、删除多个插件或直播地址（`.../index.js.md5` 或 `.../index.js`），左上角随时切换；切换时停止旧插件进程、启动新的。每个订阅的插件文件和数据分开存放
- 自动下载 `index.js`、`index.config.js`，校验发布者提供的 md5（默认不一致时只提示）；本机保存包含脚本和可执行配置的 SHA-256 TOFU 清单，首次使用、脚本或配置变化均需用户确认后才运行；完整版本成功启动后才提交
- 站点列表、分类浏览与翻页、单站/全站搜索（逐站返回成功/失败状态，近期失败站点后置）；Home/Category 有按订阅代际隔离的短期缓存
- 节目和直播频道收藏；内置/网页播放器保存播放位置并续播（常规进度十秒合并一次，暂停/关闭时刷新）；外部播放器仅记录集数
- 应用启动时验证完整性和已批准清单后默认使用最后成功缓存版本；“重新加载”和“强制下载刷新”均绕过缓存直接拉取最新订阅。新版启动失败时，可显式确认并回退原缓存版本
- 分类筛选 UI：支持 `home.filters` 中按分类 ID 提供的字符串选项格式；无效或缺失 metadata 时安全隐藏筛选控件
- 详情页、多线路剧集列表、多清晰度选择
- 播放：默认内置播放器（内嵌双栏布局：左侧 16:9 播放画面 + 右侧选集列表，深度贴合系统窗口与分屏）、外部播放器（mpv、PotPlayer、IINA、SenPlayer、自定义播放器、系统默认）；需要请求头的流经本地代理转发
- **连播**：点某一集后，同一线路的后续剧集自动接着播放。若这一集返回多个清晰度，选定一个后按该清晰度连播，后面每一集仍在播到时才向插件解析（mpv / IINA / PotPlayer / 自定义播放器通过本地 M3U 播放列表；内置播放器在窗口里用上一集 / 下一集切换，播完自动下一集，也可以回到更早的集）。SenPlayer 和系统播放器的启动方式只接受一个地址，连播会退回当前这一集。设置 → 播放里可关闭。当前一集播放时会在后台提前解析并预热下一集，mpv / IINA 还会在快结束时提前打开下一项，换集不卡顿。启动 IINA 时传入仅用于该次启动的 `disableOSDFileStartMsg` 偏好覆盖，不写入 IINA 全局偏好。记住每部剧上次看到的集数，详情页可“继续播放”
- 浅色 / 深色模式跟随系统；海报网格、骨架屏加载、空状态和错误状态（带重试）
- 插件或点播站点返回网页（`parse:1`、`jx` 或 `text/html`）时，先从页面里的 video/source、MacCMS `player_` 对象、iframe 和媒体地址提取直链，再用配置里的 `parses`（type 0 网页、type 1 JSON，按 flag 匹配）。桌面模式最后用隐藏的 WebView 嗅探媒体请求，在播放器里播放，12 秒没有结果会给出明确错误。网页模式不做嗅探。解析成功的地址进入连播队列
- 可选的播放桥（`/play?vid=`）：默认关闭，只有在设置里填写自己的 URL 模板后才启用
- 插件进程默认只监听 `127.0.0.1`，随 App 退出；局域网插件服务和局域网媒体播放均需在设置中分别明确开启
- 可选脱敏日志持久化：默认关闭；只显示近 7 天日志，最多 4 MiB（容量可能导致更早淘汰），可随时清除
- 插件由系统 Node.js 运行。默认只传递运行所需的平台环境变量和证书路径变量；独立的代理继承开关默认关闭，重载后生效；高级兼容模式可传入完整环境（可能包含代理凭据等秘密）。权限模型按钮仅检查当前 Node 帮助中列出的标志，不启用权限，也不构成安全沙箱

接口细节见 [docs/PROTOCOL.md](docs/PROTOCOL.md)。

## 运行依赖

- macOS 12+（Apple Silicon / Intel 通用包）、Windows 10/11 x64（需要 WebView2，Windows 11 自带）、Linux x64（需要 GTK 3 和 WebKitGTK 4.1）。Linux 上用内置播放器看 HLS 时，WebKitGTK 还需要 GStreamer 解码插件：`gstreamer1.0-plugins-good`、`gstreamer1.0-plugins-bad`、`gstreamer1.0-libav`。Debian 包把这三项列为依赖。AC3 等专利编码另需 `gstreamer1.0-plugins-ugly`。Fedora 上对应包名是 `gstreamer1-plugins-good`、`gstreamer1-plugins-bad-free` 和 `gstreamer1-libav`。
- Node.js 18+（CatPaw 插件需要；TVBox 的 `.js` 站点优先用内置 QuickJS，失败时才需要 Node。原生直播不需要）。可用 `--with-node` 构建包内置 Node；选择顺序为设置中的显式路径、包内 Node、系统 Node。精简包仍要求系统 Node，也可在设置里填写路径。
- 推荐安装 mpv：macOS `brew install mpv`，Windows `scoop install mpv`（或放进 PATH），Ubuntu `sudo apt install mpv`。macOS 也可用 [IINA](https://iina.io) 或 [SenPlayer](https://apps.apple.com/app/senplayer-media-player/id6443975850)。
- 桌面端与网页模式默认均使用**内置播放器（内嵌双栏布局）**：视频完全嵌在主窗口内部，原生支持 macOS 窗口平铺（Tiling）、台前调度（Stage Manager）、分屏、多显示器与全屏，无需外部独立弹窗，也不会出现窗口无法置顶或与主界面脱节的问题。若偏好独立窗口或外部解码渲染，也可在「设置 → 播放」中选用外部播放器（macOS 推荐 IINA / SenPlayer / mpv，Windows 推荐 PotPlayer / mpv，Linux 推荐 mpv）。PotPlayer 会在 PATH、注册表（App Paths 和 PotPlayer 自己的 ProgramPath）、`Program Files` / `Program Files (x86)` 下的 `DAUM\PotPlayer` 等目录里查找 `PotPlayerMini64.exe` / `PotPlayerMini.exe`。SenPlayer 先看 `/Applications/SenPlayer.app` 和 `~/Applications/SenPlayer.app`，再按 bundle id `com.wuziqi.SenPlayer` 用 Spotlight 查找。找到后用 `open -b` 打开 `senplayer://x-callback-url/play?url=`，地址是本地代理 URL，请求头由代理加上。这个 scheme 只播放一个地址，所以不会把连播 M3U 交给 SenPlayer。
- 设置 → 播放里可以选「自定义播放器」，用系统文件对话框选择 macOS `.app`、Windows `.exe` 或 Linux 可执行文件，并可选附加参数。参数里的 `{url}` 会换成代理地址或连播 M3U；不写 `{url}` 时地址追加在参数末尾。程序以参数列表启动，不经过 shell。路径必须存在且能启动，否则保存和播放都会提示错误。
- 所有外部播放器拿到的都是本地代理地址，请求头由代理加上。默认播放器只在还没保存过播放器设置时生效；已经保存过的设置不会被改动，SenPlayer 和自定义播放器也不会被自动选上。可在设置 → 播放里切换。

## 下载

在 Releases 下载对应平台的包：

| 平台 | 文件 |
|---|---|
| macOS | `XsPlayer-<版本>-macos-universal.dmg` 或 `.zip` |
| Windows | `XsPlayer-<版本>-windows-amd64-setup.exe`（安装包）或 `.zip`（免安装） |
| macOS（可选内置 Node） | `XsPlayer-<版本>-macos-universal-with-node.dmg` 或 `.zip` |
| Windows（可选内置 Node） | `XsPlayer-<版本>-windows-amd64-with-node.zip` |
| Ubuntu / Debian | `xsplayer_<版本>_amd64.deb`（`sudo apt install ./xsplayer_*.deb`） |
| 其他 Linux | `xsplayer-<版本>-linux-amd64.tar.gz` |

推送到 `main` 后，CI 会对该次推送的 commit 运行测试和三平台构建；全部成功后，使用 `mygo.json` 中的版本创建 `v<版本>` Git 标签并发布 GitHub Release。功能或行为修改在合并进 `main` 之前必须先升应用版本，并让这三处保持一致：`mygo.json` 的 `version`、设置页关于标签（`web/index.html` 的 `appVersion`）和 `CHANGELOG.md`。发布自动化拒绝已存在的同版本标签或 Release，不会移动旧标签，也不会覆盖已发布资产；沿用已发布版本号合并到 `main` 会让这次发布失败。Pull request 和其他分支推送不会自动发布。推送 `vMAJOR.MINOR.PATCH` 标签仍可从该标签源码构建并发布；也可在 Actions 手动运行工作流并填写已有标签，以恢复发布。

当前仓库未配置 Developer ID 代码签名或公证；本地构建的 app 可能带 ad-hoc 签名，但它不认证发布者。macOS 首次打开请右键 → 打开，或执行：

```sh
xattr -dr com.apple.quarantine "/Applications/XsPlayer.app"
```

macOS DMG 提供将 App 拖入 Applications 的常规方式，但拖放没有安装后脚本钩子；Launchpad/搜索结果可能异步更新。若需要在复制时精确刷新 LaunchServices，可在确认将要复制的源路径和目标路径后，手动运行仓库中的 `scripts/install-macos.sh "/path/to/XsPlayer.app"`。脚本只操作 `/Applications/XsPlayer.app`，检查现有与新 bundle ID，覆盖前把旧 app 存为脚本显示的校验过 ZIP 回滚档，并只对目标 app 调用 `lsregister -f`；它不重建全局索引、不关闭 Dock，也不清理其他副本。脚本只校验 bundle 元数据，不认证发布者；仓库未配置 Developer ID 签名或公证。此脚本不会由构建或 DMG 自动运行。不要对未知开发构建使用该脚本。

macOS 发布脚本在 `out/.jstv-build-*` 下使用唯一隔离目录，验证 ZIP 和 DMG 后才发布归档；成功时只将本轮生成的 app 精确传给 `lsregister -u`，再删除本轮目录。构建或验证失败时保留该目录并报告路径，既有归档不替换；若注销工具不可用或失败，归档仍保留但 app 也保留。`--keep-app` 会保留并报告隔离目录。脚本不安装 app、不重建全局索引、不关闭 Dock；既有 `dist/darwin-universal/XsPlayer.app` 不会自动注销或删除，需用户单独决定处理。系统 App 列表是否依据 `LSApplicationCategoryType`、何时刷新尚无项目可验证依据；Spotlight 可搜索不保证 App 列表分类或出现。

需要比较已安装 bundle 与项目 `dist/` 副本时，可运行 `scripts/diagnose-macos-apps.sh`。该诊断只读输出两个固定路径的 bundle 元数据、`mdls` 结果和 Launch Services 中的文本匹配；没有匹配不等于未注册。它不刷新/注销/注册应用、不清缓存、不启动应用，也不能证明 App 列表依赖类别键。

Windows 可能弹出 SmartScreen 提示，选“更多信息 → 仍要运行”。

## 从源码构建

需要 Go 1.27.3+（MyGo v0.8.1 要求；`go.mod` 与构建脚本的 `GOTOOLCHAIN=local` 使用这个版本）：

```sh
go vet ./... && go test ./...
./scripts/build.sh darwin                      # 验证并发布 macOS ZIP/DMG，默认清理本轮 app
./scripts/build.sh darwin --keep-app           # 可选保留本轮隔离构建目录
./scripts/build.sh windows                     # Windows x64 zip
./scripts/build.sh all                         # macOS + Windows（默认）
./scripts/build.sh darwin --with-node           # macOS universal 包内置 Node
./scripts/build.sh windows --with-node           # Windows x64 包内置 Node
./scripts/build.sh all --with-node               # 两个平台的包内置 Node 变体
go tool mygo build -platform darwin/universal   # 直接构建仍输出到 dist/，不会自动清理
go tool mygo build -platform windows/amd64      # dist/windows-amd64/（装有 NSIS 时还会生成安装包）
go tool mygo build -platform linux/amd64        # dist/linux-amd64/（可执行文件、.deb、.tar.gz、PKGBUILD）
go tool mygo dev                                # 开发模式
```

构建脚本保留 Go 查找、`GO_BIN`、`JSTV_CACHE_ROOT`、`GOCACHE`、`GOMODCACHE`、`GOPATH`、`GOTOOLCHAIN` 和 `GOPROXY` 覆盖。macOS 归档验证失败时不会替换旧归档，并保留 `.jstv-build-*` 路径供检查。脚本不自动清理既有 `dist/` 构建目录。

在 GitHub Actions 中，构建脚本使用 `setup-go` 的 Go 缓存路径，并通过 Go module proxy 下载缺少的依赖；本地默认仍使用隔离缓存和离线模式。

`--with-node` 使用脚本中固定的 Node.js 24.21.0（Node 24 LTS），仅从 `nodejs.org/dist/v24.21.0` 获取官方 macOS arm64/x64 或 Windows x64 归档，并按该目录官方 `SHASUMS256.txt` 校验 SHA-256；下载归档与校验清单缓存于 `$JSTV_CACHE_ROOT/node-v24.21.0`（本地默认工具缓存目录，CI 默认 runner 临时目录）。macOS 包同时携带 arm64 和 x64 可执行文件，按 `runtime.GOARCH` 选择，并用 `lipo` 验证架构；MyGo 将可执行资源纳入 app 的 ad-hoc 签名。此签名可在构建机验证，但不是 Developer ID 签名或公证，也不保证 Gatekeeper 在其他 Mac 放行。Node 更新需维护者更新固定版本并重新构建、验证两架构及动态依赖。只包含 Node 可执行文件和随官方发行归档提供的 `LICENSE`，不带 npm、npx 或头文件。内置 Node 只提供运行时，不包含第三方插件依赖；Windows 仍需 WebView2 Runtime。

## 使用

1. 打开 App → 设置 → 订阅，选择 JS 插件或 M3U/TXT/TVBox 直播，添加你自己的地址。自动模式将 JS/MD5 后缀识别为插件，其余按内容识别直播；目录形式的插件地址请选择“JS 插件”。
2. 点左上角的订阅名切换订阅；左侧选站点，上方选分类；回车搜索。
3. 点剧集播放。设置 → 播放里选「内置播放器（窗口内 HLS / MP4）」时，画面留在应用窗口里，可以用上一集、下一集和关闭。放不了时换 mpv / IINA / SenPlayer，或指定自定义播放器。选了外部播放器之后，仍然由那个程序打开，不会改回窗口内播放。
4. 设置 → 日志 可查看插件进程输出；播放 → 持久化脱敏日志默认关闭，开启后写入应用数据目录 `logs/`。日志页只显示行时间戳在最近 7 天内的日志，启动和读取时清理过期行；4 MiB 容量上限可能导致更早淘汰。清除日志不影响 pin、播放记录或插件配置。

数据目录：macOS `~/Library/Application Support/XsPlayer/`（旧版本兼容 `JSTV Player/`），Windows `%APPDATA%\XsPlayer\`，Linux `~/.config/XsPlayer/`（以 MyGo 的用户数据目录为准）。里面有 `settings.json`，以及每个订阅各自的 `subs/<id>/`（插件文件和插件数据）。

每个订阅在 `subs/<id>/source/pin.json` 保存首次确认的源 URL 与 SHA-256。改源地址会重新要求确认；拒绝或未完成确认时新脚本不会运行。这个 TOFU 机制只能提示已确认之后的内容变化，不验证发布者身份，也不能保护首次下载免受被攻陷的源站或中间人影响。MD5 仍按插件源协议使用，不当作发布者认证。

`settings.json` 示例：

```json
{
  "subscriptions": [
    { "id": "3f2a9c1b7e44", "name": "订阅 1", "url": "https://example.com/path/index.js.md5" }
  ],
  "active": "3f2a9c1b7e44",
  "player": "mpv",
  "customPlayer": "",
  "customArgs": "",
  "nodePath": "",
  "strictMd5": false,
  "bridgeTemplate": "",
  "persistLogs": false
}
```

0.1 版的单个 `sourceUrl` 会在启动时自动迁移成第一个订阅。

## 网页部署

桌面客户端的启动方式不变。网页模式是额外的运行方式：同一个 Go 程序加上 `--web`（或环境变量 `JSTV_MODE=web`）后，用 HTTP 提供现有界面，并要求登录。浏览器里不能启动 mpv、PotPlayer、IINA、SenPlayer 或自定义播放器，所以网页模式用页面内的 HTML5 `<video>` 和 [hls.js](https://github.com/video-dev/hls.js)（Apache-2.0，文件在 `webassets/`）播放。需要请求头的流仍走原来的本地代理，浏览器只拿到本站的 `/media/...` 地址。

未登录时，页面、`/api/*` 和播放、海报地址都会被拒绝。`/healthz` 只返回 `ok`，给容器健康检查用，不包含订阅或播放数据。桌面模式不登录。

登录是单个管理员账号。密码用 bcrypt 存在数据目录的 `auth.json`，不明文保存。会话是 HttpOnly、`SameSite=Lax` 的 cookie，服务端只存令牌的 SHA-256。改接口和退出登录还要带 `Content-Type: application/json` 和 `X-JSTV-Request: 1`。同一 IP 连续密码错误会暂时拒绝登录。

| 变量 | 作用 |
|---|---|
| `JSTV_WEB_USER` | 初次创建管理员时的用户名，默认 `admin`。不能含空格、`/`、`\`、`:` |
| `JSTV_WEB_PASSWORD` | 初次创建时必填，8–72 字节。数据目录里已经有 `auth.json` 时，单改这个变量不会改掉已保存的密码 |
| `JSTV_WEB_RESET_PASSWORD` | 设为 `1` 时，用上面的用户名和密码覆盖 `auth.json`，并清掉现有会话 |
| `JSTV_DATA_DIR` | 设置、订阅、插件、登录凭据和会话。容器里是 `/data` |
| `JSTV_WEB_ADDR` | 监听地址，默认 `:8080` |
| `JSTV_WEB_PORT` | 只用于 compose 映射到主机的端口，默认 `8080` |
| `JSTV_WEB_SECURE` | 设为 `1` 时 session cookie 带 `Secure`。只在 HTTPS 反向代理后面打开 |

播放地址和插件进程仍留在容器内的 loopback。不要把插件端口发布出去。设置里的「允许局域网访问」会让插件监听容器内所有网卡，同一 Docker 网络里的其他容器能够访问它，默认关闭。

订阅脚本会在容器里以 Node 进程运行，和桌面版一样是用户提供的程序。网页模式只是把「谁能点界面」限制为登录用户，并不是插件沙箱。

```sh
# 在仓库根目录。密码不要提交进 git。
export JSTV_WEB_USER=admin
export JSTV_WEB_PASSWORD='replace-with-your-own-password'
docker compose up -d --build
```

浏览器打开 `http://127.0.0.1:8080`（或 `JSTV_WEB_PORT` 指定的端口），登录后在设置里添加订阅。数据在 compose 的 `jstv-data` 卷；要改成目录挂载，把 `docker-compose.yml` 里的卷写成 `./data:/data`。

查看状态和日志：

```sh
docker compose ps
docker compose logs -f jstv-player
```

更换密码：

```sh
export JSTV_WEB_PASSWORD='another-long-password'
export JSTV_WEB_RESET_PASSWORD=1
docker compose up -d
```

成功登录后把 `JSTV_WEB_RESET_PASSWORD` 改回 `0`，避免每次重启都按环境变量重写密码。前面有 TLS 终结时设 `JSTV_WEB_SECURE=1`，并由反代转发到容器的 8080；同时把反代地址或网段填入 `JSTV_WEB_TRUSTED_PROXIES`（例如 `127.0.0.1,172.16.0.0/12`），以便登录限流按 `X-Forwarded-For` 中的真实客户端 IP 计数，避免所有用户共用反代 IP 被连带锁死。反代需要把播放请求的 `Range` 原样转发，不要缓冲响应体。

不用 compose 时：

```sh
export JSTV_WEB_PASSWORD='replace-with-your-own-password'
export JSTV_DATA_DIR=/var/lib/jstv-player
go build -o jstv-player .
./jstv-player --web
```

镜像里带 Node.js 24，插件不需要再装 Node。镜像只提供网页模式，不包含桌面窗口。

## 目录

```
main.go                 入口与静态资源嵌入（桌面 / --web）
internal/app/           Player 绑定、应用状态、订阅、搜索、播放编排、历史与日志
internal/webserver/     网页部署、HTTP 路由、认证、海报与媒体转发
internal/externalplayer/ 外部播放器发现、参数构造与平台进程适配
internal/catvod/        插件协议、Node 宿主、版本下载、本地代理与播放桥
internal/live/          M3U/TXT、TVBox lives 与图片包装配置的数据解析
internal/atomicfile/    共享原子文件写入与平台替换
web/                    桌面与网页共用的原生 HTML / JS 界面
webassets/              网页桥接、登录、浏览器播放器与 hls.js
resources/              图标与可选平台资源
scripts/                构建、安装与发布辅助脚本
Dockerfile              网页模式镜像
docker-compose.yml     网页模式端口、密码与数据卷
docs/                  协议、安全与架构说明
```

包职责、依赖方向和测试约定见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

## 已知限制

- 精简桌面包的 JS 插件依赖本机 Node.js；可选 with-node 包自带运行时。原生直播不依赖 Node，网页镜像自带 Node.js 24。
- 网页模式只播放浏览器能播的 HLS / MP4。插件返回的本机网页、需要外部播放器的封装，或浏览器解不了的码流，需要换线路或改用桌面客户端。
- 插件是用户提供的本机程序；Node 权限模型探测不提供沙箱。日志会对常见凭据格式做脱敏，但无法识别所有第三方插件自定义秘密格式。
- 网页地址只会在新窗口打开，不做自动嗅探。
- 筛选 UI 支持文档中的 `{key,label,options}` 和常见 CatVod `{key,name,value:[{n,v}]}` 单选格式。重复的选项值保留第一次出现的那项，不支持任意多选变体。已支持节目/频道收藏与内置/网页位置续播；外部播放器暂不回传位置。

## 许可证

本项目以 [MIT 许可证](LICENSE) 开源。许可证只覆盖本仓库的代码；第三方插件及其内容不在本项目范围内，使用方式见上方免责声明。
