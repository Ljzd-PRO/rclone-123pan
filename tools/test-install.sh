#!/bin/sh
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
tmp_dir=$(mktemp -d 2>/dev/null || mktemp -d -t 'rclone-123pan-install-test.XXXXXXXXXX')
cleanup() {
  rm -rf -- "$tmp_dir"
}
trap cleanup EXIT HUP INT TERM

fail() {
  echo "安装脚本测试失败：$*" >&2
  exit 1
}

release_tag=$("$root/tools/version.sh" tag)
upstream_tag=$("$root/tools/version.sh" rclone)
release_version=${release_tag#v}
package_name="rclone_${release_version}_linux_amd64"
archive_name="${package_name}.tar.gz"
fixture_archive="$tmp_dir/archive.tar.gz"
fixtures="$tmp_dir/fixtures"
fake_bin="$tmp_dir/fake-bin"
package_dir="$tmp_dir/$package_name"
install_dir="$tmp_dir/install"
curl_log="$tmp_dir/curl.log"
mkdir -p "$fixtures" "$fake_bin" "$package_dir" "$install_dir"

# 以下单引号内容是要写入 fixture 的 shell 源码，不应在测试进程中展开。
# shellcheck disable=SC2016
printf '%s\n' \
  '#!/bin/sh' \
  'if [ "${1:-}" = version ]; then' \
  "  echo \"rclone $release_tag\"" \
  'fi' > "$package_dir/rclone"
chmod 0755 "$package_dir/rclone"
tar -czf "$fixture_archive" -C "$tmp_dir" "$package_name"

if command -v sha256sum >/dev/null 2>&1; then
  archive_checksum=$(sha256sum "$fixture_archive" | awk '{print $1}')
else
  archive_checksum=$(shasum -a 256 "$fixture_archive" | awk '{print $1}')
fi
printf '%s  ./%s\n' "$archive_checksum" "$archive_name" > "$fixtures/SHA256SUMS"
printf '%064d  ./%s\n' 0 "$archive_name" > "$fixtures/SHA256SUMS.bad"
# shellcheck disable=SC2016
printf '%s\n' \
  '#!/bin/sh' \
  'set -eu' \
  'case "${1:-}" in' \
  '  -s) echo "${FAKE_OS:-Linux}" ;;' \
  '  -m) echo "${FAKE_ARCH:-x86_64}" ;;' \
  '  *) echo "${FAKE_OS:-Linux}" ;;' \
  'esac' > "$fake_bin/uname"
chmod 0755 "$fake_bin/uname"

# shellcheck disable=SC2016
printf '%s\n' \
  '#!/bin/sh' \
  'set -eu' \
  'output=' \
  'write_out=' \
  'url=' \
  'while [ "$#" -gt 0 ]; do' \
  '  case "$1" in' \
  '    --output) output=$2; shift 2 ;;' \
  '    --write-out) write_out=$2; shift 2 ;;' \
  '    https://*) url=$1; shift ;;' \
  '    *) shift ;;' \
  '  esac' \
  'done' \
  'test -n "$url"' \
  'printf "%s\n" "$url" >> "$CURL_LOG"' \
  'case "$url" in' \
  '  */releases/latest)' \
  '    test "$write_out" = "%{url_effective}"' \
  '    printf "https://github.com/Ljzd-PRO/rclone-123pan/releases/tag/%s" "$RELEASE_TAG"' \
  '    ;;' \
  '  */SHA256SUMS)' \
  '    test -n "$output"' \
  '    if [ "${BAD_CHECKSUM:-0}" = 1 ]; then' \
  '      cp "$FIXTURE_DIR/SHA256SUMS.bad" "$output"' \
  '    else' \
  '      cp "$FIXTURE_DIR/SHA256SUMS" "$output"' \
  '    fi' \
  '    ;;' \
  '  *.tar.gz) test -n "$output"; cp "$FIXTURE_ARCHIVE" "$output" ;;' \
  '  *) echo "unexpected URL: $url" >&2; exit 1 ;;' \
  'esac' > "$fake_bin/curl"
chmod 0755 "$fake_bin/curl"

# shellcheck disable=SC2016
printf '%s\n' \
  '#!/bin/sh' \
  'if [ "${1:-}" = version ]; then' \
  "  echo \"rclone $upstream_tag\"" \
  'fi' > "$install_dir/rclone"
chmod 0755 "$install_dir/rclone"

PATH="$fake_bin:$PATH" \
  FIXTURE_DIR="$fixtures" \
  FIXTURE_ARCHIVE="$fixture_archive" \
  CURL_LOG="$curl_log" \
  RELEASE_TAG="$release_tag" \
  RCLONE_INSTALL_DIR="$install_dir" \
  "$root/install.sh" >/dev/null

installed_version=$("$install_dir/rclone" version)
[ "$installed_version" = "rclone $release_tag" ] || fail "安装版本错误：$installed_version"
[ -x "$install_dir/rclone.official" ] || fail "未备份原 rclone"
backup_version=$("$install_dir/rclone.official" version)
[ "$backup_version" = "rclone $upstream_tag" ] || fail "原程序备份内容错误：$backup_version"
[ "$(wc -l < "$curl_log" | tr -d ' ')" = 3 ] || fail "首次安装的下载次数不是 3"

PATH="$fake_bin:$PATH" \
  FIXTURE_DIR="$fixtures" \
  FIXTURE_ARCHIVE="$fixture_archive" \
  CURL_LOG="$curl_log" \
  RELEASE_TAG="$release_tag" \
  RCLONE_INSTALL_DIR="$install_dir" \
  "$root/install.sh" >/dev/null
[ "$(wc -l < "$curl_log" | tr -d ' ')" = 4 ] || fail "相同版本仍重复下载发行版工件"

PATH="$fake_bin:$PATH" \
  FIXTURE_DIR="$fixtures" \
  FIXTURE_ARCHIVE="$fixture_archive" \
  CURL_LOG="$curl_log" \
  RELEASE_TAG="$release_tag" \
  RCLONE_INSTALL_DIR="$install_dir" \
  "$root/install.sh" "$release_tag" >/dev/null
[ "$(wc -l < "$curl_log" | tr -d ' ')" = 4 ] || fail "指定已安装标签仍发生网络请求"

bad_install_dir="$tmp_dir/bad-install"
mkdir -p "$bad_install_dir"
cp "$install_dir/rclone.official" "$bad_install_dir/rclone"
if PATH="$fake_bin:$PATH" \
  FIXTURE_DIR="$fixtures" \
  FIXTURE_ARCHIVE="$fixture_archive" \
  CURL_LOG="$curl_log" \
  RELEASE_TAG="$release_tag" \
  BAD_CHECKSUM=1 \
  RCLONE_INSTALL_DIR="$bad_install_dir" \
  "$root/install.sh" "$release_tag" >"$tmp_dir/bad-checksum.log" 2>&1; then
  fail "损坏的 SHA256SUMS 被接受"
fi
grep -F 'SHA-256 校验失败' "$tmp_dir/bad-checksum.log" >/dev/null || fail "摘要失败错误不明确"
[ "$("$bad_install_dir/rclone" version)" = "rclone $upstream_tag" ] || fail "摘要失败后覆盖了原程序"

before_unsupported=$(wc -l < "$curl_log" | tr -d ' ')
if PATH="$fake_bin:$PATH" \
  FIXTURE_DIR="$fixtures" \
  FIXTURE_ARCHIVE="$fixture_archive" \
  CURL_LOG="$curl_log" \
  RELEASE_TAG="$release_tag" \
  FAKE_OS=FreeBSD \
  RCLONE_INSTALL_DIR="$tmp_dir/unsupported" \
  "$root/install.sh" "$release_tag" >"$tmp_dir/unsupported.log" 2>&1; then
  fail "不支持的系统未被拒绝"
fi
after_unsupported=$(wc -l < "$curl_log" | tr -d ' ')
[ "$before_unsupported" = "$after_unsupported" ] || fail "不支持的系统仍发起了下载"

if grep -Eq 'archive/refs|refs/heads|raw\.githubusercontent\.com|git clone' "$root/install.sh"; then
  fail "安装脚本引用了源码分支"
fi
if grep -F 'api.github.com' "$root/install.sh" >/dev/null || grep -F 'api.github.com' "$curl_log" >/dev/null; then
  fail "安装脚本依赖 GitHub API，可能受匿名 API 限流影响"
fi
if grep -Eq 'archive/refs|refs/heads|raw\.githubusercontent\.com' "$curl_log"; then
  fail "安装请求访问了源码分支"
fi

echo "一键安装脚本测试通过"
