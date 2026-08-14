# rclone 能力清单

本清单固定对应 rclone v1.75.0。后端只声明已经实现且能满足 rclone 契约的能力；缺少可选接口并不等于普通用户命令不可用。

## 后端原生能力

| 能力 | 状态与约束 |
| --- | --- |
| List、NewObject | 完整分页后才返回；重复 ID、路径歧义和 Total 漂移均失败关闭 |
| Put、PutStream | 1 KiB 普通上传与秒传已真实通过；多片和完整契约未完成，稳定发布保持阻断 |
| Mkdir、Rmdir | 按精确 ID 协调；Rmdir 需要连续两次完整空列表 |
| Open | 支持完整读取、Range、Seek 和后缀范围；下载客户端不携带控制端凭据 |
| Update | staging/backup 状态机；回滚不完整时返回精确恢复 ID |
| Remove | 只移入回收站，不永久删除 |
| Move、DirMove | 仅限同一 UID；移动和改名分步执行并按 ID 回滚 |
| Hash | MD5 来自对象 ETag；非法或缺失值必须失败关闭 |
| About、UserInfo、Disconnect | 使用个人盘账号信息和登出接口 |
| DirCacheFlush | 清空 ID 路径缓存 |
| Command | 提供离线任务 add/status/delete |

## rclone core 回退

| 能力或命令 | 回退路径 |
| --- | --- |
| 普通 copy、copyto、sync | 缺少服务端 Copy 时使用 Open+Put/Update；Put 仍可通过 MD5 命中秒传 |
| purge | 逐项调用 Remove，再安全删除空目录 |
| `--fast-list` | 缺少 ListR 时递归调用严格 List |
| 多线程写 | 缺少 OpenWriterAt/OpenChunkWriter 时使用后端自身有界分片 Put |

## 明确不支持

| 能力 | 原因 |
| --- | --- |
| `--copy-dest` | rclone 强制要求服务端 Copy，不能使用普通 copy 回退 |
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
- 上层：bisync、VFS、mount、serve、RC、crypt 包装 remote 和离线 backend commands。

CI 使用 feature golden 检查全部 rclone `fs.Features` 字段。新增方法如果导致 Copy、Purge、ListR、CleanUp、PublicLink 等接口意外变为非空，测试必须失败。
