# Stable media source identity proposal

Status: proposal, not a migration or release claim. The single Go binary,
SQLite store, embedded UI, and Work -> Editions presentation model stay.

## Immediate containment

Move and merge transactions reject different source and target library IDs.
New-title moves create their target in the same transaction as the edition
move. Same-library linking remains available. The confined media opener stays
unchanged: logical regrouping must never authorize a different filesystem
root implicitly.

This prevents new wrong-root moves. It does not repair previously moved,
renamed, or collapsed records. This containment fix does not migrate existing physical-source ownership or repair collapsed records.

## Proposed invariants

- Edition IDs remain stable across display-title, author, and logical-work edits
- Files retain physical source ownership independently of work.library_id
- Reading/listening progress and protocol IDs stay attached to edition IDs
- Source grouping and alternate encodings have explicit identity; title matches
  can suggest grouping but cannot determine physical identity
- Content generation identifies the bytes behind covers, thumbnails, and EPUB
  location indexes separately from display metadata
- Every open and processor input resolves through its retained source root

## Preparatory inventory available

`docs/source-inventory-command.md` documents the implemented standalone read-only
command and its synthetic fixtures. It accepts an explicit consistent disposable
snapshot, optionally stats explicitly mapped copied roots, and reports lexical
ambiguity/mixed-root/possible-collapse evidence without assigning ownership.
It is not a migration, a real-library result, or proof that existing identities
are unambiguous. The schema and repair decisions below remain open.

## Migration stages

1. Inventory existing editions, files, roots, titles, and progress references in
   a read-only dry run. Classify mixed-root editions and records already outside
   their apparent library root. Never infer permission from a path prefix alone.
2. Add explicit physical-source ownership, either an edition source-library FK
   for proven single-root editions or a source entity referenced by each file
   where mixed sources must be supported. Pick one representation after the
   inventory establishes real data shapes.
3. Backfill only unambiguous ownership. Preserve IDs and progress revisions.
   Ambiguous ownership, collapsed editions, and duplicate encodings require a
   reviewable repair mapping; do not split progress automatically.
4. Update scanner upserts, confined media opens, downloads, processor inputs,
   library deletion, backup/restore, and every protocol adapter together. The
   current work-root lookup cannot remain as an alternate fallback.
5. Retire title/author uniqueness as a source key only after all writers use the
   new stable identity and rescan idempotence is verified. Rename/move matching
   may use retained source identity and validated fingerprints; hashes alone
   cannot choose among intentionally duplicated copies.
6. Re-enable cross-library logical linking only after both originals remain
   streamable and deletion/permissions behavior is explicitly defined. Decide
   which library lists the grouped work without changing physical ownership.

## Acceptance tests and rollout gate

Use a backed-up disposable corpus before any production migration:

- EPUB and audiobook in disjoint roots survive linking with unchanged progress
- Same-title distinct editions and complete alternate encodings stay distinct
- Rename, retag, and rescan preserve edition/protocol IDs and revisions
- Mixed-root and already-corrupt rows are reported, not silently rewritten
- Deleting a logical work or one source library does not remove another source
- Old backups restore safely; a failed migration transaction leaves the prior
  database valid; the rollback/version policy is documented
- Core and compatibility clients open media through the correct confined root
- Derived assets invalidate on content changes without resetting user progress

A schema decision, repair policy, and migration rehearsal are prerequisites.
The containment patch deliberately does not claim this migration is complete.
