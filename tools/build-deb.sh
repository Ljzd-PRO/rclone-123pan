#!/bin/sh
set -eu

if [ "$#" -ne 7 ]; then
  echo "用法：$0 BINARY GOARCH VERSION SOURCE_EPOCH SBOM PROVENANCE OUTPUT" >&2
  exit 2
fi

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
binary=$1
goarch=$2
version=$3
source_epoch=$4
sbom=$5
provenance=$6
output=$7

for file in "$binary" "$sbom" "$provenance" "$root/LICENSE" "$root/LICENSING.md" "$root/packaging/deb/control.in" "$root/packaging/deb/rclone.1"; do
  if [ ! -f "$file" ]; then
    echo "Debian 打包输入不存在：$file" >&2
    exit 1
  fi
done

case "$goarch" in
  amd64) deb_arch=amd64 ;;
  arm64) deb_arch=arm64 ;;
  *)
    echo "暂不生成该架构的 Debian 包：$goarch" >&2
    exit 1
    ;;
esac

case "$version" in
  [0-9]*) ;;
  *)
    echo "Debian 包版本必须以数字开头：$version" >&2
    exit 1
    ;;
esac
case "$version" in
  *[!0-9A-Za-z._+-]*)
    echo "Debian 包版本包含非法字符：$version" >&2
    exit 1
    ;;
esac
case "$source_epoch" in
  ''|*[!0-9]*)
    echo "SOURCE_EPOCH 必须为非负整数：$source_epoch" >&2
    exit 1
    ;;
esac
case "$output" in
  *.deb) ;;
  *)
    echo "Debian 包输出必须以 .deb 结尾：$output" >&2
    exit 1
    ;;
esac

for tool in go dpkg dpkg-deb md5sum install find sort sed du awk touch; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "缺少 Debian 打包依赖：$tool" >&2
    exit 1
  fi
done

if ! dpkg --validate-version "$version" >/dev/null 2>&1; then
  echo "Debian 版本无效：$version" >&2
  exit 1
fi

stage=$(mktemp -d "${TMPDIR:-/tmp}/rclone-123pan-deb.XXXXXX")
cleanup() {
  rm -rf -- "$stage"
}
trap cleanup EXIT HUP INT TERM

package_root="$stage/root"
doc_dir="$package_root/usr/share/doc/rclone"
guide_dir="$doc_dir/guides"
man_dir="$package_root/usr/share/man/man1"
mkdir -p "$package_root/DEBIAN" "$package_root/usr/bin" "$guide_dir" "$man_dir"

install -m 0755 "$binary" "$package_root/usr/bin/rclone"
install -m 0644 "$root/README.md" "$doc_dir/README.md"
install -m 0644 "$root/RELEASE_NOTES.md" "$doc_dir/RELEASE_NOTES.md"
install -m 0644 "$root/LICENSE" "$doc_dir/LICENSE"
install -m 0644 "$root/LICENSING.md" "$doc_dir/LICENSING.md"
install -m 0644 "$root/docs/123pan.md" "$guide_dir/123pan.md"
install -m 0644 "$root/docs/capabilities.md" "$guide_dir/capabilities.md"
install -m 0644 "$root/docs/compatibility.md" "$guide_dir/compatibility.md"
install -m 0644 "$root/docs/recovery.md" "$guide_dir/recovery.md"
install -m 0644 "$root/docs/security.md" "$guide_dir/security.md"
install -m 0644 "$root/packaging/deb/rclone.1" "$man_dir/rclone.1"
install -m 0644 "$sbom" "$doc_dir/SBOM.cdx.json"
install -m 0644 "$provenance" "$doc_dir/PROVENANCE.json"
go version -m "$binary" | sed '1s|^[^:]*:|/usr/bin/rclone:|' > "$doc_dir/BUILDINFO.txt"
chmod 0644 "$doc_dir/BUILDINFO.txt"

installed_size=$(du -sk "$package_root/usr" | awk '{print $1}')
sed \
  -e "s/@VERSION@/$version/g" \
  -e "s/@ARCH@/$deb_arch/g" \
  -e "s/@INSTALLED_SIZE@/$installed_size/g" \
  "$root/packaging/deb/control.in" > "$package_root/DEBIAN/control"
chmod 0644 "$package_root/DEBIAN/control"

(
  cd "$package_root"
  find usr -type f -print | LC_ALL=C sort | while IFS= read -r file; do
    md5sum "$file"
  done
) > "$package_root/DEBIAN/md5sums"
chmod 0644 "$package_root/DEBIAN/md5sums"

find "$package_root" -exec touch -h -d "@$source_epoch" {} +
mkdir -p "$(dirname -- "$output")"
export SOURCE_DATE_EPOCH="$source_epoch"
dpkg-deb --root-owner-group --uniform-compression --threads-max=1 -Zxz -z9 --build "$package_root" "$output"

echo "已生成 Debian 包：$output"
