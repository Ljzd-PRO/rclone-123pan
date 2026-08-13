# 源码版本固定

| 项目 | Tag | Commit | 用途 |
| --- | --- | --- | --- |
| rclone | `v1.75.0` | `9ee9d0a0cafd5e5fe3b271d2280b090ab6e64048` | 构建及后端 API 基线 |
| OpenList | `v4.2.5` | `cc87e88f038a5a27c8782afc7b66a3c1a3cdcb77` | `drivers/123` 协议行为参考 |

`tools/fetch-references.sh` 会把上述精确版本下载到被 Git 忽略的 `.references/` 目录并校验其 commit。开发过程中不得在未审查的情况下改用任一项目的 main 分支。
