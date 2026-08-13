# 兼容性

本模块固定使用 rclone v1.75.0 和 Go 1.25.0。主要交付物是定制静态二进制，而不是 Go 共享对象插件。

构建矩阵覆盖 Linux amd64/arm64、Windows amd64 和 macOS amd64/arm64。其他目标在经过明确测试前仅支持从源码构建。

rclone v1.75.0 要求每个已注册后端的文档都嵌入 rclone 自身的 `docs/data/backends` 包。树外后端无法向该嵌入式文件系统添加内容，因此这个固定版本会在启动时输出无害的 `no overview data found for "123pan"` 诊断信息。后端注册和实际操作不受影响。若不维护完整 fork，要消除此信息就必须修改上游 rclone；目前将其作为兼容性问题跟踪，不得通过替换或修补已固定的 rclone 依赖来隐藏。

契约测试脚本不会修改仓库中固定的依赖，也不维护 rclone fork。它会创建一个用后即弃的精确 checkout，加入一个 blank import 和本地 `go.mod replace`，测试结束后再删除该 checkout。这也是树外模块如果不进行注入，就无法直接运行上游 operations/sync/VFS/bisync 测试的原因。

## 当前个人盘 API 漂移

OpenList v4.2.5 的个人盘 driver 直接使用 `yun.123pan.com/b/api`，并把 `Reuse=true` 当作秒传成功，预签数据完成后调用 `/file/upload_complete/v2`。2026-08-14 的真实账号测试表明，当前服务可能同时返回 `Reuse=true` 和上传 key，但对应文件 ID 不可见；完成端点返回成功也不能证明对象已经生成。

近期社区客户端改为显式调用 `s3_complete_multipart_upload` 后再调用旧式 `upload_complete`，但本项目在单分片授权流程上实测该合并端点返回业务码 500。该差异必须通过当前 Web 客户端的完整线型和专用账号重新验证，不能靠放宽终态校验绕过。
