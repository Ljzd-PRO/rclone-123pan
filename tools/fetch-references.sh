#!/bin/sh
set -eu

root_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
reference_dir="$root_dir/.references"
rclone_tag=$("$root_dir/tools/version.sh" rclone)
rclone_commit=9ee9d0a0cafd5e5fe3b271d2280b090ab6e64048
openlist_tag=v4.2.5
openlist_commit=cc87e88f038a5a27c8782afc7b66a3c1a3cdcb77

fetch_reference() {
	name=$1
	url=$2
	tag=$3
	want_commit=$4
	destination="$reference_dir/$name"

	if [ ! -d "$destination/.git" ]; then
		git clone --depth 1 --branch "$tag" "$url" "$destination"
	fi

	actual_commit=$(git -C "$destination" rev-parse HEAD)
	if [ "$actual_commit" != "$want_commit" ]; then
		echo "$name: expected $want_commit, got $actual_commit" >&2
		exit 1
	fi
}

mkdir -p "$reference_dir"
fetch_reference rclone https://github.com/rclone/rclone.git "$rclone_tag" "$rclone_commit"
fetch_reference openlist https://github.com/OpenListTeam/OpenList.git "$openlist_tag" "$openlist_commit"

echo "Reference sources verified in $reference_dir"
