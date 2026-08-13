# Testing and release gates

Pull requests run unit/race tests and build the five primary targets without a
real 123Pan account.

The mock suite covers the 0/1/16 MiB boundaries, 10/11-part batching, the
10,000-part ceiling, zero-length PUT, retained-byte retry, 403 URL refresh,
short/changed sources, completion suppression, and both presigned and AWS SDK
v2 upload modes. Large boundary plans are tested without allocating their full
payload; streaming tests prove data order and content at representative
boundaries.

Stable release is blocked until a dedicated empty test account and isolated
non-zero root are available. Live tests must preserve external sentinel files,
operate only in randomly named `rclone-test-[a-z0-9]{12}` directories, and
clean up only IDs recorded by the current run.
