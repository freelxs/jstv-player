# CatPawOpen 兼容 JS 插件接口说明

本文描述本应用实现的插件接口。它根据接口的公开约定和本仓库的 mock 插件（`internal/catvod/testdata/mockspider.js`）整理，并非官方文档。文中地址均为示例（`example.com` / `127.0.0.1`）。

## 1. 插件地址

用户填写的地址形如 `https://example.com/path/index.js.md5`：

| 文件 | 作用 |
|---|---|
| `index.js.md5` | `index.js` 的 md5（32 位十六进制）。默认不一致时只提示（设置里可开启严格校验）。 |
| `index.js` | 插件本体：打包好的 **Node.js CommonJS** 程序。 |
| `index.config.js` | 可选。CommonJS 模块，`module.exports` 即传给 `start(config)` 的配置。 |

地址可以带 Basic Auth（`http://user:pass@example.com/index.js.md5`）。

## 2. 运行时约定（宿主 ↔ 插件）

插件是一个**本地 HTTP 服务**。宿主（本应用里由 Node.js 子进程 + Go 实现）需要：

1. 提供 `globalThis.catServerFactory(handler)`：返回一个 `http.Server`，请求交给 `handler(req, res)`。
2. 提供 `globalThis.catDartServerPort()`：宿主消息服务的端口；本应用返回 `0`（没有），插件发给宿主的消息成为空操作。
3. `require('index.js')`，调用导出的 `start(config)`；`stop()` 关闭服务。
4. 插件监听 `process.env.DEV_HTTP_PORT`。本应用设为随机空闲端口，并只监听 `127.0.0.1`。
5. 插件可在 `process.env.NODE_PATH` 目录下保存自己的状态文件。

插件需要 Node.js 的完整内置模块，所以由本机 Node.js 运行。

## 3. HTTP 接口

`GET /config` → 站点列表：

```json
{"video":{"sites":[{"key":"mock","name":"示例站点","type":3,"api":"/spider/mock/3",
  "searchable":1,"quickSearch":1,"filterable":1}]}}
```

每个站点 `api` 前缀下都是 `POST` + JSON（`type` 3 = 视频）：

| 路径 | 请求体 | 返回 |
|---|---|---|
| `{api}/init` | `{}` | 任意 |
| `{api}/home` | `{"filter":true}` | `{"class":[{"type_id","type_name"}], "filters"?:{...}, "list"?:[vod]}` |
| `{api}/category` | `{"id":type_id,"page":1,"filter":true,"filters":{}}` | `{"page","pagecount","limit","total","list":[vod]}` |
| `{api}/detail` | `{"id":vod_id}` | `{"list":[vod 详情]}` |
| `{api}/play` | `{"flag":线路名,"id":剧集id}` | `{"parse":0/1,"url":"..." 或 ["清晰度","url",...],"header"?:{...}}` |
| `{api}/search` | `{"wd":"关键词","page":1,"quick":false}` | `{"page","pagecount","list":[vod]}` |
| `{api}/proxy/...` 等 | GET | 插件自带的转发，play 返回的 url 可能指向这里 |

### Home filters 的应用 UI 子集

`home.filters` 可按分类 ID 提供筛选定义。应用 UI 只渲染以下明确格式（每个分类最多 20 项，每项最多 50 个选项；字段和值均须为字符串）：

```json
{"filters":{"1":[{"key":"area","label":"地区","options":[{"value":"hk","label":"香港"}]}]}}
```

每个筛选项在 UI 中是独立的单选下拉框；用户选择后向 `category` 发送 `filters:{"area":"hk"}`，翻页保留选择，切换分类/站点/订阅清空。缺失、重复 key、数组/字段类型错误或超出上限时，该分类不显示筛选控件并以空对象请求。此 UI 子集不是对任意 CatPaw 单选/多选 metadata 变体的支持声明；category 接口层仍接受字符串 map。

`vod` 字段：`vod_id, vod_name, vod_pic, vod_remarks, vod_year, vod_area, vod_actor, vod_director, vod_content, type_name, vod_play_from, vod_play_url`。

- `vod_play_from`：线路名，用 `$$$` 分隔。
- `vod_play_url`：与线路一一对应，用 `$$$` 分组；组内 `#` 分隔剧集，每集 `名称$id`。
- `vod_id` 以 `msearch:` 开头表示「用 vod_name 去全局搜索」。
- `parse:1` 表示 url 是网页；`parse:0` 是直链。`header` 是播放时要带的请求头。

## 4. 本应用的实现

```
桌面 App (Go + MyGo；macOS WKWebView / Windows WebView2 / Linux WebKitGTK 前端)
   │  mygo.call("Player.*")
   ▼
Go: 取当前订阅 → 下载 index.js(+md5, index.config.js) 到 subs/<id>/ → 启动 node 子进程(bootstrap.cjs)
   │  HTTP 127.0.0.1:随机端口   /config  /spider/*/3/{init,home,category,detail,play,search}
   ▼
Node.js: 插件 index.js
```

切换订阅时先停止旧的 node 子进程，再为新订阅启动一个；同一时间只运行一个插件进程。

播放：

- `play` 返回的 HTTP(S) 媒体必须先经应用校验并登记为服务器端播放授权，再由 Go 本地代理发给播放器。播放器代理 URL 仅包含随机 opaque stream ID；目标 URL 和请求头保存在服务端，客户端不能通过 query 自选 `u` / `h`。
- HLS 播放列表会改写绝对/相对子资源、variant 与 key URL，使子资源获得同一播放授权范围内的 opaque ID；代理保留 Range，并跟随 HTTP 重定向。重定向和清单子资源若指向 loopback/LAN 地址，仅同源插件 loopback 或用户在设置中明确允许的 LAN 媒体可继续访问。授权 ID 最长两小时有效，订阅重载/停止时撤销。外部播放器不需要读取原始播放 URL 或头。
- 所有播放地址（「系统默认」除外）都经 Go 本地代理：注入插件给的请求头，改写 HLS 播放列表（绝对地址、分片统一为 `.ts` 扩展名），再交给内置播放器、mpv 或 IINA（仅 macOS）。
- 指向 `127.0.0.1` 的地址统一改到当前插件服务的端口（`/spider/...`、`/proxy...`）。
- 播放前先请求首字节：HTTP 错误直接提示「该线路不可用」；`Content-Type: text/html` 或 `.html/.htm/.php` 地址在新窗口打开。

下载时继续使用发布方提供的 MD5 作为兼容性检查。本机 SHA-256 TOFU 首次确认与更新审批见 [SECURITY.md](SECURITY.md)；两者都不是发布者签名认证。

## 5. 可选播放桥

有的插件会返回 `http://127.0.0.1:9979/play?vid=<id>` 形式的地址，期望本机有一个「播放桥」。本应用**默认不启用**：只有用户在设置里填写自己的 URL 模板（如 `https://example.com/media/{vid}/{res}.mp4`，`{res}` 依次尝试 1080p…360p）后，才会在 `127.0.0.1:9979`（被占用则随机端口）监听并带 Range 转发。未配置时，这类地址会被提示为不可用。
