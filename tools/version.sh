#!/bin/sh
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
rclone_version=$(awk '$1 == "github.com/rclone/rclone" { print $2; exit }' "$root/go.mod")
revision=$(sed -n '1p' "$root/REVISION")

if ! printf '%s\n' "$rclone_version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "go.mod 中的 rclone 版本无效：$rclone_version" >&2
  exit 1
fi
if ! printf '%s\n' "$revision" | grep -Eq '^[1-9][0-9]*$'; then
  echo "REVISION 必须是正整数：$revision" >&2
  exit 1
fi

base_version=${rclone_version#v}
release_version="${base_version}-123pan.${revision}"
build_metadata=${BUILD_METADATA:-}
if [ -n "$build_metadata" ]; then
  if ! printf '%s\n' "$build_metadata" | grep -Eq '^[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*$'; then
    echo "BUILD_METADATA 不是有效的语义版本构建元数据：$build_metadata" >&2
    exit 1
  fi
  product_version="${release_version}+${build_metadata}"
  version_suffix="123pan.${revision}+${build_metadata}"
else
  product_version=$release_version
  version_suffix="123pan.${revision}"
fi

case "${1:-}" in
  base)
    printf '%s\n' "$base_version"
    ;;
  rclone)
    printf '%s\n' "$rclone_version"
    ;;
  revision)
    printf '%s\n' "$revision"
    ;;
  release)
    printf '%s\n' "$release_version"
    ;;
  product)
    printf '%s\n' "$product_version"
    ;;
  suffix)
    printf '%s\n' "$version_suffix"
    ;;
  tag)
    printf 'v%s\n' "$release_version"
    ;;
  *)
    echo "用法：$0 {base|rclone|revision|release|product|suffix|tag}" >&2
    exit 2
    ;;
esac
