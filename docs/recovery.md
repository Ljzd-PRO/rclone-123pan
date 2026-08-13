# Recovery and rollback

Every destructive operation identifies objects by positive server ID and then
rechecks parent, name, and type from a complete listing. Do not recover by
globally deleting names beginning with `rclone-123pan-`; another process or an
older run may own them.

## Interrupted replacement

An Update creates names shaped like:

```text
rclone-123pan-stage-<128-bit hex ID>
rclone-123pan-backup-<128-bit hex ID>
```

The intended states are:

1. target ID is the old file; staging ID contains the verified replacement;
2. old ID has the backup name; staging ID still has its staging name;
3. old ID has the backup name; staging ID has the target name;
4. staging ID is the verified target; the old ID is in the recycle bin.

On an error, the backend reverses only operations whose exact IDs it recorded.
If reversal cannot be proved, `RecoveryError` prints the staging and backup
IDs. Stop automated retries, list the parent without filters, and record each
ID/name/size/MD5. Prefer restoring the known backup ID to the target name; do
not trash either candidate until a full download verifies the desired content.
In particular, when backup deletion is ambiguous and that backup ID is no
longer visible, the backend keeps the already verified replacement at the
target name instead of risking an empty target during rollback.

## Ambiguous completion

Upload complete is never blindly replayed after a lost response. The backend
polls for the returned file ID and exact name/parent/size/MD5. If it reports an
ambiguous completion, keep the ID from the error and inspect it after the API
stabilizes. A missing visible object is not proof that no temporary S3 parts
exist, and this backend does not guess an abort endpoint.

## Recycle bin

Remove, Rmdir, replacement cleanup, and core Purge move objects into 123Pan's
recycle bin. This project has no permanent-delete or empty-trash command.
Recovery from the recycle bin must use an official 123Pan client until a
documented and independently tested restore API is implemented.

## Upgrade and rollback

The plugin and rclone pin form one release unit. Before upgrading, keep the old
`rclone-123` binary, config, SHA-256 checksum, SBOM, and provenance. Run read-only
list/MD5/download checks first, then a disposable write directory. To roll back,
restore the previous custom binary; never run `rclone selfupdate`, which would
replace it with a binary that has no `123pan` backend.
