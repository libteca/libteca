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
  dropped on rotation (already the case).
- **F09 token digests at rest + expiry policy (MEDIUM):** storing sha256
  instead of raw values requires rewriting every token reader/writer plus a
  data migration in one atomic pass (half-applied = lockout), and an expiry
  policy for interactive vs device tokens is a product decision. Revisit
  together with F04's credential work.
- **F15 per-snapshot cover generations (MEDIUM, durability half fixed):**
  switching backups/ to generation directories changes the on-disk layout,
  restore procedure and retention docs - a product decision. Durability
  (fsync, checked closes, non-regular rejection) is fixed in place.
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
