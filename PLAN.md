# Libteca — one media server, every client

One server for movies, TV, music, audiobooks, podcasts, books, comics. The moat is not
out-featuring Jellyfin, Audiobookshelf, or Kavita individually — it is **implementing their
wire protocols so their existing clients connect to us unchanged**. One spine, four faces.

Status: **BUILDING** (repo `Tyler/libteca`, private until the corpus gates pass).
All four faces implemented (Jellyfin incl. websocket/trickplay/hwaccel, ABS,
OPDS+PSE, Subsonic incl. playlists); web UI is a headline surface (home hub,
readers, PWA); podcasts, users/tokens, edition linking + ABS/Kavita importers,
benches, packaging landed in waves 1-2 (2026-09-09, DECISIONS 12-14).
Remaining codeable: providers, fsnotify, podcast episode progress, playlist
UI. Remaining gates (founder-owned): corpus capture runs, G1-G3, version
pins, launch decisions. Task ledger: SPEC.md §9.

---

## 1. Thesis

Self-hosters currently run 2-3 media servers because each covers one media type and each has
a captive client ecosystem. No product owns "one server, every client" because each incumbent
is structurally committed to its own client. We are not: our product IS the compatibility
layer. We parasitize three mature client ecosystems the way omi-rss parasitized the reader
ecosystem — at larger scale.

Inherited clients (the whole point):

| Protocol face | Free clients we inherit | Surface size | Slice |
|---|---|---|---|
| Jellyfin API (pinned, see §7.1) | Android TV, Fire TV, Jellyfin Media Player, Swiftfin (Apple TV), Roku, Findroid, Infuse | Large — the long pole | 1 |
| Audiobookshelf API | Official ABS iOS/Android apps (open source: contract is fully readable from client code) | Small-medium | 0 |
| OPDS 1.2 (+PSE) | KOReader, KyBook, Chunky, Moon+ Reader, Panel — the entire book/comics ecosystem | Small | 2 |
| Subsonic | Symfonium, Feishin, play:Sub — better music clients than Jellyfin's own | Medium | 3 |

The genuinely novel product bit (no competitor can do it without re-architecting): a unified
**Work → Editions** library. A book exists once, with EPUB, M4B, and CBZ editions linked.
Progress is per-edition, resume is per-work. Jellyfin cannot represent audiobooks well, ABS
cannot represent video, Kavita cannot link audio to text. We can, from day one of the model.

## 2. Non-goals (scope discipline)

- **No acquisition features.** No torrents, no *arr hooks, no scraping/downloading. Same
  reasoning as media-hub's Vimm/ROM privacy posture. Acquisition lives in the separate
  jellyfin-plugins concept if anywhere.
- **No client development.** We write zero native clients. If a face works in the official
  app, it works; if not, our web UI covers it. No temptation to "just fix the client."
- **No Live TV, DVR, IPTV, tuner support.** Evergreen rabbit hole, tiny user overlap.
- **No plugin system before Slice 4.** Providers are internal interfaces, not a public SDK.
- **No multi-server clustering.** Single node + SQLite. Backups and Teploy handle the rest.
- **No Neutron coupling.** Standalone product, zero one-way deps on the framework (ontos
  discipline). If Neutron's DB ever proves itself, that is a post-1.0 conversation.

## 3. Brand decision (recorded)

Standalone name: **libteca** — the natural clipping of *biblioteca*: *lib* (book) + *-teca*
(case, Greek *thēkē*) = "library." Real morphology, not invention; says the category on
first hearing. Domains libteca.com/.org/.io all free at decision time (2026-09-09);
register .com + .org. The rule, for future products:

- Teploy brand when the user is the *server operator* and the product is *infrastructure*
  (arcade = game-server fleet manager → correct call; dash, observe).
- Standalone when users are *consumers of content* and adoption must come from outside the
  Teploy userbase (lullmail, omi-rss, libteca).

Teploy recognition comes from distribution, not naming: libteca ships as a **first-class Teploy
app template** (`teploy deploy libteca`) at Slice 1 exit — flagship demonstration of Teploy
deploying a real product, zero brand dilution. Bare deploy (single binary + systemd unit)
stays first-class; Teploy is optional.

## 4. Stack (decided)

| Decision | Choice | Rationale | Rejected |
|---|---|---|---|
| Language | **Go** | Hot path is ffmpeg (subprocess); our workload is IO orchestration, API surface, websockets, scanning. Node is proven sufficient (ABS is Node); Go is proven superior for this shape (Navidrome vs every Node media server). Single static binary, low RAM (N5000 4GB constraint), goroutines = transcode session management. Matches Teploy DNA and Tyler's Go fluency. | Rust (buys nothing when the hot path is a subprocess), Node (RAM + single-binary story), C# (Jellyfin's own cage) |
| Store | **SQLite (WAL)** | Zero-config single binary. Jellyfin and Kavita both default SQLite. Write load (progress ticks) is trivial; reads are cacheable. Repository layer (sqlc) keeps a PG switch theoretically open; do not build it. | PostgreSQL (Tyler default, but wrong here — a home media server must not require a DB server) |
| Web UI | **Astro/React SPA embedded via `embed.FS`** | One artifact, no Node on the server. Same pattern as Teploy binaries. | Server-side templates (reader/player need SPA interactivity) |
| Router | **neutron-go** (dogfood; akiroo pattern) | Long-term bet gets a second, load-different production consumer (media IO). Raw `HandleFunc`/`Mount` for the compat faces — framework error contract must not leak into ABS/Jellyfin/Subsonic shapes. Unpublished module: relative `replace` for now, publish before open-sourcing. | chi (revised 2026-09-09 evening — see DECISIONS 9), gin, stdlib mux |
| Migrations | **goose, embedded** | House-adjacent, boring | atlas |
| HTTP | stdlib `net/http` | No framework cage | — |

## 5. Architecture

```
libteca (one binary)
├── cmd/libteca/
├── internal/
│   ├── api/
│   │   ├── jellyfin/   # path-compatible emulation, one pinned version
│   │   ├── abs/        # audiobookshelf emulation
│   │   ├── opds/
│   │   ├── subsonic/
│   │   └── core/       # first-party API for own web UI (the only "real" API)
│   ├── scan/           # fsnotify + periodic sweep; content hashing; NFO import
│   ├── meta/           # providers behind one interface, DB-cached, manual override
│   ├── stream/         # DeviceProfile engine: DirectPlay | Remux | Transcode
│   │                   # ffmpeg pool; HLS session manager; orphan reaper; segment LRU
│   ├── session/        # playback sessions, progress, websocket hub
│   ├── auth/           # local users, per-device tokens, admin roles
│   └── store/          # sqlc + SQLite
├── web/                # embedded SPA: player, EPUB/CBZ readers, admin
└── migrations/
```

Data model (sketch, sqlc-generated):

```
works          -- the intellectual unit (a title)
  editions     -- embodiment: epub | m4b | mp3 | cbz | pdf | video | season
    files      -- physical: path, hash, codec/container via ffprobe, chapters
people, credits, series, collections
libraries      -- typed: movies | tv | music | audiobooks | books | comics | podcasts
progress       -- (user, edition, kind=position|page|percent, value, device, updated_at)
sessions       -- playback sessions incl. transcode state
tokens, provider_cache, settings
```

Progress policy (honest, no magic): per-edition positions; work resume = latest across
editions by wall-clock. Page↔timestamp mapping across editions is explicitly NOT promised.

## 6. Streaming pipeline

- Decision engine keyed on client **DeviceProfile** (codecs/containers/bitrate caps declared
  in PlaybackInfo): DirectPlay → Remux (container change only) → Transcode (HLS fMP4).
- ffmpeg templates per profile family; hardware accel via VAAPI / NVENC / QSV / VideoToolbox
  (mac dev box). One accel path ships in Slice 1 (pick by whatever the Proxmox nodes carry),
  others follow.
- Session manager: hard cap concurrent transcodes per hardware, orphaned-ffmpeg reaper,
  segment LRU on disk with a quota. Two clients seeking the same item share output.
- Audio: direct play nearly always; transcode tier for OPUS-in-M4B edge cases. EBU R128
  loudness normalize as an opt-in per-library flag (later).
- Trickplay (scrubber tiles): generate at scan time, serve on Jellyfin's endpoints.

## 7. The four faces

### 7.1 Jellyfin (Slice 1 — the moat, the long pole)

Pin ONE server version — newest stable at kickoff (10.10.x line as of writing; verify at
slice start), recorded in DECISIONS.md. Never chase every release; re-pin deliberately.

Surface, minimum for living-room parity:
- Auth: `X-Emby-Authorization` + `X-Emby-Token` headers, `/Users/AuthenticateByName`,
  `/System/Info/Public` (startup probe). Quick Connect: no.
- Library: `/Items` typed hierarchy, `/Shows/NextUp`, `/Shows/{id}/Seasons`,
  `/Users/{id}/Items/Resume`, `/Items/{id}/Similar`, `/Items/{id}/Images`.
- Playback: `/Items/{id}/PlaybackInfo` (DeviceProfile in, DirectPlay/TranscodingUrl out),
  `/Videos/{id}/stream`, `/Videos/{id}/master.m3u8`, `/Audio/{id}/universal`,
  subtitle extraction + delivery.
- Sessions: `/Sessions`, `/Sessions/Playing`, `/Sessions/Playing/Progress`,
  `/Sessions/Capabilities/Full`, and the `/socket` websocket (ForceKeepAlive, SessionsStart,
  Play, Playstate, GeneralCommand) — without sockets, TV apps lose remote control and the
  client feels dead.

Method (this is the part most emulators skip, and why they rot):
1. Run real Jellyfin + real clients (Android TV, JMP, Swiftfin, Roku) on the LAN.
2. Record traffic (mitmproxy or a transparent recording reverse-proxy).
3. Sanitize → **golden traffic corpus** committed to the repo.
4. Replay harness in CI: every face endpoint is contract-tested against corpus fixtures.
5. Per-client compat matrix in the README, updated per release. Test the top 5 clients,
   ignore the tail until filed as issues.

### 7.2 Audiobookshelf (Slice 0 — smallest surface, proves the pattern)

Official apps are open source → the contract is read from client code, not guessed:
`/login` (token), `/libraries`, `/libraries/{id}/items`, `/me`, `/me/progress/{id}`
(sync both directions), playback session open/tick/close, cover + file streaming endpoints,
socket.io for realtime sync. Podcast RSS serving comes with Slice 3 podcasts.

### 7.3 OPDS (Slice 2)

OPDS 1.2 navigation + acquisition feeds, Basic or token auth, **OPDS-PSE** for paged CBZ
streaming (Chunky/KOReader page-at-a-time). Search via OpenSearch descriptor. This single
face covers everything Kavita's external-client story does.

### 7.4 Subsonic (Slice 3)

Navidrome's proven subset: `ping`, `authenticate`, `getArtists`/`getIndexes`, `getAlbumList2`,
`getArtist`/`getAlbum`/`getSong`, `stream`, `download`, `getCoverArt`, `search3`, `scrobble`,
minimal playlists. Inherited music clients instantly beat Jellyfin's own.

## 8. Scanner & metadata

- Watch (fsnotify) + periodic full sweep (crash reconciliation). Content hash (xxhash:
  head+tail+size for video, full for audio/books) → move/rename detection without re-probe.
- ffprobe at scan: codecs, chapters, embedded art. Chapters are load-bearing for audiobooks.
- Sidecar import at scan: Kodi/Jellyfin NFO + poster/fanart images. This IS the Jellyfin
  migration story — a switcher keeps their curated metadata.
- Providers behind one interface, DB-cached, manually overridable:
  TMDB (movies/TV) · MusicBrainz + Cover Art Archive (music) · Audible + OpenLibrary +
  Google Books (audiobooks/books — Audible for chapters) · ComicVine (comics) ·
  iTunes Search (podcast discovery) + RSS (podcast fetch, OPML import).
- Matching: filename parse → fuzzy → provider; ambiguous = inbox for manual resolution
  (never wrong-guess silently; wrong metadata is the #1 self-hoster complaint).

## 9. Slices (gates, not hopes — founder gate at each exit)

**Slice 0 — Spine + ABS face** (~2-4 weeks part-time)
Scan audiobooks (m4b/mp3 + chapters). Web UI: library, player, admin, progress. ABS API deep
enough for the official apps. Direct play only, no transcode.
Exit criteria: Tyler's phone, official ABS app (unmodified), connects / browses / plays /
syncs progress both directions. Golden corpus for the ABS face exists and replays in CI.

**Slice 1 — Jellyfin core** (~2-3 months)
Auth, Items, images, PlaybackInfo + DeviceProfile engine, `/Sessions` + websockets, HLS
transcode via one hardware accel path.
Exit criteria: Android TV + Jellyfin Media Player browse and play (direct + one transcode
case); transcode start p95 < 2s; `teploy deploy libteca` template published. Golden corpus
covering the five priority clients' session traffic.

**Slice 2 — Books & comics (Kavita parity)**
EPUB/CBZ/PDF scan, web readers (epub.js; CBZ paged reader with prefetch), OPDS + OPDS-PSE.
Exit criteria: KOReader or Chunky browses + downloads via OPDS; web EPUB reader round-trips
progress to a phone resume in ABS app (work-level resume working across two editions).

**Slice 3 — Music, podcasts, polish**
Subsonic subset, music library, podcast fetch/retention/OPML, trickplay tiles.
Exit criteria: Symfonium or Feishin plays + scrobbles; a podcast subscribes, downloads new
episodes, appears in ABS app.

**Slice 4 — Unified works, migrations, maybe DLNA**
Multi-edition linking UI, ABS/Kavita instance importers (read their data dirs), DLNA/uPnP
only if filed by real users.

## 10. Verification & performance (simval discipline, applied to a product)

- Golden traffic corpus (§7.1) is the contract; replay harness runs in CI on every push.
- Benchmarks as tests, tracked per release: idle RSS, scan throughput (10k-file synthetic
  library, cold + warm), transcode start latency p95, concurrent-session ceiling.
- Targets: idle RSS < 100 MB (ABS ~250 MB Node, Jellyfin 300+ MB C# — beatable, Go);
  10k-file cold scan < 10 min; direct play = disk-bound (no server CPU); one 1080p software
  transcode sustainable on a 4 GB N5000 (that box is the floor, not the target — the
  Proxmox cluster is the real deployment).
- Cross-platform: linux/amd64 + arm64 (cluster + RPi), darwin (dev). Bit-reproducible
  releases via GoReleaser or hand-rolled Make, Forgejo `Tyler/libteca`, public mirror +
  `libteca/libteca` when it goes public.

## 11. Security posture

Local-first, zero telemetry. Per-device tokens with revocation. Admin surface separate from
user surface. No exposure to the public internet in any default; Tailscale-first (matches
the household topology). Media path traversal is the classic media-server kill vector —
all file serving goes through the file table's resolved paths, never client-supplied paths
(arcade's BUGS.md filesystem lessons apply directly).

## 12. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Jellyfin API churn breaks faces | Hard version pin; corpus replay catches regressions before users do; re-pin is a deliberate gated decision, not a chase |
| Protocol emulation = permanent bug tail | Top-5-clients matrix, ignore the tail; issues filed with client + version; faces are thin adapters over `core` so a face bug is never a data bug |
| Scope creep (three products in a trench coat) | §2 non-goals; slice gates; nothing enters a slice after its gate is set |
| Solo bandwidth across 6 active priorities | Slice 0 is deliberately small (ABS face proves the pattern in weeks); kill switch at every gate if the Jellyfin face balloons |
| Incumbents are free and good enough | The wedge is consolidation + inherited clients, not parity; a Teploy-template one-command install undercuts their Docker-compose setup ritual |

## 13. Relationships

- **media-hub** (parked, Sides/): superseded by this. Fold anything reusable from its
  streaming frontend into web/, then archive media-hub. Open decision, below.
- **Teploy**: deployment target + app template (§3). No shared code required; shared
  conventions (chi, layout, single binary) deliberate.
- **Neutron**: none. No coupling, no dogfooding — this must be deployable and maintainable
  as a plain Go project forever (ontos one-way-dep discipline).
- **jellyfin-plugins concept**: stays separate; if it revives, libteca is its target host,
  but libteca itself never grows acquisition.

## 14. Open decisions (founder owes)

1. media-hub: archive on Slice 0 exit, or earlier?
2. Public at Slice 1 exit (recommended — adoption needs eyes; golden corpus + compat matrix
   make a credible launch post in self-hosting communities) or stay private longer?
3. Podcasts in Slice 0 scope (ABS apps expect them) or hold at Slice 3 (recommended —
   audiobook library + progress is the Slice 0 proof, podcasts are fetch-scheduling noise)?
4. License: MIT (house default for public).
