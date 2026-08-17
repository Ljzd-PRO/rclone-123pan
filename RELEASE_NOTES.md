# rclone-123pan 发布说明

> 本项目仍处于实验性阶段，使用 123 网盘官方 Web 的非公开个人盘接口。请先在隔离目录验证，再用于重要数据。

## 对应上游版本

本发行版基于 [rclone v1.75.0](https://github.com/rclone/rclone/releases/tag/v1.75.0)，固定上游 commit 为 `9ee9d0a0cafd5e5fe3b271d2280b090ab6e64048`。版本号 `v1.75.0-123pan.3` 中，`v1.75.0` 表示对应的上游 rclone 版本，`123pan.3` 表示本项目针对该上游版本的第 3 个适配修订。

## 主要变更

- 修复一键安装脚本在 GitHub 匿名 API 配额耗尽后返回 403 的问题；Latest Release 改由公开网页重定向解析，不再依赖 REST API。
- 增加只安装 GitHub 正式 Release 的一键安装脚本，支持平台识别、摘要与版本双重验证、一次性备份、原子替换及幂等更新。
- 增加账号风控、短信验证码和微信登录提示的 fatal 识别，以及验证后可显式执行的 `rclone config reconnect` 事务式重新认证。
- 版本统一为 `v<rclone版本>-123pan.<适配修订号>`，当前版本为 `v1.75.0-123pan.3`。
- 基于 rclone v1.75.0 提供独立的 `123pan` 树外后端和五平台定制二进制。
- 支持严格列表、MD5、Range/Seek 下载、秒传、预签名分片上传和可恢复 Update。
- 支持按精确 ID 验证的 Move、DirMove、软删除和安全空目录删除。
- 根据当前官方 Web API 实现服务端 Copy，包括异步任务轮询、任意目标名称和安全覆盖。
- 提供离线任务 backend commands、中文运维文档、状态化故障测试和固定 rclone 契约测试。
- 提供包名为 `rclone` 的 Linux amd64/arm64 Debian 安装包，安装标准 `/usr/bin/rclone` 并替换其他 rclone Debian 包。
- 项目原创代码与文档采用 MIT 许可证，作者为 [Ljzd-PRO](https://github.com/Ljzd-PRO)。

## 发布工件

每个平台压缩包均包含名为 `rclone`（Windows 为 `rclone.exe`）的定制二进制、README、许可说明、SBOM、构建来源和嵌入式构建信息。Linux amd64/arm64 还提供 `.deb`，安装 `/usr/bin/rclone`、中文项目文档和 `rclone(1)` 手册。独立 `install.sh` 资产用于 Linux/macOS 一键安装；它与其他工件一起列入 `SHA256SUMS`。

## 已知限制

- 个人盘 Web API 可能漂移或触发账号风控，当前版本不承诺稳定兼容性。
- 删除只表示移入回收站，不支持永久删除或清空回收站。
- 正确性敏感的同步和 bisync 应使用 `--checksum`。
- 其他平台原生挂载、专用空账号完整契约、七日 canary 和第三方来源审查仍属于稳定版门禁。
