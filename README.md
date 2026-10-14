# JSTV Player

用 Go + [MyGo](https://github.com/egoist/mygo) 写的 macOS 桌面播放器外壳，实现 **CatPawOpen 兼容的 JS 插件接口**：加载用户自己提供的 JS 插件（`index.js`），浏览、搜索并播放插件返回的媒体地址。

> ⚠️ **免责声明**
>
> - 本项目只是一个通用的播放器外壳，**不包含、不提供、不推荐任何内容、插件或源地址**，与任何内容提供方无关。
> - 插件和源地址完全由用户自行提供。请只使用你有权使用的内容，并遵守你所在地区的法律法规。
> - 因使用本项目或第三方插件产生的任何后果，由使用者自行承担，维护者不承担任何责任。

## 功能

- 在设置里填写插件地址（`.../index.js.md5` 或 `.../index.js`），自动下载 `index.js`、`index.config.js`，校验 md5（默认不一致时只提示）
- 站点列表、分类浏览与翻页、单站/全站搜索（结果逐站流式返回）
- 详情页、多线路剧集列表、多清晰度选择
- 播放：内置播放器（HLS/MP4）、IINA、mpv、系统默认；需要请求头的流经本地代理转发
- 插件返回网页（`parse:1` 或 `text/html`）时在新窗口打开
- 可选的播放桥（`/play?vid=`）：默认关闭，只有在设置里填写自己的 URL 模板后才启用
- 插件进程只监听 `127.0.0.1`，随 App 退出

接口细节见 [docs/PROTOCOL.md](docs/PROTOCOL.md)。

## 运行依赖

- macOS 12+（Apple Silicon / Intel 通用包）
- **Node.js 18+**（插件本身是 Node.js 程序）：`brew install node`。App 会自动在 `/opt/homebrew/bin`、`/usr/local/bin`、nvm、volta 中查找，也可在设置里填写路径。
- 可选：[IINA](https://iina.io) 或 `brew install mpv`

## 下载

在 Releases 下载 dmg 或 zip，得到 `JSTV Player.app`。因为没有签名，首次打开请右键 → 打开，或执行：

```sh
xattr -dr com.apple.quarantine "/Applications/JSTV Player.app"
```

## 从源码构建

需要 Go 1.27+（MyGo 要求）：

```sh
go vet ./... && go test ./...
go tool mygo build -platform darwin/universal   # 输出 dist/darwin-universal/JSTV Player.app（在 macOS 上还会生成 dmg）
go tool mygo dev                                # 开发模式
```

## 使用

1. 打开 App → 设置，填入你自己的插件地址，保存并加载。
2. 左侧选站点，上方选分类；回车搜索。
3. 点剧集播放。内置播放器放不了时，在设置里换 IINA / mpv。
4. 设置 → 源日志 可查看插件进程输出，便于排错。

数据目录：`~/Library/Application Support/JSTV Player/`（settings.json、下载的插件及其数据文件）。

## 目录

```
main.go              App 入口（MyGo 窗口 + 嵌入前端）
service.go           绑定给前端的 Player 服务、播放器调用
internal/catvod/     接口层：下载/md5、Node 宿主、HTTP 客户端、剧集解析、本地代理、可选播放桥（含单元测试与 mock 插件）
web/                 前端（纯 HTML/JS，无构建步骤）
docs/PROTOCOL.md     接口说明
```

## 已知限制

- 依赖本机 Node.js；没有内置 Node 运行时。
- 网页地址只会在新窗口打开，不做自动嗅探。
- 暂不支持筛选（filters）UI、播放记录、收藏。
