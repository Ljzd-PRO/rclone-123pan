#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
rclone_commit=9ee9d0a0cafd5e5fe3b271d2280b090ab6e64048
module=github.com/ljzd/rclone-123pan
checkout=$(mktemp -d "${TMPDIR:-/tmp}/rclone-123-contract.XXXXXX")
trap 'rm -rf "$checkout"' EXIT HUP INT TERM

git clone --quiet https://github.com/rclone/rclone.git "$checkout/rclone"
git -C "$checkout/rclone" checkout --quiet "$rclone_commit"
actual=$(git -C "$checkout/rclone" rev-parse HEAD)
if [ "$actual" != "$rclone_commit" ]; then
  echo "rclone pin mismatch: got $actual" >&2
  exit 1
fi

cd "$checkout/rclone"
go mod edit -require="$module@v0.0.0"
go mod edit -replace="$module=$root"
printf '%s\n' 'package all' 'import _ "github.com/ljzd/rclone-123pan/backend/pan123"' > backend/all/pan123_external.go

# Prove that the fixed upstream operations/sync/VFS/bisync packages compile
# with the external backend registered. This is safe and account-free.
go test -run '^$' ./fs/operations ./fs/sync ./vfs ./cmd/bisync

if [ "${RCLONE_123_RUN_LIVE:-0}" != "1" ]; then
  echo "contract compile passed; live test_all skipped (RCLONE_123_RUN_LIVE is not 1)"
  exit 0
fi
if [ -z "${RCLONE_123_LIVE_ROOT_ID:-}" ] || [ "$RCLONE_123_LIVE_ROOT_ID" = "0" ]; then
  echo "live contract tests require a fixed non-zero RCLONE_123_LIVE_ROOT_ID" >&2
  exit 1
fi
if [ -z "${RCLONE_123_LIVE_SENTINELS:-}" ]; then
  echo "live contract tests require RCLONE_123_LIVE_SENTINELS" >&2
  exit 1
fi

cp "$root/tools/test-all.yaml" "$checkout/test-all.yaml"
go run ./fstest/test_all -config "$checkout/test-all.yaml" -backends 123pan -tests fs/operations,fs/sync,vfs,cmd/bisync -maxtries 1
