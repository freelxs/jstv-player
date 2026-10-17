# JSTV Player

用 Go + [MyGo](https://github.com/egoist/mygo) 写的跨平台（macOS / Windows / Linux）桌面播放器外壳，实现 **CatPawOpen 兼容的 JS 插件接口**：加载用户自己提供的 JS 插件（`index.js`），浏览、搜索并播放插件返回的媒体地址。

> ⚠️ **免责声明**
>
> - 本项目只是一个通用的播放器外壳，**不包含、不提供、不推荐任何内容、插件或源地址**，与任何内容提供方无关。
> - 插件和源地址完全由用户自行提供。请只使用你有权使用的内容，并遵守你所在地区的法律法规。
> - 因使用本项目或第三方插件产生的任何后果，由使用者自行承担，维护者不承担任何责任。

## 功能

- **多订阅**：在设置里添加、重命名、删除多个插件地址（`.../index.js.md5` 或 `.../index.js`），左上角随时切换；切换时停止旧插件进程、启动新的。每个订阅的插件文件和数据分开存放
- 自动下载 `index.js`、`index.config.js`，校验发布者提供的 md5（默认不一致时只提示）；本机保存 SHA-256 TOFU pin，首次使用或内容变化需用户确认后才运行
- 站点列表、分类浏览与翻页、单站/全站搜索（结果逐站流式返回）
- 分类筛选 UI：支持 `home.filters` 中按分类 ID 提供的字符串选项格式；无效或缺失 metadata 时安全隐藏筛选控件
- 详情页、多线路剧集列表、多清晰度选择
- 播放：内置播放器、mpv（三个平台）、PotPlayer（仅 Windows）、IINA（仅 macOS）、系统默认；需要请求头的流经本地代理转发
- **连播**：点某一集后，同一线路的后续剧集自动接着播放（mpv / IINA / PotPlayer 通过本地 M3U 播放列表，每一集在播到时才向插件解析；内置播放器播完自动下一集）。设置 → 播放里可关闭。当前一集播放时会在后台提前解析并预热下一集，mpv / IINA 还会在快结束时提前打开下一项，换集不卡顿。启动 IINA 时传入仅用于该次启动的 `disableOSDFileStartMsg` 偏好覆盖，不写入 IINA 全局偏好。记住每部剧上次看到的集数，详情页可“继续播放”
- 浅色 / 深色模式跟随系统；海报网格、骨架屏加载、空状态和错误状态（带重试）
- 插件返回网页（`parse:1` 或 `text/html`）时在新窗口打开
- 可选的播放桥（`/play?vid=`）：默认关闭，只有在设置里填写自己的 URL 模板后才启用
- 插件进程默认只监听 `127.0.0.1`，随 App 退出；局域网插件服务和局域网媒体播放均需在设置中分别明确开启
- 可选脱敏日志持久化：默认关闭；只显示近 7 天日志，最多 4 MiB（容量可能导致更早淘汰），可随时清除
- 插件由系统 Node.js 运行。默认只传递运行所需的平台环境变量和证书路径变量；高级兼容模式可传入完整环境（可能包含代理凭据等秘密）。权限模型按钮仅检查当前 Node 帮助中列出的标志，不启用权限，也不构成安全沙箱

接口细节见 [docs/PROTOCOL.md](docs/PROTOCOL.md)。

## 运行依赖

- macOS 12+（Apple Silicon / Intel 通用包）、Windows 10/11 x64（需要 WebView2，Windows 11 自带）、Linux x64（需要 GTK 3 和 WebKitGTK 4.1）
- Node.js 18+（插件本身是 Node.js 程序）。可用 `--with-node` 构建包内置 Node；选择顺序为设置中的显式路径、包内 Node、系统 Node。精简包仍要求系统 Node，也可在设置里填写路径。
- 推荐安装 mpv：macOS `brew install mpv`，Windows `scoop install mpv`（或放进 PATH），Ubuntu `sudo apt install mpv`。macOS 也可用 [IINA](https://iina.io)。
- Windows 和 Linux 的系统 web view 一般不能直接播放 HLS，所以默认用外部播放器：Windows 上依次选 PotPlayer、mpv、内置；Linux 上选 mpv、内置。PotPlayer 会在 PATH、注册表（App Paths 和 PotPlayer 自己的 ProgramPath）、`Program Files` / `Program Files (x86)` 下的 `DAUM\PotPlayer` 等目录里查找 `PotPlayerMini64.exe` / `PotPlayerMini.exe`。所有外部播放器拿到的都是本地代理地址，请求头由代理加上。
- 默认播放器只在还没保存过播放器设置时生效；已经保存过的设置不会被改动，可在设置 → 播放里切换。

## 下载

在 Releases 下载对应平台的包：

| 平台 | 文件 |
|---|---|
| macOS | `JSTV-Player-<版本>-macos-universal.dmg` 或 `.zip` |
| Windows | `JSTV-Player-<版本>-windows-amd64-setup.exe`（安装包）或 `.zip`（免安装） |
| macOS（可选内置 Node） | `JSTV-Player-<版本>-macos-universal-with-node.dmg` 或 `.zip` |
| Windows（可选内置 Node） | `JSTV-Player-<版本>-windows-amd64-with-node.zip` |
| Ubuntu / Debian | `jstv-player_<版本>_amd64.deb`（`sudo apt install ./jstv-player_*.deb`） |
| 其他 Linux | `jstv-player-<版本>-linux-amd64.tar.gz` |

当前仓库未配置 Developer ID 代码签名或公证；本地构建的 app 可能带 ad-hoc 签名，但它不认证发布者。macOS 首次打开请右键 → 打开，或执行：

```sh
xattr -dr com.apple.quarantine "/Applications/JSTV Player.app"
```

macOS DMG 提供将 App 拖入 Applications 的常规方式，但拖放没有安装后脚本钩子；Launchpad/搜索结果可能异步更新。若需要在复制时精确刷新 LaunchServices，可在确认将要复制的源路径和目标路径后，手动运行仓库中的 `scripts/install-macos.sh "/path/to/JSTV Player.app"`。脚本只操作 `/Applications/JSTV Player.app`，检查现有与新 bundle ID，覆盖前把旧 app 存为脚本显示的校验过 ZIP 回滚档，并只对目标 app 调用 `lsregister -f`；它不重建全局索引、不关闭 Dock，也不清理其他副本。脚本只校验 bundle 元数据，不认证发布者；仓库未配置 Developer ID 签名或公证。此脚本不会由构建或 DMG 自动运行。不要对未知开发构建使用该脚本。

macOS 发布脚本在 `out/.jstv-build-*` 下使用唯一隔离目录，验证 ZIP 和 DMG 后才发布归档；成功时只将本轮生成的 app 精确传给 `lsregister -u`，再删除本轮目录。构建或验证失败时保留该目录并报告路径，既有归档不替换；若注销工具不可用或失败，归档仍保留但 app 也保留。`--keep-app` 会保留并报告隔离目录。脚本不安装 app、不重建全局索引、不关闭 Dock；既有 `dist/darwin-universal/JSTV Player.app` 不会自动注销或删除，需用户单独决定处理。系统 App 列表是否依据 `LSApplicationCategoryType`、何时刷新尚无项目可验证依据；Spotlight 可搜索不保证 App 列表分类或出现。

需要比较已安装 bundle 与项目 `dist/` 副本时，可运行 `scripts/diagnose-macos-apps.sh`。该诊断只读输出两个固定路径的 bundle 元数据、`mdls` 结果和 Launch Services 中的文本匹配；没有匹配不等于未注册。它不刷新/注销/注册应用、不清缓存、不启动应用，也不能证明 App 列表依赖类别键。

Windows 可能弹出 SmartScreen 提示，选“更多信息 → 仍要运行”。

## 从源码构建

需要 Go 1.27+（MyGo 要求）：

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
go tool mygo build -platform linux/amd64        # dist/linux-amd64/（可执行文件、.deb、.tar.gz）
go tool mygo dev                                # 开发模式
```

构建脚本保留 Go 查找、`GO_BIN`、`JSTV_CACHE_ROOT`、`GOCACHE`、`GOMODCACHE`、`GOPATH`、`GOTOOLCHAIN` 和 `GOPROXY` 覆盖。macOS 归档验证失败时不会替换旧归档，并保留 `.jstv-build-*` 路径供检查。脚本不自动清理既有 `dist/` 构建目录。

在 GitHub Actions 中，构建脚本使用 `setup-go` 的 Go 缓存路径，并通过 Go module proxy 下载缺少的依赖；本地默认仍使用隔离缓存和离线模式。

`--with-node` 使用脚本中固定的 Node.js 24.21.0（Node 24 LTS），仅从 `nodejs.org/dist/v24.21.0` 获取官方 macOS arm64/x64 或 Windows x64 归档，并按该目录官方 `SHASUMS256.txt` 校验 SHA-256；下载归档与校验清单缓存于 `$JSTV_CACHE_ROOT/node-v24.21.0`（本地默认工具缓存目录，CI 默认 runner 临时目录）。macOS 包同时携带 arm64 和 x64 可执行文件，按 `runtime.GOARCH` 选择，并用 `lipo` 验证架构；MyGo 将可执行资源纳入 app 的 ad-hoc 签名。此签名可在构建机验证，但不是 Developer ID 签名或公证，也不保证 Gatekeeper 在其他 Mac 放行。Node 更新需维护者更新固定版本并重新构建、验证两架构及动态依赖。只包含 Node 可执行文件和随官方发行归档提供的 `LICENSE`，不带 npm、npx 或头文件。内置 Node 只提供运行时，不包含第三方插件依赖；Windows 仍需 WebView2 Runtime。

## 使用

1. 打开 App → 设置 → 订阅，添加你自己的插件地址（可以添加多个）。
2. 点左上角的订阅名切换订阅；左侧选站点，上方选分类；回车搜索。
3. 点剧集播放。内置播放器放不了时，在设置 → 播放里换 mpv / IINA。
4. 设置 → 日志 可查看插件进程输出；播放 → 持久化脱敏日志默认关闭，开启后写入应用数据目录 `logs/`。日志页只显示行时间戳在最近 7 天内的日志，启动和读取时清理过期行；4 MiB 容量上限可能导致更早淘汰。清除日志不影响 pin、播放记录或插件配置。

数据目录：macOS `~/Library/Application Support/JSTV Player/`，Windows `%APPDATA%\JSTV Player\`，Linux `~/.config/JSTV Player/`（以 MyGo 的用户数据目录为准）。里面有 `settings.json`，以及每个订阅各自的 `subs/<id>/`（插件文件和插件数据）。

每个订阅在 `subs/<id>/source/pin.json` 保存首次确认的源 URL 与 SHA-256。改源地址会重新要求确认；拒绝或未完成确认时新脚本不会运行。这个 TOFU 机制只能提示已确认之后的内容变化，不验证发布者身份，也不能保护首次下载免受被攻陷的源站或中间人影响。MD5 仍按插件源协议使用，不当作发布者认证。

`settings.json` 示例：

```json
{
  "subscriptions": [
    { "id": "3f2a9c1b7e44", "name": "订阅 1", "url": "https://example.com/path/index.js.md5" }
  ],
  "active": "3f2a9c1b7e44",
  "player": "mpv",
  "nodePath": "",
  "strictMd5": false,
  "bridgeTemplate": "",
  "persistLogs": false
}
```

0.1 版的单个 `sourceUrl` 会在启动时自动迁移成第一个订阅。

## 目录

```
main.go              App 入口（MyGo 窗口 + 嵌入前端）
service.go           绑定给前端的 Player 服务、播放器调用
subscriptions.go     多订阅的增删改、切换和旧设置迁移
internal/catvod/     接口层：下载/md5、Node 宿主、HTTP 客户端、剧集解析、本地代理、可选播放桥（含单元测试与 mock 插件）
web/                 前端（纯 HTML/JS，无构建步骤）
docs/PROTOCOL.md     接口说明
```

## 已知限制

- 依赖本机 Node.js；没有内置 Node 运行时。
- 插件是用户提供的本机程序；Node 权限模型探测不提供沙箱。日志会对常见凭据格式做脱敏，但无法识别所有第三方插件自定义秘密格式。
- 网页地址只会在新窗口打开，不做自动嗅探。
- 不支持任意插件 filters 单选/多选协议；UI 仅支持本文档列明的格式。收藏暂未实现；已记录最近播放的线路与集数，可从详情页继续播放。

## 许可证

本项目以 [MIT 许可证](LICENSE) 开源。许可证只覆盖本仓库的代码；第三方插件及其内容不在本项目范围内，使用方式见上方免责声明。
