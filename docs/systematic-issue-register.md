# Libteca systematic review and issue register

Date: 2026-10-03. Baseline: `fbf2f20c291533216cb6a21699e40729622ec4f9`. Local synthetic-fixture work only; no production data, remote publication, merge or deployment.

This records confirmed repairs and remaining work. A mapped review and passing tests are not a promise of zero defects. Status means: confirmed = supported by source/evidence; reproduced = exercised by a failing fixture where noted; fixed = local code changed; tested = applicable regression passed; remaining/unverified = not closed. Severity describes user impact, not a security certification.

## Repaired issues

### LT-001 — Reader pending progress could be lost during recovery
- Severity/status: High; fixed; tested
- Source: web/src/progressQueue.ts; web/src/reader/shared.tsx
- Reproduce: Recover equal/lower pending positions, fail replacement persistence, or race a foreign record update
- Change: Merge complete pending states, preserve winning positional tuple, publish replacement before removing unchanged sources
- Regression evidence: progressRecovery.test.ts; readerProgress.test.ts

### LT-002 — Reader lifecycle/conflict saves used inconsistent baselines
- Severity/status: Medium; fixed; tested
- Source: web/src/reader/shared.tsx
- Reproduce: Hide/close with an in-flight save, or discard a conflict already covered by remote state
- Change: Normal/lifecycle reconciliation share high-water state; include in-flight operation and advance acknowledged revision
- Regression evidence: readerProgress.test.ts; progressQueue.test.ts

### LT-003 — Video replacement and delayed progress crossed playback sessions
- Severity/status: High; fixed; tested
- Source: web/src/players/video.tsx; videoProgress.ts
- Reproduce: Switch edition/file with delayed save or failed completion
- Change: Key media sessions, serialize writes within a session, retain unacknowledged completion
- Regression evidence: videoPlayer.test.ts; videoProgress.test.ts
- Limits: Durable offline media queue and content-order generations remain separate open work

### LT-004 — Reset progress remained visible in discovery
- Severity/status: Medium; fixed; tested
- Source: internal/store/discovery.go; nextup.go; opds.go; subsonic.go
- Reproduce: Reset through ABS, then query resume/library/search/Next Up and another user
- Change: Filter reset tombstones from discovery while preserving baseline revision reads
- Regression evidence: discovery_reset_test.go; progress_reset_integration_test.go

### LT-005 — Library browsing truncated large collections or applied stale pages
- Severity/status: Medium; fixed; tested
- Source: web/src/libraryPagination.ts; views/library.tsx; internal/store/discovery.go
- Reproduce: Browse more than 200 works; change sort/library while pages are pending
- Change: Explicit bounded pages with stable ties, scoped child queries, cancellation, retry and honest loaded counts
- Regression evidence: libraryPagination.test.ts; libraryPaginationView.test.tsx; discovery_pagination_test.go
- Limits: Offset pages are not a snapshot under concurrent scans/deletes

### LT-006 — Cross-library grouping changed the physical serving root
- Severity/status: High; fixed; tested
- Source: internal/store/linking.go; internal/api/core/linking.go
- Reproduce: Move/merge editions into a work in a different library
- Change: Reject cross-library moves in the transaction; create new targets atomically
- Regression evidence: internal/store/linking_test.go; internal/api/core/linking_test.go
- Limits: Containment only; existing wrong-root/collapsed rows and full source migration remain open

### LT-007 — Audio queue replacement reused the old media session
- Severity/status: High; fixed; tested
- Source: web/src/players/audio.tsx
- Reproduce: Replace ordered file IDs at same queue index or emit detached completion
- Change: Key by ordered file IDs; save through original callback and ignore detached events
- Regression evidence: audio.test.tsx
- Limits: Metadata-only rerenders preserve playback

### LT-008 — Scan observer followed stale/newest jobs or overlapped requests
- Severity/status: Medium; fixed; tested
- Source: web/src/scan.ts; web/src/api.ts
- Reproduce: 409 start, superseded observer, transient 408/429/5xx or stalled body
- Change: Pin accepted job ID, use generations and serial deadline-bounded retry
- Regression evidence: scan.test.tsx; apiDeadline.test.ts

### LT-009 — OPML UI reported synchronous counts for asynchronous work
- Severity/status: Medium; fixed; tested
- Source: web/src/opml.tsx; views/podcasts.tsx
- Reproduce: Import OPML; close/reopen while running; lose server status
- Change: Follow status until done, resume existing job, prevent duplicate starts and refresh subscriptions
- Regression evidence: opml.test.tsx; apiDeadline.test.ts
- Limits: Server import status is still in-memory and can be lost on restart

### LT-010 — Local aggregate gate omitted frontend tests; embedding depended on prior build
- Severity/status: Low; fixed; tested
- Source: Makefile; .github/workflows/ci.yml
- Reproduce: Run make test from a fresh source checkout
- Change: Build locked frontend assets before Go and execute frontend test suite in make test
- Regression evidence: make-test-final.log; CI/Make dry-run order

### LT-101 — Audiobooks ignored disc/track tags
- Severity/status: Medium; fixed; tested
- Source: internal/scan/scan.go; audio_order.go
- Reproduce: Filename order a/b/z conflicts with positive disc/track order z/b/a
- Change: Apply disc/track tags (including N/total); natural fallback; one-time legacy repair preserving existing IDs and metadata, recomputing cumulative resume from same-edition file+offset and advancing changed revisions without changing activity timestamps
- Regression evidence: audio_order_test.go
- Limits: Order-changing rescans require playback pause/reload; already-collapsed groups are not silently split

### LT-102 — Warm audio scans missed covers on tag-named works
- Severity/status: Medium; fixed; tested
- Source: internal/scan/audio_order.go
- Reproduce: Embedded album differs from folder; add cover.jpg after first scan
- Change: Resolve existing work ownership through persisted file rows
- Regression evidence: TestAudioWarmScanFindsTaggedWorkCover

### LT-103 — ABS optional podcast metadata fallback failed
- Severity/status: Medium; fixed; tested
- Source: internal/importer/abs.go
- Reproduce: Remove podcasts table or itunesAuthor column from synthetic source DB
- Change: Keep fallback SELECT projection aligned with Scan
- Regression evidence: TestABSPodcastMetadataFallback

### LT-104 — ABS podcast episodes joined on the wrong ID
- Severity/status: Medium; fixed; tested
- Source: internal/importer/abs.go
- Reproduce: libraryItems.id=2, mediaId=42, podcastEpisodes.podcastId=42
- Change: Use media ID for the podcast episode collection
- Regression evidence: TestABSPodcastUsesMediaID

### LT-105 — ABS progress durations collided between media types
- Severity/status: Medium; fixed; tested
- Source: internal/importer/abs.go
- Reproduce: Book and episode share ID 1 but durations differ
- Change: Scope duration maps by media type and ID
- Regression evidence: TestABSProgressDurationSeparatesMediaTypes

### LT-106 — Unnumbered imported podcast episodes collapsed into one edition
- Severity/status: High; fixed; tested
- Source: internal/importer/abs.go
- Reproduce: Import distinct episodes with NULL or zero episode numbers
- Change: Use numbered identity only for positive episode numbers
- Regression evidence: TestABSUnnumberedPodcastEpisodesStayDistinct
- Limits: Previously collapsed imports need an explicit repair mapping

### LT-107 — Failed file import batches left partial writes
- Severity/status: Medium; fixed; tested
- Source: internal/importer/importer.go
- Reproduce: Valid first file followed by vanished second file; include existing-row case
- Change: Apply each file batch in one store transaction
- Regression evidence: TestApplyFilesRollsBackFailedBatch
- Limits: Whole-import destination atomicity is now covered by LT-120; real foreign-server schemas and concurrent source mutation remain unverified

### LT-108 — Cancelled podcast EOF could publish incomplete audio
- Severity/status: Medium; fixed; tested
- Source: internal/podcast/download.go
- Reproduce: Reader cancels context while returning bytes with EOF
- Change: Check cancellation after copy/close and before publication; retain retry eligibility
- Regression evidence: TestCancelledDownloadAtEOFStaysPending

### LT-109 — Cancelled podcast work ingested data or delayed the next retry
- Severity/status: Medium; fixed; tested
- Source: internal/podcast/podcast.go
- Reproduce: Cancel before feed ingestion or during fetch
- Change: Stop further ingestion/retention and avoid advancing last_fetch_at on cancellation
- Regression evidence: TestCancelledFeedDoesNotIngestEpisodes; TestCancelledRefreshDoesNotDelayRetry

### LT-110 — Reader byte limits ran after unbounded allocation
- Severity/status: High; fixed; tested
- Source: web/src/reader/resources.ts; cbz.tsx; epub.tsx
- Reproduce: Oversized streamed body or compressed page expands beyond configured cap
- Change: Bound retained download/extraction chunks; cancel download and pause ZIP stream at first excess; refuse no-stream body fallback
- Regression evidence: readerResources.test.ts: real JSZip DEFLATE test plus cancellation/overflow cases
- Limits: 512 MiB download / 256 MiB CBZ page byte policy; not an absolute heap, image-pixel, ZIP-directory or EPUB-internal-decompression bound

### LT-111 — CBZ navigation saved offscreen pages, retained stale jobs, or never finished even double spreads
- Severity/status: Medium; fixed; tested
- Source: web/src/reader/cbz.tsx
- Reproduce: Jump during extraction, close during decode, use webtoon keys, reach final spread in even-length book
- Change: Cancel/drop superseded work, separate viewport and prefetch observation, scroll on webtoon keys, finish at last visible page
- Regression evidence: cbzReader.test.tsx

### LT-112 — EPUB iframe keybindings and async lifecycle were broken
- Severity/status: Medium; fixed; tested
- Source: web/src/reader/epub.tsx
- Reproduce: Arrow/Escape inside iframe; close during hashing; index/navigation rejection
- Change: Use forwarded epub.js keydown, reject late work, release resources, surface retryable navigation/index feedback and persist current CFI/percent
- Regression evidence: epubReader.test.tsx
- Limits: Typing targets remain excluded; no representative EPUB or real browser acceptance is claimed

### LT-113 — PDF resume controls disagreed with viewer or late metadata
- Severity/status: Medium; fixed; tested
- Source: web/src/reader/pdf.tsx
- Reproduce: Open finished book, switch edition, fail/delay metadata fetch or restore out-of-range page
- Change: Key edition state, align finished restart at page 1, clamp pages, preserve saved position on failure, deadline/abort metadata and defer input until resolved
- Regression evidence: pdfReader.test.tsx
- Limits: Native PDF viewer behavior remains real-browser unverified

### LT-114 — Search stale responses reopened dismissed UI or replaced newer results
- Severity/status: Medium; fixed; tested
- Source: web/src/views/search.tsx
- Reproduce: Slow old query, then new query/clear/Escape/outside click/unmount
- Change: Generations, immediate invalidation, cancellation and deadline-bound requests; refocus can restart dismissed search
- Regression evidence: searchLifecycle.test.tsx: six tests fail on baseline, seven pass after fix

### LT-115 — Secondary playlist/podcast players mixed sessions and lost revisit resume
- Severity/status: High; fixed; tested
- Source: web/src/players/sessionAudio.tsx; views/playlists.tsx; views/podcasts.tsx
- Reproduce: Switch items then emit old media events; slow save during 1→2→1; revisit podcast before/after metadata; late playlist response; queue a progress write, then change the signed-in session before it drains
- Change: Key media and detail sessions, pause/save outgoing element, ignore detached events, serialize same-token/endpoint writes across sessions, retain local podcast resume without overwriting pending restore; discard queued writes when the current session no longer matches their originating session
- Regression evidence: secondaryAudioSessions.test.tsx; secondaryAudioReview.test.tsx: six baseline failures plus four independent-review reproductions; queued-session-change regression (secondaryAudioSessions.test.tsx)
- Limits: Durable offline storage and cross-device reconciliation remain open. Multipart playlist audio is now covered by LT-122

### LT-116 — Podcast/playlist HTTP failures rendered malformed or misleading empty/missing views
- Severity/status: Medium; fixed; tested
- Source: web/src/views/podcasts.tsx; views/playlists.tsx
- Reproduce: Return HTTP 503 error-shaped JSON for list/detail reads
- Change: Validate response shapes and distinguish server failures from missing playlists
- Regression evidence: secondaryAudioSessions.test.tsx: three HTTP-error regressions

### LT-117 — Games downgrade left dangling work/playlist/scan-job foreign keys
- Severity/status: Medium; fixed; tested
- Source: internal/store/migrations/0011_games_type.sql
- Reproduce: Seed game work plus empty game work, mixed playlist and scan jobs, downgrade to 0010 then re-upgrade
- Change: Delete unsupported dependencies in order before removing games libraries
- Regression evidence: migrations_roundtrip_test.go: broken FK reproduction, fixed downgrade; full empty cycle and populated 0001→0015 upgrade
- Limits: Down migration already intentionally removes unsupported game data; do not downgrade a real database without a verified backup

### LT-118 — Container frontend build did not require the exact lockfile or typecheck
- Severity/status: Low; confirmed; fixed; container unverified
- Source: Dockerfile
- Reproduce: Docker recipe used npm install and only build
- Change: Use npm ci and TypeScript before building web assets
- Regression evidence: Recipe inspected; equivalent locked frontend/typecheck/build runs in make test
- Limits: Docker is not installed here; no container runtime validation

### LT-119 — Runtime-library watcher regression raced its unfinished fixture
- Severity/status: Low; fixed; tested
- Source: internal/watch/watch_test.go
- Reproduce: Register a new library while the test is still writing its CBZ; initial reconciliation can correctly report an incomplete archive before later watch events retry
- Change: Complete the runtime-add fixture before registering its library and explicitly wait for both initial and new watches; separately test partial-write failure followed by completed-file ingestion and shutdown
- Regression evidence: TestLibraryAddedAtRuntimeGetsWatched and TestPartialWriteIsRetriedAfterCompletion each pass 20 race-enabled repetitions
- Limits: Test reliability repair; the separate regression confirms existing watcher retry behavior and does not claim all live filesystem timing cases are covered

### LT-120 — Late ABS and Kavita import failures left partial destination state
- Severity/status: High; fixed; tested
- Source: internal/importer/abs.go; kavita.go; importer.go; internal/store transaction seams
- Reproduce: Inject stage and commit failures after new/existing users, libraries, works, editions, files, reading/audio progress or playlists; replace a discovered file with a directory
- Change: Apply all destination reads and writes through one transaction; share Tx query seams without nested file/playlist transactions; return no successful plan on failure
- Regression evidence: atomicity_test.go: 38 rollback cases fail before repair; importer/store suites and race pass; repeated/concurrent imports, single-connection routing, read-only source and dry-run controls
- Limits: Closes LT-P03 for supported destination writes. Discovery is read-only but not a foreign-server snapshot; large-import writer-lock duration and real foreign schema/version acceptance remain unverified

### LT-121 — Primary audio progress writes overlapped, crossed sessions or lost completion
- Severity/status: High; fixed; tested
- Source: web/src/players/audioProgress.ts; audio.tsx; views/work.tsx
- Reproduce: Delay a save then rewind/finish; return HTTP failure after completion/unmount; seek to zero; restart while an old completion retries; switch login or file before queued work drains
- Change: Serialize endpoint writes in the originating login, bound requests, check HTTP results, retain failed in-tab completion with backoff, fence explicit/automatic restarts, preserve local edition resume and report exact outgoing file positions before new seek intent
- Regression evidence: primaryAudioProgress.test.tsx; audioProgressDelivery.test.ts; audio.test.tsx; audioReliabilityReview.test.tsx: five independent review reproductions plus slow writes, rewind/zero/restart, deadline, HTTP failure, replacement, local edition resume and originating-account coverage
- Limits: In-tab retry only. No durable tab/process-loss queue, content generation, cross-device conflict protocol or exactly-once server ordering after ambiguous timeouts

### LT-122 — Playlist editions played only their first physical file
- Severity/status: High; fixed; tested
- Source: web/src/players/editionAudio.tsx; views/playlists.tsx
- Reproduce: Play a two-file edition with resume in the second file, seek in either direction across the boundary, finish the first file and revisit an earlier item
- Change: Load ordered file IDs/durations and progress from work detail; map cumulative positions to file-relative offsets; isolate detached file sessions and complete only the last file
- Regression evidence: multipartPlaylist.test.tsx: four failures on the preceding checkpoint pass after repair; existing secondary-audio regressions retained with realistic metadata events and work-detail fixtures
- Limits: First-party direct audio only; multipart video/HLS/subtitles/thumbnails and live file-order changes remain open

### LT-123 — CBR cancellation left extractors running or published failed partial output
- Severity/status: High; fixed; tested
- Source: internal/scan/books.go
- Reproduce: Cancel or expire the scan during unrar/unar extraction/listing; fail after partial cover bytes; cancel immediately before store publication
- Change: Propagate caller context/deadline, unblock reads, kill/reap the launched process, reject failed partial output, clean temporary extraction and recheck cancellation before publication
- Regression evidence: cbr_cancellation_test.go: eight baseline subcases fail; cancellation, deadline, cleanup, partial-output and byte-cap controls pass under race and repetition
- Limits: Synthetic extractors. Ongoing extraction-disk/process-tree budgets and real unrar/unar version compatibility remain separate

### LT-124 — Slow work detail responses replaced newer navigation
- Severity/status: Medium; fixed; tested
- Source: web/src/views/work.tsx
- Reproduce: Navigate from a slow old work request to a newer work, then resolve the old response
- Change: Key work and edition view sessions, abort old detail requests, guard results by generation and distinguish load errors
- Regression evidence: primaryAudioProgress.test.tsx stale-response regression fails before repair and passes afterward
- Limits: Component tests; actual browser navigation and native media behavior remain acceptance gates

## Remaining work and acceptance gates

### LT-P01 — Physical source identity and existing data repair
- Severity/status: High; remaining; unverified acceptance
- Evidence/limit: Standalone read-only snapshot inventory implemented and synthetically tested; reports lexical ambiguity, mixed-root candidates, possible collapse and progress references without ownership assignment. Physical-source migration and existing-data repair remain unimplemented.
- Next acceptance: Run source-inventory-command.md against a consistent disposable real-library snapshot and optional copied-root mapping; review ambiguous identity/progress assignments, choose schema, then rehearse migration and rollback

### LT-P02 — Content/order generations and derived assets
- Severity/status: Medium; remaining; unverified acceptance
- Evidence/limit: Sidecar fingerprints and thumbnail/trickplay/content generations remain incomplete; running clients can retain a pre-rescan queue.
- Next acceptance: Pause/reload before order-changing scans; define a generation-aware playback and invalidation contract

### LT-P04 — Durable offline media progress
- Severity/status: Medium; remaining; unverified acceptance
- Evidence/limit: Pure intent/conflict and recovery-transition model plus decision table implemented and fixture-tested, without runtime imports, browser persistence, server receipts or podcast revisions. Current audio retry remains in-tab.
- Next acceptance: Approve media-intent-contract.md decisions, implement durable storage/server protocol, then test native browser reload/eviction and cross-device conflicts

### LT-P05 — Multipart video, HLS and derived timelines
- Severity/status: Medium; remaining; unverified acceptance
- Evidence/limit: Pure multipart resolver and proposed HTTP fixtures implemented without player/server integration. Current video/HLS/thumbnail handlers still select the first file.
- Next acceptance: Use multipart-video-contract.md to agree file/generation/offset semantics; integrate all handlers and adapters, then validate real browser/HLS multipart corpus

### LT-P06 — Broader archive/decode/transcode budgets
- Severity/status: Medium; remaining; unverified acceptance
- Evidence/limit: Disposable generated archive/native-media measurement harness implemented; separates compressed/entry/decode-estimate/disk/RSS evidence. No production quota or runtime budget implementation added.
- Next acceptance: Run representative long-title/archive corpus through appropriate browser/native measurements and choose justified caps, rolling-window policy and failure cleanup tests

### LT-P07 — Runtime dependency gate
- Severity/status: Medium; remaining; unverified acceptance
- Evidence/limit: Existing epubjs/xmldom and devalue dependency paths were previously reported by npm audit. They were not upgraded or newly security-audited in this pass.
- Next acceptance: Representative EPUB CFI/restore/navigation corpus before a breaking dependency change; coordinate with existing dependency update PRs rather than duplicating or blindly upgrading them; rerun the dependency gate only under permitted coverage

### LT-P08 — Real library, browser and compatibility corpus
- Severity/status: Acceptance; remaining; unverified acceptance
- Evidence/limit: No Gate W founder week, real-device browser playback, native PDF or live ABS/Jellyfin/OPDS acceptance. ABS corpus contains only one ping fixture; Jellyfin corpus is absent.
- Next acceptance: Backed-up disposable library; representative EPUB/CBZ/PDF/audio/video; live-client capture and bidirectional progress checks

### LT-P09 — Focused authentication/security review
- Severity/status: Coverage; remaining; unverified acceptance
- Evidence/limit: Excluded by the prior platform restriction; not retried or reconstructed. Existing automated suites ran, but this is not an independent security assessment.
- Next acceptance: Only pursue through an authorized supported review route; no inference of security completeness

### LT-P10 — Production/container/hardware and performance acceptance
- Severity/status: Operations; remaining; unverified acceptance
- Evidence/limit: No Docker runtime, live deployment, hardware encoder, real unrar/unar implementation, representative long-play disk budget or production-scale benchmark was exercised. CBR cancellation uses synthetic extractors.
- Next acceptance: Run container/service smoke, native-target runs, hwaccel/CBR checks and representative resource measurements before release

## Coverage map

| Subsystem | Review depth and evidence | Remaining limit |
|---|---|---|
| Core/store/discovery/progress/linking | Prior repairs recompiled and exercised under full Go/race suites; paging/reset/cross-library HTTP regressions; migration full cycle, populated upgrade and downgrade FK checks | Source identity, real-library races and live playback during reorder |
| Scanner/audio/video/music/books/games/watch | Deep audiobook order/warm-scan review; synthetic media + ffmpeg fixtures; CBR cancellation/reaping/cleanup fixtures; complete scan/watch suites | Every real encoding/layout, sidecars and source generation |
| Importers/podcasts | Deep ABS/Kavita destination-transaction and rollback review; source read-only/dry-run checks; podcast cancellation/download/refresh review | Foreign schema/version acceptance, source snapshots and large-import writer-lock duration |
| Readers/offline | Deep CBZ/EPUB/PDF lifecycle and byte-stream review; 36 new focused tests; previous durable-reader tests retained; service-worker cache policy inspected | Real browser/EPUB/native PDF; decoded-memory bounds; media offline persistence |
| Audio/video and browse/search | Primary/secondary keyed sessions, deadline-bound endpoint ordering, restart/completion retry, multipart playlist offsets, revisit-resume and search/work-detail races; component regressions | Actual codecs/browser events, multipart video/HLS, durable/cross-device media conflict policy |
| Metadata | Provider/cache/chapter application code sampled; full meta/core provider suites execute | Live providers, credentials, rate limits and cover/cache generations |
| Transcoding/trickplay/jobs | Full race suites including real ffmpeg fixtures, cancellation/reaping/session checks; core playback paths inspected | Hardware devices, long-title resource budgets and multipart video timelines |
| Compatibility faces | Full ABS/Jellyfin/OPDS/Subsonic unit/HTTP suites execute | One ABS ping corpus only; no Jellyfin corpus or live-client acceptance |
| Build/ops/export/backup | Locked make test, release compilation, migration checks, snapshot/export/CLI tests; recipes inspected | Docker/service deployment, native-target execution and one skipped snapshot failure injection |
| Authentication/security | Existing tests executed as part of ordinary full suite | Focused review excluded by restriction; no new security completeness claim |

Mapped source, test and build surface is listed in `review-source-inventory.md`; inventory inclusion is not a claim of line-by-line independent review. The historical audit ledger remains in `AUDIT_OPEN.md`; standing deferrals are not silently closed by this register.

## Verification

See `review-validation.md` and the attached validation logs for exact final commands, test counts, coverage, skips and patch reconstruction. `media-consistency-proposal.md` gives implementation contracts, safe next steps and concrete acceptance evidence for the larger remaining media work.
