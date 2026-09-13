# libteca — recorded decisions

Decisions are append-only. Each entry: date, decision, why, what would reverse it.

## 2026-09-09

1. **Go module `github.com/libteca/libteca`** (org owned). Repo root = the
   umbrella folder; `libteca-site/` stays gitignored inside it.
   Reverse: never expected.

2. **SQLite driver: `modernc.org/sqlite` (pure Go), not `mattn/go-sqlite3`.**
   CGo-free keeps the single-binary cross-compile story (linux/amd64, arm64,
   darwin) intact with no toolchain矩阵. Cost: some throughput vs CGo —
   acceptable; progress writes are trivial, reads are cached.
   Reverse: if scan throughput on large libraries proves CPU-bound on the
   pure-Go driver, benchmark first (SPEC task 4 gate), swap second.

3. **Migrations: goose, embedded; queries: sqlc.** House-adjacent, boring,
   compiled SQL. Reverse: no.

4. **ABS API contract = recorded traffic corpus, not docs.** SPEC §6. Version
   pin recorded here after task 10 capture (expected: server 2.x line, app
   0.9.x+). Reverse: no — this is the method.

5. **Web UI: Neutron app (static preset, Preact) in `web/`, embedded via
   `embed.FS`.** Revised same day from "plain Vite" — the no-Neutron-coupling
   rule (PLAN §2) guards the Go server's runtime, not the web build tool; a
   static bundle is a static bundle. Dogfooding matches the house pattern and
   exercises Neutron's static preset under a new embed target. Constraint: the
   UI is client-side only — no loaders (our data is per-request from the Go
   API, not build-time); all data flows through `fetch('/api/...')`. Dev flow:
   `neutron-ts dev` proxying to the Go server on :8096.
   Reverse: build-tool swap only; the API contract is the UI's real seam.

6. **Slice 0 metadata = embedded tags + filename parse + manual edit only.**
   Audible/OpenLibrary providers land with Slice 1 prep. Rationale: G1 gate is
   scanner correctness on real files; network matching multiplies failure
   modes. Reverse: if tag/filename quality on Tyler's library is too poor to
   browse, provider work moves INTO Slice 0 as task 4b.

7. **socket.io (ABS row 15) is task 11 stretch, not gate.** Apps degrade to
   polling; live cross-device sync can trail the exit gate. Reverse: if phone
   testing shows the app hard-requires socket for progress sync (check during
   task 10 capture), it becomes blocking.

8. **ABS item id == edition id** (SPEC §4). Locked early because Slice 4's
   work-unification must not break emitted ids.

## Open (unpinned)

- ABS server/app version pin (after corpus capture). Code currently emits
  `"Version": "2.19.4"` unpinned.
- Jellyfin server version pin (after corpus capture). Code currently emits
  `"Version": "10.10.0"` unpinned.

Closed here, not in Open: cover tiles = first-party cloth UI (`cover.tsx`);
backup = `libteca backup` (15g); neutron-go published (17).

## 2026-09-09 (evening) — revisions

9. **Server rides neutron-go from Slice 0, not Slice 1.** Founder call after
   discussion — full akiroo pattern (neutron-go backend + Neutron TS UI).
   The isolated-Slice-0-risk argument lost to never-migrating. Mitigations:
   ABS face uses `HandleFunc`/`Mount` raw handlers (compat faces override the
   framework contract — RFC 7807 errors must not leak into ABS/Jellyfin/
   Subsonic shapes); server runs `App.Handler()` on its own `http.Server`
   because neutron's `Run` default `WriteTimeout` would abort long Range
   streams. Dependency via relative replace (see Open above).
   Reverse: publish neutron-go, drop the replace.

10. **sqlc deferred; store hand-rolled on database/sql.** sqlc/goose CLIs not
    installed locally; goose runs as a library. Adopt sqlc when query volume
    justifies the toolchain.
    Reverse: mechanical adoption later.

11. **Slice 1 core pulled into the Slice 0 build session (2026-09-09).** Built:
    video/music scanners (movies, TV S/E parsing, music albums as works with
    track editions), the Jellyfin face v1 (Views/Items/Seasons/Episodes/
    PlaybackInfo with direct-play vs HLS decision, /Videos stream + Range,
    ffmpeg HLS transcode sessions with reaper, /Audio universal, Sessions
    progress, Images), and first-party players in the web UI (video with
    resume/next-episode, TV episode list, music track bar, audiobooks).
    Still owed from SPEC task list: Jellyfin websocket (remote control),
    trickplay, /Shows/NextUp real implementation, per-library scan locking
    (currently one global scan at a time), corpus capture for both faces.
    ABS face shape-verification against real app traffic remains the gate.

## 2026-09-09 (session 2)

12. **Owed items 3+4 built in parallel; websocket demoted to optional.**
    Founder call: the Jellyfin `/socket` is optional (nice-to-have remote
    control), not a gate — build it only after corpus traffic exists.
    Recorded this session: (a) per-library scan locks replace the global
    atomic — scan_jobs table (migration 0003), concurrent different-library
    scans, 409 same-library, SSE progress events, crash-restart marks
    zombie jobs `interrupted`; (b) NextUp real, ordering key
    (season_num, episode_num) on editions; (c) trickplay generated LAZILY
    on first request (singleflight, data/trickplay cache), not at scan time
    as PLAN §6 sketched — scan-time generation would block scans on ffmpeg
    and double scan-session complexity; guessed shapes carry `// corpus:`
    markers for capture verification; (d) HLS cold start fixed in
    transcode (hls_init_time 2 + Prebuffer + WaitForSegmentFile; cmd.Wait
    goroutine fixes ffmpeg zombies).
    Reverse: corpus capture (owed #1-2) arbitrates every `// corpus:`
    marker; websocket timing reverses if a priority client hard-requires it.

14. **Waves 1-2 (2026-09-09, parallel agent orchestration): all four faces
    exist; codeable scope of Slices 0-4 substantially complete.** Method:
    strict per-agent file ownership + pre-wired shared collision points
    (server.go face mounts, pre-assigned migration numbers 0004-0006,
    one-line Mount additions reported to the orchestrator). Recorded calls:
    (a) editions format CHECK extended with 'audio' in 0004 — music scan
    inserts were silently violating 0001 (found by the subsonic agent);
    (b) OPDS auth = HTTP Basic with 15-min argon2-verify cache (PSE page
    requests would otherwise cost 64MB hashes per image);
    (c) playlists owner-only v1 (subsonic 70 on cross-user); (d) warm-scan
    probe-skip = mtime+size match → skip probe AND hash (group-level for
    audio; 10k warm re-scan 211.6s → 0.17s); (e) Jellyfin /socket =
    hand-rolled RFC6455 subset (text frames, no continuation) rather than a
    websocket dep — controlled use, zero-dep discipline; (f) importers open
    foreign DBs read-only via modernc, apply only through existing upserts,
    dryRun-first; (g) release = hand-rolled Make cross-compile (4 platforms,
    SHA256SUMS), no GoReleaser; (h) teploy template ships as target-shape
    with two blockers documented: no published image (neutron-go relative
    replace) and teploy.yml cannot express /dev/dri device passthrough.
    Reverse: corpus capture arbitrates every `// corpus:` marker (~20);
    Basic-auth cache reverses if a security review objects; podcast episode
    progress needs a schema decision when built.

15. **Wave 3 (2026-09-09 evening): providers + watch + polish; the codeable
    backlog is now tail-items only.** Recorded calls: (a) provider interface
    as pinned in meta.go — two agents built against one written contract
    (same method as the web/discovery split); cache keys include the
    provider base URL so per-test httptest servers can't collide; (b) auto-
    apply metadata ONLY on single-candidate strong match (>= 0.85
    Levenshtein) — ambiguity goes to a manual inbox (PLAN §8's
    never-wrong-guess rule made mechanical); provider keys are env-gated,
    absent key = provider disabled, never a scan error; (c) Audible
    chapters fill ONLY empty single-file m4b editions — never clobber
    ffprobe data; (d) fsnotify dep added (approved, sole new dep of the
    wave); watch triggers the same scan-job machinery as HTTP (DB-level
    running-job guard; a server.go one-line injection to unify instances
    remains open); (e) podcast episode progress in its own table (0007) —
    episodes are deliberately NOT editions; both faces read it via
    UserData/inline shapes, writes via podcast-scoped routes because
    ServeMux can't register partial-wildcard segments (`pe-{id}`); (f) CBR
    via exec'd unrar/unar only — no RAR Go dep ever; (g) `libteca backup` =
    VACUUM INTO + covers mirror + keep-10 prune — document, don't build
    backup servers; (h) SW reader chunks: verified already covered by
    runtime caching (hashed same-origin assets) — documented, not precached.
    Reverse: corpus capture still arbitrates face shapes; TMDB key absence
    just disables that provider.

16. **Wave 4 (2026-09-09 night): metadata tail + scan-instance unification
    + init-admin idempotency.** (a) TMDB season fetch + apply-episodes fills
    only filename-like titles (`SxxExx`); never clobbers good titles.
    (b) works.genres JSON column (0008); leftover settings-KV rows stay.
    (c) OpenLibrary Fetch resolves `/authors/{key}` (personal_name||name).
    (d) Audible chapters distribute across multi-file editions by
    cumulative duration; ffprobe titles never clobbered. (e) Server.Core
    is constructed once in New and handed to watch — HTTP and watch share
    the in-process run guard (closes 15(d)). (f) --init-admin: create if
    empty DB; same name+password = ok; wrong password or second admin =
    error. Reverse: none expected; TMDB episode apply is still
    corpus-unverified against real season payloads.

13. **First-party web UI is a headline surface, not a fallback; built
    contract-first.** Founder call 2026-09-09: the web UI must beat
    Jellyfin/Plex/Kavita/ABS standalone (inherited clients are the moat,
    but the product can't depend on them). Method: the discovery layer
    (resume/search/recent/sort-filter/subtitles) and the UI were built in
    parallel against one written JSON contract — field names load-bearing,
    timestamps Unix milliseconds everywhere (matches /me, progress), media
    tags authenticate via `?token=` query (auth middleware already accepted
    it; covers were silently 401ing before). Admin scan-jobs + SSE now
    consumed by the UI (closes owed item 4). Found gap recorded as owed:
    users/tokens core endpoints (SPEC §5 promised, never built).
    Plex note: no Plex face ever — closed protocol, account-tethered
    clients; libteca beats Plex positionally (no subscription, no paywalled
    remote/transcode), not by emulation.
    Reverse: corpus capture may still reshape nothing here (first-party
    contract is ours alone); readers arrive Slice 2.

17. **neutron-go published (2026-09-10).** Nested module in the Neutron
    monorepo: `github.com/neutron-build/neutron/go` @ `go/v0.1.0`. The
    sibling `neutron-build/neutron-go` / Forgejo `Tyler/neutron-go` repos
    were created then deleted — nested is the shape. libteca requires
    v0.1.0, no `replace`. Reverse: none.

18. **Product is the first-party web UI; protocol faces are niceties
    (2026-09-10).** Founder call after the emulation path was shown to be
    a years-long tail (DeviceProfile, socket.io, Kavita protocol) for a
    solo founder. The UI must be the thing you open instead of Jellyfin /
    Kavita / ABS web. Faces stay mounted; a README cell still only says
    "works" after corpus + live client — but that work is Gate F, optional,
    after the web daily-use gate. Native apps are a non-goal (PWA now;
    inherit JMP/ABS/KOReader later if needed). Reverse: if Tyler still
    lives in the other three UIs after Gate W, either the web bar was
    wrong or faces become the wedge again — record which.

20. **Five-agent audit wave (2026-09-10 evening): security gates, scan
    correctness, perf indexes, player/reader races, shell/mobile.**
    (a) Admin gates now cover library add/scan/delete, linking
    move/split/merge, all matching mutations, refresh-meta, podcast
    subscribe/refresh/patch/delete/OPML — previously any user could add a
    library pointing anywhere on disk and stream it. (b) podcasts
    libraries are no longer scanned as audiobooks (phantom works +
    retention revive). (c) migration 0009: idx editions(work),
    files(edition,seq), files(hash); works files-pass scoped to the
    library; synchronous(NORMAL); UpsertWork no longer clobbers provider
    descriptions on rescan; NextUp SQL filters to series with progress.
    (d) `position` no longer leaks the music sort-order column as seconds
    (tracks started N seconds in). (e) HEVC is never browser-direct
    (black-screen); HLS carries a start offset for resume and a
    DELETE /hls/{sid} stop so closing the player kills ffmpeg. (f)
    password reset revokes the user's tokens. (g) Player/reader fixes:
    video finished-flag race (episodes never marked watched), detached
    audio kept playing after unmount, PDF page input navigates + finished
    reopens at p1, reader progress-load failure no longer wipes position,
    epub iframe keyboard + location shape + book leak, CBZ LRU eviction
    (200-page books OOM), webtoon saved-page accuracy. (h) Shell: user
    menu was clipped at every width (header overflow-x), header/player
    bar responsive + safe-area insets, faint contrast to 4.6:1, playlist
    404 tri-state, play-all accepts m4b/mp3 + writes progress, podcasts
    list error state, 409-subscribe jumps to the show, search 1-char
    hint, admin add-library honest errors, login busy state.
    Reverse: none expected; gates may need per-route relaxation if a
    non-admin matching flow is ever wanted.

21. **Wave 2 (2026-09-10 night): cinematic video player + richer demo
    corpus + view polish.** (a) video.tsx rewritten: native controls
    replaced with a quiet overlay — auto-hiding scrim bars, custom
    scrubber (buffered track + hover time tooltip + keyboard-seekable
    range input), play/pause glyph flash, prev/next episode, volume
    slider, speed cycle, iOS webkit fullscreen; all save/race/HLS
    logic preserved. (b) Demo corpus: multi-mp3 audiobook (The
    Ensemble - Nocturnes), chaptered m4b with embedded cover (David
    Shaw - The Long Road), PDF edition joined to the Frankenstein work
    (epub+pdf = two editions, one work), Sintel movie.nfo (plot +
    genres verified in DB), plain + .en.srt sidecars, Blender Shorts
    season 2. (c) work/home/library polish (mobile stacking via view-
    local style block, description clamp+toggle, hero progress bar,
    movie-card year fallback, movie facts height bug) and admin/
    matching redesign (panel cards, two-pane matching). (d) Faces
    regression-verified E2E on the demo (Jellyfin PlaybackInfo
    transcodes HEVC, Range streams, ABS items/progress, OPDS feeds,
    Subsonic ping) — all 20 packages green.
    Reverse: overlay player drops native controls entirely; if a client
    needs native PIP controls, gate on a setting.

22. **Multi-pass bug sweep (2026-09-10 late): five fix-agents + live
    verification.** Players: dblclick/double-toggle race (240ms click
    timer), seek NaN guards, indicator priority (waiting > skip >
    glyph), stale state across episode switches, event-driven play
    state, chapter-popover viewport/backdrop-filter fix, queue-end
    unmount no longer clobbers finished. Readers: PageStore eviction
    queue thrash, webtoon restore regression + inert slider, RTL tap
    labels, TOC active highlight, PDF double-post/empty-input/inputMode,
    deep-link back bounce. Views: hero recency (was category-priority),
    chapter-click mount race (kick()), no-edition flash, Scan/Match
    disabled symmetry + lib=0 guard, Cover stale broken-image state.
    Shell: boot-error Retry screen, 401 reload (was a no-op hash set),
    apply-episodes affordance on TV matches, busy/error states across
    admin/matching/podcasts/playlists (network throws previously stuck
    busy forever), playlist play-all progress on stop/unmount. Server:
    scan dedup 409 (running + recently-done window), /nextup includes
    unstarted series. SW: app shell is network-first now (was
    install-cached forever — the "stale build" reports were real).
    Demo corpus upgraded to real media: full 10-min Big Buck Bunny
    (webm), 16.6-min spoken Alice audiobook (m4b, 2 chapters,
    Tenniel cover) as a second edition of the CBZ work.
    Reverse: none.

19. **Gate W code (2026-09-10): delete-library, grid percent, type-first
    library nav, first-party HLS, PDF page control, playlist workId.**
    HLS uses existing transcode.Manager, session `web-{editionId}`, direct
    play when codecs are browser-safe; `hls.js` is a lazy import (Safari
    native HLS pays nothing). PDF stays native `<embed>` plus a page
    control — no pdf.js. DeleteLibrary does not touch disk media.
    Reverse: drop hls.js if first-party video stays direct-play-only.

23. **Consolidation + completion accounting (2026-09-11).** Docs
    rewritten to a single live state (SPEC §9 table, PLAN status,
    README). Method for the percentages: per-area "% of v1" where v1 =
    Gate W (web daily-use bar), with faces explicitly scored built-
    but-unverified. Recorded so "90% done" claims have a denominator.
    Same session: header category nav (`#/library?type=` deep links,
    icon-only under 1024px), audiobook corpus made real literature
    (TTS-spoken Gutenberg texts replacing song-derived fakes).
    Reverse: none.

24. **Deferred tail closed (2026-09-11, four-agent wave).** Store:
    linking/user-delete/relink transactions; works pagination +
    bounded inbox; provider-cache pruning; non-ASCII search via
    title_l/author_l (0010 + Go backfill; opds/subsonic predicates
    migrated); login dummy-hash oracle fix; refresh-meta SSE
    404/heartbeat/unsubscribe. Podcasts: one Service instance shared
    by routes/scheduler/OPML; retention deletes episode files
    (data-dir-prefixed guard); OPML import is a background job with
    status polling; feed-URL normalization (https, no trailing slash;
    loopback exempt). Web: toast system wired app-wide; skeleton
    loaders; empty-state icons. Video: core trickplay endpoints
    (/editions/{id}/thumbs[...]) + scrubber hover thumbnails + PiP.
    Ops: CI workflow (go vet/test + web tsc/build). Tests: 16/16
    green incl. new unicode-search, pagination, prune, rollback,
    retention-disk, OPML-status, dedup tests.
    Reverse: none.

25. **Final sweep to ~95% (2026-09-11).** Three agents + live Gate W
    simulation. Notable finds: neutron's error interceptor swallowed
    all non-RFC7807 404 bodies (every handler's real "not found"
    detail died at the router — fixed by emitting problem+json);
    linking 500s on missing ids; Dockerfile (multi-stage, embed-aware,
    ffmpeg in final image); playlist added-flag; per-view titles; PWA
    head wiring. Live simulation verified: audio resume mid-book,
    movie resume + seek + auto-hide controls + Escape close, PDF and
    EPUB readers, search, Next Up, category nav. Remaining to 100%:
    Gate W (founder week) and Gate F (corpus + live clients) — both
    human-owned by design.
    Reverse: none.

26. **Provider keys via admin UI (2026-09-11).** GET/PUT
    /settings/providers (admin-gated): TMDB + ComicVine keys stored in
    settings KV (`provider:tmdb|comicvine`), KV overrides env
    (LIBTECA_*_KEY fallback), reads return masked last-4 only, empty
    PUT value clears. meta.envKey consults a injected lookup
    (SetKeyLookup, wired in cmd from db.GetSetting) so keys apply
    without restart. Audible/MusicBrainz/OpenLibrary listed as
    keyless. Admin → Provider Keys section with per-row replace/clear.
    Closes the "keys at deploy" gap for self-hosted use.
    Reverse: none.

27. **Self-serve password change (2026-09-11).** User menu → Change
    password (modal: current + new + confirm for non-admins; admins
    skip current per admin-reset parity). Server: self-changes by
    non-admins now require oldPassword (auth.Verify) — closes the
    stolen-token-rotates-password hole; admins still reset without
    it. All tokens revoked on success; UI signs the user back in to
    the login screen. Username rename rejected as unnecessary surface
    (single-household identity glue).
    Reverse: none.

28. **Release waves (2026-09-11).** Wave 1 - four-agent release audit,
    fixed in full: security (library paths admin-only, scan-path leak
    masking, covers traversal, 4MiB body limit, Subsonic stale-token
    hole + secret cleared on password change, err.Error()
    genericization), races (jellyfin socketHub, zombie ffmpeg, watch
    debounce re-arm), tx safety (_txlock=immediate + pool cap,
    single-tx search backfill, all read-then-write upserts wrapped,
    per-book scan txs, NFO title_l unicode fix), panics (zero-file
    editions), faces (query-param casing, season/series browsing,
    System/Ping, session cap 8 + orphan wipe, trickplay partial-tile
    cleanup), web (safe-parse api, 15s progress heartbeat, every view
    error+retry, NaN guards, focus-trapped modal), lifecycle (bounded
    shutdown reaps transcode + sockets). Wave 2 - three-agent polish:
    global motion system w/ reduced-motion, rail masks + chevrons,
    glass player/readers refinements, toast system polish, complete
    PWA manifest + icons, sw v4. 17/17 packages green incl. -race.
    Remaining: Gate W + Gate F (human), scan ctx threading + login
    rate-limit (documented limits, not blockers for single-household).
    Reverse: none.

29. **Gate W agent soak + last two non-blockers (2026-09-11).** (a)
    Scan cancellation: ctx threaded through all scanners + watch +
    HTTP/API paths; shutdown marks running jobs "cancelled"; per-book
    txs keep partial progress consistent. (b) Login rate limiting:
    auth.Limiter (rolling 10-min window, 5 fails -> 15-min lockout,
    success resets, 10k IP cap) shared by core, ABS, and Subsonic
    plain-password auth; RemoteAddr-keyed (no XFF trust - no proxy
    config exists). (c) Agent soak (machine Gate W): finish-TV ->
    nextup advances (1x1->1x2), finish-movie -> resume drops,
    finished flag round-trips, progress persists restart, concurrent
    scan+range-stream+progress no 5xx, faces 200 under limiter, RSS
    stable, zero server errors. Progress POST contract is
    {"position","duration","finished"} - soak initially used
    "isFinished" (wrong key, silently ignored) - no app bug.
    Remaining human: founder week on real corpus (Gate W), live
    client + corpus (Gate F).
    Reverse: none.

30. **Gate F (partial): Subsonic face verified against a real client
    (2026-09-11).** Airsonic-Refix (open-source Subsonic web client)
    built and connected live. Login (plain -> cached token auth), artist
    index, album lists (newest/random/alpha), album detail w/ tracks,
    cover art, genres, play-queue save, scrobble all work end-to-end.
    Fixes the live client forced, all real: (a) no CORS on /rest -
    every web Subsonic client requires it (Navidrome convention);
    (b) neutron router emits 404s outside group middleware - unknown
    /rest routes lost CORS -> catchall route now returns code-70 in
    the requested format; (c) getOpenSubsonicExtensions unimplemented
    despite advertising openSubsonic:true (refix crashed .map on the
    omitempty-dropped array - pointer-to-slice idiom); (d) missing
    getStarred2/getPlayQueue/getAlbumInfo2/savePlayQueue killed
    client flows (play treats queue-save failure as fatal). /rest/
    stream verified byte-exact via the client's own URL (200 full +
    206 ranges). Client audio engine does not start under synthetic
    automation (autoplay policy) - not a server defect; earlier
    trusted-input runs did produce AudioController playback events.
    Jellyfin/ABS/OPDS remain built-but-unverified. Corpus capture
    tooling unchanged (founder-owned).
    Reverse: none.

31. **Founder calls on PLAN 14 (2026-09-11).** media-hub stays parked;
    libteca goes PUBLIC now (was private since inception) under MIT.
    Scrub verified: no secrets/session files ever tracked, data/ never
    in history. LICENSE + public AGENTS.md committed; Live at
    github.com/libteca/libteca (org existed, repo created empty by a
    parallel agent; pushed + made public + description/homepage set;
    origin stays Forgejo, `github` remote mirrors). Header "Library" nav link removed (route remains, Home +
    category links cover it); admin/settings formatting pass landed
    same day.
    Reverse: none.
32. **Podcast egress is public-only (2026-09-12, ChatGPT audit #4).** Feed,
    cover and enclosure URLs are attacker-supplied input to a server-side
    fetch. The podcast package's HTTP clients now refuse loopback,
    RFC1918/ULA, link-local, multicast and unspecified destinations at dial
    time and re-validate redirects. Consequence: a podcast genuinely hosted
    on a LAN address cannot be subscribed; that is the trade for not being a
    metadata-endpoint probe. Tests inject unrestricted clients because
    httptest binds loopback.
33. **HLS session ids are per-playback, not per-edition (2026-09-12, ChatGPT
    audit #2).** Manager.Get deduplicates by edition match, so a deterministic
    "ps-<edition>"/"web-<edition>" id made two viewers, tabs or seeks share
    one ffmpeg with one start position, and either could stop the other's
    stream. Session ids now end in 12 random bytes; the edition prefix
    survives for routing. Segment URLs and stop requests carry the full id
    unchanged.
34. **Podcasts library deletion goes through the podcast service
    (2026-09-12, ChatGPT audit #11).** Downloaded episode files park in
    `files` with edition_id NULL; a bare library-row delete orphaned them.
    The library-delete endpoint now walks the library's podcasts through
    Service.DeletePodcast (rows + disk media, single-flight-locked) before
    dropping the library, and the store transaction removes episode-linked
    file rows as defense in depth.
    Reverse: none of these three reverts cleanly without reintroducing the
    audited defect.
35. **Audit passes 3-5 closed 2026-09-12 (26 further findings).** Decisions
    worth recording beyond the registers (`AUDIT_OPEN.md`, passes 3-5):
    (a) Podcast-library deletion is one gated store transaction
    (`DeletePodcastsLibrary`) covering podcast children AND the library row -
    the API no longer deletes the row separately; single-flight slots are
    acquired before any path/cover snapshot. (b) Jellyfin session model:
    every playback session id is owner-bound ("u<uid>-" prefix minted by the
    frontend, or rebound at the handler), /Sessions and WS session updates
    are per-user views, and WS command authorization resolves and delivers to
    the same socket in one hub operation. (c) The shared login limiter keys
    are principal-aware (ip|username) on every face - a success for one
    account can no longer erase another's failure bucket. (d) CBR/PDF
    resource caps are enforced during extraction/read (kill oversize
    children, reject instead of truncate). Reverse: each reopens a
    demonstrated attack or resource-exhaustion path recorded in the reports.
36. **Subsonic face verified against real clients per PLAN G-gates; runtime
    smoke 2026-09-12 covers all four faces on a real binary** (scan of a
    generated CBZ, watch auto-scan, core/ABS/OPDS/Subsonic/Jellyfin answers,
    overflow guards 200-not-500, restart persistence, zero panics). Founder
    gates G1-G3 remain the arbiter for release claims.
37. **Games as a fifth library type — PARKED, not scheduled (deliberated
    2026-09-13).** Post-launch v2 direction only, and only at the
    Gameyfin-simple end: game/ROM folders scanned like any other library,
    TheGamesDB/IGDB providers via the existing provider pattern,
    work/edition/file mapping unchanged, served over the API for
    download-then-play. Explicitly out: DAT fingerprinting (No-Intro/Redump
    identity) — that treadmill is RomM's forever, never libteca's; and a
    libteca-shipped viewer app — killed in deliberation because Linux is
    covered (Playnite runs under Wine with a bridge plugin, a native
    Playnite port is in the works, ES-DE/Lutris are mature) and the only
    durable gap, macOS, belongs to omilator desktop. Player story: omilator
    is the client; its RomM client mode may land first and could make this
    type unnecessary. Precondition: G1–G3 gates and launch.
    Reverse: if omilator-as-RomM-client covers the need, this never gets
    built — and that is the preferred outcome. (Design frozen same day in
    `PLAN-GAMES.md` — parked, not scheduled.)
