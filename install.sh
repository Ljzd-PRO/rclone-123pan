#!/usr/bin/env bash

set -Eeuo pipefail

readonly repository="Ljzd-PRO/rclone-123pan"
readonly api_base="https://api.github.com/repos/${repository}"
readonly release_base="https://github.com/${repository}/releases"

tmp_dir=""
pending_target=""

cleanup() {
  if [[ -n "$pending_target" ]]; then
    rm -f -- "$pending_target"
  fi
  if [[ -n "$tmp_dir" && -d "$tmp_dir" ]]; then
    rm -rf -- "$tmp_dir"
  fi
}
trap cleanup EXIT HUP INT TERM

fail() {
  printf '安装失败：%s\n' "$*" >&2
  exit 1
}

usage() {
  cat >&2 <<'EOF'
用法：
  sudo -v
  curl -fsSL https://github.com/Ljzd-PRO/rclone-123pan/releases/latest/download/install.sh | sudo bash

也可以把一个已发布标签作为第一个参数，以安装指定发行版：
  bash install.sh v<rclone版本>-123pan.<修订号>

可选环境变量：
  RCLONE_INSTALL_DIR        自定义安装目录，必须是绝对路径
EOF
  exit 2
}

need_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    fail "缺少依赖命令：$1"
  fi
}

download() {
  local accept=$1
  local url=$2
  local output=$3
  if ! curl "${curl_args[@]}" --header "Accept: ${accept}" --output "$output" "$url"; then
    fail "无法下载 ${url}；请检查网络连接以及 Release 是否存在"
  fi
}

download_json() {
  local url=$1
  local output=$2
  download 'application/vnd.github+json' "$url" "$output"
}

download_asset() {
  local url=$1
  local output=$2
  download 'application/octet-stream' "$url" "$output"
}

first_version_line() {
  "$1" version 2>/dev/null | sed -n '/^rclone v/{p;q;}' || true
}

case "${1:-}" in
  -h|--help)
    usage
    ;;
  ""|latest)
    requested_tag=latest
    ;;
  *)
    requested_tag=$1
    ;;
esac
if [[ $# -gt 1 ]]; then
  usage
fi

need_command curl
need_command awk
need_command chmod
need_command id
need_command install
need_command mkdir
need_command mktemp
need_command mv
need_command rm
need_command sed
need_command tar
need_command tr
need_command uname

case "$(uname -s)" in
  Linux)
    release_os=linux
    default_install_dir=/usr/local/bin
    ;;
  Darwin)
    release_os=darwin
    default_install_dir=/usr/local/bin
    ;;
  *)
    fail "当前系统没有可用的预构建发行版；一键安装仅支持 Linux 和 macOS"
    ;;
esac

case "$(uname -m)" in
  x86_64|amd64)
    release_arch=amd64
    ;;
  arm64|aarch64)
    release_arch=arm64
    ;;
  *)
    fail "当前 CPU 架构没有可用的预构建发行版：$(uname -m)"
    ;;
esac

install_dir=${RCLONE_INSTALL_DIR:-$default_install_dir}
case "$install_dir" in
  /*) ;;
  *) fail "RCLONE_INSTALL_DIR 必须是绝对路径：$install_dir" ;;
esac
case "/$install_dir/" in
  */../*|*/./*) fail "RCLONE_INSTALL_DIR 不能包含 . 或 .. 路径段：$install_dir" ;;
esac

curl_args=(
  --fail
  --silent
  --show-error
  --location
  --retry 3
  --connect-timeout 20
  --proto '=https'
  --tlsv1.2
  --header 'X-GitHub-Api-Version: 2022-11-28'
)

umask 022
tmp_dir=$(mktemp -d 2>/dev/null || mktemp -d -t 'rclone-123pan-install.XXXXXXXXXX')

if [[ "$requested_tag" == latest ]]; then
  release_json="$tmp_dir/release.json"
  download_json "${api_base}/releases/latest" "$release_json"
  release_tag=$(sed -n 's/^[[:space:]]*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' "$release_json" | sed -n '1p')
else
  release_tag=$requested_tag
fi

if ! [[ "$release_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+-123pan\.[1-9][0-9]*$ ]]; then
  fail "Release 标签格式无效：${release_tag:-<empty>}"
fi

release_version=${release_tag#v}
package_name="rclone_${release_version}_${release_os}_${release_arch}"
archive_name="${package_name}.tar.gz"
download_root="${release_base}/download/${release_tag}"
target="${install_dir}/rclone"

if [[ -x "$target" ]]; then
  installed_version=$(first_version_line "$target")
  if [[ "$installed_version" == "rclone ${release_tag}" ]]; then
    printf '%s 已安装在 %s，无需更新。\n' "$installed_version" "$target"
    exit 0
  fi
fi

checksums="$tmp_dir/SHA256SUMS"
archive="$tmp_dir/$archive_name"
download_asset "${download_root}/SHA256SUMS" "$checksums"
download_asset "${download_root}/${archive_name}" "$archive"

expected_checksum=$(awk -v name="$archive_name" '$2 == name || $2 == "./" name {print $1; exit}' "$checksums")
if ! [[ "$expected_checksum" =~ ^[0-9a-fA-F]{64}$ ]]; then
  fail "SHA256SUMS 中缺少 $archive_name 的有效摘要"
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual_checksum=$(sha256sum "$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual_checksum=$(shasum -a 256 "$archive" | awk '{print $1}')
else
  fail "缺少 SHA-256 校验工具：请安装 sha256sum 或 shasum"
fi
actual_checksum=$(printf '%s' "$actual_checksum" | tr '[:upper:]' '[:lower:]')
expected_checksum=$(printf '%s' "$expected_checksum" | tr '[:upper:]' '[:lower:]')
if [[ "$actual_checksum" != "$expected_checksum" ]]; then
  fail "$archive_name 的 SHA-256 校验失败"
fi

extract_dir="$tmp_dir/extract"
mkdir -p -- "$extract_dir"
if ! tar -xzf "$archive" -C "$extract_dir" "${package_name}/rclone"; then
  fail "发行版压缩包结构无效：$archive_name"
fi
candidate="$extract_dir/$package_name/rclone"
chmod 0755 "$candidate"
candidate_version=$(first_version_line "$candidate")
if [[ "$candidate_version" != "rclone ${release_tag}" ]]; then
  fail "发行版二进制版本不匹配：期望 rclone ${release_tag}，实际 ${candidate_version:-<empty>}"
fi

if ! mkdir -p -- "$install_dir"; then
  fail "无法创建安装目录 $install_dir；请使用 sudo 运行脚本或选择可写目录"
fi
if [[ ! -w "$install_dir" ]]; then
  fail "安装目录不可写：$install_dir；请使用 sudo 运行脚本"
fi

if [[ -e "$target" && ! -e "${target}.official" ]]; then
  previous_version=$(first_version_line "$target")
  if [[ "$previous_version" != rclone\ v*-123pan.* ]]; then
    if ! install -m 0755 "$target" "${target}.official"; then
      fail "无法备份原程序到 ${target}.official"
    fi
    printf '原程序已备份到 %s\n' "${target}.official"
  fi
fi

pending_target="${target}.new.$$"
install -m 0755 "$candidate" "$pending_target"
if [[ "$(id -u)" == 0 ]]; then
  chown 0:0 "$pending_target"
fi
mv -f -- "$pending_target" "$target"
pending_target=""

installed_version=$(first_version_line "$target")
if [[ "$installed_version" != "rclone ${release_tag}" ]]; then
  fail "安装后版本验证失败：${installed_version:-<empty>}"
fi

printf '%s 已成功安装到 %s。\n' "$installed_version" "$target"
printf '运行 "rclone config" 即可配置 123 网盘 remote。\n'
