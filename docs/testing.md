# Testing and release gates

Pull requests run unit/race tests and build the five primary targets without a
real 123Pan account.

The mock suite covers the 0/1/16 MiB boundaries, 10/11-part batching, the
10,000-part ceiling, zero-length PUT, retained-byte retry, 403 URL refresh,
short/changed sources, completion suppression, and both presigned and AWS SDK
v2 upload modes. Large boundary plans are tested without allocating their full
payload; streaming tests prove data order and content at representative
boundaries.

The Update state machine is exercised with faults before application and after
application with a lost response at both rename steps and backup trash. Tests
assert either the fully verified replacement or the exact original object is
the sole visible target; an incomplete rollback must return explicit recovery
IDs rather than performing name-pattern cleanup.

Mutation tests cover lost mkdir responses, two-pass empty Rmdir, root and
non-empty guards, stale object snapshots, idempotent Remove, combined
move+rename, directory subtree rejection, and dircache invalidation. All mock
deletions require the exact ID and current server name.

Offline command tests cover resolve/submit, destination root ID, stable JSON,
all documented status mappings, unknown states, positive/unique ID validation,
pagination consistency, and delete-then-query confirmation.

`internal/testserver` supplies a socket-free transport that records method,
path, body length, and MD5, and injects failures before state application,
after application with a lost response, or while blocked until context
cancellation. Individual backend tests layer an ID file tree and task model on
that transport.

`backend/pan123/fstests_test.go` calls `fstests.Run` with `Test123Pan:` but is
hard-skipped unless `RCLONE_123_RUN_LIVE=1`, a non-zero
`RCLONE_123_LIVE_ROOT_ID`, and immutable external
`RCLONE_123_LIVE_SENTINELS` are all present. `tools/test-rclone-contract.sh`
clones commit `9ee9d0a0cafd5e5fe3b271d2280b090ab6e64048`, adds a test-only blank import
to `backend/all`, and uses a local module replace. Account-free CI compiles the
fixed upstream `fs/operations`, `fs/sync`, `vfs`, and `cmd/bisync` packages;
with the same live guards it runs those suites via the checked-in custom
`test_all` YAML. No ignore list is configured.

Before any live run, operators must also verify two immutable sentinels outside
the test root, create only a fresh `rclone-test-[a-z0-9]{12}` directory, record
every created ID, and clean only those IDs. A stable release additionally
requires the planned 205-file/three-page, zero-byte, 16 MiB+1, rapid upload,
move/rename/delete, full-download MD5, mount smoke, and seven-day canary gates.

The manually triggered internal-alpha workflow uses `tools/build-alpha.sh` to
cross-build all five supported targets with Go 1.25.0, `CGO_ENABLED=0`,
`-trimpath`, `-buildvcs=false`, and mandatory `noselfupdate`. It normalizes
archive ownership and timestamps to the source commit, strips gzip/zip
metadata, emits one deterministic CycloneDX 1.6 module SBOM and provenance
record, and verifies `SHA256SUMS`. Artifacts remain private and expire after
seven days; the workflow has no release or package-write permission.

Stable release is blocked until a dedicated empty test account and isolated
non-zero root are available. Live tests must preserve external sentinel files,
operate only in randomly named `rclone-test-[a-z0-9]{12}` directories, and
clean up only IDs recorded by the current run.
