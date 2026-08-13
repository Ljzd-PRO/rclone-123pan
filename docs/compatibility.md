# Compatibility

The module is pinned to rclone v1.75.0 and Go 1.25.0. The primary artifact is a
custom static binary, not a Go shared-object plugin.

The build matrix covers Linux amd64/arm64, Windows amd64, and macOS
amd64/arm64. Other targets are source-build only until explicitly tested.

