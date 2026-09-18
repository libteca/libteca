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
- **F09 token digests at rest + expiry policy (MEDIUM):** storing sha256
  instead of raw values requires rewriting every token reader/writer plus a
  data migration in one atomic pass (half-applied = lockout), and an expiry
  policy for interactive vs device tokens is a product decision. Revisit
  together with F04's credential work. Audit 7's F14 (binding ABS playback
  session URLs to their issuing token plus a fixed expiry) is the same
  credential-lifecycle family; that deferral covers it too.
- **F15 per-snapshot cover generations (MEDIUM, durability half fixed):**
  switching backups/ to generation directories changes the on-disk layout,
  restore procedure and retention docs - a product decision. Durability
  (fsync, checked closes, non-regular rejection) is fixed in place. Audit
  7's F05 adds materially to the in-place half - publication now happens
  only after the covers copy completes, per-cover copies are atomic
  replaces, and the whole snapshot/retention operation holds a cross-process
  flock - so the residual is purely the shared-covers layout decision.
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

- **F05 backup generations:** DB snapshots publish only after the covers copy
  completes and each cover is replaced atomically; generations still SHARE
  one covers/ directory (pass-6 F15 deferral above).
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
    localStorage patch persistence and the server-side revision protocol
    (audit P9) are NOT built - cross-device ordering needs a coordinated
    API+client change.
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

- **F03 processor inputs unconfined (HIGH):** fd-passing ffmpeg inputs
  (ExtraFiles + protocol_whitelist fd, capability probe, fail-closed
  fallback) across transcode/trickplay/subtitles is an architectural
  refactor of the process factories and session lifecycles; the pass-7
  residual note stands (scan-time regular-file check bounds it). Revisit as
  one deliberate change, not per-caller patches.
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
- **F27 backup cover generations (HIGH):** pass-6 F15 / pass-7 F05
  deferral stands (on-disk layout + restore-doc product decision); the
  in-place durability fixes from those passes remain.

Also noted: the audit's additional hardening items (transcode byte quotas,
subsonic Argon2-on-success under legacy auth, VAAPI filter-chain
validation, cross-adapter service extraction, SBOM/provenance) remain
unimplemented recommendations, consistent with pass-7 H01-H08 posture.

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
