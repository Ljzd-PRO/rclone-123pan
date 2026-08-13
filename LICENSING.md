# Licensing status

The repository's final license is intentionally unresolved during internal
development. rclone is MIT licensed; OpenList is AGPL-3.0.

Until a provenance and license review is complete:

- do not publish source or binary releases;
- use OpenList only as a source of protocol facts and independently authored
  test vectors;
- treat any direct source adaptation as an AGPL compliance event.

The repository intentionally contains no vendored OpenList files. The helper
clones both references only into gitignored `.references/` paths and verifies
their commits. Before any public release, review the full diff for expressive
similarity, choose and add the repository license, document third-party
notices/SBOM output, and decide whether any finding requires releasing the
plugin under AGPL-3.0-compatible terms. Passing tests does not clear this gate.
