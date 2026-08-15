#!/bin/sh
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
version_tool="$root/tools/version.sh"
expected_rclone=$(awk '$1 == "github.com/rclone/rclone" { print $2; exit }' "$root/go.mod")
expected_base=${expected_rclone#v}
expected_revision=$(sed -n '1p' "$root/REVISION")
expected_release="${expected_base}-123pan.${expected_revision}"

assert_equal() {
  expected=$1
  actual=$2
  if [ "$actual" != "$expected" ]; then
    echo "版本断言失败：期望 $expected，实际 $actual" >&2
    exit 1
  fi
}

assert_equal "$expected_base" "$("$version_tool" base)"
assert_equal "$expected_rclone" "$("$version_tool" rclone)"
assert_equal "$expected_revision" "$("$version_tool" revision)"
assert_equal "$expected_release" "$("$version_tool" release)"
assert_equal "$expected_release" "$("$version_tool" product)"
assert_equal "123pan.${expected_revision}" "$("$version_tool" suffix)"
assert_equal "v${expected_release}" "$("$version_tool" tag)"

assert_equal "${expected_release}+ci.42.a1b2c3" "$(BUILD_METADATA=ci.42.a1b2c3 "$version_tool" product)"
assert_equal "123pan.${expected_revision}+ci.42.a1b2c3" "$(BUILD_METADATA=ci.42.a1b2c3 "$version_tool" suffix)"
assert_equal "v${expected_release}" "$(BUILD_METADATA=ci.42.a1b2c3 "$version_tool" tag)"

for invalid in '.ci' 'ci.' 'ci..1' 'ci_1' 'ci+1' 'ci/1'; do
  if BUILD_METADATA=$invalid "$version_tool" product >/dev/null 2>&1; then
    echo "非法 BUILD_METADATA 未被拒绝：$invalid" >&2
    exit 1
  fi
done

if "$version_tool" unknown >/dev/null 2>&1; then
  echo "未知版本命令未被拒绝" >&2
  exit 1
fi

echo "版本派生与校验通过"
