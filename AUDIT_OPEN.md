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
