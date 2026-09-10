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

- ABS server/app version pin (after corpus capture, task 10)
- Cover tile generator: share marketing-site Penguin-style code or regenerate
  (task 5 detail)
- Backup story timing (Slice 1)
- **Publish neutron-go before libteca goes public** — the dep is currently a
  relative `replace` to the local Neutron checkout (akiroo vendors it the same
  way; v0.0.0 = unpublished). External builds break until neutron-go is on a
  real module path.

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
