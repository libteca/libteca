# libteca deeper source-review pass (2026-10-06) — AUDIT-CHATGPT-14

Revision worked: `237633f` (main, pass C landed). This pass implements the
LT-F01..F17 work index, reconciled first against pass C (table below).
LT-A01..A07 stay decision-gated and untouched.

## Reconciliation against pass C (237633f)

| Finding | Verdict at 237633f | Evidence |
|----|----|----|
| LT-F05 stale sha256 after failed rehash | FIXED by pass C (C-02) | `internal/store/queries.go` updateFileRow writes `sha256 = ?` directly; games warm-skip requires a stored hash via FileStatByPathWithSHA; regressions TestScanGamesFailedRehashClearsStaleSHA256 |
| LT-F15 background jobs ignore server cancellation | PARTIALLY fixed by pass C (C-05) | launch contexts are `a.scanCtx()` with per-unit cancellation checks; residual: final-unit cancellation still published `done` — fixed here |
| LT-F06/F07/F08, LT-F01..F04, LT-F09, LT-F10, LT-F13, LT-F14, LT-F16, LT-F17 | SURVIVED pass C | verified at the exact evidence sites before fixing; all fixed in this pass |
| LT-F11 reader reset-wins policy, LT-F12 atomic cross-tab recovery | LANDED in the 2026-10-07 extension below | owner accepted the reset-wins policy; see "Extension" section |

## What was implemented

### LT-F10 conditional checksum publication

`SetFileSHA256` (error-only) is replaced by
`store.SetFileSHA256IfCurrent(FileVersion, sum) (applied bool, err error)`
conditioning the UPDATE on id + edition_id + path + size_bytes + mtime_ns +
`missing = 0 AND sha256 IS NULL`, inspecting RowsAffected. The lazy backfill
now stats the opened descriptor before and after the read, requires the
observed size/mtime_ns to match the row it loaded, and only publishes the
checksum into the WorkView when the SQL actually applied. A no-op after a
concurrent scan stored a newer hash leaves the response without a checksum
instead of advertising an uncommitted value.
Tests: TestBackfillNoOpDoesNotPublishChecksum,
TestBackfillConcurrentScanWinsNotPublished, TestBackfillRelinkedRowNotPublished.

### LT-F01..F04 joint media progress integration (web + ABS close)

`web/src/players/videoProgress.ts` is rewritten as the shared
transport-injected coordinator in the proven AudioProgressSaver shape:
endpoint+token-scoped tail shared across component replacement, an intent
generation advanced on every new session (constructor), acknowledged state
initialized to null (dedup only against successful acknowledgement),
sub-second heartbeat dedup bypassed for explicit saves (seeks, retries),
pending completion retained through teardown with deadline-bound exponential
retry (1s to 30s cap) owned by the timer closure so it survives unmount,
terminal 4xx (except 408/429) dropping their patch instead of retrying
forever, and login-change invalidation.

- video.tsx: periodic and cleanup saves no longer exclude zero (guarded by
  a live flag set at metadata/timeupdate, so an uninitialized element can
  never erase a pending restore); explicit seeks (buttons, scrubber,
  onSeeked) save immediately with the explicit flag; a `localProgress` map +
  `onProgress` callback publish fresh local intent to the work view; resume
  prefers the local position over the stale work-detail snapshot; Retry
  captures the current valid position (including zero) before teardown and
  re-boots from the local map.
- work.tsx: a work-scoped video progress map merged into every
  VideoPlayer/EpisodeList render, so reopening on the same page, edition
  A→B→A, and completed badges reflect fresh progress.
- sessionAudio.tsx: reuses the coordinator (its private write-serializer
  removed; the endpoint scope serializes), guards saves on a
  metadata/restore-complete flag instead of `position <= 0`, and deliberate
  zero seeks persist.
- internal/api/abs/abs.go sessionClose: `currentTime` is presence-aware
  (`*float64`); an explicit valid zero closes the session WITH a progress
  write, an absent field keeps the empty-close compatibility path, and
  invalid values stay 400 with the session open.

Tests: videoProgress.test.ts (11 cases incl. zero persistence, first
sub-second save, negative/NaN rejection, same-endpoint ordering across
replacement, superseded completion, post-teardown retry liveness, terminal
rejection), videoResume.test.ts (reopen from fresh local position, retry
capture, A→B→A, zero through pause/teardown), ABS
TestSessionCloseExplicitZeroUpdatesProgress,
TestSessionCloseWithoutCurrentTimePreservesProgress,
TestSessionCloseInvalidCurrentTimeRejected.

### LT-F09 game playtime above 720 hours

`store.ValidPlaytime` (finite, nonnegative, <= 2^53-1 seconds) now gates
game-* editions in core setProgress; ValidPosition is unchanged for bounded
media. Tests: TestGamePlaytimeAbove720HoursAccepted,
TestGamePlaytimeBoundsRejected, TestAudioUnknownDurationCapUnchanged.

### LT-F13 podcast library deletion dependency families

`deleteEditionBackedLibraryRows` and `deleteLibraryScanJobs` are extracted
from the generic DeleteLibrary and shared by `DeletePodcastsLibrary`, which
now removes imported edition-backed works (progress, playback sessions,
playlist items, files, editions, works), scan jobs, native podcast children
and the library in ONE transaction behind the existing lifecycle gate and
single-flight slots. Post-commit native byte cleanup and imported catalog
references (source bytes untouched) are preserved. Tests:
TestDeletePodcastsLibraryRemovesEditionBackedWorks,
TestDeletePodcastsLibraryWithNativeSubscriptionsAndScanJobs,
TestDeletePodcastsLibraryEmptyControlAndUnrelatedUntouched,
TestDeletePodcastsLibraryFailureIsAtomic.

### LT-F14 watcher busy admission

`scan.ErrScanRunning` (dependency-neutral sentinel; core.ErrScanRunning is
an alias) is now the busy verdict: the watcher re-arms dirty intent on
`errors.Is(err, scan.ErrScanRunning)` instead of re-deciding from a later
scan_jobs snapshot, closing the running→done transition gap that dropped
events. The DB-status probe is gone. Tests:
TestBusyAdmissionRearmsEvenWhenJobAlreadyDone (fake scanner rejects busy
with the job already done — the exact gap), TestPermanentFailureDoesNotRearm.

### LT-F15 residual: final-unit cancellation

runOPMLImport and runRefreshMeta recheck the lifecycle context after their
final unit and publish `error`/`canceled` with honest partial counts instead
of `done`. opmlImportStatus gains an `error` field. Tests:
TestRefreshMetaCanceledDuringFinalItemIsNotDone,
TestOPMLImportCanceledDuringFinalFeedIsNotDone (request/shutdown paths from
pass C still pass).

### LT-F16 symlink watch roots

`resolveWatchRoot` (Abs → EvalSymlinks → Stat → IsDir) resolves library
roots before arming; watches are placed on the real directories while the
stored library path spelling is untouched (the scanner's alias behavior is
unchanged). A configured-root → resolved-root map reconciles target
replacement by unwinding the old subtree on change, and an unresolvable root
arms nothing and reports failure instead of pretending to be watched.
Tests: TestSymlinkLibraryRootGetsWatched (alias-spelled stored paths),
TestSymlinkRootTargetReplacementRearms, TestUnresolvableRootIsNotWatched.

### LT-F17 commit-scoped mutation telemetry

All five scanner write closures (scanBook, storeBook, video, music,
storeGame) accumulate works/files deltas in closure-local counters and apply
them to the tracker only after `db.Update` commits; the video/music returned
counts likewise advance post-commit. Progress callbacks that persist
scan_jobs counts can no longer run while a catalog write transaction is
open (the SQLITE_BUSY self-contention window), and a rolled-back or
cancelled group no longer reports phantom added/works counters in its
terminal error job. Tests: TestRolledBackGroupReportsNoCommittedCounts
(injected trigger failure mid-transaction; asserts zero counts and zero
rows), TestCommittedGroupStillCounts (success + warm-scan controls).

### LT-F06/F07 ABS released-schema adapter + media identity

`detectABSLayout` inspects the books table read-only: `audioFiles` column →
released layout, `durationSec`+`author` → legacy layout, anything else →
refusal before any destination write (no empty successful import on
structural mismatch). Released discovery reads `books.duration`,
per-file path/index/duration tuples from `books.audioFiles` (sorted by
index; each file's own duration preserved — no natural-path position paired
with an unrelated duration array), authors via `bookAuthors`/`authors`
(optional with warning), and tries `mediaProgresses` before `mediaProgress`.
All foreign ids are `foreignID` strings (int64 legacy values normalized),
so UUID sources convert. Edition references, eligibility, progress and
playlists are keyed by tagged `absMediaKey{kind, id}` using Book/PodcastEpisode
media ids — the library-item id is no longer conflated with the media id,
so distinct namespaces and deliberate textual collisions (book item id ==
another book's media id, episode id == book media id) cannot misassign.
The single whole-destination `db.Update` transaction is unchanged; the
38-stage rollback matrix still passes against both layouts. Tests:
TestABSReleasedSchemaImports (UUID fixture, cross-namespace collision,
book/episode shared textual id, reordered multi-file audiobook, playlist
targeting), TestABSReleasedDryRunWritesNothing,
TestABSUnsupportedSchemaRefused, TestABSReleasedMissingUsersWarns,
TestABSReleasedGoneAudioFileSkipsEntry.

### LT-F08 Kavita released-schema adapter

`detectKavitaLayout` requires the Library table (fatal otherwise) and the
Series/Chapter/Volume/MangaFile catalog tables (fatal on a detected layout —
required structures must not silently disappear). Users come from
AspNetUsers with Admin resolved through AspNetUserRoles/AspNetRoles;
progress from AppUserProgresses; both are optional-with-warning, and every
stage now checks scan errors and rows.Err. `kavitaLibraryType` implements
the released LibraryType meanings (Manga=0/Comic=1/ComicVine=5 → comics,
Book=2/LightNovel=4 → books, Image=3 and anything else → structured
unsupported-type warning including the library id). `kavitaFormat` returns
(format, supported): extension-first archive subtype recognition, then the
released MangaFormat mapping (Archive=1, Epub=3, Pdf=4); Unknown=2 and
Image=0 are warned and skipped instead of fabricating PDF/CBZ. The whole
import transaction is unchanged. The synthetic fixture now uses the
released tables (the previous AppUser/Roles and Comic=2 shapes were
fictional and concealed the mapping defects). Tests:
TestKavitaLibraryTypeReleasedEnums, TestKavitaFormatReleasedEnums,
TestKavitaReleasedLibraryClassification,
TestKavitaRequiredTableMissingIsFatal, TestKavitaMissingUserTablesWarn, plus
the existing dry-run/commit/repeat suite on the released fixture.

## External schema verification (resolved 2026-10-06 against upstream sources)

1. ABS v2.37.1 `books.audioFiles` JSON entry keys: the released
   serialization nests the on-disk location under `metadata.path` /
   `metadata.relPath` (top-level keys carry `index`, `ino`, `duration`,
   `metadata: {filename, ext, path, relPath, size, ...}`). The adapter
   resolves `metadata.path` first with a top-level `path` fallback —
   verified against advplyr/audiobookshelf v2.37.1 `server/models/Book.js`
   (AudioFileObject typedef); the released-schema fixture exercises the
   nested shape.
2. ABS v2.37.1 author normalization: `bookAuthors(bookId, authorId)` →
   `authors(id, name)` confirmed by `server/models/Book.js`
   (`bookAuthorModel.create({ bookId, authorId })`, `au.name`) and the
   `bookAuthor`/`author` model names.
3. ABS v2.37.1 progress table: `mediaProgresses` is the physical table
   (model `mediaProgress`; raw `UPDATE "mediaProgresses"` appears in
   `server/models/MediaProgress.js`). The `mediaProgress` fallback stays
   as a harmless legacy alias.
4. Kavita v0.9.1.4 identity/progress tables:
   `DataContext : IdentityDbContext<AppUser, AppRole, int>` with no
   ToTable overrides, so ASP.NET Identity defaults apply —
   `AspNetUsers(Id, UserName)`, `AspNetUserRoles(UserId, RoleId)`,
   `AspNetRoles(Id, Name)`; `DbSet<AppUserProgress> AppUserProgresses`
   with `AppUserId`, `ChapterId`, `PagesRead` columns confirmed on the
   entity. LibraryType mapping verified against the released enum
   (Manga=0, Comic=1, Book=2, Image=3, LightNovel=4, ComicVine=5);
   MangaFormat likewise (Image=0, Archive=1, Unknown=2, Epub=3, Pdf=4).

A real disposable v2.37.1 / v0.9.1.4 backup remains the final acceptance
gate for both importers.

## Known limitations

- LT-F17: the mid-transaction cancellation and true commit-failure (vs
  statement-failure) fixtures are not separately automated; the rollback
  regression pins the same commit boundary, and no telemetry path can emit
  inside an open transaction by construction.
- LT-F04: in-tab liveness only; durable browser/process-loss recovery and
  cross-device ambiguity remain LT-A03. The coordinator keeps scopes for
  the tab lifetime (mirroring AudioProgressSaver) rather than reference
  counting idle scopes.
- LT-F06: the legacy ABS layout is retained (existing suites pin it); it is
  a synthetic contract and may be dropped once released-schema imports are
  confirmed against a real backup.

## What was run

- `go vet ./...` — clean.
- `go test ./... -count=1 -timeout 900s` — all packages green.
- `go test -race ./internal/store/ ./internal/scan/ ./internal/watch/
  ./internal/importer/ ./internal/api/core/ ./internal/api/abs/ -count=1` —
  green.
- web: `npx tsc --noEmit` clean; `npx vitest run` 335/335 in 25 files
  (baseline 324/24); `npm run build` clean.

## Extension (2026-10-07): LT-F11/LT-F12 landed after owner accepted the reset-wins policy

Verified at `6ac48f2` first: both findings survived the deeper pass
unchanged. DeleteProgress still created a tombstone only when a row
existed and bumped no lineage counter; the 409 `current` payload still
omitted any reset/deleted signal; the reader sender still adopted the
conflict revision, max-merged against a position-free tombstone, and
resent the pre-reset page at the adopted revision (reproduction
re-confirmed live: baseline returns 200 `revision:3` for the relabeled
replay — the reset is silently undone). createProgressStorage still
retired foreign records with a compare-then-remove over keys a live
writer keeps overwriting, so a write landing between the compare and the
delete of a shared key is lost with only the absorber's older copy
surviving.

### LT-F11 reset wins

- Migration 0017 adds `progress.reset_generation` (default 0 = never
  reset). DeleteProgress is one atomic upsert: an existing row is
  tombstoned with revision+1 AND generation+1; an absent row gets a
  tombstone (revision 1, generation 1) so a base-0 offline record can
  never insert over a reset lineage. Ordinary writers never touch the
  generation — it is retained through tombstone clears and row
  recreation and advances only on reset.
- `SetReadingProgressRevision` takes a base generation: negative keeps
  the documented revision-only compatibility scope (ABS/Jellyfin/
  Subsonic, media savers, beacons without the field — unchanged); a
  presented generation is compared in the same atomic statement.
- GET /progress answers `resetGeneration` and `deleted` in every branch
  (absent, tombstone, live). POST answers them on 200 and inside the 409
  `current`; a generation mismatch answers 409 with error `progress was
  reset` (distinct from `stale progress revision`).
- The reader sender classifies every 409 by generation BEFORE the
  merge-resend loop: same generation keeps the documented adopt/merge/
  resend behavior; a different generation throws ResetConflictError. The
  queue drops the operation without retry, rebases its base+generation
  onto the server state the conflict carried, leaves the durable record
  in place as quarantined evidence (finished intent included), and the
  reader shows "progress was reset". A deliberate new save publishes a
  fresh operation at the post-reset base and supersedes the quarantine.
  Mount-time quarantine: recovery records older than the server
  generation known from GET are not replayed at all. Legacy records
  without a generation parse as generation 0 — the only lineage they can
  ever match — never relabeled from a fresh read.
- Tests (Go): TestDeleteProgressTombstonesAbsentRow,
  TestResetGenerationFenceRejectsPreResetOperations (store);
  TestResetWinsAgainstRelabeledReplay,
  TestResetConflictResponseCarriesLineage (core HTTP, fail-before
  demonstrated at `6ac48f2` in a clean worktree: post-reset GET lacks
  lineage keys; relabeled pre-reset replay is ACCEPTED with 200 and
  resurrects page 80; ordinary-conflict `current` lacks lineage).
- Tests (web): "does not relabel a pre-reset operation after a reset
  conflict", "establishes a fresh operation on deliberate post-reset
  input", "retains explicit finished intent inside the quarantined
  evidence", "skips replay when the server lineage advanced before
  mount", "still merges and resends an ordinary same-generation
  conflict", plus queue-level quarantine/rebase tests (fail-before at
  baseline: the hook reproduction was called 2 times, not 1).

### LT-F12 transactional cross-tab retirement

- Durable queue storage is rewritten as immutable uniquely keyed records:
  every publish writes a new key (per-tab writer id + monotonic
  sequence) and no key is ever rewritten. A racing recovery can only
  retire the exact record it captured (compare-unchanged + removeItem of
  that exact key); a live writer's newer record lives under a different
  key and cannot be deleted underneath it. Recovery publishes the merged
  replacement BEFORE retiring sources (write-before-retire preserved,
  including on quota failure). Same-producer supersession removes the
  writer's own previous key only after the replacement is durable;
  clear() acknowledges the exact published version instead of deleting
  whatever occupies a mutable key.
- Recovery merges only records in the newest reset generation; older-
  generation records are left in place as quarantined evidence (bounded
  residue: one record per tab per reset), never max-merged (LT-F11
  interaction).
- IndexedDB was considered and deferred: the immutable-key discipline
  closes the demonstrated race without a storage-backend migration, and
  legacy records remain readable/migratable in place. Storage denial
  still degrades to the in-memory queue.
- Tests: "keeps a live writer's newer record when recovery retires its
  older one" (the forced interleaving — a write injected between the
  compare and the delete survives; fails at baseline),
  "acknowledges only the exact immutable version it published",
  "leaves older-generation evidence in place instead of merging or
  dropping it", "keeps a durable full-winner copy across two successive
  recovering tabs", "never merges recovery across reset generations",
  "migrates legacy records by assigning the never-reset generation",
  plus the preserved write-before-retire/quota/updated-source controls.

### Scope and limits

- Absent `resetGeneration` in a request is the documented compatibility
  boundary: unconditional faces and media savers are untouched; a
  strictly global reset guarantee for them would be its own scoped
  change.
- Quarantined lower-generation records are retained (never silently
  purged); a deliberate fresh save supersedes the tab's own record.
- Go HTTP/browser integration against a real deployed server was not
  run; coverage is the modeled-server vitest suite plus real-handler Go
  HTTP tests.

### Extension verification (all on the working tree at `6ac48f2`+changes)

- `go vet ./...` — clean; `gofmt` — clean.
- `go test ./... -count=1 -timeout 900s` — all packages green.
- `go test -race ./internal/store/ ./internal/api/core/ ./internal/api/abs/
  ./internal/api/jellyfin/ ./internal/api/subsonic/ ./internal/api/opds/
  -count=1` — green.
- web: `npx tsc --noEmit` clean; `npx vitest run` 348/348 in 25 files
  (baseline 335; +13); `npm run build` clean; `make build` clean
  (embedded webdist + binary).
- Fail-before evidence: new Go and web tests copied into clean worktrees
  at `6ac48f2` — API tests fail on the missing lineage payload and the
  accepted relabeled replay (200 `revision:3`); the web hook
  reproduction shows the relabeled resend firing twice; the
  interleaving test loses the injected newer record.
