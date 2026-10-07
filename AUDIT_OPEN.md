# Open audit items

Register libteca-01 through libteca-14 (local scout, 2026-09-11) is fully fixed - see `audit libteca-NN` commits. Remaining items are coverage gaps, not known defects:

- **Unaudited areas** (scout skipped deliberately): `web/` TS UI, importer bodies (abs/kavita internals), trickplay/cbz internals, discovery/works/queries store internals (grep-level only), `tools/record` + test corpus (founder-gated - do not automate).
- **Noted during fixes, unfixed, out of register scope:** `internal/egress/proxy_test.go` gofmt-dirty (pre-existing); `Reap`-style untrack-before-remove patterns elsewhere in the codebase were not swept.
- **Founder-owned gates remain:** corpus capture runs (`make record`), G1-G3, launch decisions.

## ChatGPT audit pass (2026-09-12, AUDIT-CHATGPT.md) - all 12 findings verified against source and fixed

1. HIGH failed scans mark library missing - FIXED: waitForJob returns the terminal job status; reconcileMissing runs only after a `done` scan.
2. HIGH deterministic HLS session ids share one ffmpeg across viewers - FIXED: transcode.NewSessionID adds an unpredictable suffix (ps-<edition>-<rand> / web-<edition>-<rand>); hlsFile parses the edition back out of the suffixed id.
3. HIGH podcast delete races refresh/download - FIXED: DeletePodcast takes the per-podcast single-flight lock (409 to the caller when busy); downloadEpisode rolls back the file row and bytes when LinkEpisodeFile fails.
4. HIGH podcast SSRF - FIXED: publicHTTPClient dial refuses loopback/private/link-local/multicast/unspecified destinations at resolve time, redirects re-validated; both feed and download clients use it. Test servers inject unrestricted clients.
5. HIGH last-admin delete race - FIXED: DeleteUserGuarded does lookup + admin count + token collection + deletes in one transaction (ErrLastAdmin).
6. HIGH playlist create/replace not atomic - FIXED: CreatePlaylistWithItems + ReplacePlaylist transactions; core create and subsonic createPlaylist use them.
7. HIGH pagination panics - FIXED: sliceWindow (jellyfin respondItems + podcastItems) and overflow-safe pageWindow (abs) clamp negatives and cap page*limit.
8. HIGH nil Work deref on bad season id - FIXED: detailFor handles WorkByID/WorksInLibrary errors.
9. HIGH header-auth HLS loses token on child URLs - FIXED: auth.Middleware and jfAuth stash the token in request context (auth.Token); HLS URL builders fall back to it when no query token.
10. HIGH covers truncated at size cap stored as valid - FIXED: read one byte past the cap and reject over-limit bodies (providers cover, podcast FetchBytes).
11. HIGH podcasts library delete leaves orphan files - FIXED: core deleteLibrary routes podcasts through Service.DeletePodcast (disk rows + media) first; DeleteLibrary also removes episode-linked files rows inside the transaction as defense in depth.
12. HIGH reaper goroutine never stops - FIXED: stop channel + sync.Once in CloseAll.

Verification: go build, go vet, go test ./... (15 pkgs), go test -race on podcast/watch/transcode, web tsc+build - all clean (2026-09-12).

## ChatGPT audit pass 2 (2026-09-12, AUDIT-CHATGPT-2.md) - all 3 findings verified and fixed

1. HIGH egress guard proxy/shared-space bypass - FIXED: the egress transport no longer inherits HTTP(S)_PROXY (a selected proxy moved validation off the destination), and publicIP rejects RFC 6598 shared space 100.64.0.0/10 (Tailscale/carrier NAT), which IsPrivate does not cover.
2. HIGH podcast deletion not failure-atomic - FIXED: store.DeletePodcastWithFiles and store.DeleteLibraryPodcasts remove subscription+episodes+file rows in ONE transaction (file ids captured before the FK-holding episode rows); Service.DeleteLibraryPodcasts acquires every single-flight slot BEFORE deleting anything (a busy subscription aborts with 409 and nothing deleted, instead of half a library destroyed); store.DeleteLibrary's defense-in-depth branch now deletes episodes before files (the old order violated the FK).
3. MEDIUM Jellyfin podcast child URLs dropped header tokens - FIXED: podcastEpisodeItem sources the credential through requestToken(r).

Also found while verifying (introduced in pass 1, masked by truncated test output): auth context keys collided (two separate `const ... = iota` made userIDKey == tokenKey == 0, so WithToken overwrote the user id and every admin check failed) - FIXED as one iota block; the core podcast tests needed the egress seam (podcast.NewWithClient) because httptest binds loopback; parseWebSessionID accepts the legacy bare web-<id> shape again (sessions are in-memory; the strict parser broke the pinned 404 contract).

Verification: go test ./... -count=1 exit 0 (17 pkgs), go test -race on podcast+core, web tsc+build - all clean (2026-09-12).

## ChatGPT audit pass 3 (2026-09-12, AUDIT-CHATGPT-3.md) - all 8 fixed

1. HIGH subscribe vs library-delete race - FIXED: Service.lifecycleMu gates row publication + single-flight acquisition (Subscribe) against the whole DeleteLibraryPodcasts operation.
2. HIGH Jellyfin session hijack - FIXED: /Sessions is filtered to own sessions for non-admins; playbackInfo mints u<uid>-prefixed session ids; sessionStopped refuses foreign prefixed sessions; WS command forwarding resolves the target's owner (sender identity from its authenticated connection) and drops cross-user targets.
3. MEDIUM egress dial gave up after first public IP - FIXED: every public address is tried before failing.
4. MEDIUM enclosure downloads capped at 30s - FIXED: New builds separate feed (30s) and download (no whole-request timeout) clients; NewWithClient takes both.
5. MEDIUM subsonic token oracle - FIXED: the t/s branch runs under the login limiter (Allow before verify, Failure on mismatch, Success on pass).
6. HIGH scanner nil-deref panic on vanished files - FIXED: Info() errors handled at all four walker sites; runScan goroutine carries a recover that fails the job.
7. HIGH PDF/CBR scan memory blowout - FIXED: pdfPageCount streams 1 MiB chunks (overlapped for marker boundaries); cbrExtract enforces the 20 MiB cap through LimitReader during extraction on both unrar and unar paths.
8. HIGH ABS import slice desync - FIXED: a planned file vanishing mid-import fails the import instead of silently compacting the id slice.

Verification: go test ./... -count=1 exit 0; go test -race on podcast/jellyfin/subsonic; web tsc+build - all clean (2026-09-12).

## ChatGPT audit pass 4 (2026-09-12, AUDIT-CHATGPT-4.md) - all 10 fixed

1. HIGH podcast-library lifecycle races - FIXED: single-flight slots acquired BEFORE path/cover snapshotting (a post-snapshot refresh could no longer orphan new bytes); store.DeletePodcastsLibrary removes podcast children AND the library row in one transaction inside the service's lifecycle gate (the separate ungated DB.DeleteLibrary call is gone).
2. HIGH Jellyfin session ownership gaps - FIXED: WS SessionsStart pushes a per-socket filtered snapshot; body-supplied PlaySessionId in sessionStopped gets the same ownership check; hlsMaster refuses foreign u-prefixed sessions (Manager.Get would kill the victim's session on edition mismatch); deviceOwnedBy resolves ownership from the target SOCKET's authenticated user (playback rows keyed by client-supplied DeviceID were poisonable).
3. MEDIUM subsonic limiter bucket reset - FIXED: limiter keys are principal-aware (ip|username); a success for A no longer erases V's failures.
4. MEDIUM scan panic left job 'running' - FIXED: the recovery defer persists counts + terminal error status via FinishScanJob before finishing the in-memory run.
5. HIGH cbr cap deadlock/truncation - FIXED: max+1 read detects oversize; the unrar child is killed instead of deadlocking Wait; the unar path size-checks the extracted file before reading; oversize is REJECTED, never silently truncated.
6. MEDIUM pagination overflow in abs.items + jellyfin.nextUp - FIXED: both now slice via pageWindow/sliceWindow.
7. MEDIUM truncated book images - FIXED: epub cover, cbz scan cover, and OPDS-PSE pages read max+1 and skip/reject oversize instead of persisting/serving corrupt bytes.
8. MEDIUM subsonic updatePlaylist partial application - FIXED: the complete request (removals, additions, rename) is resolved and validated before any mutation runs.
9. MEDIUM pdf boundary double-count - FIXED: markers fully contained in the carried tail are subtracted back out; only straddling markers survive.
10. MEDIUM providerKeysPut partial commit - FIXED: the whole key set is validated before any write.

Verification: go test ./... -count=1 exit 0; go test -race on podcast/jellyfin/subsonic/scan; web tsc - all clean (2026-09-12).

## ChatGPT audit pass 5 (2026-09-12, AUDIT-CHATGPT-5.md) - all 7 fixed

1. MEDIUM cover snapshot before exclusion - FIXED: DeletePodcast reads cover state under the slot; DeleteLibraryPodcasts snapshots only IDs first and re-reads covers after acquiring every slot.
2. MEDIUM empty podcasts library undeletable - FIXED: the zero-subscription path still runs the gated DeletePodcastsLibrary transaction.
3. HIGH session isolation residuals - FIXED: playback-update broadcasts fan out per-user (broadcastPerUser); every session id reaching Manager.Get/Close is owner-bound (bindPlaySession rewrites unprefixed ids to carry the caller's uid and drops foreign u-prefixed ids; hlsMaster's fallback mints u<uid>- ids); WS command authorization+delivery resolve the same socket in one hub operation (sendToOwnedBy); idless sockets get a per-user fallback device id instead of a shared dev-0.
4. MEDIUM limiter bucket reset on other faces - FIXED: core, ABS, Jellyfin and OPDS all key the shared limiter by ip|username.
5. HIGH unar/listing resource caps - FIXED: cbrList streams through a 4 MiB LimitReader; the unar walk tracks a running extracted-total ceiling and SkipAlls past the cap.
6. MEDIUM updatePlaylist partial commit - FIXED: removals/additions/rename apply through one UpdatePlaylistDelta transaction; duplicate removal indexes dedupe against the pre-request snapshot.
7. MEDIUM OPDS pagination overflow - FIXED: pageParam clamps to 2^26 and next-link arithmetic no longer wraps.

Verification: go test ./... -count=1 exit 0; go test -race on podcast/jellyfin/subsonic/abs/opds/scan - all clean (2026-09-12).

## Games library type — PLAN-GAMES G1 shipped (2026-09-13)

Built per the parked design after DECISIONS #38 unparked it: scanner
(internal/scan/games.go, platform table + disambiguation ported from
omilator's GameSystem with its documented shared-extension preference),
migration 0011 (libraries type CHECK widened; editions format gains the
'game-<platform>' namespace so new platforms never need a migration — both
tables rebuilt with NO TRANSACTION so the FK toggle is real, and the 0008
description column + 0009 editions index are preserved both directions),
faces exclude games libraries (jellyfin views + ABS listings; OPDS and
Subsonic were already type-scoped), web gains the Games tab + platform
format labels. Edition.Title carries the platform display name; the bare
platform tag rides on files.container; work titles come from
omilator-derived ROM filename cleanup (region/revision/tag stripping).

Runtime smoke: 3 fake ROMs across sfc/gba/gen → 3 works on correct
platforms, rescan no-op, Jellyfin views show only the book library,
Subsonic unaffected, zero panics.

## ChatGPT audit pass 6 (2026-09-17, AUDIT-CHATGPT-6.md) - 41 of 45 fixed, 4 deferred/partial

Fixed (verified against source first; `audit 6-NN` commits):

1. HIGH bearer-token cache resurrection - FIXED: UserForToken queries the DB on every request (no positive cache), bounded token length, throttled last_seen SQL write (F10 rolled in).
2. HIGH OPDS basic-auth revocation race - FIXED: per-API proof cache keyed by user id + CURRENT stored hash + password; user looked up on every request; DB outage is 500, not a failure count.
3. HIGH non-atomic password rotation - FIXED: store.RotatePassword swaps hash + revokes tokens in one transaction; login/token issuance is conditional (IssueTokenForPassword on the verified hash, IssueTokenFromParent on an active parent) across core/ABS/Jellyfin/tokenIssue.
5. HIGH unbounded concurrent Argon2 - FIXED: process-wide 2-slot KDF budget (VerifyRequest/HashRequest/CheckPassword) on every request-time verify/hash incl. unknown-user dummy, user create and rotation; ErrKDFBusy maps to 429 + Retry-After on every face.
6. MEDIUM malformed stored hash params - FIXED: Verify accepts exactly the one format Hash emits (version, params, salt/key lengths, total length).
7. LOW unknown-user timing - FIXED: CheckPassword / OPDS + Subsonic dummy-hash burn make missing-user and wrong-password cost identical.
8. MEDIUM bootstrap bypass - FIXED: name/password validation, non-admin refusal, empty-install check + insert in one transaction; no more never-delivered bootstrap token.
11. HIGH main returns before shutdown finishes - FIXED: HTTP server runs in a goroutine, main owns Shutdown→srv.Close→worker join→core.WaitJobs→exit; scan/meta/OPML jobs go through a launchJob registry (503 on shutdown admission); exit code preserved after deferred cleanup.
12. MEDIUM no body-read deadline - FIXED: ReadTimeout 30s (hijacked websocket upgrades keep their own lifecycle).
13. LOW CLI precedence/validation - FIXED: explicit --watch beats LIBTECA_WATCH, strict env parsing, port range check, overflow-guarded sweep, invalid --hwaccel fatal, --host flag. (README reverse-proxy/Tailscale trust-boundary paragraph not rewritten - doc-only residue.)
14. MEDIUM backup name collision - FIXED: snapshot staged privately and published with a no-replace hard link (nanosecond + random suffix name); BackupTo refuses existing destinations.
15. MEDIUM cover backup durability - PARTIAL: checked Close, fsync of files+dirs, non-regular entries rejected. Per-snapshot cover generations DEFERRED (see below).
16. LOW retention over-keep with protected oldest - FIXED: the protected file counts toward the keep budget.
17. MEDIUM DSN reserved characters - FIXED: file URI built via net/url; paths with ? # % spaces open literally, pragmas survive (tested).
18. LOW leaked handles on failed Open - FIXED: pool closed unless init succeeds.
19. MEDIUM reads silently empty - FIXED: me/getProgress/work-detail propagate failures (500); missing edition is 404; WorksInLibrary checks erows.Err; FileByID/EditionByID report iteration errors before NotFound. (findWorkID left two-valued: its callers funnel into UpsertWork, which re-queries and propagates.)
20. MEDIUM JSON committed then failed - FIXED: writeJSON marshals before WriteHeader; failure degrades to a valid 500; 404 problem+json preserved.
21. MEDIUM work detail loads whole library - FIXED: WorkViewByID loads only the work's editions/files in the WorksInLibrary shape (file-less editions still listed).
22. MEDIUM progress validation - PARTIAL: non-finite/negative position/duration/page, oversized device/locator rejected; Device finally persisted. position>total bound skipped (client end-position tolerance is a product call; Locate pins to the last file today).
23. LOW misleading id/type responses - PARTIAL: addLibrary validates name/type (podcasts excluded). Malformed numeric ids stay 404-by-design; sweeping every handler to 400 is contract churn without security impact.
24. MEDIUM unchecked stat/nullable derefs - FIXED: serveFile stats + regular-file checks before/after open; HLS segments served through it; width/height emitted independently.
25. MEDIUM discarded scan persistence errors - FIXED: counts checked (warn), terminal FinishScanJob retried 3x and failures logged; FailRunningScanJobs at startup remains the crash backstop. Durable outbox DEFERRED (overkill for a single-node server; see below).
26. MEDIUM validators before apply - FIXED: ETag/Last-Modified advance only after episodes applied (subscribe + refresh); metadata writes checked.
27. HIGH unbounded enclosures - FIXED: 2 GiB byte cap + 2 h read ceiling, declared-length rejection, LimitReader(max+1), empty-body rejection, temp cleanup.
28. MEDIUM filename collision overwrite - FIXED: title fallback re-checked; ultimate fallback is the immutable episode id.
29. MEDIUM DeleteLibrary orphan podcast files - FIXED: file ids captured in-transaction before episode rows; delete guarded by NOT EXISTS.
30. MEDIUM NaN/Inf/negative durations - FIXED: strict 1-3 field parser, sub-60 fields, finite/nonnegative, 30-day cap; 0 = unknown as before.
31. MEDIUM capacity eviction of active viewers - FIXED: only idle-expired sessions reclaimed at cap; excess admission gets ErrCapacity (503 + Retry-After on core and Jellyfin).
32. MEDIUM closed manager admits sessions - FIXED: closed flag under the manager lock; Get returns ErrClosed after CloseAll.
33. MEDIUM clean short transcodes re-encoded - FIXED: process exit error recorded per generation; fallback only on actual failure, never on clean exit or kill.
34. MEDIUM HLS sessions not user-owned - FIXED: bounded expiring per-user web tickets; hlsFile derives the edition from the caller's ticket, hlsStop consumes it; fabricated/foreign/replayed ids get 404.
35. MEDIUM unvalidated starts/non-media - FIXED: NaN/Inf/negative/out-of-duration starts 400; game-*/codec-less editions never reach ffmpeg (415 at playback, 404 at segment fetch).
36. HIGH service worker caches protocol faces - FIXED: static-asset allowlist only; query/Authorization/Range requests and non-shell navigations bypass; cache failures fail open to the network. skipWaiting/claim retained deliberately (existing update behavior).
37. LOW activation deletes foreign caches - FIXED: only libteca-static-* plus the four known legacy names are deleted.
38. MEDIUM api() treats failures as data - FIXED: every non-ok response normalized to {error, status...} reading problem+json detail/title; 401 storage access guarded.
39. LOW Headers merge + Library types - FIXED: platform Headers merge with override preservation; path optional, podcasts/games in LibraryType.
40. MEDIUM EPUB percentage resume with cached locations - FIXED: only location GENERATION gates on the cache; percentage→CFI mapping runs either way.
41. LOW EPUB unmount/stale state - FIXED: AbortController cancels the transfer; per-edition state reset at effect start; intentional abort shows no error overlay.
42. MEDIUM false-success Subsonic stubs - FIXED: savePlayQueue/getPlayQueue answer errNotImplemented; getStarred2/getAlbumInfo2 return their truthful empty payloads.
43. LOW CI gaps - FIXED: race-enabled go job on go.mod toolchain with ffmpeg, npm ci with cache, least-privilege permissions, job timeouts. (Exposed and fixed the map-order flake in TestLimiterMaxIPs.)
44. LOW Handler() recreates destructive state - FIXED: once-only construction; trickplay generator is per-API (no package-global map retention).
45. MEDIUM unchanged feed blocks retries - FIXED: downloadPending returns joined failures; the 304 branch runs pending downloads + retention (auto-download toggled on after subscription now recovers without a feed change).

Deferred (product/architecture decisions, not contained fixes):

- **F04 plaintext subsonic password (HIGH):** disabling legacy t/s token auth
  breaks every deployed Subsonic client that defaults to it (the repo's own
  corpus tests pin the behavior); the safe alternative - a separate
  revocable Subsonic-only app secret - is a deliberate protocol/credential
  migration that needs a founder decision and client-compat verification.
  Interim posture: the captured secret is hash-validated on every use and
  dropped on rotation (already the case). Audit 7's F02 re-raises this with
  the same evidence base; the deferral stands.
- **F09 token digests at rest (MEDIUM): DIGESTS LANDED 2026-09-18**
  (`fix(auth)` commit, migration 0013): token values are stored as sha256
  digests; ONE transactional startup rewrite converges legacy plaintext
  rows (idempotent via the marker column; half-applied = lockout was the
  recorded risk); every token writer/reader hashes the presented value.
  The expiry-policy half of this item (interactive vs device token
  lifetimes) remains a product decision and is still open - revisit with
  F04's credential work as originally recorded. Audit 7's F14 (ABS
  playback-URL token binding + fixed expiry) is the same credential-
  lifecycle family; the deferral stands for it.
- **F15 per-snapshot cover generations (MEDIUM, durability half fixed):
  RESOLVED 2026-09-18** (see "Audit deferrals closed 2026-09-18 -
  generations + revision protocol" below). The layout half landed as
  generation directories; the durability fixes from this pass and pass 7's
  F05 (fsync, checked closes, non-regular rejection, publish-after-copy,
  atomic per-cover replaces, cross-process flock) carry over unchanged.
- **F25 durable scan-terminal outbox:** bounded retry + startup
  reconciliation (FailRunningScanJobs) already bound the stuck-job window
  to one restart; an outbox is beyond a single-node server's needs.

Also noted while verifying: TestLibraryAddedAtRuntimeGetsWatched in
internal/watch flakes when the 200ms periodic sync scans a CBZ mid-write
(pre-existing; neither watch nor scan was touched this pass). The remaining
gofmt-dirty files (jellyfin/ws.go, opds/cbz.go, meta/thegamesdb.go,
scan/books_test.go, scan/games_test.go, store/podcasts.go and tests) are
pre-existing and untouched.

Verification: go vet ./... clean; go test ./... -count=1 green across
repeated full runs; go test -race ./... -timeout 600s green; web npx tsc
--noEmit + npm run build clean (2026-09-17).

## ChatGPT audit pass 7 (2026-09-17, AUDIT-CHATGPT-7.md) - 41 of 50 fixed, 6 partial, 3 deferred

Fixed (verified against source first; `audit 7-NN` commits):

1. F01 HIGH unbounded login bodies - FIXED: 32 KiB MaxBytesReader + checked decode (413 on overflow, 400 on malformed) on ABS login and Jellyfin authenticate; 1 MiB caps on the mutating ABS/Jellyfin bodies.
2. F03 HIGH library path escape - FIXED: scanners register only regular files (symlinks/FIFOs skipped, walk errors fail the scan); every media open goes through os.Root confinement (internal/mediafs) resolved from the file's library - core stream/subtitles, ABS tracks + podcast episodes, Jellyfin streams. Residual: ffmpeg/ffprobe children open source paths directly, bounded by the scan-time regular-file check.
3. F04 HIGH concurrent retention removes all backups - FIXED: snapshot/publication/retention under a cross-process flock on a kept .backup.lock.
4. F06 HIGH podcast filename fallback overwrite - FIXED: ownership checked at every fallback (errors fail the download), final fallback is a random nonce name, publication is link-no-replace; only self-owned or orphan files may be replaced.
5. F07 HIGH CBR listing deadlock - FIXED: cap+1 read, overflow/read error kills the child, 30s deadline, Wait called exactly once.
6. F10 HIGH trickplay bypasses limits - FIXED: process-wide 2-slot generation budget shared by all Generator instances (ErrBusy -> 503 + Retry-After), widths restricted to 160/320 (others 400).
7. F11 HIGH HLS resume timeline - FIXED (web): no server-side start truncation; hls.js resumes via startPosition, native HLS via seekable-range restore against the full edition timeline.
8. F12 MEDIUM sign-out leaves token valid - FIXED: POST /api/core/logout revokes the exact request token; web sign-out calls it (with an offline warning) before clearing storage.
9. F13 MEDIUM stale password change - FIXED: RotatePasswordChecked does hash-CAS update + token revocation + session close + subsonic secret drop in one tx; self-service passes the verified hash (409 on stale), only admin resets pass nil; both user-delete variants drop the secret too.
10. F15 MEDIUM username-cycling limiter bypass - FIXED: per-IP aggregate bucket (30 failures, not cleared by success) on core/ABS/Jellyfin/OPDS/Subsonic; Subsonic's unknown-user and bad-enc branches now register failures like every other path.
11. F16 MEDIUM public cache-control on authenticated assets - FIXED: OPDS covers/thumbs/PSE pages and the ABS cover answer `private, max-age` (query-keyed Jellyfin image URLs keep public, matching upstream Jellyfin).
12. F18 MEDIUM disc interleave - FIXED: audiobook groups sort by natural root-relative path.
13. F20 MEDIUM warm-music ordinals - FIXED: positions come from the complete ordered album; unchanged tracks get their stored edition's position repaired in the same tx; UpsertEdition updates position only when provided.
14. F21 MEDIUM game update reorders discs - FIXED: same-edition updates keep their seq; max(seq)+1 only for genuinely new files.
15. F22 HIGH failed walks as success - FIXED: walk/info errors fail the scan; unavailable or non-directory roots are refused before enumeration.
16. F23 MEDIUM reconcile only in watcher - FIXED: reconciliation (MarkMissingLibraryFiles, ErrNotExist-only) runs inside the shared scan path after every complete scan - HTTP, watcher and CLI alike.
17. F24 MEDIUM sampled hash as identity - FIXED (bounded): relink requires same library + equal recorded size + row already flagged missing by reconciliation; ambiguous matches insert fresh rows. Residual: the old bytes are gone by then, so a same-size/same-ends collision on a missing row cannot be content-verified - documented rather than hidden.
18. F25 MEDIUM stat errors as disappearance - FIXED: only fs.ErrNotExist counts; other stat errors abort the relink; candidates scoped to the destination library.
19. F26 MEDIUM overlapping roots - FIXED: addLibrary rejects duplicate/ancestor/descendant roots (symlink-resolved + SameFile), 409.
20. F28 MEDIUM killed launch skips Wait - FIXED: launch/kill serialized by a lifecycle mutex; a waiter starts after EVERY successful start; kill waits (bounded 2s) for exit before removing output and preserves the directory on timeout.
21. F29 MEDIUM expired session silently recreated - FIXED: Manager.Existing for segment fetches; expired sessions answer 410 and the client's retry bootstraps a fresh session.
22. F30 MEDIUM direct resume gated on HLS capability - FIXED (web): resume keyed on the selected playback mode.
23. F31 MEDIUM blank hls.js player - FIXED (web): bounded network/media error recovery, fatal errors surface with a retry action, loading/error UI no longer depends on a literal src, boot rejections render.
24. F32 MEDIUM rewind stops periodic saves - FIXED (web): wall-clock 10s cadence + forced save on seeked.
25. F33 MEDIUM false-success saves - FIXED (web): apiChecked throws on error-shaped responses; reader saver and video save use it; video dedup watermark advances only after ack.
26. F34 MEDIUM PDF close undoes finished - FIXED: Finished is *bool with PATCH semantics (omission preserves, explicit false reopens) atomic in the upsert; the PDF reader only posts after real user edits.
27. F35 MEDIUM CBZ ordering divergence - FIXED: one byte-based natural comparator (internal/natural) in scan/OPDS/importer and its exact TypeScript port in the reader; .jxl dropped from the web list (server never counted it).
28. F36 LOW unguarded localStorage - FIXED (web): guarded boot token read and validated playback-rate init.
29. F37 MEDIUM close-before-progress - FIXED: CloseSessionWithProgress commits final progress + close (+ listened delta) in one idempotent transaction.
30. F38 MEDIUM truncated track offsets + duplicate chapter ids - FIXED: float-second cumulative offsets in play() and itemPayload; chapter ids unique across the whole response.
31. F39 MEDIUM season zero = no filter - FIXED: *int sentinel; specials requests return only season 0; malformed SeasonId is 400.
32. F40 MEDIUM double JSON document - FIXED: saveFromSession owns the response; rejection branches just skip teardown.
33. F41 MEDIUM compat progress without validation - FIXED: shared store.ValidPosition (non-finite/negative rejection, duration bound +5s tolerance, 30-day unknown-duration cap) on ABS progress/sync/close and Jellyfin session saves; progress fraction > 1 rejected before multiplication.
34. F42 MEDIUM importer 10 before 2 - FIXED via internal/natural (regression-tested).
35. F43 LOW unescaped foreign DSN - FIXED: net/url-built file URI; read-only verified behind URI-significant filenames.
36. F45 MEDIUM parallel release embeds stale web - FIXED: release-% depends on web directly.
37. F46 MEDIUM unknown-length downloads at 90s - FIXED: unknown Content-Length gets the 2h ceiling; size-derived deadlines round up.
38. F47 LOW inconsistent admin-creation validation - FIXED: shared ValidateCredentials/ValidatePassword (trim/bounds/control chars, password 8-1024) in initialization, creation and rotation; validation failures are 400, only real KDF capacity is 429.
39. F48 LOW watcher overflow unhandled - FIXED: fsnotify overflow marks every library dirty (lost events recover via rescan); child watches are retried even when the root is armed.
40. F49 LOW README password rejected - FIXED: quick start uses a valid placeholder and states the minimum.
41. F50 MEDIUM false metadata success - FIXED: episode title/description write failures surface (joined error + partial counts); cover summary requires the DB link; an existing cover file now repairs a missing link instead of reporting nothing-to-do.

Partial (in-place fixes shipped, remainder needs product/schema work):

- **F05 backup generations: RESOLVED 2026-09-18** (see the second closure
  section below) - DB snapshots publish only after the covers copy
  completes and each cover is replaced atomically; generations no longer
  share one covers/ directory.
- **F08 cancellable/bounded probes:** ffprobe is context-bound with a 60s
  deadline and a 4 MiB output cap that kills the child; CBR listing and the
  unar extraction are deadline-bound; cover extraction carries a 2-minute
  deadline. The transcode hwaccel startup probe and a real disk quota for
  unar extraction (vs the post-hoc total cap) remain.
- **F09 cover decode budget:** OPDS thumbnails preflight dimensions via
  DecodeConfig (16384px/16MP caps), read bounded, and decode under a
  2-slot budget with pass-through degradation; scanner sidecar reads are
  bounded. Re-encoding arbitrary stored cover bytes to their true format
  (vs the .jpg naming convention) is not done.
- **F17 identity collapse:** root-level media files now group per-file
  instead of collapsing into the library identity, but the full
  source-key/edition-identity redesign (same-title editions, tag-driven
  reparenting) is architectural - see below.
- **F27 same-second changes:** files carry mtime_ns (migration 0012; legacy
  rows re-probe once). A sidecar fingerprint (NFO/cover-only edits) needs a
  per-type definition of relevant sidecars and is deferred with F17's
  metadata work.
- **F44 derived assets:** trickplay publishes behind a COMPLETE marker and
  regenerates partial directories instead of treating 0.jpg as done.
  Versioned cache keys (stale thumbnails/trickplay after source changes)
  remain open - they tie into F17's content-versioning.

Deferred (architectural/product decisions, not contained fixes):

- **F02 plaintext subsonic password (HIGH):** pass-6 F04 deferral stands
  (see above).
- **F14 ABS playback URLs not bound to token/expiry (MEDIUM):** pass-6 F09
  credential-lifecycle deferral covers it (see above).
- **F17 scanner identity redesign (HIGH, library integrity):** title/author
  match keys currently define work and edition identity (works unique
  index on lower(title)+author; editions matched on format+title). Moving
  to source keys means a schema migration, a repair plan for already
  collapsed records (progress cannot be split blindly), and retiring the
  title-match unique index across every upsert caller - a founder-level
  data-model decision, not a contained fix. The contained root-level
  grouping defect IS fixed (item 2 above).
- **F19 alternate encodings concatenated / M4A mislabel (MEDIUM):** proper
  variant partition (complete M4B vs MP3 track set of the same book) is the
  same identity work as F17; the edition format CHECK constraint also gates
  new format values behind a migration.

Also noted: audit 7's H01-H08 are engineering recommendations rather than
findings; H02 (container USER), H03 (npm ci in Docker/Make), H04 (browser
E2E suite) and H06 (provider artwork egress policy) remain open ideas, H05
disk/memory budgets partially follow from F10, H07 (data-dir lock) and H08
(work-scoped progress reads) are candidates for the next pass.

Verification: go vet ./... clean; go test ./... -count=1 green; go test
-race ./... -timeout 600s green; web npx tsc --noEmit + npm run build clean
(2026-09-17).

## ChatGPT audit pass 8 (2026-09-17, AUDIT-CHATGPT-8.md) - 25 of 35 fixed, 10 deferred

Fixed (verified against source first; `audit 8-NN` commits):

1. F01 HIGH library confinement bypass - FIXED: core edition download, OPDS
   download + PSE and Subsonic stream all open through
   LibraryRootForEdition + mediafs.OpenWithin like the rest of the serving
   surface.
2. F02 HIGH embedded subtitle cache in library trees - FIXED: extraction
   cache lives under DataDir/subtitles, keyed by file id+path+mtime_ns,
   bounded reads, atomic publication, 2-slot extraction budget (503 +
   Retry-After on contention). Extraction's ffmpeg input still receives the
   pathname (F03 family residual below).
3. F06 MEDIUM public cache-control on authenticated assets - FIXED on core
   covers/thumb tiles and Subsonic getCoverArt (private, max-age). Jellyfin
   image/trickplay URLs keep public per the documented pass-7 decision
   (query-keyed api_key URLs, matching upstream Jellyfin).
4. F07 MEDIUM auth 401 on DB failure - FIXED: LookupTokenUser separates
   ErrNotFound (401) from operational errors (503) across core middleware,
   jfAuth and the websocket; web api() clears stored credentials on 401 only
   when the failing request used the current token.
5. F08 MEDIUM overlapping-root race - FIXED: AddLibraryChecked runs the
   overlap check inside the insert transaction (BEGIN IMMEDIATE); the HTTP
   path routes through it. Importer placeholder libraries keep the raw
   insert deliberately (they are not validated roots; admin fixes the path
   before scanning).
6. F09 MEDIUM symlink root scans empty - FIXED: scanners resolve the root
   via EvalSymlinks and walk the real directory while recording alias-based
   paths, so rooted serving keeps matching. Regression covers alias-path
   spelling.
7. F11 MEDIUM one-scan rename loses identity - FIXED: MarkMissingLibraryFiles
   runs after the completed traversal but before the first upsert (inside
   every scanner), so the renamed path relinks onto its old row in the same
   scan. runScan/CLI post-scan reconcile removed as redundant.
8. F14 MEDIUM scan failures reported as done - FIXED: per-item probe errors
   are collected into the returned error, all-fail groups no longer create
   empty works, late cancellation is surfaced even when the scanner returned
   nil, and reconciliation failure (now inside the scanner) fails the scan.
9. F15 HIGH second server damages the instance - FIXED: server/scan/init
   paths hold a lifetime exclusive flock on <data>/.server.lock (kept inode,
   idempotent release) taken before store.Open; backups keep their own
   .backup.lock.
10. F16 MEDIUM audio-only HLS fails - FIXED: `-map 0:v:0?` (audit-reproduced
    mitigation; hardware paths untested on real GPUs - see open items).
11. F18 MEDIUM small starts dropped + silent session reuse - FIXED:
    `startSecs > 0`; Session carries StartSecs, reuse with changed
    source/start answers ErrSessionParams; Jellyfin mints a fresh
    cryptographic session id on that path (fallback timestamp ids gone) and
    strictly parses/bounds StartTimeTicks; core maps the error onto the 410
    retry path.
12. F19 MEDIUM unbounded hwaccel probes - FIXED: probes run under a 5s
    deadline + 1s WaitDelay, so a hung probe can no longer hold hwMu (and
    through it the manager) indefinitely.
13. F20 LOW explicit auto loses to env - FIXED: SetHwAccel("auto") keeps the
    sentinel and detection skips LIBTECA_HWACCEL; the CLI falls back to env
    only when --hwaccel was not explicitly set (flag.Visit).
14. F21 MEDIUM dual trickplay generators - FIXED: server.New injects one
    Generator into core and jellyfin; per-API lazy construction remains only
    for isolated tests.
15. F23 MEDIUM jellyfin segment route unowned - FIXED: hlsSegment enforces
    the u<uid> prefix against the caller (unprefixed ids are admin-only,
    since only admins create them) and binds the URL item to the session's
    edition via Existing before touching the session.
16. F24 MEDIUM download error suppresses retention - FIXED: applyFeed and
    the 304 branch run downloads and retention independently and join the
    errors.
17. F25 MEDIUM purge discards unlink errors - FIXED: unlink errors fail
    retention (ErrNotExist tolerated), untracked/out-of-root paths are
    refused with the episode kept linked, and PurgeEpisode commits the
    missing flag + link removal in one transaction. The audit's persistent
    deletion outbox stays deferred under the pass-6 F25-outbox rationale
    (single-node server, bounded retry windows).
18. F26 MEDIUM URL normalization changes identity - FIXED: no http->https
    upgrade; fragments still stripped. Trailing-slash trimming is KEPT
    deliberately: it backs duplicate-feed detection (the repo's own tests
    pin it) and the client follows redirects anyway - flagged in case a
    feed ever really distinguishes /feed from /feed/.
19. F28 HIGH partial progress resets fields - FIXED: core setProgress decodes
    position/duration/device as pointers and SetReadingProgressFields
    applies per-column patch semantics; omitted fields keep stored values,
    explicit zero/false applies. ABS POST/PATCH keep documented full-state
    semantics per the Audiobookshelf protocol.
20. F29 HIGH reader queue loses patches - FIXED (web): ProgressQueue is a
     serial merge-preserving retrying queue (extracted module + vitest
     suite); page + completion survive together, failures re-insert under
     newer patches, one delivery in flight, queue replaced on edition swap.
     The localStorage persistence and the server-side revision protocol
     (audit P9) LANDED 2026-09-18 - see the closure section below.
21. F30 MEDIUM cover download - FIXED: egress-guarded shared client,
    context-bound request, DecodeConfig + dimension/format budget, atomic
    temp+rename publication, apply summary carries a coverWarning.
22. F31 MEDIUM watcher stale descendants - FIXED: remove/rename unwatches
    the whole subtree; staleLibrary treats terminal statuses other than done
    as retryable (boot/sweep bound the rate).
23. F33 MEDIUM CI without frontend behavior - FIXED: vitest + committed
    suite for the progress queue, `npm run test` wired into the web CI job.
24. F34 MEDIUM ABS masks DB failures - FIXED: userPayload returns an error
    (no nil deref, no empty-history success), getProgress only answers the
    zero payload on ErrNotFound, deleteProgress reports failures.
25. F35 MEDIUM importer discovery - FIXED: audioPathsIn propagates walk
    errors and requires regular files (vanished dirs stay a per-book skip);
    applyFiles rejects nonregular planned files and records mtime_ns;
    applyUsers creates only after ErrNotFound. Confined opens against the
    target library root are NOT added: import libraries are placeholders by
    design and serving is already confined (F01), so a stray imported path
    fails to stream rather than escaping.

Deferred (standing families confirmed against current source, plus new
architectural items; `docs:` commit):

- **F03 processor inputs unconfined (HIGH): RESOLVED 2026-09-18**
  (`fix(media)` commit). Every ffmpeg/ffprobe child now receives its input
  as an already-opened descriptor (exec ExtraFiles -> child fd 3, argv
  `-protocol_whitelist fd[,file|pipe]` + `-fd 3 -i fd:`) resolved through a
  per-library os.Root: transcode (session-owned fd surviving the hw->sw
  fallback with offset reset), trickplay, embedded subtitle extraction,
  audio/video probe and scanner cover extraction. internal/procfd probes
  the installed binaries once per process (fd-option, fd-URL and
  /dev/fd/N forms, logged at startup) and every processor path fails
  closed when confinement is unavailable - there is no pathname fallback.
  Residual: CBR listing/extraction still hands the archive pathname to
  unar/unrar (they accept no descriptor input); inputs remain scan-time
  regular-file checks within the library, outputs stay under DataDir.
- **F04/F05 credential lifecycle (HIGH):** ABS playback-URL token binding +
  expiry and the Subsonic plaintext-password capture are the pass-6 F04/F09
  / pass-7 F02/F14 deferrals; no materially new evidence beyond what those
  records already weigh (deployed-client compat, corpus-pinned t+s
  behavior, separate-credential design needed).
- **F10/F12/F13/F17/F22 scanner identity + content versioning:** the
  title/author identity redesign (pass-7 F17), full-content hashes for
  relink evidence (pass-7 F24 documented the sampled-hash residual;
  accidental same-size/same-ends collisions require an admin-controlled
  library), sidecar dependency fingerprints and versioned derived-cache
  keys all hang off the same source-generation model - founder-level
  schema+repair decision. F17's multipart HLS timeline joins this family
  (single-file HLS - the overwhelmingly common case - is correct, and the
  first-party player has no per-file video picker to fall back onto).
- **F27 backup cover generations (HIGH): RESOLVED 2026-09-18** - pass-6
  F15 / pass-7 F05 closed with it (see the closure section below); the
  in-place durability fixes from those passes remain.
- **F29 reader queue revision protocol: REMAINDER LANDED 2026-09-18** -
  server-side per-row revision counter (migration 0014) with conditional
  progress writes (409 + current state on a stale base) and localStorage
  persistence of the queue's pending patch. See the closure section below.

Also noted: the audit's additional hardening items (transcode byte quotas,
subsonic Argon2-on-success under legacy auth, VAAPI filter-chain
validation, cross-adapter service extraction, SBOM/provenance) remain
unimplemented recommendations, consistent with pass-7 H01-H08 posture.

## Audit deferrals closed 2026-09-18 - pass-6 F09 digests + pass-8 F03 processor confinement

Both landed as one deliberate change each, verified against current source
first (commits `fix(auth)` aa87c5c, `fix(media)` 485cf8a):

- **F09 digests at rest (pass-6) + pass-7 F14 family:** migration 0013 adds
  the per-row marker; store.Open converges legacy plaintext tokens in one
  transaction before the server serves anything; issuance/lookup/parent
  check/revocation all compare digests. Live convergence verified on the
  demo database: 72/72 legacy rows digested, a pre-captured plaintext token
  authenticates after restart. Token expiry policy remains open (product
  decision, see the pass-6 F09 entry above).
- **F03 fd-passing confinement (pass-8, closes the pass-7 F03 residual
  note):** the audit's recommended shape landed - ExtraFiles descriptors
  rooted per-library, protocol_whitelist, capability probe, fail-closed.
  Live smoke on demo media: HEVC/MKV HLS transcode (hwaccel videotoolbox)
  and trickplay tiles generated entirely through descriptors; a rescan of
  the movies library stayed a warm no-op. Regressions cover mp4/mkv/
  audio-only + input-seek over descriptors, fallback fd reuse with offset
  reset, argv pathname-leak refusal, symlink escape via os.Root, and
  fail-closed refusals. CBR pathname residual documented in the pass-8
  entry above.

Verification: go vet ./... clean; go test ./... -count=1 -timeout 600s
green; go test -race on transcode/trickplay/scan/core/mediafs/procfd/auth
green; live demo-server smoke (startup probe logs, 72-token convergence,
HEVC transcode + segment fetch, trickplay tile, rescan 202->no-op,
health 200) (2026-09-18).

## Audit deferrals closed 2026-09-18 - pass-8 F27 generations + F29 revision protocol

Two more deliberate changes (DECISIONS 41-42 record the shapes and the
rejected alternatives):

- **F27 backup generations (closes pass-6 F15 + pass-7 F05, raised HIGH in
  pass 8):** snapshots publish as self-contained
  `backups/gen-<date>-<id>/` directories (snapshot.db + covers/), staged
  privately and renamed into place; retention counts generations and
  legacy libteca-*.db files in one budget, removes generations whole, and
  keeps the legacy shared covers/ until the last legacy backup is pruned.
  Existing backups keep working unchanged (read-side compatibility; no
  migration under the lock). Restore procedure documented per-generation in
  README. Regressions cover generation cover isolation across snapshot
  chains, legacy-layout survival, shared-covers lifetime, mixed
  gen+legacy prune ordering, publish-after-covers-fails, same-second
  isolation and clock-skew protection.
- **F29 revision protocol (pass-8 F29 remainder + audit P9):** migration
  0014 adds progress.revision; every progress write bumps it (reader
  upserts, audio SetProgress, session close) so any concurrent writer
  invalidates held bases across faces. POST /progress/{id} with `revision`
  applies conditionally and answers 409 + current server state on a stale
  base; absent revision keeps last-writer-wins for the non-reader faces
  and the pagehide beacons. The web ProgressQueue persists its pending
  patch (plus the in-flight batch until ack) in localStorage per edition -
  a reload replays undelivered patches, safe under the revision check -
  and the sender rebases + max-merges on 409 (monotonic page/percent,
  locator follows the winner, explicit finished intent survives).
  Regressions: store stale-base reject/apply + legacy-write advancement +
  post-delete lineage; API 409-shape protocol test; vitest persistence,
  in-flight survival, throwing-storage and merge-matrix suites.

Verification: go vet ./... clean; go test ./... -count=1 -timeout 600s
green; go test -race on store + core green; web npx tsc --noEmit +
npm run test (vitest) + npm run build clean (2026-09-18).

## Housekeeping (2026-09-17) - pass-8 F32 resolved, gofmt drift cleared

- **F32 container USER (MEDIUM): RESOLVED** (`fix(docker)` commit). The
  runtime stage creates a dedicated uid/gid 10001 identity (addgroup/adduser,
  chown /data) and runs USER 10001:10001. Nothing in the image needs root:
  every writable path (database, covers, backups, transcode/subtitle caches,
  .server.lock) lives under /data. The Dockerfile comment and
  deploy/README.md document the one-time `chown -R 10001:10001 <data>`
  bind-mount migration (the application never chowns host paths itself);
  named volumes initialize from the image and need nothing; read-only media
  mounts keep working. Pass-7 H02 (container USER) closes with it.
- **gofmt drift cleared** (`style` commit): gofmt -w over the six drifted
  files (core/games_face_test.go, jellyfin/ws_test.go, opds/cbz.go,
  meta/thegamesdb.go + test, transcode/transcode.go) - pure formatting,
  verified against git diff. The earlier per-pass gofmt-dirty notes above
  are historical.

Verification: go vet ./... clean; go test ./... -count=1 -timeout 600s
green; go test -race on transcode/podcast/scan/core/watch/auth green; web
npx tsc --noEmit + npm run test (vitest) + npm run build clean (2026-09-17).

## ChatGPT audit pass 9 (2026-09-19, AUDIT-CHATGPT-9.md) - 34 of 37 fixed, 3 deferred

Fixed (verified against source first; `audit 9-NN` commits):

1. F01 HIGH cross-account persisted progress - FIXED (web): queue storage
   scoped to the /me user id (never the token, never unscoped); identity
   cleared on sign-out/rotation; sends pause when identity changes;
   legacy unscoped entries retired, not imported.
2. F02 HIGH persisted patch loses its base - FIXED (web): the durable
   record is {baseRevision, patch} and the queue owns the base per
   (user, edition); replays carry their ORIGINAL base and the base moves
   only on ack/409. Receipt-table idempotency intentionally omitted:
   uncertain retries already re-present the same base, so the server's
   conditional write turns any replay into 409 + idempotent max-merge
   (bounded double-apply of explicit finished intent is the documented
   merge policy, DECISIONS 42).
3. F03 MEDIUM conflict winner forgotten - FIXED (web): remote high-water
   mark (seeded from loaded progress, fed by every 409) folds into every
   automatic save until the reader genuinely passes it.
4. F04 MEDIUM locator-only conflicts - FIXED (web): an unordered
   locator-only patch never defeats a positioned server state; only
   explicit finished intent survives; EPUB saves locator+percent together
   once the location index exists.
5. F05 HIGH ABA across delete/recreate - FIXED (store, migration 0015):
   progress rows tombstone (deleted flag + revision bump) instead of being
   removed; conditional creation (base 0) is INSERT ... DO NOTHING (never
   applies onto any existing row) and positive bases are pure conditional
   UPDATEs (never insert onto an absent row - base 99 on absence used to
   insert at revision 1); every writer clears the tombstone; lists skip
   tombstones; the single-row reader still exposes the tombstone revision
   so a fresh client restarts from a real base.
6. F06 MEDIUM unconditional beacons - FIXED (web): pagehide/visibility
   beacons carry patch + base revision (conditional); a rejected beacon is
   not a loss - the patch stays durably stored and replays on next open.
   Supersedes the "unconditional beacons by design" half of DECISIONS 42
   (recorded there; the last-writer-wins semantics for ABSENT revision on
   the compatibility faces are unchanged).
7. F07 MEDIUM queue teardown/edition-shared revision state - FIXED (web):
   the base revision lives inside the per-(user,edition) queue instance,
   the queue stops on unmount, and requests carry a deadline so a late
   response cannot wedge or cross-contaminate a replacement edition.
8. F08 MEDIUM two tabs erase each other's pending work - FIXED (web):
   per-instance storage records; a mount absorbs foreign pending records
   (max-merge, oldest base) and acknowledgements remove only the
   instance's own record.
9. F09 MEDIUM malformed/permanent failures retried forever - FIXED (web):
   strict per-record validation with quarantine of malformed entries;
   shouldRetry policy - 400/401/403/404/410 drop their patch (terminal)
   instead of blocking every later save; observer exceptions cannot break
   queue housekeeping.
10. F10 MEDIUM stalled request wedges the queue - FIXED (web): progress
    requests run under a 15s deadline (headers + body) honoring the
    reader teardown signal.
11. F11 MEDIUM PDF bypasses the saver - FIXED (web): page/completion
    changes route through useProgressSaver (persistence, retries,
    revisions, unload handling); page input clamps to the known count.
12. F12 MEDIUM explicit zero/empty cannot clear - FIXED (core):
    duration:0 and device:"" apply as presence-decoded values.
13. F13 MEDIUM SetReadingProgress not full-state - FIXED (store):
    Position/Duration/Device/Finished mask.
14. F14 MEDIUM core position policy gap - FIXED (core): positions run
    through store.ValidPosition against the EDITION's duration (the
    client-supplied duration is no longer the bound); pages bounded to
    2^53-1. Aligns core with the ABS/Jellyfin faces (pass-7 F41).
15. F15 MEDIUM DB failures as 404/fabricated 409 - FIXED (core/hls):
    work/setProgress/getProgress/scanJob/playback/thumbs lookups answer
    404 only for ErrNotFound and 500/503 otherwise; a failed post-CAS
    reread is 503 instead of a synthetic revision-zero 409 baseline.
16. F16 LOW revision zero in list readers - FIXED (store): GetProgress,
    UserProgressList, ReadingListByUser select revision (and skip
    tombstones).
17. F17 MEDIUM browser archive budgets - FIXED (web): CBZ/EPUB downloads
    bounded (512 MiB declared and streamed), 10k entry cap, per-page
    extraction byte cap, CBZ abort controller.
18. F18 LOW failed CBZ page stuck loading - FIXED (web): failed pages
    render a retryable error; retry clears only the failed slot.
19. F19 MEDIUM EPUB location cache keyed by edition id - FIXED (web):
    cache key is the content SHA-256 + chunk size; skipped entirely when
    Web Crypto is unavailable; legacy id-keyed cache retired.
20. F20 MEDIUM SSE send-on-closed/lost terminal - FIXED (core): metaRun
    subscribe registers AND sends under the closure lock, publish refuses
    after close, finish never drops the terminal event (drops an obsolete
    running one instead).
21. F21 MEDIUM optional metadata failures silent - FIXED (core):
    chapters/genres/episodes failures surface as summary warnings;
    applyChapters/distributeChapters propagate errors; rows.Err checked
    before writes.
22. F22 MEDIUM chapter check-then-write race - FIXED (store/core):
    ReplaceChaptersIfUnchanged compares-and-swaps (null-safe chapters IS
    ?); a concurrent authoritative write is preserved, not clobbered.
23. F23 MEDIUM chapters lose file-spanning continuation - FIXED (core):
    distributeChapterMath intersects every chapter interval with every
    file interval (identity + title preserved across the continuation);
    unknown durations and invalid intervals are errors, not guesses.
24. F24 MEDIUM covers stored as mislabeled/possibly-truncated bytes -
    FIXED (core): full decode + white-matte JPEG normalization at the
    .jpg path (DecodeConfig alone accepted header-valid truncations);
    existing cache entries are not rewritten (see residual note).
25. F25 MEDIUM snapshot/cover-writer consistency - FIXED (store/scan/
    podcast/core, new internal/assets): Snapshot holds the exclusive
    cross-process covers lock across VACUUM INTO + copy + publication;
    every cover mutation (scan book/game/video/NFO + ffmpeg extraction,
    provider publication, podcast fetch/delete) holds it shared over the
    file+DB-reference pair; lock order .backup.lock -> .covers.lock
    everywhere, writers never take .backup.lock.
26. F26 MEDIUM missing cover root backs up as success - FIXED (store):
    requireCoverRoot fail-closed at snapshot AND copy; only an initialized
    empty directory is a valid zero-asset source (tests updated).
27. F27 LOW copyTree TOCTOU open race - FIXED (store): os.Root-pinned
    nonblocking opens with stat-after-open regular-file checks; swapped
    symlinks/FIFOs are rejected or contained.
28. F28 LOW abandoned stages + unsynced retention - FIXED (store):
    crashed .libteca-stage-* dirs removed under the backup lock before a
    new stage; retention directory-syncs after deletions.
29. F30 LOW last-seen UPDATE on every request - FIXED (auth): the SELECT
    carries last_seen_at and the UPDATE is submitted only when stale
    (SQL-side condition retained for concurrent requests).
30. F31 MEDIUM dead encoder serves incomplete segment - FIXED
    (transcode): independent_segments+temp_file so final names only
    appear complete; readiness = published at final name AND listed by
    the playlist (exact URI-line compare); isDead() no longer makes
    unlisted bytes servable.
31. F32 MEDIUM cleanup holds the manager lock - FIXED (transcode):
    closing reservation under the lock, kill+RemoveAll outside it via a
    shared finalize; timed-out kills keep their reservation for the
    reaper; closing sessions count against capacity and refuse id reuse;
    session spawn/open/mkdir happen outside the manager lock with a
    reserved placeholder; MkdirAll errors checked.
32. F35 LOW VP8 excluded from its own WebM branch - FIXED (hls):
    browserPlayable admits VP8 inside WebM (container-restricted) with
    accepted audio.
33. F36 LOW CI supply-chain - FIXED: actions pinned to review-resolved
    commit SHAs (annotated), dependabot for actions/go/npm, gofmt gate
    (already caught drift in this pass), npm audit --omit=dev at high.
34. F37 LOW cover publication not crash-durable - FIXED (core):
    publishJPEG fsyncs the file, renames, and fsyncs the directory before
    the caller commits the DB reference.

Deferred (standing families, confirmed against current source):

- **F29 MEDIUM media URLs expose the general bearer credential:** same
  credential-lifecycle family as pass-6 F04/F09 and pass-7 F02/F14 -
  query-token authentication is what media elements/EventSource can send,
  and scoped short-lived media tickets bound to a live parent token are a
  protocol migration needing its own compatibility decision. The standing
  deferral covers it.
- **F33 MEDIUM transcode disk budgets:** joins pass-7 H05 (disk/memory
  budgets) as an open hardening item. A wrong byte cap breaks legitimate
  long-title playback (segments are deliberately retained for seeking -
  deleting advertised segments changes the player contract), so the
  per-session/aggregate budget needs real playback measurements, not a
  guess. Session count + idle TTL + startup wipe bound steady state.
- **F34 MEDIUM edition-level playback limited to the first file:** the
  multipart timeline family already deferred with pass-8 F10/F12/F17
  (single-file HLS is correct; the first-party player has no per-file
  picker to fall back onto). A selected-file + edition-offset ticket is
  the same architectural work.

Also noted: existing cover cache entries written before the F24
normalization keep their original bytes (a re-download short-circuits on
the existing file); they age out as works are re-identified. Token
expiry policy (pass-6 F09 remainder) and the subsonic plaintext secret
(F04 family) remain product decisions, unchanged.

Verification: go vet ./... clean; gofmt -l cmd internal clean; go test
./... -count=1 -timeout 600s green (22 pkgs); go test -race on
store/core/scan/podcast/transcode/auth green; web npm run test (vitest,
22 tests) + npx tsc --noEmit + npm run build clean (2026-09-19).

## Reliability repair follow-up (2026-10-02)

Implemented locally against fbf2f20, with regression tests; not deployed:

- Reader recovery no longer uses a reconciliation delta as the accumulated
  durable state. Equal/lower pending positions retain the complete winner;
  failed replacement persistence keeps the original records; changed foreign
  records are not deleted after recovery.
- Close/hide and ordinary saves share high-water reconciliation and conditional
  bases. Close includes unresolved in-flight state. Successful saves update
  the high-water mark; a conflict discarded as already covered still advances
  the revision baseline.
- Video episode/file sessions are keyed, keeping old media events and delayed
  acknowledgements with their original edition. Writes are serialized with a
  request deadline, and failed completion stays due rather than being changed
  to unfinished by teardown.
- Reset tombstones are excluded from resume, library filters, search/percent,
  Next Up, OPDS in-progress, and Subsonic recent/frequent queries. Baseline
  progress reads retain tombstones and their revision. Store/HTTP regressions
  cover ABS reset through core discovery and preservation of another user.
- Library browsing now exposes 200-item pages, load-more/retry, and honest
  loaded counts. Generation/cancellation guards discard old requests after
  library/filter/sort changes. Child rows and percent queries are restricted
  to the selected IDs, with stable order ties. Offset paging is not a snapshot
  during concurrent scans or deletions; refresh remains necessary then.
- Cross-library edition moves and work merges fail inside their transaction.
  New-title creation and reparenting are atomic. Same-library moves remain
  supported, and regression tests assert source streaming survives rejection.
  Existing wrong-root/collapsed records are not repaired. The standing source
  identity migration remains deferred; see docs/source-identity-proposal.md.
- The Go CI job builds real webdist before vet/test; make test depends on the
  locked frontend build. The dependency audit gate is retained unchanged.

Validation: 65 frontend tests, TypeScript, and make web pass locally. Eight
reader-hook regressions all fail against the untouched baseline and pass on
this patch. Exact source SQL passes against all 15 migrations, including a
10,000-work paging fixture with indexed child-query plans. Go regression tests,
go vet, gofmt, and race tests remain unrun: this environment lacks the required
Go 1.26.6 compiler. No full browser, real-client, production-library, or complete
security verification is claimed.

Still open:

- Dependency compatibility: npm audit --omit=dev --audit-level=high still fails
  on the existing epubjs/xmldom and devalue dependency paths. Do not force the
  suggested breaking EPUB upgrade; validate restore/CFI/navigation against a
  representative EPUB corpus before changing that reader dependency.
- Audio queue replacement is resolved in the continuation below, including
  identity-scoped media-event and detached-node coverage.
- Pass-9 F17 archive resource limits are partial: post-allocation page-size
  rejection does not bound decompression memory, and EPUB body buffering needs
  a true incremental bound. The prior blanket closure is not sufficient.


## Reliability continuation (2026-10-02)

Local fixes on the same fbf2f20 baseline, integrated with the preceding repair:

- Audio queue identity: a change to ordered file IDs creates a fresh media
  session even at the same index. Teardown pauses the original element and
  saves its position through its original callback. Detached elements cannot
  report completion, play, pause, or errors into the replacement session.
  Metadata-only rerenders retain playback and controller behavior.
- OPML import: the UI follows the asynchronous server status instead of reading
  nonexistent synchronous counts. It reports completion only at `done`, derives
  existing subscriptions from the terminal totals, prevents duplicate starts,
  resumes a running import on reopening, and refreshes subscriptions after
  returning from a podcast detail. Lost server status is an explicit incomplete
  result rather than success.
- Scan monitoring: rejected/unconfirmed starts surface an error; 409 responses
  with a job ID follow that job. Idle closes observation. Generation guards
  discard old start, SSE, and polling responses; fallback polling stays pinned
  to the accepted job ID and uses serial requests.
- Both new status pollers retry network failures and HTTP 408/429/5xx. Requests
  have a 15-second headers-and-body deadline and abort their fetch on timeout;
  permanent status errors terminate observation without reporting success.

Validation: 99 combined frontend tests pass (65 preceding + 34 new), TypeScript
and make web pass, and the prior source-SQL/CI-order checks pass against this
combined tree. Twenty initial continuation regressions were run on untouched
source: 18 fail and two compatibility controls pass. Independent review found
and verified fixes for detached audio completion, transient HTTP retry, detail
navigation refresh, and stalled polling. The new jsdom dependency is test-only.

Coverage is bounded to audio queue replacement and the OPML/scan UI lifecycle.
No Go source changed in this continuation. Go compilation/tests/race checks,
real browser media behavior, real-device playback, importer data conversion,
scanner identity design, archive resource bounds, and full security review
remain unverified or deferred as described above. No production or remote
repository changes were made.

## Systematic correctness and executable validation (2026-10-02)

The expanded local review is recorded in docs/systematic-issue-register.md
and its JSON companion, with scope in docs/review-source-inventory.md and
validation in docs/review-validation.md. These records supersede the earlier
compiler-unavailable status for this working patch; historical audit claims
above retain their original dates and scope.

New repairs cover tagged audiobook order and exact persisted resume during
legacy order repair; warm audio cover ownership; ABS optional-schema, media-ID,
duration-map and unnumbered-episode conversion; transactional import file
batches; podcast cancellation; incremental reader byte caps; CBZ viewport,
spread and async lifecycle; EPUB iframe keyboard handling and indexing errors;
PDF resume/metadata state; search cancellation; playlist/podcast keyed media,
within-tab endpoint write ordering and revisit resume; error-shaped load
responses; downgrade referential integrity; and locked/complete build gates.

The specific pass-9 F17 CBZ post-allocation and EPUB body-buffering gaps are
fixed with stream limits. The broader archive resource family remains partial:
EPUB dependency decompression/DOM expansion, image-pixel decoding, aggregate
cache memory, ZIP directory parsing and extraction disk quotas are not bounded
by these byte-stream repairs. No dependency upgrade was forced.

Legacy audiobook order repair preserves file/edition identity and file-relative
progress, recalculates changed cumulative positions transactionally, and bumps
only their revisions without changing user activity timestamps. Pause/reopen
playback around an order-changing scan: live clients can retain the old queue.
Historical playback sessions and arbitrary retag/source regrouping are not
rewritten. Complete imports remain non-atomic across stages despite file-batch
rollback. Audio/video durable offline progress and multipart timelines remain
open, as do the physical-source migration and existing-data repair proposal.

The focused authentication/security review remains excluded by the prior
platform restriction and was not retried. Ordinary pre-existing automated tests
are not a substitute for that assessment. Gate W, real browsers/devices,
representative media/EPUB and live compatibility-client corpus acceptance are
not inferred from synthetic/component tests. No production data or remote
repository was changed.

The final independent reconstruction run exposed a timing-dependent watcher
regression fixture, not a confirmed lost-ingest defect. Runtime-library
registration now uses a complete archive fixture and explicitly waits for
watch registration. A separate regression forces an initial partial-archive
scan failure, completes that same file, and requires a later successful
ingestion with one present file plus clean shutdown. Both watcher regressions
passed 20 race-enabled repetitions. See LT-119 and the final validation logs.

## Whole-import and media reliability follow-through (2026-10-02)

LT-120 closes the earlier whole-import atomicity limit: ABS and Kavita apply
all destination stages in one transaction, including existing-row revisions
and playlists. Failure-injection, deferred commit failure, read-only source,
dry-run, repeat import, and concurrent import fixtures cover the boundary.
The foreign source itself is not snapshotted by this change; real schema and
large-import lock-duration acceptance remain open.

LT-121 repairs primary-audio save ordering, login fencing, zero-position seeks,
failed completion retention and deadline-bounded in-tab retry. LT-122 adds
ordered multi-file playlist audio with cumulative resume/seeks and final-file
completion. LT-124 prevents a slow old work response from replacing a newer
route. These do not close durable offline media, generation-aware playback,
ambiguous network/cross-device conflicts, or multipart video/HLS.

LT-123 passes cancellation through CBR listing/extraction, reaps the launched
extractor, rejects failed partial bytes, checks cancellation before storing,
and cleans temporary output. Synthetic fake-extractor fixtures cover these
conditions and existing byte limits. This does not establish real unrar/unar
compatibility, process-tree quotas, or ongoing extraction-disk bounds.

The remaining implementation contracts and safe next steps are recorded in
docs/media-consistency-proposal.md. The prior security-review restriction,
real-library/browser/client/container/hardware acceptance and publication
boundaries remain unchanged. Existing dependency update pull requests should
be coordinated rather than duplicated or blindly included in this patch.

## Architecture gate preparation (2026-10-03)

A standalone read-only source inventory and isolated executable contract models
now prepare LT-P01/P04/P05; generated resource measurements prepare LT-P06.
These are not deployed fixes, source repair, durable browser storage, a media
schema migration, multipart HLS integration or selected production quotas.
See docs/source-inventory-command.md, docs/media-intent-contract.md,
docs/multipart-video-contract.md and docs/resource-measurement.md. Current
runtime behavior and all real-data/browser/client/platform acceptance gates
remain unchanged. No production input, external publication or excluded
focused authentication/security review was performed.

## GPT audit pass (2026-10-04, AUDIT-CHATGPT-10.md) - 4 fixed, 7 confirmed-deferred

AUD-01..AUD-11 verified against current source at 50fc978 before acting.

Fixed:

- **AUD-01 media URLs expose the general bearer credential (HIGH):** the
  standing F29 deferral is narrowed to the compat faces. The core face now
  authenticates media-element/EventSource/beacon traffic with an HttpOnly
  SameSite=Strict cookie (Path=/api/core) set at login and re-established by
  GET /me, cleared at logout; the auth middleware refuses query-token
  authentication on media routes (core.MediaRequest classifies stream,
  covers, subtitles, hls, thumbs, download, export-opml, episode stream,
  progress, scan/refresh-meta SSE) so no credential rides in a first-party
  URL; HLS playlist rewriting no longer embeds the caller's token in segment
  URLs. The web media() helper and both EventSource builders carry no token.
  The cookie value is still the account's general token - short-lived scoped
  media tickets remain the deeper redesign, deferred with the F04/F09
  credential-lifecycle family. ABS/Jellyfin/Subsonic/OPDS faces unchanged.
- **AUD-07 ABS /play ignores malformed JSON + RNG failure:** shared bounded
  decodeBody (empty body stays a valid zero value for client compat;
  malformed 400; over-limit 413 via MaxBytesReader; trailing document
  rejected); newPlaySessionID entropy failure is a 500 with no session row.
- **AUD-08 ABS /session/{id}/close treats malformed JSON as zero-position
  close:** same decoder; rejected closes leave the session open and progress
  untouched.
- **AUD-09 password hashing panics on entropy failure:** Hash returns an
  error (readRandom seam), HashRequest/InitAdmin/importer propagate it, and
  the dummy hash is a checked-in format-valid Argon2id constant (verifying
  its published non-account password, which is the property that keeps the
  unknown-user timing burn on the full KDF path).

Confirmed against source, deferred (standing recorded families; see the
deferral sections above and docs/source-identity-proposal.md /
media-consistency-proposal.md):

- **AUD-02 multipart first-file-only playback:** pass-8 F10/F12/F17 family;
  decision 49 keeps the timeline contract test-isolated until generation
  semantics land.
- **AUD-03 trickplay cache has no content generation:** pass-8 F44 versioned
  cache keys hang off the same generation model.
- **AUD-04 physical source ownership via work.library_id:** decision 45/49;
  migration proposal awaits the founder gate.
- **AUD-05 durable media progress + podcast revision/CAS:** decisions 46-48
  record the in-tab boundary; media-intent contract stays isolated (49).
- **AUD-06 transcode/trickplay byte budgets:** decision 49 requires measured
  representative workloads before any quota; pass-9 F33.
- **AUD-10 subsonic plaintext password capture:** F04 family deferral
  (client compat, dedicated app-secret migration is a founder decision).
- **AUD-11 (library,title,author) work identity:** pass-7 F17 family.

Verification: gofmt clean over changed files; go vet ./... clean; go test
./... -count=1 green; go test -race on store/core/abs/auth/transcode/
trickplay/server green; web npx tsc --noEmit + npm run test (324 tests) +
npm run build clean (2026-10-04).

## GPT audit follow-up pass (2026-10-04, AUDIT-CHATGPT-11.md) - 5 fixed

Review of the audit10 delta at `a15c17f` plus adjacent ABS/auth/importer/
session code. Register read first; standing deferrals not re-reported.

- **A11-01 required-vs-optional body contract:** decodeBody now takes
  allowEmpty; applied to sessionSync, postProgress and podcast episode
  progress POST. Empty play/close stays 200 (audit10 client compat);
  oversized bodies now 413 (was 400).
- **A11-02 non-atomic ABS progress update:** new transactional
  UpdateSessionWithProgress mirroring CloseSessionWithProgress, shared
  setProgressAt UPSERT, edition loaded before mutation with surfaced
  errors; dead non-atomic UpdateSession removed.
- **A11-03 KDF-busy misclassification:** only ErrKDFBusy is 429 with
  Retry-After; entropy/other errors are 500; cancellation sends no
  response. hashRequest seam added for tests.
- **A11-04 temp-password entropy:** tempPassword via readRandom seam, 16
  bytes (32 hex, was 12); propagated through applyUsers; whole import
  rolls back on failure.
- **A11-05 cookie Secure flag:** explicit --secure-cookie /
  LIBTECA_SECURE_COOKIE operator control threaded main->server->core
  cookie set/clear; README deployment note added. No X-Forwarded-Proto
  inference.

Verification: gofmt clean; go vet ./... clean; go test ./... -count=1
22 pkgs ok; go test -race on auth/importer/store/abs/core ok; web tsc +
324 tests + build clean (2026-10-04).

## GPT audit pass 12 (2026-10-04, AUDIT-CHATGPT-12.md) - 3 fixed + 1 auditor-missed

Regression/gap review on current main after audit11 (2fdef07). Standing
deferrals not re-reported.

- **A12-01 cookie accepted for any method / no origin defense:** media
  cookie routes are now GET/HEAD-only; a new MediaMutationRequest
  classification gates cookie-authenticated mutations (POST /progress
  beacon, DELETE /hls stop - an auditor-missed mutation found during the
  fix) behind sameOrigin (Sec-Fetch-Site + Origin host == Host, no
  forwarded-header trust); query tokens refused on both classifications.
- **A12-02 sync/close position validated against client duration before
  server-duration substitution:** both bypass vectors closed via shared
  sessionDuration (server authoritative when >0, else client, 30-day cap).
- **A12-03 timeListened validation broken in postProgress:** negative/
  absurd values now 400 with rows untouched; store-level
  validateListenedDelta in all three close/update paths (defense in depth).

Verification: gofmt/vet clean; go test ./... 23 pkgs ok; race on
abs/core/auth/store ok; web tsc + 324 tests + build clean (2026-10-04).
TLS-proxy deployments without --secure-cookie fail closed on beacons
(covered by persisted-queue replay) - noted for operators.

## Post-audit12 design decisions (2026-10-04) - cookie shrunk to read-only

Two deliberate design changes superseding parts of the audit12 entry above:

- **Media mutations went Bearer.** POST /progress and DELETE /hls now
  require the Authorization header; the web reader beacon switched from
  sendBeacon (cannot carry headers) to a keepalive fetch with Bearer, and
  the HLS stop carries Bearer on its keepalive fetch. The audit12
  MediaMutationRequest classification and origin-gating machinery is
  DELETED as planned simplification: the cookie exists for technology that
  cannot set headers (media elements, EventSource/SSE) and is now valid
  only on GET/HEAD media routes and SSE. Consequence: ?token= query auth
  follows the generic middleware fallback on the two mutation routes
  (query refusal remains on media routes proper).
- **Per-file sha256 exposed in the games payload** (migration 0016,
  scan-time hash for new/changed files + on-demand persisted backfill for
  legacy rows) per the omilator client contract update - omilator now
  verifies downloads against it.

Verification: gofmt/vet clean; go test ./... green (one pre-existing
unar-availability flake in scan, green isolated + baseline-stashed);
race on core/auth/store green; web tsc + 324 tests + build clean.

## 2026-10-06 — Subagent audit pass C (AUDIT-CHATGPT-13): closed

- **C-01 (fixed)**: full-ROM hashing no longer runs inside the BEGIN
  IMMEDIATE transaction — multi-GB sha256 could hold SQLite's write lock
  past busy_timeout, failing concurrent progress saves and other
  libraries' parallel scans (SQLITE_BUSY reproduced). Hashes hoisted;
  regression proves concurrent writes + scans succeed during a slow hash.
- **C-02 (fixed)**: a changed file whose re-hash failed kept its stale
  sha256 forever (coalesce + warm-skip). Rows now retry when the stored
  hash is NULL; update writes sha256 directly.
- **C-03 (fixed)**: lazy backfill in GET /works/{id} gained request-context
  cancellation, a 2-slot budget, and per-file singleflight.
- **C-04 (fixed)**: podcast detail/refresh/patch/episode-progress DB
  failures now surface as HTTP 500, never 200-with-error-body.
- **C-05 (fixed)**: refresh-meta/OPML jobs use the scan context (cancel
  branches live again; WaitJobs no longer stalls shutdown).
- **C-06 (fixed)**: ffmpeg subtitle extraction output capped at the
  subtitle cache limit (child killed on overflow).
- **Bonus (fixed, DECISIONS §50)**: bounded writers embedded bytes.Buffer,
  promoting ReadFrom — the byte cap never fired (embedded io.Copy growth);
  latent in the pass-7 ffprobe boundedBuffer too. Both now compose the
  buffer; probe cap has its own regression.

Verification: build/vet/gofmt clean; go test ./... 23 pkgs green
(discrimination checks run for C-01/C-02/C-06); web tsc clean + 324 green.
