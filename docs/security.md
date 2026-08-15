# 安全说明

123 网盘个人盘后端使用逆向分析所得的 Web API。凭据只能发送至 `login.123pan.com`；经过认证的控制请求只能发送至官方 Web 实测的 `api.123278.com`。下载请求绝不能把 API Authorization 请求头转发给存储主机。

密码、token、签名 URL、Authorization 请求头、Cookie 和预签名查询参数必须从错误、日志和 CI artifact 中移除或脱敏。

密码使用 rclone 的 obscure 形式存储，只在构建认证客户端时还原。token 属于敏感信息，在配置器和 CLI 中均隐藏；只有登录成功后才会持久化，并在断开连接时清除。

并发收到 401 响应时，以被拒绝的精确 token 值进行协调：一个调用方负责刷新，其他调用方使用新 token 重放。每个逻辑请求最多重放一次，因此第二次 401 不会形成登录循环。

删除操作仅表示把一个经过验证的对象 ID 移入回收站。永久删除和清空回收站不在项目范围内。

生产二进制不提供 API endpoint 覆盖选项，编译时固定使用 `login.123pan.com` 和 `api.123278.com/b/api`，Web 页面来源固定为 `yun.123pan.cn`。无套接字 mock 测试和跨仓库测试只有显式启用 `123pantest` 构建 tag 时，才可通过环境变量替换 endpoint。发布构建绝不能包含该 tag。

自动测试和构建 workflow 只授予 `contents: read`。Release workflow 也默认只读，只有在全部门禁通过后执行的发布 job 获得 `contents: write`，并使用 GitHub 自动生成、仅限当前仓库的 `GITHUB_TOKEN`。发布标签必须预先存在；workflow 不创建、移动或覆盖标签，也拒绝覆盖已有 Release。发布正文固定来自受版本控制的 `RELEASE_NOTES.md`。

一键安装脚本会以安装目标所需的权限运行，因此其网络目标固定为本项目公开 GitHub Releases 和 GitHub Releases API，不接受 endpoint 或仓库覆盖。脚本只接受严格的正式版本标签格式，只从归档中提取精确的 `rclone` 路径，并在覆盖前同时验证 Release 中的 SHA-256 和候选程序报告的版本。摘要可以发现传输损坏或资产错配，但不能替代对 GitHub 发布账号本身的信任；要求更强审计时应先独立下载并检查 `install.sh`、`SHA256SUMS`、SBOM 和 provenance，再执行安装。

serve、RC 和挂载测试默认只能绑定本机回环地址。若操作人员主动把 HTTP、DLNA、WebDAV、FTP、SFTP、S3 或 NFS 暴露到 LAN/公网，必须另行配置协议认证、TLS、主机防火墙和最小 remote root；这不属于后端默认安全边界。
