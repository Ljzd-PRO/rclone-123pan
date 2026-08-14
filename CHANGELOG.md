# 变更日志

## 尚未发布

- 统一后端目录、import path、CLI flag 前缀和测试 build tag 为 `123pan`；Go 包声明采用语法合法且同序的 `_123pan`。
- 增加自动测试、main 分支成功后自动构建和标签/手动 Release workflow；五平台工件通过双重摘要验证，Release 正文固定读取 `RELEASE_NOTES.md`，仅最终发布 job 具有写权限。
- 根据 GitHub runner 实测，将 checkout、Go 环境和 artifact actions 升级到当前 Node.js 24 稳定主版本，消除 Node.js 20 弃用兼容性警告。
- 搭建基于 rclone v1.75.0 的树外发行结构，并注册实验性的 `123pan` 后端。
- 增加严格的签名协议处理、协同认证、由 ID 支撑的一致性列表、MD5、经过验证的下载、秒传、AWS SDK v2 上传和有界预签名上传。
- 增加可恢复的对象替换、回收站写操作、服务端 `Move`/`DirMove`、配额与用户信息，以及离线任务命令。
- 根据当前 123 网盘官方 Web 的 `file/copy/async` 与 `file/copy/task` 协议实现服务端 `Copy`；使用唯一 staging 目录补齐任意目标名，通过 ID、父目录、名称、大小和 MD5 核验结果，并以 backup 事务安全覆盖已有目标。
- 增加服务端 Copy 的同步/异步、响应丢失、失败恢复、元数据冲突和 rclone operations 新建/覆盖测试；`--copy-dest` 真实账号闭环仍是发布门禁。
- 增加无套接字故障注入、竞态测试、受门禁保护的 fstests、固定 rclone 版本的契约注入、五目标 CI，以及运维、安全和恢复文档。
- 完成 2026-08-14 隔离真实账号冒烟测试：确认登录、严格列表、随机测试目录创建与按 ID 安全删除可用；确认当前个人盘上传协议相对 OpenList v4.2.5 已漂移，预签上传无法形成可见对象，因此继续阻断发布。
- 根据 2026-08-14 冒烟测试修复缺失命令根创建、命令根安全删除、分片 URL 结束边界、复用候选核验和父目录 ID 线型；当时加入的无证据 S3 merge 流程现已被后续官方 Web 证据否定。
- 通过 2026-08-15 官方 Web 脱敏抓线确认当前控制域名、单片与多片端点顺序和 `upload_complete/v2` 完成结构；确认先前加入的 S3 merge、旧 complete 与固定等待没有协议证据，进入移除修复。
- 重建当前 Web 预签上传状态机，要求完整 v2 完成上下文和 `file_info` 映射；后端 1 KiB 上传、完整下载、MD5 与精确软删除真实闭环通过。
- 固化 `Reuse=true, FileId=0` 秒传线型；只通过父目录内唯一精确元数据匹配协调正 ID，真实复测确认不读取正文、不发数据 PUT且下载内容一致。
- 收紧 MD5 与能力语义：非法或缺失 ETag 的 `Hash(MD5)` 明确失败，增加 `ParentIDer`，并把 `PartialUploads` 固定为 false。
- 为真实测试恢复 ledger 增加不可回退的 `active → trashed → missing_confirmed` 清理状态，禁止把哨兵标记为已清理。
- 真实通过 0、1、16 MiB−1、16 MiB 和 16 MiB+1 上传边界及完整下载校验；固化完成响应可把预申请 ID 映射为不同最终 ID 的契约。
- 真实验证 16 MiB+1 完整下载、前缀 Range、Seek、suffix-range 和路径大小写敏感行为。
- 修复 rooted rclone Fs 的父路径复核基准和 Update 特殊名称二次编码；真实通过可恢复 Update、同 ID Move/DirMove、非空 Rmdir 拒绝及由内向外安全清理。
- 真实通过 48 MiB+1 四分片并发 3 上传和完整下载；160 MiB+1 预申请确认服务端动态返回 32 MiB `SliceSize`，生产实现只接受实测的 16/32 MiB 两档，其他值在数据 PUT 前失败关闭。
- 给真实测试 ledger 增加原子批量配额预留和受前缀/直系路径/精确数量约束的 `lsjson` 导入；真实验证两次 100 项与两次 101 项完整列表分别一致，跨页无遗漏或重复。
- 将固定 rclone 的树外契约注入从编译检查升级为实际运行 operations、sync、VFS 与 bisync 的账号无关测试；真实验证 core Copy/sync/purge、max-delete、backup-dir、copyurl 和未知大小 rcat。
- 真实验证 checksum bisync 的 resync、双向增量、check-access、max-delete 阻断与 recover，以及四种 VFS cache mode 和 VFS RC。
- 真实验证 HTTP、DLNA、WebDAV、FTP、SFTP、S3、NFS serve，macOS nfsmount/full-cache 随机写与 truncate，以及 crypt 包装 remote；离线任务 live 因会枚举个人账号既有任务而有意阻断。
- 增加 408/429/503 故障矩阵、10,000 组上传规划模型、250 步移动模型、两个 fuzz 入口、100 次取消资源回收，以及真实流过 1 GiB mock 数据面的有界内存测试。
- 五个核心目标固定使用 `noselfupdate`、`CGO_ENABLED=0`、`-trimpath` 与确定性归档；生成 CycloneDX SBOM、来源记录和 SHA-256 校验值。其他平台原生挂载、七日 canary、专用空账号契约和许可审查仍阻断稳定版。
