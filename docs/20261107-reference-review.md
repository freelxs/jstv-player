# 外部参考核验：F11 / F12

核验日期：2026-11-07。仅做源码阅读，没有执行远程 JavaScript，也没有实测外部项目播放。

## tvbox-silicon：可以借鉴行为，但应缩小“直接移植”的承诺

本次通过 GitHub API 取得 `main` 快照提交 `aed302d22f619df7b16a2666140fe158af22347c`；以下源码链接固定到此提交。它可能与计划作者此前审计的版本不同。

- [Playlist.swift](https://github.com/jo32/tvbox-silicon/blob/aed302d22f619df7b16a2666140fe158af22347c/Sources/TVCore/Playlist.swift)：确有引号内逗号保护、`group-title` / `tvg-logo`、`#EXTGRP`、TXT `分组,#genre#`、URL 尾 `|k=v&…` header 解析、BOM 去除及相对 URL 解析。`#EXTVLCOPT` **只映射** `http-user-agent` 与 `http-referrer`，不能概括为通用 VLC option 转 headers。属性正则只识别双引号写法；URL 尾参数只对值做百分号解码。没有读 `tvg-id` 的代码。
- [Models.swift](https://github.com/jo32/tvbox-silicon/blob/aed302d22f619df7b16a2666140fe158af22347c/Sources/TVCore/Models.swift)：`Channel.id` 为 `group|name|url`，所以同频道同 URL 而 header 不同会被 Playlist 去重。`Subscription.parse` 先尝试 JSON，再识别单脚本，再尝试直播格式。直播嗅探需要 UTF-8、首个非空行为 `#EXTM3U` / `#EXTINF:` 或 TXT 分组/频道，并且完整 `Playlist.parse` 至少产出一个频道。它不是仅靠后缀或一个宽松字符串判断。
- 同一文件中的 TVBox `lives[]` 只接收带字符串 `url` 的条目，读取 `name`、字符串值 `header` 及覆盖 `User-Agent` 的 `ua`；这不足以声称覆盖所有 TVBox 历史直播结构，例如内嵌 `channels`。直播源定义的 `id` 是 `name|url`，本次没有阅读 `SubscriptionMerge.swift`，因此不把“跨订阅按 URL 去重”列为已核实。
- Playlist 解析成功只能证明得到 URL / headers。不能据此证明 jstv 的授权、代理、HLS 子资源、长时直播及播放器 header 传递已经贯通。F11 应保留独立的播放接入与验收任务，特别是 header 冲突的去重语义与直播持续时长。
- [官方 README 的许可说明](https://github.com/jo32/tvbox-silicon#license)明确其原创代码采用 `GPL-3.0-only`。计划中的“约 70 行移植 Go”不能默认与本仓库 MIT 许可相容。建议写为学习格式与行为后独立实现；若确实翻译/复用源码，应先明确相应许可与分发方案。此处仅报告许可事实，不作法律结论。

本次只确认 Playlist 不解析 EPG，不能凭这些文件证明整个外部项目“完全没有 EPG”；计划应限定断言范围。

## F12：`.md5` 可作更新提示，不能替代本地完整性与信任检查

浏览器工具读取的`index.js.md5` 返回 `dd6a129c5a03d7ecb94dab8eb55bfb30`，与计划的 `a5532c5b…fc56` 不同。该结果来自工具网页抓取，可能受缓存影响；本次没有下载对应脚本字节重新计算摘要，也没有取得该分支的固定提交。因此只能说计划的摘要不再适合作为无时间限定的“已核”基准，不能断言当前脚本损坏或哪个版本正确。

建议 F12 明确：侧车 `.md5` 为可选更新提示；仅在本地缓存已验证、对应配置一致、现有 SHA-256 信任校验仍成立时短路下载。缺失、格式错误、超时、摘要不一致与更新失败需要单独定义回退；不能因为同源侧车返回旧值便跳过本地完整性检查，也不能将 MD5 当作可信签名。脚本与可执行配置应作为同一次缓存/信任变更处理。

## 尚未核实的外部事实

本次没有下载/执行插件 bundle，没有核实 `/website`、`/live`、配置 API 与静态资源的完整路由清单，没有验证当前插件的管理页、直播空状态、代理行为或播放可用性。主审查中应要求以固定脚本+配置快照静态列出路由后再设计反代范围，不能将只代理 `/website/*` 视为完整验收依据。
