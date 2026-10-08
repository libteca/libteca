# Reviewed physical source repair

File roots are stored separately from logical work grouping. Existing databases
retain their former authorization roots during upgrade. A moved or collapsed
legacy catalog must be reviewed before changing those roots or splitting its
physical files. A title match does not authorize a new root.

Stop the server and back up its data directory. Inspect a consistent disposable
copy with the source-inventory command and keep inventories and maps outside
this repository. Use the real configured library IDs, not arbitrary directory
strings. The selected root must safely open the file and reproduce its SHA-256.

Run `libteca source-repair --data <copy> --map <reviewed-map.json>` to validate a
version 1 map without applying it. Add `--apply` only after reviewing the result.
The command takes the same data-directory lock as the server. Store opening
performs ordinary schema upgrades even in validation mode.

The map contains `version: 1`, a nonempty `reason`, optional
`resetAffectedProgress`, and `files`. Each file entry requires `fileId`,
`editionId`, `generation`, `oldSourceLibraryId`, `newSourceLibraryId`, `path`,
`sizeBytes`, `mtimeNs`, and `sha256`. Generation is the current opaque value
returned by the edition timeline. A stale file, source, edition, generation,
size, timestamp or digest rejects the whole map. Repeated file IDs are rejected.

To move a file into an existing physical edition, provide `targetEditionId` and
its `targetGeneration`. To split a collapsed edition into a newly created target,
provide a relative `targetSourceKey` that does not already exist in the selected
library. Entries sharing that key share the new edition. Both operations require
`resetAffectedProgress: true`; affected progress resets and advances its epoch.
The command preserves file IDs and never guesses which old cumulative bookmark
belongs to the split. Logical work metadata remains separate from this repair.

Validate playback, rescans and progress on the copy before production rollout.
To reverse a repair, create a new reviewed map against current values; an old map
cannot be replayed. Restore the backup if the intended repair cannot be expressed
safely. A schema downgrade refuses to erase roots that differ from the logical
work library. Reconcile those roots before downgrading.
