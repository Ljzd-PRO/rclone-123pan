# 测试与发布门禁

每次 PR 都会在不使用真实 123 网盘账号的情况下运行单元测试、竞态测试，并构建五个主要目标平台。

mock 测试覆盖 0/1/16 MiB 边界、服务端 16/32 MiB `SliceSize` 档位、10/11 分片批次、10,000 分片上限、零字节 PUT、保留原分片字节后的重试、403 URL 刷新、输入源短读或变化、禁止错误调用 complete，以及预签名上传和 AWS SDK v2 上传两种模式。超大边界只验证规划逻辑，不会分配完整 payload；流式测试则在有代表性的边界上验证数据顺序和内容。

`Update` 状态机会在两个 rename 步骤和 backup 移入回收站步骤中，分别注入操作应用前故障，以及操作已经应用但响应丢失的故障。测试断言最终唯一可见的目标要么是经过完整验证的新文件，要么是精确的原文件；回滚不完整时必须返回明确的恢复 ID，不得按名称模式清理。

写操作测试覆盖 mkdir 响应丢失、两次空列表确认的 `Rmdir`、根目录和非空目录保护、过期对象快照、幂等 `Remove`、组合 move+rename、目录子树保护和 dircache 失效。所有 mock 删除都要求精确匹配当前服务端 ID 和名称。

测试还覆盖两个 remote 实例并发创建同名对象、共享 UID 锁协调、过期父目录 ID 拒绝，以及当旧 backup 已无法证明可恢复时不干扰已验证新文件的规则。

离线命令测试覆盖解析与提交、目标根 ID、稳定 JSON、全部已知状态映射、未知状态、正数且唯一的 ID 校验、分页一致性，以及删除后的再次查询确认。

`internal/testserver` 提供无套接字 transport，记录方法、路径、正文长度和 MD5，并可在状态应用前、状态已经应用但响应丢失后，或阻塞等待 context 取消时注入故障。各后端测试在此基础上构建 ID 文件树和任务模型。

`backend/pan123/fstests_test.go` 只在 `pan123live` build tag 下编译，并会用 `Test123Pan:` 调用 `fstests.Run`。完整 fstests 不适合在含有个人数据的账号上运行，除非同时满足以下条件，否则会强制拒绝：

- `RCLONE_123_RUN_LIVE=1`；
- `RCLONE_123_LIVE_ACK=DEDICATED_EMPTY_ACCOUNT`；
- `RCLONE_123_LIVE_ROOT_ID` 是非零固定测试根 ID；
- `RCLONE_123_LIVE_MANIFEST` 指向权限为 0600 的结构化 manifest；
- manifest 模式为 `dedicated-contract`，且记录两个 anchor 内、work root 外的不可变 sentinel 文件。

manifest 对文件数、目录数、单文件大小和累计 payload 实施不可放宽的硬配额。每次 upload request 的分配都永久消耗本轮配额；删除对象不会返还配额。manifest 和恢复 ledger 只允许保存对象 ID、parent ID、名称、大小、MD5 和单调清理状态 `active → trashed → missing_confirmed`，不得出现任何凭据或签名 URL；哨兵不能标记为已清理。

`tools/test-rclone-contract.sh` 会克隆 commit `9ee9d0a0cafd5e5fe3b271d2280b090ab6e64048`；若 `tools/fetch-references.sh` 已取得 HEAD 精确匹配的只读参考副本，则优先从本地以 `--no-hardlinks` 克隆，CI 中没有参考副本时使用最多三次的网络浅拉取。脚本随后向 `backend/all` 添加仅测试使用的 blank import，并使用本地 module replace。无需账号的 CI 会真实运行固定上游 `fs/operations`、`fs/sync`、`vfs` 和 `cmd/bisync` 的账号无关单元契约；提供同样的专用空账号门禁后，才会通过仓库中的定制 `test_all` YAML 运行破坏性测试套件。没有配置任何 ignore 列表。

任何真实测试开始前，操作人员还必须核验两个不可变 sentinel，只创建一个全新的 `rclone-test-[a-z0-9]{12,64}` anchor，记录所有新建 ID，并且只清理本轮记录的 ID。当前授权活动的硬上限为 100 个文件、50 个目录、单文件 160 MiB+1 和累计上传 payload 512 MiB。当前真实分页测试在已有 10 个可见测试文件的基础上，使用 45 个 1 字节文件和 46 个空目录形成 101 个可见条目；两次 100 项列表和两次 101 项列表分别完全一致。更大的分页、1 GiB 流式和 10,000 分片边界只在 mock 中验证。

手动触发的内部 alpha 工作流通过 `tools/build-alpha.sh`，使用 Go 1.25.0、`CGO_ENABLED=0`、`-trimpath`、`-buildvcs=false` 和强制 `noselfupdate` 交叉编译五个支持目标。脚本会把归档文件的所有者和时间戳统一为源码 commit，移除 gzip/zip 元数据，生成确定性的 CycloneDX 1.6 module SBOM 与来源记录，并校验 `SHA256SUMS`。artifact 保持私有且七天后过期；工作流没有 release 或 package 写权限。

2026-08-15 已在同一授权账号的全新随机 anchor 中，通过官方 Web 上传新建的 1 KiB 和 16 MiB+1 文件并取得脱敏的单片/多片/秒传线型。修复后的后端已经完成 1 KiB 普通上传、秒传、0/1/16 MiB−1/16 MiB/16 MiB+1/48 MiB+1 边界、并发 3、100/101 项真实分页、完整下载 MD5、可恢复 Update、同 ID Move/DirMove、非空 Rmdir 拒绝、精确软删除、双哨兵复核、同尺寸不同内容的 `check --checksum`、core Copy/sync 回退、max-delete 与只读 RC。固定上游的账号无关 operations/sync/VFS/bisync 单元契约也已真实运行通过。160 MiB+1 预申请进一步确认服务端会返回 32 MiB `SliceSize`，但本轮因不可退款 payload 配额不再重试数据上传。离线任务、mount 和专用空账号 `test_all` 仍须按门禁继续。详细记录见[真实账号测试记录](live-testing.md)。

在获得专用空测试账号、隔离的非零根目录和两个根外不可变 sentinel 前，稳定版发布仍保持阻断。后续真实测试必须只在随机命名的 `rclone-test-[a-z0-9]{12}` 目录中操作，并且只清理由本轮记录的 ID。
