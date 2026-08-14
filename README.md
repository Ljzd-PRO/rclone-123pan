<div align="center">

# rclone-123pan

**在 Windows、macOS 和 Linux 上，用 rclone 管理 123 网盘个人盘**

上传 · 下载 · 同步 · MD5 校验 · 秒传 · 服务端复制 · 软删除

**当前阶段：实验性 Alpha** · **内置 rclone：v1.75.0** · **界面与文档：中文**

</div>

> [!WARNING]
> 本项目使用 123 网盘官方网页所调用、但未公开承诺兼容性的个人盘 API。接口可能随时变化，也可能触发账号风控。请先在新建的临时目录中用少量、可丢失的数据试用，不要把它作为重要数据的唯一副本。

## 这是什么？

`rclone-123pan` 是一个包含 123 网盘个人盘支持的定制版 rclone。它可以让你在命令行、脚本、NAS 或服务器上管理自己的 123 网盘文件。

普通用户**不需要安装 Go，也不需要自己编译程序**。下载与自己系统匹配的现成二进制，解压后即可使用。

它目前适合这些场景：

- 把电脑或 NAS 中的文件上传到 123 网盘；
- 把网盘目录下载到本地；
- 使用 MD5 检查两边文件是否一致；
- 定期同步照片、文档或备份目录；
- 在同一 123 网盘账号中快速复制、移动或重命名文件；
- 用 rclone 的 VFS、serve、crypt 等高级功能包装 123 网盘。

## 主要特性

| 功能 | 对普通用户意味着什么 |
| --- | --- |
| 上传与下载 | 支持普通文件、零字节文件、空目录和大文件分片上传。 |
| MD5 校验 | 可以使用 `md5sum`、`check` 和 `--checksum` 检查内容，而不只比较文件大小。 |
| 秒传 | 123 网盘已有相同 MD5 内容时，可能无需再次传输文件正文。 |
| 服务端复制 | 同一账号内复制文件时，尽量由 123 网盘服务器完成，不经过本机下载再上传。 |
| 安全覆盖 | 替换已有文件时先验证新文件，并保留可恢复的临时状态，降低覆盖失败导致数据丢失的风险。 |
| Range / Seek | 支持按范围读取，便于媒体读取、VFS 和部分下载场景。 |
| 软删除 | 删除只会移入 123 网盘回收站；本项目不提供永久删除或清空回收站。 |
| 离线任务 | 可以通过 `rclone backend` 添加、查询和删除 123 网盘离线下载任务。 |
| 多平台 | 提供 Windows amd64、macOS Intel/Apple 芯片、Linux amd64/arm64 构建。 |

详细能力和限制见 [rclone 能力清单](docs/capabilities.md)。

## 五分钟开始使用

### 第一步：下载现成程序

有两种下载方式：

1. **Release 版本（最方便）**：打开 [Releases 页面](https://github.com/Ljzd-PRO/rclone-123pan/releases)，展开最新版本的 Assets，下载与你的系统匹配的压缩包，同时下载 `SHA256SUMS`。

2. **自动构建版本（适合私有测试）**：如果 Releases 页面暂时没有可用版本，请打开 [自动构建工件](https://github.com/Ljzd-PRO/rclone-123pan/actions/workflows/internal-alpha.yml)，选择最新的绿色运行记录，在页面底部下载 Artifacts。解压外层 artifact 后，会看到五个平台压缩包和 `SHA256SUMS`。这类 artifact 目前只保留 7 天。

> [!NOTE]
> 这是私有仓库时，你必须先登录有访问权限的 GitHub 账号，才能下载 Release 或 Actions artifact。

根据设备选择文件：

| 你的设备 | 选择名称中包含 |
| --- | --- |
| 常见的 64 位 Windows 电脑 | `windows_amd64.zip` |
| Apple 芯片 Mac（M1、M2、M3、M4 等） | `darwin_arm64.tar.gz` |
| Intel 芯片 Mac | `darwin_amd64.tar.gz` |
| Intel / AMD 处理器的 Linux、NAS、服务器 | `linux_amd64.tar.gz` |
| ARM64 Linux、ARM NAS 或开发板 | `linux_arm64.tar.gz` |

当前不提供 32 位系统构建。

### 第二步：校验并解压

每次构建都会附带 `SHA256SUMS`。建议在运行程序前核对下载文件的 SHA-256：

Windows PowerShell：

```powershell
Get-FileHash .\rclone-123pan_*_windows_amd64.zip -Algorithm SHA256
```

macOS：

```console
shasum -a 256 rclone-123pan_*_darwin_*.tar.gz
```

Linux：

```console
sha256sum rclone-123pan_*_linux_*.tar.gz
```

计算结果应与 `SHA256SUMS` 中对应文件的值完全相同。然后使用系统自带的解压功能解压压缩包。

解压后的主要程序是：

- Windows：`rclone-123.exe`
- macOS / Linux：`rclone-123`

在程序所在目录打开终端，先确认它可以运行。

Windows PowerShell：

```powershell
.\rclone-123.exe version
```

macOS / Linux：

```console
chmod +x ./rclone-123
./rclone-123 version
```

> [!TIP]
> 下文统一使用命令名 `rclone-123`。如果你没有把程序目录加入系统的 `PATH`，Windows 请改用 `.\rclone-123.exe`，macOS / Linux 请改用 `./rclone-123`。

这些构建目前没有商业代码签名。如果 Windows SmartScreen 或 macOS Gatekeeper 阻止运行，请先确认文件来自本仓库且 SHA-256 正确，再决定是否在系统安全设置中允许；不信任文件时不要绕过系统保护。

### 第三步：连接你的 123 网盘账号

运行交互式配置：

```console
rclone-123 config
```

按提示完成以下操作：

1. 选择 `n`，新建一个 remote；
2. 给它起一个容易记住的英文名称，例如 `my123`；
3. 在存储类型列表中选择 `123pan`，描述为“123 网盘个人账号（实验性）”；
4. 输入你的 123 网盘手机号或邮箱；
5. 按提示输入账号密码；
6. 普通用户无需修改高级设置，保持默认值即可；
7. 确认保存配置。

#### 完整初始化示例

下面演示如何创建一个名为 `my123` 的 remote。示例账号 `user@example.com` 是虚构的，请换成你自己的 123 网盘手机号或邮箱。密码输入时终端不会显示字符，这是正常现象。

这段记录来自当前项目实际构建的 `rclone-123`。参照 rclone 官方各后端文档的写法，存储类型列表中与本项目无关的条目以“省略”标记代替；除此以外，从首次创建到保存并退出的交互均完整保留。存储类型序号可能随 rclone 版本变化，直接输入 `123pan` 最稳妥。

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

如果开头出现 `no overview data found for "123pan"`，这是当前固定版 rclone 对树外后端文档的已知提示，不代表配置失败，详见下方[常见问题](#为什么命令提示-no-overview-data-found-for-123pan)。首次运行时还可能提示配置文件尚不存在；完成上述保存后，程序会自动创建它。

配置中的密码会经过 rclone obscure 处理后保存，但 obscure **不是加密保险箱**。能够读取 rclone 配置文件的人仍可能恢复密码，请保护好该文件。以下命令可以显示配置文件位置：

```console
rclone-123 config file
```

本后端不会把账号密码发送给 OpenList 公共服务或其他第三方初始化服务。登录和控制请求直接访问 123 网盘官方服务。

### 第四步：确认连接成功

假设刚才把 remote 命名为 `my123`：

```console
rclone-123 about my123:
rclone-123 lsf my123:
```

`my123:` 末尾的英文冒号不能省略。如果第二条命令没有输出，也可能只是网盘根目录为空。

建议先创建一个只用于试验的小目录：

```console
rclone-123 mkdir my123:rclone-test
```

然后准备一个包含少量小文件的本地 `test-data` 文件夹，执行第一次上传和下载：

```console
rclone-123 copy ./test-data my123:rclone-test --progress
rclone-123 copy my123:rclone-test ./download-test --progress
```

确认内容无误后，再开始处理真实数据。

## 常用命令

下面示例继续使用 remote 名称 `my123`。路径中有空格时，请用英文双引号包住完整路径。

### 查看文件

```console
# 查看根目录中的文件和文件夹
rclone-123 lsf my123:

# 用树状形式查看某个目录
rclone-123 tree my123:Photos

# 查看容量使用情况
rclone-123 about my123:

# 查看文件 MD5
rclone-123 md5sum my123:Photos
```

### 上传文件

上传一个本地文件夹的内容：

```console
rclone-123 copy ./Photos my123:Backup/Photos --progress
```

上传单个文件并指定目标文件名：

```console
rclone-123 copyto ./report.pdf my123:Documents/report.pdf --progress
```

### 下载文件

```console
rclone-123 copy my123:Photos ./Downloaded-Photos --progress
```

下载单个文件：

```console
rclone-123 copyto my123:Documents/report.pdf ./report.pdf --progress
```

### 校验两边内容

123 网盘个人盘不能可靠地写入 rclone 所使用的修改时间。涉及备份正确性时，应使用 `--checksum`：

```console
rclone-123 check ./Photos my123:Backup/Photos --checksum
```

如果希望逐字节下载后校验，可以使用：

```console
rclone-123 check ./Photos my123:Backup/Photos --download
```

### 同步目录

`sync` 会让目标目录变得和来源一致，目标中多余的文件会被删除。请始终先运行预演：

```console
rclone-123 sync ./Photos my123:Backup/Photos --checksum --dry-run
```

仔细检查输出，确认没有误删后再去掉 `--dry-run`：

```console
rclone-123 sync ./Photos my123:Backup/Photos --checksum --progress
```

> [!CAUTION]
> 本后端的删除会移入 123 网盘回收站，但同步仍可能删除大量目标文件。第一次使用 `sync`、`move`、`delete` 或 `purge` 时，务必限定在新建的测试目录中。

### 在网盘内复制或移动

同一账号内复制单个文件：

```console
rclone-123 copyto my123:Documents/report.pdf my123:Archive/report-copy.pdf --progress
```

后端会优先使用 123 网盘服务端 Copy，不需要把文件下载到本机再上传。该功能仍处于 Alpha 验证阶段，请先用小文件和临时目录测试。

移动目录内容：

```console
rclone-123 move my123:Inbox my123:Archive --progress
```

### 删除文件或空目录

```console
# 删除一个文件：移入123网盘回收站
rclone-123 deletefile my123:Backup/old.zip

# 删除一个已经为空的目录
rclone-123 rmdir my123:Backup/empty-folder
```

需要恢复时，请使用 123 网盘官方客户端打开回收站。本项目不提供永久删除和清空回收站。

## 离线下载任务

> [!WARNING]
> 离线任务命令已经通过状态化测试，但尚未完成隔离真实账号闭环。请先用体积很小、可删除的任务验证当前账号是否兼容。

把一个 URL 添加到指定网盘目录：

```console
rclone-123 backend offline-add my123:Downloads "https://example.com/file.zip"
```

命令会返回 `task_id`。使用该 ID 查询状态或删除任务：

```console
rclone-123 backend offline-status my123: 123456
rclone-123 backend offline-delete my123: 123456
```

删除离线任务不等于永久删除已经下载完成的网盘文件。

## 使用前必须了解的限制

- **仅支持个人账号密码登录。** 不支持短信、微信、扫码或自动验证码处理。
- **不支持 123 开放平台、123Link 和 123Share。** 本项目只实现个人盘 remote 类型 `123pan`。
- **API 可能漂移。** 123 网盘网页更新后，某些操作可能暂时失效。
- **修改时间不可写。** 正确性敏感的同步和 bisync 应使用 `--checksum`。
- **不支持公开链接。** 没有 `PublicLink`、分享链接或匿名分享功能。
- **没有永久删除。** 删除仅进入回收站，也不能用本程序清空回收站。
- **挂载能力因平台而异。** macOS 当前核心构建包含 `nfsmount`；其他原生 mount 依赖 macFUSE、WinFsp 或 FUSE 等系统组件，详见[兼容性说明](docs/compatibility.md)。
- **可能使用临时磁盘。** 当输入源不能提供 MD5 或大小时，程序需要先缓存内容；默认最多在内存保留 10 MiB，更大的内容写入 rclone `--temp-dir`。
- **不要执行 `rclone-123 selfupdate`。** 官方 rclone 更新包不包含本后端，会把定制程序替换掉。升级时请下载本项目的新版本。

当前真实账号隔离测试覆盖了上传、下载、秒传、分页、Update、Move、DirMove、同步、bisync、VFS、serve、crypt 和 macOS nfsmount 等场景。服务端 Copy 已通过状态化测试和 rclone 契约测试，但仍需继续进行隔离真实账号复测。完整记录见[真实账号测试记录](docs/live-testing.md)。

## 常见问题

### 为什么命令提示 `no overview data found for "123pan"`？

这是 rclone v1.75.0 对树外后端文档的已知兼容性提示，不代表登录或文件操作失败。只要后面的实际命令正常完成，可以暂时忽略。详见[兼容性说明](docs/compatibility.md)。

### 登录失败或要求验证码怎么办？

本项目不会绕过验证码或风控。请先在 123 网盘官方网站或官方客户端完成登录、验证或密码修改，等待账号恢复正常后再重试。避免让同一账号同时从多个相距较远的出口 IP 高频访问。

### 为什么同步时建议加 `--checksum`？

123 网盘个人盘不能设置 rclone 需要的修改时间。不加 `--checksum` 时，某些命令可能主要依据文件大小判断是否相同；同尺寸但内容不同的文件可能被误判。使用 MD5 校验更可靠，但会增加检查时间。

### 删除的文件还能恢复吗？

可以尝试在 123 网盘官方客户端的回收站中恢复。本项目只执行软删除，不会永久清除文件。

### 覆盖或复制失败后出现 staging / backup ID 怎么办？

请停止自动重试，保留完整错误信息，不要按名称批量删除临时对象。按照[恢复与回滚说明](docs/recovery.md)使用精确 ID 检查和恢复。

### 如何升级？

下载新的 `rclone-123` / `rclone-123.exe`，保留旧二进制和配置作为回退，然后先运行只读列表与小文件测试。不要使用 rclone 官方 `selfupdate`。

## 高级配置

普通用户建议保持默认值。需要调整时运行 `rclone-123 config`，编辑已有 remote 并进入高级设置。

| 设置 | 默认值 | 什么时候需要修改 |
| --- | --- | --- |
| `root_folder_id` | `0` | 只允许 remote 访问某个已核验的网盘文件夹时。不了解文件夹数字 ID 就不要修改。 |
| `upload_concurrency` | `3` | 网络或内存较紧张时调低；允许范围为 1 至 10。 |
| `hash_memory_limit` | `10Mi` | 调整未知 MD5 输入在写入临时磁盘前可使用的内存上限。 |
| `api_min_interval` | `700ms` | 遇到限流时可适当增大，不建议为了速度调小。 |
| `verify_timeout` | `60s` | 服务器繁忙、写入完成后较久才可见时可适当增大。 |
| `platform`、`encoding` | 安全默认值 | 协议或文件名兼容性调试使用，普通用户不要修改。 |

## 从源码构建

只有开发者或没有匹配二进制的平台需要自行构建。需要 Git、Make 和 Go 1.25.0：

```console
git clone https://github.com/Ljzd-PRO/rclone-123pan.git
cd rclone-123pan
make build
./bin/rclone-123 version
```

Makefile 已启用 `noselfupdate`，生成的程序位于 `bin/rclone-123`。

## 更多文档

- [后端配置与协议行为](docs/123pan.md)
- [rclone 能力清单](docs/capabilities.md)
- [平台兼容性](docs/compatibility.md)
- [安全说明](docs/security.md)
- [恢复与回滚](docs/recovery.md)
- [测试说明](docs/testing.md)
- [真实账号测试记录](docs/live-testing.md)
- [来源固定与许可证边界](SOURCE_PINS.md)、[来源说明](PROVENANCE.md)、[许可说明](LICENSING.md)

项目固定基于 rclone `v1.75.0`。OpenList `v4.2.5` 的 `drivers/123` 只用于提取可验证的协议事实和测试向量；项目不直接复制其 AGPL 表达性代码，也不包含 `123_open`、`123_link` 或 `123_share`。
