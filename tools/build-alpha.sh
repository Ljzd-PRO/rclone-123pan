#!/bin/sh
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

product_version=$(./tools/version.sh product)
release_version=$(./tools/version.sh release)
downstream_revision=$(./tools/version.sh revision)
rclone_version=$(./tools/version.sh rclone)
version_suffix=$(./tools/version.sh suffix)
rclone_commit=9ee9d0a0cafd5e5fe3b271d2280b090ab6e64048
source_commit=$(git rev-parse HEAD)
source_epoch=${SOURCE_DATE_EPOCH:-$(git show -s --format=%ct HEAD)}
dist="$root/dist"
stage="$dist/stage"

case "$dist:$stage" in
  "$root/dist:$root/dist/stage") ;;
  *)
    echo "拒绝清理非预期构建路径: $dist / $stage" >&2
    exit 1
    ;;
esac

for tool in go git tar gzip zip find touch dpkg dpkg-deb md5sum sed awk grep; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "缺少构建依赖：$tool" >&2
    exit 1
  fi
done

rm -rf -- "$dist"
mkdir -p "$stage"

sbom="$dist/rclone-123pan_${product_version}.cdx.json"
go run ./tools/sbom \
  -output "$sbom" \
  -version "$product_version" \
  -commit "$source_commit" \
  -rclone-version "$rclone_version" \
  -rclone-commit "$rclone_commit"

go_version=$(go version | sed 's/"/\\"/g')
provenance="$dist/rclone-123pan_${product_version}.provenance.json"
printf '{\n  "product_version": "%s",\n  "release_version": "%s",\n  "downstream_revision": %s,\n  "rclone_version": "%s",\n  "rclone_commit": "%s",\n  "source_commit": "%s",\n  "source_date_epoch": %s,\n  "go_version": "%s",\n  "build_tags": ["noselfupdate"],\n  "cgo_enabled": false\n}\n' \
  "$product_version" "$release_version" "$downstream_revision" "$rclone_version" "$rclone_commit" \
  "$source_commit" "$source_epoch" "$go_version" > "$provenance"

targets='linux amd64
linux arm64
windows amd64
darwin amd64
darwin arm64'

echo "$targets" | while read -r goos goarch; do
  package="rclone-123pan_${product_version}_${goos}_${goarch}"
  package_dir="$stage/$package"
  mkdir -p "$package_dir"
  binary="$package_dir/rclone-123"
  binary_name=rclone-123
  if [ "$goos" = windows ]; then
    binary="$binary.exe"
    binary_name=rclone-123.exe
  fi
  echo "building $goos/$goarch"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
    -buildvcs=false -trimpath -tags noselfupdate \
    -ldflags "-s -w -X github.com/rclone/rclone/fs.VersionSuffix=${version_suffix}" \
    -o "$binary" ./cmd/rclone-123
  go version -m "$binary" | sed "1s|^[^:]*:|$binary_name:|" > "$package_dir/BUILDINFO.txt"
  cp README.md RELEASE_NOTES.md LICENSE LICENSING.md "$sbom" "$provenance" "$package_dir/"
  if [ "$goos" = linux ]; then
    deb="$dist/rclone-123pan_${product_version}_${goos}_${goarch}.deb"
    ./tools/build-deb.sh \
      "$binary" "$goarch" "$product_version" "$source_epoch" "$sbom" "$provenance" "$deb"
  fi
  find "$package_dir" -exec touch -h -d "@$source_epoch" {} +
  if [ "$goos" = windows ]; then
    (cd "$stage" && zip -X -q -r "$dist/$package.zip" "$package")
  else
    tar --sort=name --mtime="@$source_epoch" --owner=0 --group=0 --numeric-owner -C "$stage" -cf - "$package" | gzip -n > "$dist/$package.tar.gz"
  fi
done

rm -rf -- "$stage"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$dist" && sha256sum ./*.tar.gz ./*.zip ./*.deb ./*.json | sort -k2 > SHA256SUMS)
else
  (cd "$dist" && shasum -a 256 ./*.tar.gz ./*.zip ./*.deb ./*.json | sort -k2 > SHA256SUMS)
fi
echo "release artifacts written to $dist"
