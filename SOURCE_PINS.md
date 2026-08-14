# 源码版本固定

| 项目 | Tag | Commit | 用途 |
| --- | --- | --- | --- |
| rclone | `v1.75.0` | `9ee9d0a0cafd5e5fe3b271d2280b090ab6e64048` | 构建及后端 API 基线 |
| OpenList | `v4.2.5` | `cc87e88f038a5a27c8782afc7b66a3c1a3cdcb77` | `drivers/123` 协议行为参考 |

`tools/fetch-references.sh` 会把 rclone 和 OpenList 的精确版本下载到被 Git 忽略的 `.references/` 目录并校验其 commit。开发过程中不得在未审查的情况下改用任一项目的 main 分支。

当前官方 Web 行为不以 Git commit 固定，因此按采集日期记录在 `protocol/current-web-2026-08-15.json`。该文件只包含域名、路径、调用顺序、字段名与类型、非敏感固定值、状态码和响应结构指纹；不会保存原始 HAR、凭据、签名 URL、文件名、MD5 或真实对象 ID。
