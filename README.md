<div align="center">

<img src="assets/rclone-123pan-logo.png" alt="rclone-123pan 项目 Logo" width="200">

# rclone-123pan

**为 rclone 提供 123 网盘个人盘支持**

</div>

> [!WARNING]
> 本项目使用 123 网盘官方 Web 客户端调用的个人盘接口。该接口未公开承诺兼容性，可能随网页更新而变化，也可能触发账号风控。重要数据请保留独立副本，并先在隔离目录中验证。

## 简介

`rclone-123pan` 是静态集成 123 网盘 backend 的定制 rclone 发行版，remote 类型为 `123pan`，主程序为 `rclone`。它保留 rclone 的命令、配置格式和生态能力，同时实现 123 网盘个人盘所需的认证、文件操作、传输与校验逻辑。

项目提供 Windows、macOS 和 Linux 预构建程序，以及 Debian/Ubuntu 安装包；也可以作为 out-of-tree backend 从源码构建。

## 功能

| 功能 | 描述 |
| --- | --- |
| 文件操作 | 支持目录遍历、上传、下载、更新、移动、重命名、空目录和软删除。 |
| 数据完整性 | 暴露文件 MD5，并在上传、秒传和覆盖完成后核验对象 ID、路径、大小与摘要。 |
| 传输优化 | 支持秒传、并发分片上传、可重试数据源以及 Range、Seek 和 suffix range 读取。 |
| 服务端复制 | 同一账号内复制文件时调用 123 网盘服务端接口，无需经本机下载再上传。 |
| 安全覆盖 | 更新已有对象时使用 staging/backup 状态机，并按对象 ID 协调、验证和回滚。 |
| rclone 集成 | 支持 rclone core 的 copy、sync、bisync、VFS、serve、crypt、RC 等通用能力。 |
| 离线任务 | 通过 backend command 添加、查询和删除 123 网盘离线下载任务。 |
| 删除语义 | 删除操作移入 123 网盘回收站，不实现永久删除或清空回收站。 |

完整接口矩阵见 [能力清单](docs/capabilities.md)。

## 安装

从 [Releases](https://github.com/Ljzd-PRO/rclone-123pan/releases) 下载对应平台的压缩包或 Debian 安装包。没有可用 Release 时，可从 [GitHub Actions 构建记录](https://github.com/Ljzd-PRO/rclone-123pan/actions/workflows/internal-alpha.yml) 下载自动构建工件。

压缩包解压后直接运行 `rclone`；Windows 程序名为 `rclone.exe`。

Debian、Ubuntu 及其衍生系统可以直接安装下载的 `.deb`：

```console
sudo apt install ./rclone_*.deb
```

Debian 包安装的命令为 `/usr/bin/rclone`，包名也为 `rclone`。它会替换系统中已安装的其他 rclone Debian 包，不能与发行版提供的 rclone 包并存；卸载使用 `sudo apt remove rclone`。

### 替换已有 rclone

Linux 与 macOS 可先用 `whereis` 查看安装位置，再备份并覆盖当前实际执行的程序：

```console
whereis rclone
RCLONE_TARGET="$(command -v rclone)"
test -n "$RCLONE_TARGET"
if [ ! -e "${RCLONE_TARGET}.official" ]; then sudo cp -p "$RCLONE_TARGET" "${RCLONE_TARGET}.official"; fi
sudo install -m 0755 ./rclone "$RCLONE_TARGET"
"$RCLONE_TARGET" version
```

Windows 请在有权限写入目标目录的 PowerShell 中执行：

```powershell
Get-Command rclone.exe -All | Select-Object -ExpandProperty Source
$RcloneTarget = (Get-Command rclone.exe -CommandType Application -ErrorAction Stop).Source
if (-not (Test-Path "$RcloneTarget.official")) { Copy-Item $RcloneTarget "$RcloneTarget.official" }
Copy-Item .\rclone.exe $RcloneTarget -Force
& $RcloneTarget version
```

覆盖前应停止正在运行的 rclone、mount、serve 和 RC 进程。Homebrew、apt、winget、Chocolatey 或 Scoop 后续升级可能再次覆盖该文件；详细说明见[平台兼容性](docs/compatibility.md)。

## 配置

运行 `rclone config`，新建 remote 并选择 `123pan`。输入 123 网盘手机号或邮箱、账号密码，其他选项通常保持默认值。配置完成后即可使用 `remote:path` 形式访问网盘。

密码由 rclone obscure 后保存在配置文件中；obscure 不等同于加密保险箱，应限制配置文件的访问权限。

### 初始化交互示例

以下示例创建名为 `my123` 的 remote。账号为虚构值，存储类型列表中无关条目已省略。

```text
No remotes found, make a new one?
n) New remote
s) Set configuration password
q) Quit config
n/s/q> n

Enter name for new remote.
name> my123

Option Storage.
Type of storage to configure.
Choose a number from below, or type in your own value.
 1 / 123 网盘个人账号（实验性） -
   \ (123pan)
[省略其他存储类型]
Storage> 123pan

Option user.
123 网盘个人账号使用的手机号或邮箱。
Enter a value.
user> user@example.com

Option pass.
123 网盘账号密码。
Choose an alternative below.
y) Yes, type in my own password
g) Generate random password
y/g> y
Enter the password:
password:
Confirm the password:
password:

Edit advanced config?
y) Yes
n) No (default)
y/n> n

Configuration complete.
Options:
- type: 123pan
- user: user@example.com
- pass: *** ENCRYPTED ***
Keep this "my123" remote?
y) Yes this is OK (default)
e) Edit this remote
d) Delete this remote
y/e/d> y

Current remotes:

Name                 Type
====                 ====
my123                123pan

e) Edit existing remote
n) New remote
d) Delete remote
r) Rename remote
c) Copy remote
s) Set configuration password
q) Quit config
e/n/d/r/c/s/q> q
```

## 使用

以下示例以 `my123` 为 remote；完整参数与命令说明见 [rclone 文档](https://rclone.org/docs/) 和 [命令索引](https://rclone.org/commands/)。

```console
rclone lsf my123:                                      # 列出网盘根目录
rclone copy ./data my123:backup --progress             # 上传本地目录
rclone sync ./data my123:backup --checksum --dry-run   # 预演同步
```

### 已知启动提示

当前版本启动时会输出 `ERROR internal error: no overview data found for "123pan"`。这是 rclone 无法从自身的嵌入式文档中找到树外 `123pan` backend 说明而产生的已知诊断，与账号、配置或 123 网盘 API 无关；backend 注册和文件操作不受影响，命令会继续正常执行。技术原因见[平台兼容性](docs/compatibility.md)。

## 约束与安全

- 仅支持个人账号的手机号或邮箱加密码登录，不支持短信、扫码、微信或自动验证码处理。
- 不包含 123 开放平台、123Link、123Share、公开链接和匿名分享能力。
- 123 网盘个人盘不能可靠写入 rclone 修改时间；正确性敏感的同步和 bisync 应启用内容校验。
- 服务端复制仅适用于同一账号；其他场景由 rclone core 回退为读取后上传。
- 删除只进入回收站；永久删除和清空回收站不在本项目范围内。
- 输入源缺少大小或 MD5 时可能缓存到内存或 rclone `--temp-dir`，以获得可重放且可校验的数据源。
- 写操作失败并返回 staging、backup 或对象 ID 时，应停止盲目重试，并按 [恢复说明](docs/recovery.md)核验对象。
- 不要使用 rclone 官方 `selfupdate`，它会把定制二进制替换为不含此 backend 的官方版本。

更多协议限制、安全边界和离线任务用法见 [后端文档](docs/123pan.md) 与 [安全说明](docs/security.md)。

## 从源码构建

构建需要 Git、Make 和项目要求的 Go 工具链：

```console
git clone https://github.com/Ljzd-PRO/rclone-123pan.git
cd rclone-123pan
make build
```

生成的程序位于 `bin/rclone`。

## 文档

- [后端配置与协议行为](docs/123pan.md)
- [rclone 能力清单](docs/capabilities.md)
- [平台兼容性](docs/compatibility.md)
- [安全说明](docs/security.md)与[恢复说明](docs/recovery.md)
- [测试说明](docs/testing.md)与[真实账号测试记录](docs/live-testing.md)
- [来源说明](PROVENANCE.md)、[来源固定](SOURCE_PINS.md)与[许可说明](LICENSING.md)

## 许可证

本项目采用 [MIT License](LICENSE)，作者为 [Ljzd-PRO](https://github.com/Ljzd-PRO)。第三方组件及 clean-room 边界见[许可说明](LICENSING.md)。
