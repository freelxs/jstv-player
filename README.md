# JSTV Player

用 Go + [MyGo](https://github.com/egoist/mygo) 写的跨平台（macOS / Windows / Linux）桌面播放器外壳，实现 **CatPawOpen 兼容的 JS 插件接口**：加载用户自己提供的 JS 插件（`index.js`），浏览、搜索并播放插件返回的媒体地址。

> ⚠️ **免责声明**
>
> - 本项目只是一个通用的播放器外壳，**不包含、不提供、不推荐任何内容、插件或源地址**，与任何内容提供方无关。
> - 插件和源地址完全由用户自行提供。请只使用你有权使用的内容，并遵守你所在地区的法律法规。
> - 因使用本项目或第三方插件产生的任何后果，由使用者自行承担，维护者不承担任何责任。

## 功能

- **多订阅**：在设置里添加、重命名、删除多个插件地址（`.../index.js.md5` 或 `.../index.js`），左上角随时切换；切换时停止旧插件进程、启动新的。每个订阅的插件文件和数据分开存放
- 自动下载 `index.js`、`index.config.js`，校验 md5（默认不一致时只提示）
- 站点列表、分类浏览与翻页、单站/全站搜索（结果逐站流式返回）
- 详情页、多线路剧集列表、多清晰度选择
- 播放：内置播放器、mpv（三个平台）、PotPlayer（仅 Windows）、IINA（仅 macOS）、系统默认；需要请求头的流经本地代理转发
- **连播**：点某一集后，同一线路的后续剧集自动接着播放（mpv / IINA / PotPlayer 通过本地 M3U 播放列表，每一集在播到时才向插件解析；内置播放器播完自动下一集）。设置 → 播放里可关闭。当前一集播放时会在后台提前解析并预热下一集，mpv / IINA 还会在快结束时提前打开下一项，换集不卡顿。启动 IINA 时传入仅用于该次启动的 `disableOSDFileStartMsg` 偏好覆盖，不写入 IINA 全局偏好。记住每部剧上次看到的集数，详情页可“继续播放”
- 浅色 / 深色模式跟随系统；海报网格、骨架屏加载、空状态和错误状态（带重试）
- 插件返回网页（`parse:1` 或 `text/html`）时在新窗口打开
- 可选的播放桥（`/play?vid=`）：默认关闭，只有在设置里填写自己的 URL 模板后才启用
- 插件进程只监听 `127.0.0.1`，随 App 退出

接口细节见 [docs/PROTOCOL.md](docs/PROTOCOL.md)。

## 运行依赖

- macOS 12+（Apple Silicon / Intel 通用包）、Windows 10/11 x64（需要 WebView2，Windows 11 自带）、Linux x64（需要 GTK 3 和 WebKitGTK 4.1）
- **Node.js 18+**（插件本身是 Node.js 程序）。App 会在 PATH 和常见安装位置（Homebrew、`/usr/bin`、`C:\Program Files\nodejs`、nvm、volta）查找，也可在设置里填写路径。
- 推荐安装 mpv：macOS `brew install mpv`，Windows `scoop install mpv`（或放进 PATH），Ubuntu `sudo apt install mpv`。macOS 也可用 [IINA](https://iina.io)。
- Windows 和 Linux 的系统 web view 一般不能直接播放 HLS，所以默认用外部播放器：Windows 上依次选 PotPlayer、mpv、内置；Linux 上选 mpv、内置。PotPlayer 会在 PATH、注册表（App Paths 和 PotPlayer 自己的 ProgramPath）、`Program Files` / `Program Files (x86)` 下的 `DAUM\PotPlayer` 等目录里查找 `PotPlayerMini64.exe` / `PotPlayerMini.exe`。所有外部播放器拿到的都是本地代理地址，请求头由代理加上。
- 默认播放器只在还没保存过播放器设置时生效；已经保存过的设置不会被改动，可在设置 → 播放里切换。

## 下载

在 Releases 下载对应平台的包：

| 平台 | 文件 |
|---|---|
| macOS | `JSTV-Player-<版本>-macos-universal.dmg` 或 `.zip` |
| Windows | `JSTV-Player-<版本>-windows-amd64-setup.exe`（安装包）或 `.zip`（免安装） |
| Ubuntu / Debian | `jstv-player_<版本>_amd64.deb`（`sudo apt install ./jstv-player_*.deb`） |
| 其他 Linux | `jstv-player-<版本>-linux-amd64.tar.gz` |

都没有代码签名。macOS 首次打开请右键 → 打开，或执行：

```sh
xattr -dr com.apple.quarantine "/Applications/JSTV Player.app"
```

Windows 可能弹出 SmartScreen 提示，选“更多信息 → 仍要运行”。

## 从源码构建

需要 Go 1.27+（MyGo 要求）：

```sh
go vet ./... && go test ./...
go tool mygo build -platform darwin/universal   # dist/darwin-universal/JSTV Player.app（在 macOS 上还会生成 dmg）
go tool mygo build -platform windows/amd64      # dist/windows-amd64/（装有 NSIS 时还会生成安装包）
go tool mygo build -platform linux/amd64        # dist/linux-amd64/（可执行文件、.deb、.tar.gz）
go tool mygo dev                                # 开发模式
```

## 使用

1. 打开 App → 设置 → 订阅，添加你自己的插件地址（可以添加多个）。
2. 点左上角的订阅名切换订阅；左侧选站点，上方选分类；回车搜索。
3. 点剧集播放。内置播放器放不了时，在设置 → 播放里换 mpv / IINA。
4. 设置 → 源日志 可查看插件进程输出，便于排错。

数据目录：macOS `~/Library/Application Support/JSTV Player/`，Windows `%APPDATA%\JSTV Player\`，Linux `~/.config/JSTV Player/`（以 MyGo 的用户数据目录为准）。里面有 `settings.json`，以及每个订阅各自的 `subs/<id>/`（插件文件和插件数据）。

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
  "bridgeTemplate": ""
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
- 网页地址只会在新窗口打开，不做自动嗅探。
- 暂不支持筛选（filters）UI、播放记录、收藏。

## 许可证

本项目以 [MIT 许可证](LICENSE) 开源。许可证只覆盖本仓库的代码；第三方插件及其内容不在本项目范围内，使用方式见上方免责声明。
