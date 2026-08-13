# Provenance

The implementation is written against rclone's public backend interfaces.
OpenList v4.2.5 is used to identify observable protocol facts such as endpoint
paths, JSON field names, header requirements, pagination, and upload flows.

Do not copy OpenList function bodies, comments, tests, or other expressive
source. Record any future reference revision in `SOURCE_PINS.md` and review its
license before using it.

Protocol facts independently reimplemented from the fixed OpenList reference
include endpoint paths, envelope success codes, JSON field spellings, the
UTC+8 CRC32 URL signature, page size/order, download redirect shapes, MD5 rapid
upload, temporary-credential versus presigned upload selection, 16 MiB parts,
ten-URL batches, and offline status values. Safety state machines, strict
consistency rules, retry policy, rclone interfaces, tests, documentation, and
all source expression in this repository were authored independently.
