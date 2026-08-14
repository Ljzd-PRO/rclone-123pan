# 协议证据夹具

本目录只保存可复核、已脱敏的协议事实，不保存原始 HAR、响应正文、账号标识、对象标识、文件名、MD5、Cookie、Authorization 值、临时凭据或预签 URL。

- `openlist-v4.2.5.json`：从固定 tag 的 `drivers/123` 提取的 clean-room 行为事实。
- `current-web-2026-08-15.json`：通过 123 网盘官方 Web 页面，在全新隔离目录中上传 1 KiB 与 16 MiB+1 文件所得的脱敏请求线型。
- `provider-upload-profiles-2026-08-15.json`：修复后端在隔离真实账号中观察到的 16/32 MiB `SliceSize` 响应及失败关闭事实；不含对象 ID、文件名、MD5 或预签信息。

这些夹具描述观察结果，不是可盲目回放的请求模板。生产实现仍须严格解析响应、校验字段组合，并以最终对象的 ID、父目录、名称、大小和 MD5 作为成功条件。
