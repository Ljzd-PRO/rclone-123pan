# Source pins

| Project | Tag | Commit | Role |
| --- | --- | --- | --- |
| rclone | `v1.75.0` | `9ee9d0a0cafd5e5fe3b271d2280b090ab6e64048` | Build and backend API baseline |
| OpenList | `v4.2.5` | `cc87e88f038a5a27c8782afc7b66a3c1a3cdcb77` | `drivers/123` protocol behavior reference |

`tools/fetch-references.sh` downloads and verifies these exact revisions into
the gitignored `.references/` directory. Development must not silently move to
either project's main branch.

