# Read-only source inventory

Status: implemented standalone preparatory command with synthetic regressions. It does not migrate, repair, scan, re-key or serve media, and does not close LT-P01.

## Input contract

Use a consistent, standalone, disposable database snapshot from the existing backup workflow, with no concurrent writers. A raw copy of a running SQLite database may be incomplete even when its WAL is omitted. This command cannot certify how a snapshot was obtained. Keep the original backup unchanged.

The command requires an explicit path, rejects a missing database, final-component symlink, non-regular file or any `-wal`, `-shm` or `-journal` sidecar, and opens SQLite with `mode=ro`, `immutable=1` and connection-local `query_only`. It never calls the application's store opener, migration runner, scanner, backup routine or processor. It hashes the snapshot before and after reading and fails if bytes changed. It does not lock a live server, create a snapshot, remove a sidecar or make a hot copy safe.

Run from the repository with the normal Go toolchain:

```sh
go run ./cmd/source-inventory --snapshot /absolute/disposable/snapshot.db > /absolute/separate-output/source-inventory.json
```

Choose a new output file outside the snapshot/media trees. The program writes only JSON to stdout and errors to stderr; shell redirection itself can overwrite an existing file, so do not redirect to a database, media file or backup.

Without a root map, the report reads database metadata only. Stored library/media paths are never opened. To inspect copied media, provide an explicit JSON array:

```json
[
  {"library_id": 1, "disposable_root": "/absolute/disposable/copied-audiobooks"},
  {"library_id": 2, "disposable_root": "/absolute/disposable/copied-books"}
]
```

```sh
go run ./cmd/source-inventory --snapshot /absolute/disposable/snapshot.db --root-map /absolute/separate-output/roots.json > /absolute/separate-output/source-inventory.json
```

Every mapped root must be absolute and correspond to an existing library ID; duplicate mappings and unknown JSON fields are rejected. Only metadata for candidate relative paths is inspected through `os.Root.Lstat`. Final-component symlinks are reported without following them; traversal outside a mapped root is unavailable. No media contents are read, hashed, probed, rewritten or deleted. Mapping a directory is an explicit operator choice; the command cannot prove it is disposable. This preparation was exercised only on generated data.

## Report contract, version 1

- Original and normalized library roots; stable library/work/edition/file IDs; display metadata; recorded file order, duration, byte size, mtime, stored hash, codec/container, chapters and embedded metadata
- Every lexical root candidate and normalized relative path. Overlapping/nested roots retain every candidate rather than choosing the longest prefix
- Optional copied-file evidence: not requested, regular file, size mismatch, missing, symlink not followed, non-regular or unavailable/outside mapped root
- Every edition-backed progress row, including original user/file IDs, position tuple, reading fields, timestamps, revisions and reset tombstones
- Orphan edition-progress references and unattached files are retained in separate arrays. Unattached files may be valid podcast downloads. Podcast episode ownership/progress, historical playback sessions, playlists and empty works without editions are outside this report
- Deterministic ordering and finding counts. Snapshot SHA-256 identifies the exact input; no run timestamp makes identical inputs produce identical output

Only current required columns are supported. An older or incomplete schema fails without an attempted migration. Report files can contain private paths, titles and progress; retain them locally and review/redact before any sharing. User names, password hashes, tokens and unrelated tables are not selected.

## Interpretation

| Finding | Meaning and next step |
| --- | --- |
| `ambiguous_lexical_root` | File path fits multiple registered roots. Do not choose ownership by prefix length |
| `no_lexical_root` / `invalid_file_path` | No usable registered absolute-root candidate; obtain historical/source evidence |
| `mixed_lexical_roots` | At least two files have different singleton lexical candidates. Supports per-file investigation; not proof of ownership |
| `apparent_root_mismatch` | At least one file does not fit its logical work's library. Existing stream lookup can be wrong |
| `ownership_review_required` | At least one file has zero/multiple candidates |
| `possible_collapsed_edition` | Multiple document/archive files or duplicate sequence numbers. Could be corruption or intentional grouping; never automatically split |
| `multiple_parent_directories_review` | Files span directories. Multi-disc layouts may be valid; inspect rather than repair |
| `progress_file_not_in_edition` / `progress_offset_outside_file` | Preserve row/revision and decide a specific repair from evidence |
| `empty_edition`, missing work/library | Preserve and review incomplete references |

Even a single candidate with a matching copied-file size is not proof of physical identity or permission. This command does not compare file content, recover historical scanner grouping or identify every alternate encoding. A same-directory collapsed audio group with unique sequences can remain undetected. The report never assigns a new source owner, selects a schema, changes progress or emits an executable repair mapping.

## Evidence and stopping gate

`internal/sourceinventory/inventory_test.go` creates real current-schema disposable SQLite fixtures via the application migrations during setup only. Tests compare complete database/media-tree bytes, entries, modes and modification times before/after repeated inventories and failures; attempted SQL row/schema writes on the inventory connection fail. Tests include URI-significant snapshot names, mixed roots, nested ambiguous roots, possible collapsed PDF groups, path-prefix near misses, invalid/missing paths, wrong-file progress references, reset revisions, denied maps, final symlinks, sidecar rejection, cancellation and old-schema refusal. Access-time updates by filesystem reads are not included in the no-mutation assertion.

The next input is one consistent disposable snapshot and, optionally, copied media roots mapped by library ID. The next decision comes after its local report: identify source ownership for genuinely ambiguous files and specify which existing edition/progress record retains each logical identity when a group must be split. Do not ask users to guess this mapping without a report. Schema choice, every writer/opener/protocol adapter, backup compatibility and rollback rehearsal remain separate prerequisites before a migration.
