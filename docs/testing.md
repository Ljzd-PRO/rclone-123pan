# Testing and release gates

Pull requests run unit/race tests and build the five primary targets without a
real 123Pan account.

Stable release is blocked until a dedicated empty test account and isolated
non-zero root are available. Live tests must preserve external sentinel files,
operate only in randomly named `rclone-test-[a-z0-9]{12}` directories, and
clean up only IDs recorded by the current run.

