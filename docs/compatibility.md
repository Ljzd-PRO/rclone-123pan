# 兼容性

本模块固定使用 rclone v1.75.0 和 Go 1.25.0。主要交付物是定制静态二进制，而不是 Go 共享对象插件。

构建矩阵覆盖 Linux amd64/arm64、Windows amd64 和 macOS amd64/arm64。其他目标在经过明确测试前仅支持从源码构建。

五平台核心工件固定使用 `CGO_ENABLED=0`。当前 macOS 核心工件只含 `nfsmount`，不含 `mount` 子命令；需要 macFUSE/WinFsp 等平台依赖的原生挂载必须作为单独工件构建和测试。serve WebDAV 或 VFS 单元契约通过，不能替代 native mount runner 的结论。

rclone v1.75.0 要求每个已注册后端的文档都嵌入 rclone 自身的 `docs/data/backends` 包。树外后端无法向该嵌入式文件系统添加内容，因此这个固定版本会在启动时输出无害的 `no overview data found for "123pan"` 诊断信息。后端注册和实际操作不受影响。若不维护完整 fork，要消除此信息就必须修改上游 rclone；目前将其作为兼容性问题跟踪，不得通过替换或修补已固定的 rclone 依赖来隐藏。

契约测试脚本不会修改仓库中固定的依赖，也不维护 rclone fork。它会创建一个用后即弃的精确 checkout，加入一个 blank import 和本地 `go.mod replace`，真实运行账号无关的 operations/sync/VFS/bisync 单元契约，测试结束后再删除该 checkout。这也是树外模块如果不进行注入，就无法直接运行这些上游测试的原因。需要专用空账号的 `test_all` 仍受单独的 sentinel 和 manifest 门禁保护。

## 当前个人盘 API 漂移

OpenList v4.2.5 的个人盘 driver 使用 `https://yun.123pan.com/b/api`。2026-08-15 的官方 Web 脱敏抓线确认当前控制 API 已改为 `https://api.123278.com/b/api`，页面来源为 `https://yun.123pan.cn/`。

固定 OpenList 与当前 Web 都在预签数据 PUT 后只调用 `/file/upload_complete/v2`，请求包含 `StorageNode`、`bucket`、`fileId`、`fileSize`、`isMultipart`、`key` 和 `uploadId`。当前 Web 没有调用 `s3_complete_multipart_upload`、旧式 `upload_complete` 或固定等待。单片使用 `s3_upload_object/auth`；16 MiB+1 的实测多片上传先查询 `s3_list_upload_parts`，再按独占上界申请分片 URL，最后只调用一次 v2 完成接口。

OpenList v4.2.5 固定使用 16 MiB 分片且忽略响应中的 `SliceSize`。隔离真实 API 在 48 MiB+1 文件上返回 16 MiB，在 160 MiB+1 预申请上返回 32 MiB。后端因此不照搬 OpenList 的固定值，而只接受已观察到的 16/32 MiB 两档并用返回值计算分片、offset 和内存上界；其他值在任何数据 PUT 前失败关闭。

当前 Web 的 `upload_request` 还要求 `parentFileId` 为 JSON number、`RequestSource=null`，且没有发送 `duplicate`。这些协议事实已固化为脱敏 fixture；实现仍不能把 `Reuse=true`、PUT 200 或完成接口 `code=0` 单独视为成功，必须严格核验最终对象。

同 MD5 异名秒传还有一个独立响应形状：当前 Web 返回 `Reuse=true`、`FileId=0` 和非空 key，但不发数据 PUT，也不调用完成接口；随后重新列表并使用新出现的正 ID 查询对象。后端只在目标父目录中恰有一个名称、大小和 MD5 全部匹配的正 ID 文件时接受该候选。0 ID 永远不会进入删除、移动、重命名或数据上传请求。
