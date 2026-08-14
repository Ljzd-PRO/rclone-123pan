# rclone 能力清单

本清单固定对应 rclone v1.75.0。后端只声明已经实现且能满足 rclone 契约的能力；缺少可选接口并不等于普通用户命令不可用。

## 后端原生能力

| 能力 | 状态与约束 |
| --- | --- |
| List、NewObject | 完整分页后才返回；重复 ID、路径歧义和 Total 漂移均失败关闭；100/101 项跨页已真实验证 |
| Put、PutStream | 0/1/16 MiB−1/16 MiB/16 MiB+1/48 MiB+1、并发 3 与秒传已真实通过；32 MiB 动态档位已由真实响应和 mock 固化，完整契约未完成 |
| Mkdir、Rmdir | 按精确 ID 协调；Rmdir 需要连续两次完整空列表 |
| Open | 支持完整读取、Range、Seek 和后缀范围；下载客户端不携带控制端凭据 |
| Update | staging/backup 状态机；回滚不完整时返回精确恢复 ID |
| Remove | 只移入回收站，不永久删除 |
| Move、DirMove | 仅限同一 UID；移动和改名分步执行并按 ID 回滚 |
| Copy | 仅限同一 UID；使用官方 Web 的异步复制任务，在唯一 staging 目录核验新 ID/父目录/名称/大小/MD5 后再安全提升到任意 rclone 目标名；不读取正文 |
| Hash | 快速 MD5 来自对象 ETag；非法或缺失值返回明确协议错误 |
| About、UserInfo、Disconnect | 使用个人盘账号信息和登出接口 |
| DirCacheFlush | 清空 ID 路径缓存 |
| Command | 提供离线任务 add/status/delete |

对象实现 `IDer` 与 `ParentIDer`。特征固定为 `PartialUploads=false`、`SlowHash=false`、`DuplicateFiles=false`、`CaseInsensitive=false`；feature golden 会防止 core 反射意外启用未支持接口。

## rclone core 回退

| 能力或命令 | 回退路径 |
| --- | --- |
| 本地或其他后端到 123Pan 的 copy、copyto、sync | 使用 Open+Put/Update；Put 仍可通过 MD5 命中秒传 |
| purge | 逐项调用 Remove，再安全删除空目录 |
| `--fast-list` | 缺少 ListR 时递归调用严格 List |
| 多线程写 | 缺少 OpenWriterAt/OpenChunkWriter 时使用后端自身有界分片 Put |

旧版 core 回退已真实覆盖远端到远端 copyto、checksum sync、max-delete、backup-dir 和 purge。启用服务端 Copy 后，同配置的 123Pan→123Pan copy/copyto/sync 会优先使用服务端协议；本地或不同后端来源仍走 Put。copyurl 与未知大小 rcat 也已分别完成小文件闭环；purge 的文件与目录均按精确 ledger ID 软删除，而不是调用服务端批量清理。

## 明确不支持

| 能力 | 原因 |
| --- | --- |
| PublicLink | 当前范围排除 123Link 和 123Share |
| CleanUp | 清空回收站属于永久删除，与软删除边界冲突 |
| SetModTime、DirSetModTime | 没有经过验证的服务端写入接口 |
| ChangeNotify | 没有增量变更流；VFS 使用缓存期限和手动 refresh |
| PutUnchecked、MergeDirs | 与不允许重复路径的身份模型冲突 |
| OpenWriterAt | 123Pan 没有随机写 API；VFS 随机写使用 full cache mode |

## 命令验收组

- 只读与信息：`lsd`、`lsf`、`lsjson`、`lsl`、`tree`、`size`、`about`、`cat`、`md5sum`、`hashsum`。
- 传输：`copy`、`copyto`、`copyurl`、`rcat`、`sync`、`move`、`moveto`、`--backup-dir`。
- 校验：`check`、`check --download`、`checksum`；正确性敏感的同步必须使用 `--checksum`。
- 删除：`delete`、`deletefile`、`rmdir`、`rmdirs`、core `purge`、dry-run 和 max-delete。
- 上层：bisync、VFS、mount、serve、RC、crypt 包装 remote 和离线 backend commands。当前已真实完成 checksum bisync 的 resync/双向增量/check-access/max-delete/recover，四种 VFS cache mode 和 VFS RC，HTTP/DLNA/WebDAV/FTP/SFTP/S3/NFS serve，macOS nfsmount 与 crypt 包装；其他平台挂载和离线任务 live 仍按门禁推进。

CI 使用 feature golden 检查全部 rclone `fs.Features` 字段：`Copy` 必须启用，Purge、ListR、CleanUp 和 PublicLink 等未支持接口必须保持为空。`--copy-dest` 因而具备所需的服务端 Copy 基础能力，状态化 mock 与 `operations.Copy` 已覆盖新建和覆盖目标；真实账号命令闭环仍是发布门禁。RC `operations/list` 与 `operations/stat` 已和 CLI 结果做过真实 ID/MD5 对等验证。

真实只读验收已经覆盖本节列出的全部信息类命令、`check --download`、`checksum` 和 `--fast-list`；传输与删除类命令的逐项状态以[真实账号测试记录](live-testing.md)为准。

VFS 的随机写和 truncate 只以 `--vfs-cache-mode full` 作为正式支持口径。macOS 已经通过 NFS/full-cache 挂载完成真实文件描述符级的 seek、覆写和 truncate 探测；稳定门禁仍要求 Linux 与 Windows 对应 runner 独立通过，不能把 WebDAV PUT 或单平台结果外推。
