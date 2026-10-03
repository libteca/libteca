# Remaining media consistency and resource contracts

Status: implementation design and acceptance plan. These are not implemented migrations, production approvals, or a claim of complete correctness. The architecture remains a single Go service, SQLite, and the embedded first-party web UI.

## What this checkpoint establishes

- ABS and Kavita destination changes commit as one transaction. A failed apply or commit returns no successful plan and preserves existing rows and revisions. Foreign database discovery remains read-only and precedes the destination transaction; it is not a consistent snapshot of a concurrently changing foreign server. Use a stopped source or a consistent copy for real imports.
- Primary and playlist audio serialize in-tab writes by the originating login and progress endpoint, check HTTP failures, use request deadlines, and retain failed completion for in-tab retry. An explicit restart invalidates older queued/retry work. These operations are still unconditional at the server and do not survive tab/process loss.
- Playlist audio uses ordered file IDs and durations from work detail. It restores an edition position to a file-relative offset, seeks across files, and completes an edition only after its final file. Video and HLS still need their own multipart contract.
- CBR listing/extraction follows caller cancellation, reaps the launched process, rejects failed partial output, and cleans its temporary output. Cancellation is not a disk quota, nor does a synthetic extractor establish compatibility with every installed unrar/unar version.

## 1. Stable physical source identity

The detailed proposal is in `source-identity-proposal.md`. The next safe implementation is a read-only inventory/report command against a disposable database and media tree. It should enumerate every edition's actual file ownership, library root, normalized relative path, stable IDs, file order, metadata, and progress references, then classify ambiguous/mixed-root/collapsed records without changing them.

The ownership representation cannot be chosen responsibly from title matching alone. Select an edition-level source-library FK only if the inventory proves the single-root invariant; otherwise use explicit per-file source references. Preserve public edition/file IDs, all progress revisions, and physical containment. Apply only a reviewed repair mapping in a transaction with a backup and rollback rehearsal. A title-only rescan must not silently create, split, or collapse physical identities.

Safe next step: add synthetic fixtures and a read-only inventory format. Needed before real repair: a backed-up disposable inventory and an approved mapping of genuinely ambiguous existing records. No production migration should be inferred from the local correctness-fix authorization.

## 2. Durable media progress and cross-device conflicts

The reader already has durable operations and revision-aware conflict handling; media cannot simply reuse its maximum-position merge because rewinds, explicit seeks, listen-again, and completion are intentional state changes.

Recommended contract:

1. Persist an operation before transmission in user-scoped browser storage. Each operation includes an operation ID, installation/session identity, edition or podcast episode ID, content/order generation, base revision, intent (`heartbeat`, `seek`, `restart`, `finish`, or `reset`), and the complete file-ID/file-offset/cumulative-position tuple. Never persist authentication tokens as queue keys or payloads.
2. Use the existing core edition revision/CAS endpoint for media too, after defining intent-aware conflict resolution. Add revision/tombstone semantics to podcast progress, which currently has no revision column. Keep legacy compatibility writes backward-compatible and documented as unconditional until adapters explicitly opt in.
3. A successful operation acknowledgement must identify the committed revision. Retain ambiguous-response operations until a read/reconciliation proves the intended result. If exact operation receipts are introduced, define retention and replay behavior before relying on them for idempotence.
4. A same-generation heartbeat may merge forward only within its playback session. An explicit seek/restart/reset can move backwards and must not be replaced by a stale high-water heartbeat or completion. Concurrent explicit choices on two devices should produce a visible choice or a specified server ordering, not an implicit maximum.
5. On content/order-generation mismatch, refetch the timeline. Rebase from a still-valid file ID and offset only when that identity is proven unchanged; otherwise require confirmation rather than silently saving a guessed cumulative position.
6. Fence storage and replay by authenticated owner and generation. Account changes stop pending delivery; pagehide requests are best effort, with durable replay as the recovery mechanism. Use one cooperating replay owner per origin with a safe fallback contract.

Acceptance: offline pause/seek/restart/finish; browser restart before and after an ambiguous response; two tabs and two devices; token/account replacement while a write is pending; reset tombstones; reorder while playback is open; deleted/replaced files; storage quota/denial; queued operations from older app versions. Real browser eviction, native playback, and cross-device acceptance require runtime access, not just jsdom.

Safe next step: write intent/state-transition fixtures and an explicit conflict-resolution table before adding storage or migrations. The current in-tab serialized queue remains useful containment and must not be described as durable or exactly-once across network timeouts.

## 3. Multipart video, HLS, subtitles and thumbnails

Core edition playback and thumbnail handlers currently choose the first file. A playlist audio fix does not make these surfaces multipart.

Recommended response contract: ordered file IDs, each file duration and cumulative offset, total duration, and an immutable content/order generation. Playback creation must accept a selected file ID and file-relative start offset, verify that the file belongs to the edition, and bind every HLS session/ticket to that file and generation. A seek crossing a boundary must stop the prior HLS session and create the next one without applying stale manifests/events.

Chapters retain cumulative edition positions plus file identity. Subtitles and thumbnails need file/generation-scoped cache keys; offsets must be explicit so a second file's cues or thumbnail grid are not interpreted as time zero for the whole edition. Resume uses the same file-relative and cumulative tuple as progress. Completion is emitted only at the final file. Direct-to-HLS fallback must retain the selected file and offset.

Acceptance: resume and seeks before/at/after every boundary; mixed direct/HLS codecs; differing file durations and subtitle tracks; thumbnail offsets; failed or cancelled replacement; detached events; order-generation change; final completion; all applicable compatibility adapters. Preserve the single-file response path while introducing a versioned or additive multipart shape.

Safe next step: implement a pure shared timeline resolver and HTTP contract fixtures, then add server selected-file support behind explicit additive fields. End-to-end acceptance needs a representative multipart video corpus and actual browser/HLS playback.

## 4. Resource budgets and cleanup

Existing byte caps are partial protection. Measure representative ordinary workloads before selecting stricter limits that might break large books or long videos.

- Browser archives: separately budget compressed download, ZIP directory/entry count, one extracted entry, total retained page bytes, decoded pixels, active decodes, and total cache. An image's compressed byte count does not bound its decoded memory.
- EPUB: bound dependency extraction and document/DOM work without breaking legitimate CFI restore, navigation, fonts, and resource loading. Validate a representative EPUB corpus before changing epub.js or its parser dependencies.
- CBR: enforce temporary-output and elapsed-work quotas while extraction runs, with deterministic cancellation/reaping and cleanup. A post-extraction walk is not an ongoing disk bound. Decide how to implement quotas portably for each supported extractor/platform.
- HLS/trickplay: track aggregate and per-session disk bytes, concurrent processes, retention, and seek/regeneration policy. Deleting old segments while a client may still request them needs an explicit rolling-window or restart contract.

Acceptance: cap-minus-one, exact-cap, cap-plus-one, high-compression and huge-pixel fixtures, malformed listings, cancellation at every publish boundary, low-disk behavior, concurrent jobs, repeated seek regeneration, process/server restart, and cleanup after failure. Record peak memory/disk/time and quality effects. Synthetic limits alone cannot establish normal long-title performance.

Safe next step: instrumentation and a disposable resource-measurement corpus; implement individually justified limits with isolated regressions. Existing dependency update PRs should be reviewed on their own tested contracts rather than duplicated or blindly upgraded as part of this patch.

## Acceptance and release boundaries

These designs do not authorize publication, deployment, production writes, or a focused security review excluded by the current platform restriction. The outstanding real-library week, native browser/PDF/EPUB/audio/video behavior, live protocol corpus, container runtime, hardware encoder, and ARM/macOS execution gates remain explicit in the issue register. Passing synthetic and component tests cannot close those gates.
