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
