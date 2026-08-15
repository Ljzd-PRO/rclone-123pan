# rclone-123pan 发布说明

> 本项目仍处于实验性阶段，使用 123 网盘官方 Web 的非公开个人盘接口。请先在隔离目录验证，再用于重要数据。

## 主要变更

- 基于 rclone v1.75.0 提供独立的 `123pan` 树外后端和五平台定制二进制。
- 支持严格列表、MD5、Range/Seek 下载、秒传、预签名分片上传和可恢复 Update。
- 支持按精确 ID 验证的 Move、DirMove、软删除和安全空目录删除。
- 根据当前官方 Web API 实现服务端 Copy，包括异步任务轮询、任意目标名称和安全覆盖。
- 提供离线任务 backend commands、中文运维文档、状态化故障测试和固定 rclone 契约测试。
- 提供包名为 `rclone-123pan` 的 Linux amd64/arm64 Debian 安装包，可与发行版官方 rclone 并存。
- 项目原创代码与文档采用 MIT 许可证，作者为 [Ljzd-PRO](https://github.com/Ljzd-PRO)。

## 发布工件

每个平台压缩包均包含定制二进制、README、许可说明、SBOM、构建来源和嵌入式构建信息。Linux amd64/arm64 还提供 `.deb`，安装 `/usr/bin/rclone-123`、中文项目文档和 `rclone-123(1)` 手册，不覆盖 `/usr/bin/rclone`。请下载 `SHA256SUMS` 并在使用前验证工件摘要。

## 已知限制

- 个人盘 Web API 可能漂移或触发账号风控，当前版本不承诺稳定兼容性。
- 删除只表示移入回收站，不支持永久删除或清空回收站。
- 正确性敏感的同步和 bisync 应使用 `--checksum`。
- 其他平台原生挂载、专用空账号完整契约、七日 canary 和第三方来源审查仍属于稳定版门禁。
