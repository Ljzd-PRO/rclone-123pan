#!/bin/sh
set -eu

dist=${1:-dist}
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
product_version=$("$root/tools/version.sh" product)

for tool in dpkg-deb md5sum find grep head; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "缺少 Debian 包测试依赖：$tool" >&2
    exit 1
  fi
done

test_root=$(mktemp -d "${TMPDIR:-/tmp}/rclone-123pan-deb-test.XXXXXX")
cleanup() {
  rm -rf -- "$test_root"
}
trap cleanup EXIT HUP INT TERM

for arch in amd64 arm64; do
  matches=$(find "$dist" -maxdepth 1 -type f -name "rclone-123pan_${product_version}_linux_${arch}.deb" -print)
  count=$(printf '%s\n' "$matches" | grep -c . || true)
  if [ "$count" -ne 1 ]; then
    echo "${arch} Debian 包数量应为 1，实际为 $count" >&2
    exit 1
  fi
  package=$matches

  if [ "$(dpkg-deb --field "$package" Package)" != rclone-123pan ]; then
    echo "Debian 包名不是 rclone-123pan：$package" >&2
    exit 1
  fi
  if [ "$(dpkg-deb --field "$package" Architecture)" != "$arch" ]; then
    echo "Debian 架构不匹配：$package" >&2
    exit 1
  fi
  version=$(dpkg-deb --field "$package" Version)
  if [ "$version" != "$product_version" ]; then
    echo "Debian 版本不匹配：期望 $product_version，实际 $version" >&2
    exit 1
  fi

  extract="$test_root/$arch"
  mkdir -p "$extract/DEBIAN"
  dpkg-deb --extract "$package" "$extract"
  dpkg-deb --control "$package" "$extract/DEBIAN"

  test -x "$extract/usr/bin/rclone-123pan"
  test ! -e "$extract/usr/bin/rclone"
  test -s "$extract/usr/share/doc/rclone-123pan/README.md"
  test -s "$extract/usr/share/doc/rclone-123pan/LICENSE"
  test -s "$extract/usr/share/doc/rclone-123pan/LICENSING.md"
  test -s "$extract/usr/share/doc/rclone-123pan/SBOM.cdx.json"
  test -s "$extract/usr/share/doc/rclone-123pan/PROVENANCE.json"
  test -s "$extract/usr/share/man/man1/rclone-123pan.1"
  head -n 1 "$extract/usr/share/doc/rclone-123pan/LICENSE" | grep -Fx 'MIT License' >/dev/null
  grep -F 'https://github.com/Ljzd-PRO' "$extract/usr/share/doc/rclone-123pan/LICENSE" >/dev/null
  head -n 1 "$extract/usr/share/doc/rclone-123pan/BUILDINFO.txt" | grep -F '/usr/bin/rclone-123pan:' >/dev/null

  (
    cd "$extract"
    md5sum --check DEBIAN/md5sums >/dev/null
  )

  case "$(uname -m):$arch" in
    x86_64:amd64|amd64:amd64|aarch64:arm64|arm64:arm64)
      version_output=$("$extract/usr/bin/rclone-123pan" version 2>&1)
      printf '%s\n' "$version_output" | grep -F "rclone v${product_version}" >/dev/null
      ;;
  esac
done

echo "Debian 包结构、元数据、md5sums 与本机架构二进制验证通过"
