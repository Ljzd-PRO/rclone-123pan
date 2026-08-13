# Compatibility

The module is pinned to rclone v1.75.0 and Go 1.25.0. The primary artifact is a
custom static binary, not a Go shared-object plugin.

The build matrix covers Linux amd64/arm64, Windows amd64, and macOS
amd64/arm64. Other targets are source-build only until explicitly tested.

rclone v1.75.0 expects every registered backend to have documentation embedded
in rclone's own `docs/data/backends` package. An out-of-tree backend cannot add
to that embedded filesystem, so this pinned version emits a harmless
`no overview data found for "123pan"` startup diagnostic. Backend registration
and operation are unaffected. Removing this diagnostic without a full fork
requires an upstream rclone change; it is tracked as a compatibility issue and
must not be hidden by replacing or patching the pinned rclone dependency.

The contract runner does not modify the checked-in dependency or maintain an
rclone fork. It creates a disposable exact checkout, adds one blank import and
a local `go.mod replace`, then deletes the checkout. This is also why upstream
operations/sync/VFS/bisync tests cannot simply be run from the out-of-tree
module without injection.
