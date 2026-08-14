# 来源说明

本实现基于 rclone 公开的后端接口编写。OpenList v4.2.5 仅用于识别可观察的协议事实，例如端点路径、JSON 字段名、请求头要求、分页方式和上传流程。

禁止复制 OpenList 的函数体、注释、测试或其他具有表达性的源码。未来如需使用新的参考版本，必须先在 `SOURCE_PINS.md` 中记录，并在使用前审查其许可证。

从固定 OpenList 版本中识别并独立重新实现的协议事实包括：端点路径、响应 envelope 成功码、JSON 字段拼写、UTC+8 CRC32 URL 签名、分页大小与排序、下载重定向结构、MD5 秒传、临时凭据与预签名上传的选择逻辑、16 MiB 分片、每批十个 URL，以及离线任务状态值。本仓库中的安全状态机、严格一致性规则、重试策略、rclone 接口、测试、文档和全部源码表达均为独立编写。

2026-08-15 使用 123 网盘官方 Web 页面，在全新隔离目录中上传新建的 1 KiB 与 16 MiB+1 文件，确认了当前个人盘上传域名、端点顺序、字段组合、单片/多片分流以及完成响应结构。采集过程没有保存原始 HAR；只将脱敏后的协议事实写入 `protocol/current-web-2026-08-15.json`。该证据表明官方 Web 不调用 `s3_complete_multipart_upload` 和旧式 `upload_complete`，而是对预签数据 PUT 后只调用一次 `upload_complete/v2`。

固定 OpenList 事实与当前 Web 事实分别保存在 `protocol/openlist-v4.2.5.json` 和 `protocol/current-web-2026-08-15.json`。两者的共同事实可直接用于实现；差异项必须以当前官方 Web 实测为准，并继续通过状态化 mock 与真实终态校验防止接口漂移造成假成功。
