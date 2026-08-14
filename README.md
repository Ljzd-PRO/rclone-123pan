# rclone 123Pan 后端

本仓库用于构建定制的 `rclone-123` 二进制，其中包含面向 123 网盘个人账号的实验性树外后端。

> [!WARNING]
> 当前版本是内部 alpha，使用逆向分析所得的 Web API。2026-08-15 已通过官方 Web 脱敏抓线闭合当前单片、多片和秒传协议；修复后的后端已完成 1 KiB 普通上传与同 MD5 异名秒传的隔离真实闭环。多片、更新和高层契约尚未完成，因此仍不适合公开发布或生产使用。

## 固定基线

- rclone `v1.75.0`（`9ee9d0a0cafd5e5fe3b271d2280b090ab6e64048`）
- OpenList `v4.2.5`（`cc87e88f038a5a27c8782afc7b66a3c1a3cdcb77`）

只将 OpenList `drivers/123` 的协议行为作为参考。`123_open`、`123_link` 和 `123_share` 均不在本项目范围内。

## 当前里程碑

| 能力 | 状态 |
| --- | --- |
| 定制静态 rclone 入口 | 已实现 |
| 注册 `123pan` 后端 | 已实现 |
| 协议类型、签名、严格响应 envelope、脱敏 | 已实现并通过单元测试 |
| 密码登录、token 缓存、401 协同刷新、退出登录 | 已实现并通过单元测试 |
| 严格列表、路径解析、MD5、配额和用户信息 | 已实现并通过单元测试 |
| 验证式下载、Range/Seek/后缀范围、单次 403 刷新 | 已实现并通过单元测试 |
| 可重放输入源、零字节处理和验证式秒传 | 已实现并通过单元测试 |
| AWS SDK v2 旧式上传和有界预签名分片上传 | 当前官方 Web 协议已取证；1 KiB 预签上传已真实通过，多片待验证 |
| 可恢复的 staging/backup 对象替换 | 已实现并通过故障测试 |
| 安全的 mkdir/remove/rmdir 和按 ID 验证的 Move/DirMove | 已实现并通过单元测试 |
| 离线任务 add/status/delete 后端命令 | 已实现并通过单元测试 |
| 故障 transport、受门禁保护的 fstests、固定 rclone 契约注入 | 已实现 |
| 隔离真实账号测试 | 后端 1 KiB 上传、完整下载、MD5、秒传和精确软删除已通过 |
| 专用空账号完整契约测试 | 阻断：尚无满足 sentinel 门禁的专用账号 |
| 公开发布 | 阻断：许可证审查和真实环境验证未完成 |

## 构建

需要 Go 1.25.0。

```console
make build
./bin/rclone-123 version
```

必须启用 `noselfupdate` 构建 tag，否则自更新会用不含此后端的官方二进制替换定制版本。

## 配置

请使用定制二进制的交互式配置器，使密码经过 rclone obscure 后再写入配置：

```console
./bin/rclone-123 config
```

新建 remote，选择 `123pan`，然后输入个人账号的手机号或邮箱及密码。默认根 ID 为 `0`；生产使用自定义根目录时，应设置已经核验的数字 `root_folder_id`。典型检查命令如下：

```console
./bin/rclone-123 lsd my123:
./bin/rclone-123 md5sum my123:
./bin/rclone-123 copy ./local-file my123:destination/
./bin/rclone-123 check ./local-dir my123:destination/
```

本后端有意不提供服务端 `Copy` 或 `Purge`。rclone core 会回退到 `Put`（仍可通过 MD5 命中秒传）以及逐 ID 删除。当前也不实现链接、分享、公开链接、修改时间写入、`ListR`、清理、变更通知和永久删除。

预期行为和发布门禁详见[后端说明](docs/123pan.md)、[rclone 能力清单](docs/capabilities.md)、[安全说明](docs/security.md)和[测试说明](docs/testing.md)。隔离实测及官方 Web 脱敏协议证据见[真实账号测试记录](docs/live-testing.md)和[协议证据夹具](protocol/README.md)，替换失败后的处理见[恢复与回滚](docs/recovery.md)。

`make contract` 会临时克隆精确的 rclone commit，把本模块注入其 `backend/all`，并编译上游 operations、sync、VFS 和 bisync 测试包。只有在明确提供全部真实测试门禁变量后，才会运行具有破坏性的 `test_all`。

手动触发的内部 alpha 工作流会为 Linux amd64/arm64、Windows amd64 和 macOS amd64/arm64 构建可复现压缩包，同时生成 SHA-256 校验值、确定性的 CycloneDX 1.6 SBOM、嵌入式 Go 构建信息和构建来源记录。工作流只上传保留七天的私有 artifact，绝不会创建公开 release。在许可证和真实账号门禁全部解除前，不得分发这些构建产物。
