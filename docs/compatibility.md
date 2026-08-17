# 兼容性

本模块固定使用 rclone v1.75.0 和 Go 1.25.0。主要交付物是定制静态二进制，而不是 Go 共享对象插件。

构建矩阵覆盖 Linux amd64/arm64、Windows amd64 和 macOS amd64/arm64。其他目标在经过明确测试前仅支持从源码构建。

Debian 包支持 `amd64` 与 `arm64`，适用于具备兼容 `dpkg`/`apt` 的 Debian、Ubuntu 及其衍生系统。包名和主程序均为 `rclone`，安装到 `/usr/bin/rclone`；它会替换系统中已安装的其他 rclone Debian 包，不能与发行版官方包并存。`.deb` 不携带 apt 软件源、自动升级服务、安装脚本或卸载脚本，也不会创建、修改或删除用户配置。其他 Debian 架构、RPM、Arch Linux、Alpine 和容器镜像尚未作为安装包交付。

## 一键安装脚本

Release 提供独立的 `install.sh`，仅支持 Linux/macOS 的 amd64 与 arm64。默认不接触源码分支，而是通过 GitHub `releases/latest` 的公开网页重定向选择最新的非草稿、非预发布版本，不调用受匿名限额约束的 GitHub REST API；随后下载与平台完全匹配的 `.tar.gz` 和同一 Release 的 `SHA256SUMS`。安装前依次验证归档摘要和候选二进制报告的版本，任一不匹配均在覆盖现有程序前失败。

默认安装路径是 `/usr/local/bin/rclone`。可以在直接执行脚本时通过绝对路径环境变量 `RCLONE_INSTALL_DIR` 修改目录，也可以向脚本传入一个完整的已发布标签以固定版本。相同版本属于幂等成功，不重复下载归档；覆盖非本项目程序时只在备份不存在的情况下创建 `<目标路径>.official`。新程序先写入同目录临时文件，再以 rename 原子替换目标。

安装器本身也作为 Release 资产发布并列入 `SHA256SUMS`。推荐入口使用 `/releases/latest/download/install.sh`，因此不会执行仓库 `main` 中尚未发布的脚本版本。

## 替换已有 rclone 程序

覆盖程序前必须停止正在运行的传输、mount、serve、RC 服务和其他 rclone 进程，并保留原程序备份。配置文件与二进制相互独立，不需要移动或覆盖。

Linux 与 macOS 使用 `whereis rclone` 查看所有已知位置；其输出可能同时包含程序和 man page，因此再用 `command -v rclone` 取得当前 shell 实际执行的路径：

```console
whereis rclone
RCLONE_TARGET="$(command -v rclone)"
test -n "$RCLONE_TARGET"
if [ ! -e "${RCLONE_TARGET}.official" ]; then sudo cp -p "$RCLONE_TARGET" "${RCLONE_TARGET}.official"; fi
sudo install -m 0755 ./rclone "$RCLONE_TARGET"
"$RCLONE_TARGET" version
```

若目标路径属于当前用户且可写，可以去掉 `sudo`。Debian/Ubuntu 上若原程序由 `dpkg` 管理，应优先安装本项目 `.deb`，避免包数据库记录与磁盘文件不一致。

Windows 使用 PowerShell 查询 PATH 中全部同名程序，并替换优先级最高的应用程序。目标位于 `Program Files` 等受保护目录时，需要使用管理员 PowerShell：

```powershell
Get-Command rclone.exe -All | Select-Object -ExpandProperty Source
$RcloneTarget = (Get-Command rclone.exe -CommandType Application -ErrorAction Stop).Source
if (-not (Test-Path "$RcloneTarget.official")) { Copy-Item $RcloneTarget "$RcloneTarget.official" }
Copy-Item .\rclone.exe $RcloneTarget -Force
& $RcloneTarget version
```

Homebrew、apt、winget、Chocolatey、Scoop 等包管理器后续升级可能恢复其维护的官方程序。需要回退时，停止所有 rclone 进程后把 `.official` 备份复制回原路径。本项目二进制强制启用 `noselfupdate`；不要尝试使用官方 `rclone selfupdate` 更新它。

五平台核心工件固定使用 `CGO_ENABLED=0`。当前 macOS 核心工件只含 `nfsmount`，不含 `mount` 子命令；本轮 `nfsmount` 与独立 NFS serve + `mount_nfs` 均已真实挂载通过。需要 macFUSE/WinFsp 等平台依赖的其他原生挂载仍必须作为单独工件构建和测试；单个平台或 serve WebDAV 通过不能替代其他 native runner 的结论。

rclone v1.75.0 要求每个已注册后端的文档都嵌入 rclone 自身的 `docs/data/backends` 包。树外后端无法向该嵌入式文件系统添加内容，因此这个固定版本会在启动时输出无害的 `no overview data found for "123pan"` 诊断信息。后端注册和实际操作不受影响。若不维护完整 fork，要消除此信息就必须修改上游 rclone；目前将其作为兼容性问题跟踪，不得通过替换或修补已固定的 rclone 依赖来隐藏。

契约测试脚本不会修改仓库中固定的依赖，也不维护 rclone fork。它会创建一个用后即弃的精确 checkout，加入一个 blank import 和本地 `go.mod replace`，真实运行账号无关的 operations/sync/VFS/bisync 单元契约，测试结束后再删除该 checkout。这也是树外模块如果不进行注入，就无法直接运行这些上游测试的原因。需要专用空账号的 `test_all` 仍受单独的 sentinel 和 manifest 门禁保护。

## 当前个人盘 API 漂移

OpenList v4.2.5 的个人盘 driver 使用 `https://yun.123pan.com/b/api`。2026-08-15 的官方 Web 脱敏抓线确认当前控制 API 已改为 `https://api.123278.com/b/api`，页面来源为 `https://yun.123pan.cn/`。

OpenList v4.2.5 的 `drivers/123` 明确不实现 Copy；这不能代表当前 123 网盘官方 Web 的能力。2026-08-15 当前官方 Web 公开生产资源已经使用 `/restful/goapi/v1/file/copy/async`，并在异步模式轮询 `/restful/goapi/v1/file/copy/task`。本后端据此独立实现文件级服务端 Copy，不复制 OpenList 的 AGPL 表达性代码。官方接口只保留源名复制到目录，因此 rclone 任意目标名通过唯一 staging 目录和既有 Move/rename 安全补齐。

固定 OpenList 与当前 Web 都在预签数据 PUT 后只调用 `/file/upload_complete/v2`，请求包含 `StorageNode`、`bucket`、`fileId`、`fileSize`、`isMultipart`、`key` 和 `uploadId`。当前 Web 没有调用 `s3_complete_multipart_upload`、旧式 `upload_complete` 或固定等待。单片使用 `s3_upload_object/auth`；16 MiB+1 的实测多片上传先查询 `s3_list_upload_parts`，再按独占上界申请分片 URL，最后只调用一次 v2 完成接口。

OpenList v4.2.5 固定使用 16 MiB 分片且忽略响应中的 `SliceSize`。隔离真实 API 在 48 MiB+1 文件上返回 16 MiB，在 160 MiB+1 预申请上返回 32 MiB。后端因此不照搬 OpenList 的固定值，而只接受已观察到的 16/32 MiB 两档并用返回值计算分片、offset 和内存上界；其他值在任何数据 PUT 前失败关闭。

当前 Web 的 `upload_request` 还要求 `parentFileId` 为 JSON number、`RequestSource=null`，且没有发送 `duplicate`。这些协议事实已固化为脱敏 fixture；实现仍不能把 `Reuse=true`、PUT 200 或完成接口 `code=0` 单独视为成功，必须严格核验最终对象。

同 MD5 异名秒传还有一个独立响应形状：当前 Web 返回 `Reuse=true`、`FileId=0` 和非空 key，但不发数据 PUT，也不调用完成接口；随后重新列表并使用新出现的正 ID 查询对象。后端只在目标父目录中恰有一个名称、大小和 MD5 全部匹配的正 ID 文件时接受该候选。0 ID 永远不会进入删除、移动、重命名或数据上传请求。
