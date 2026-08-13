# 源码版本固定

| 项目 | Tag | Commit | 用途 |
| --- | --- | --- | --- |
| rclone | `v1.75.0` | `9ee9d0a0cafd5e5fe3b271d2280b090ab6e64048` | 构建及后端 API 基线 |
| OpenList | `v4.2.5` | `cc87e88f038a5a27c8782afc7b66a3c1a3cdcb77` | `drivers/123` 协议行为参考 |
| Bao-qing/123pan | `master` 诊断快照 | `228b5017c1eabfc88ea63bd2852f371048f1c75a` | 仅用于解释 2026-08-14 真实服务漂移，不作为规范基线 |

`tools/fetch-references.sh` 会把 rclone 和 OpenList 的精确版本下载到被 Git 忽略的 `.references/` 目录并校验其 commit。开发过程中不得在未审查的情况下改用任一项目的 main 分支。社区客户端快照只在真实测试发现固定参考与当前服务不一致后用于诊断，未被 vendoring，也不替代 OpenList v4.2.5 基线。
