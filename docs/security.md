# 安全说明

123 网盘个人盘后端使用逆向分析所得的 Web API。凭据只能发送至 `login.123pan.com`；经过认证的控制请求只能发送至官方 Web 实测的 `api.123278.com`。下载请求绝不能把 API Authorization 请求头转发给存储主机。

密码、token、签名 URL、Authorization 请求头、Cookie 和预签名查询参数必须从错误、日志和 CI artifact 中移除或脱敏。

密码使用 rclone 的 obscure 形式存储，只在构建认证客户端时还原。token 属于敏感信息，在配置器和 CLI 中均隐藏；只有登录成功后才会持久化，并在断开连接时清除。

并发收到 401 响应时，以被拒绝的精确 token 值进行协调：一个调用方负责刷新，其他调用方使用新 token 重放。每个逻辑请求最多重放一次，因此第二次 401 不会形成登录循环。

删除操作仅表示把一个经过验证的对象 ID 移入回收站。永久删除和清空回收站不在项目范围内。

生产二进制不提供 API endpoint 覆盖选项，编译时固定使用 `login.123pan.com` 和 `api.123278.com/b/api`，Web 页面来源固定为 `yun.123pan.cn`。无套接字 mock 测试和跨仓库测试只有显式启用 `pan123test` 构建 tag 时，才可通过环境变量替换 endpoint。发布构建绝不能包含该 tag。

serve、RC 和挂载测试默认只能绑定本机回环地址。若操作人员主动把 HTTP、DLNA、WebDAV、FTP、SFTP、S3 或 NFS 暴露到 LAN/公网，必须另行配置协议认证、TLS、主机防火墙和最小 remote root；这不属于后端默认安全边界。
