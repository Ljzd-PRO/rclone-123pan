#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
rclone_commit=9ee9d0a0cafd5e5fe3b271d2280b090ab6e64048
module=github.com/ljzd/rclone-123pan
go_cmd=${RCLONE_123_GO:-go}
checkout=$(mktemp -d "${TMPDIR:-/tmp}/rclone-123-contract.XXXXXX")
cleanup() {
  case "$checkout" in
    "${TMPDIR:-/tmp}"/rclone-123-contract.*) rm -rf -- "$checkout" ;;
    *) echo "拒绝清理意外路径：$checkout" >&2 ;;
  esac
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$checkout/rclone"
reference="$root/.references/rclone"
if [ -d "$reference/.git" ] && [ "$(git -C "$reference" rev-parse HEAD)" = "$rclone_commit" ]; then
  rmdir "$checkout/rclone"
  git clone --quiet --no-hardlinks "$reference" "$checkout/rclone"
  git -C "$checkout/rclone" checkout --quiet "$rclone_commit"
else
  git -C "$checkout/rclone" init --quiet
  git -C "$checkout/rclone" remote add origin https://github.com/rclone/rclone.git
  fetch_attempt=1
  until git -C "$checkout/rclone" fetch --quiet --depth=1 origin "$rclone_commit"; do
    if [ "$fetch_attempt" -ge 3 ]; then
      echo "固定 rclone commit 拉取连续失败三次" >&2
      exit 1
    fi
    fetch_attempt=$((fetch_attempt + 1))
    echo "固定 rclone commit 拉取中断，进行第 $fetch_attempt 次尝试" >&2
    sleep 2
  done
  git -C "$checkout/rclone" checkout --quiet FETCH_HEAD
fi
actual=$(git -C "$checkout/rclone" rev-parse HEAD)
if [ "$actual" != "$rclone_commit" ]; then
  echo "rclone pin mismatch: got $actual" >&2
  exit 1
fi

cd "$checkout/rclone"
"$go_cmd" mod edit -require="$module@v0.0.0"
"$go_cmd" mod edit -replace="$module=$root"
printf '%s\n' 'package all' 'import _ "github.com/ljzd/rclone-123pan/backend/pan123"' > backend/all/pan123_external.go

# 在固定上游版本中真实运行账号无关的 operations/sync/VFS/bisync 单元
# 契约；缓存严格位于本次临时 checkout，不写用户默认缓存目录。
mkdir -p "$checkout/cache"
RCLONE_CACHE_DIR="$checkout/cache" "$go_cmd" test ./fs/operations ./fs/sync ./vfs ./cmd/bisync

if [ "${RCLONE_123_RUN_LIVE:-0}" != "1" ]; then
  echo "上游账号无关契约已通过；未设置 RCLONE_123_RUN_LIVE=1，跳过 live test_all"
  exit 0
fi
if [ "${RCLONE_123_LIVE_ACK:-}" != "DEDICATED_EMPTY_ACCOUNT" ]; then
  echo "live test_all 要求 RCLONE_123_LIVE_ACK=DEDICATED_EMPTY_ACCOUNT" >&2
  exit 1
fi
if [ -z "${RCLONE_123_LIVE_ROOT_ID:-}" ] || [ "$RCLONE_123_LIVE_ROOT_ID" = "0" ]; then
  echo "live 契约测试要求固定且非零的 RCLONE_123_LIVE_ROOT_ID" >&2
  exit 1
fi
if [ -z "${RCLONE_123_LIVE_MANIFEST:-}" ]; then
  echo "live 契约测试要求 RCLONE_123_LIVE_MANIFEST" >&2
  exit 1
fi
"$go_cmd" run "$root/tools/livemanifest" \
  -file "$RCLONE_123_LIVE_MANIFEST" \
  -root-id "$RCLONE_123_LIVE_ROOT_ID" \
  -mode dedicated-contract

cp "$root/tools/test-all.yaml" "$checkout/test-all.yaml"
"$go_cmd" run ./fstest/test_all -config "$checkout/test-all.yaml" -backends 123pan -tests fs/operations,fs/sync,vfs,cmd/bisync -maxtries 1
