# Libteca — one library, one UI

One server for movies, TV, music, audiobooks, podcasts, books, and comics.
The product is the **first-party web UI**: browse, play, read, resume, admin.
Work → Editions is the bit no incumbent can do without a rewrite (EPUB + M4B
+ CBZ as one book; progress per-edition, resume per-work).

Protocol faces (Jellyfin, Audiobookshelf, OPDS, Subsonic) are a **nicety**:
if an existing app happens to connect, good. They are not the moat, not the
critical path, and not a reason to delay the web. Native apps are not in
scope — PWA now; inherit a TV/phone client later if the web is not enough.

Status: **RELEASE-READY, AWAITING GATE W** (repo `Tyler/libteca`, private).
Everything codeable through the 2026-09-11 release waves is closed
(DECISIONS 19-28): full audit wave (security, races, tx safety, faces,
web correctness) + polish wave (motion system, players, readers, PWA);
17 packages green incl. -race, CI workflow, Dockerfile, provider keys
UI. Faces built but corpus-unverified (founder gate). Remaining to
"finished v1" is Gate W (the founder week) plus Gate F if faces are
ever claimed "works". CI shipped; the codeable deferred tail is closed.
Server is neutron-go (DECISIONS 9, 17); §2's old
"no Neutron" line is superseded — it still bars Neutron DB and SSR
loaders.

---

## 1. Thesis (revised 2026-09-10)

Self-hosters run 2–3 media servers because each covers one medium and each
ships a captive client. The original bet was “parasitize their apps.” That
is a years-long emulation tail (DeviceProfile, socket.io, Kavita protocol)
for a solo founder. The honest product is:

1. **One binary** that holds every library type.
2. **One web UI** good enough that you do not open the other three.
3. **Work → Editions** so a title is not three rows in three apps.
4. Faces, if cheap, so a phone or TV you already own can keep working.

Beat Jellyfin/Kavita/ABS on *their web*, not on their native apps. Inherit
those apps only where the face already exists and a live client pass is
cheap. Do not grow a Kavita protocol. Do not write a native client.

What this is not: a fourth Plex. No Live TV, no plugins, no acquisition, no
account cloud.

---

## 2. Non-goals

- **No native apps.** PWA is the installable client. A future iOS/Android/TV
  app is a new product decision, not a slice in this plan. Reverse: web+PWA
  cannot be the living-room client *and* no inherited TV face is green.
- **No Kavita API face.** OPDS + our reader is the books path. Kavita apps
  will not point at us; do not spend months so they can.
- **No Plex face.** Closed protocol, account-tethered clients (DECISIONS 13).
- **No acquisition.** No torrents, *arr, scraping. jellyfin-plugins concept
  stays separate.
- **No Live TV / DVR / IPTV.**
- **No plugin SDK** before the web gate.
- **No clustering.** One node, SQLite. Backups + Teploy.
- **No Neutron DB / no SSR loaders.** Server is neutron-go; web is Neutron
  static/Preact, client-side only, data via `fetch('/api/core/...')`.
  (DECISIONS 5, 9, 17 — supersedes the old “zero Neutron” line.)

---

## 3. Brand

**libteca** — *biblioteca* clipped: *lib* + *-teca*. Domains libteca.com/.org
(2026-09-09). Standalone consumer product, not a Teploy sub-brand. Ships as
an optional Teploy template; bare binary + systemd stays first-class.

---

## 4. Stack (decided)

| Decision | Choice | Notes |
|---|---|---|
| Language | Go | ffmpeg is the hot path; single static binary; N5000 4GB floor |
| Store | SQLite WAL, `modernc.org/sqlite`, goose embedded, hand-rolled SQL | sqlc deferred (DECISIONS 10). No Postgres. |
| Web | Neutron static + Preact in `web/`, `embed.FS` | Client-side only. `neutron-ts dev` → :8096 |
| Router | neutron-go `github.com/neutron-build/neutron/go` v0.1.0 | No `replace`. Compat faces use raw `HandleFunc` |
| Transcode | ffmpeg HLS (MPEG-TS), hwaccel auto + software | First-party player is direct play today; in-browser HLS is a later web gap |
| HTTP | stdlib `net/http` | |

---

## 5. Architecture

```
libteca (one binary)
├── cmd/libteca/          # --data, --port, --scan, --watch, --init-admin; backup
├── internal/
│   ├── api/core/         # THE API. Web is the only first-party client.
│   ├── api/jellyfin/     # nicety
│   ├── api/abs/          # nicety
│   ├── api/opds/         # nicety
│   ├── api/subsonic/     # nicety
│   ├── scan/ watch/ meta/ audio/ transcode/ trickplay/
│   ├── podcast/ importer/ auth/ store/
│   └── server/           # neutron-go App, embed webdist
├── web/                  # Neutron static / Preact — the product
└── testcorpus/           # face fixtures; empty except one ABS ping
```

Data model (exists):

```
libraries (typed) → works → editions → files
progress (per-edition; reading: page/percent/locator)
podcasts / podcast_episodes (not editions)
playlists, users, tokens, scan_jobs, provider_cache
```

Progress policy: per-edition. Work resume = latest in-progress edition by
wall-clock. Page↔timestamp mapping across editions is **not** promised.

`people` / `credits` / `series` / `collections` were sketched and never
built. Do not add them unless the web gate fails without them. Series for
comics is Kavita's identity; our answer is editions on one work, not a
fake series tree.

---

## 6. First-party web (the product)

Quality bar: must beat Jellyfin, Kavita, and ABS **in the browser** for a
household that has all three library types. Inherited apps are irrelevant
to this bar.

### Surfaces (all exist; gaps called)

| Surface | Now | Gap vs the bar |
|---|---|---|
| Home | Continue watch/listen/read, Next Up, recently added, libraries | — |
| Library | Type-first tabs, sort/filter, scan/match, cover progress | — |
| Work | Cover, metadata, genres, edition tabs, TV seasons, music tracks with resume | Linking is under Manage (admin) |
| Video | Direct play when browser-safe; else HLS via transcode.Manager + lazy hls.js | Untested on a real non-H.264 file |
| Audio | Chapters, rate, sleep, resume | Fine for v1 |
| Readers | EPUB (epub.js + CFI), CBZ (paged/RTL/webtoon), PDF (`<embed>` + page control) | CBR = download, no reader |
| Podcasts | Subscribe, OPML, play, progress | Fine for v1 |
| Search | Global, 2+ chars | Percent for books now coalesced; still thin |
| Admin | Add/remove library, scan SSE, users/tokens, matching, ABS/Kavita import, backup | — |
| PWA | Installable, runtime cache | Fine |
| Playlists | CRUD + play-all; rows link to work | — |

### Web-complete (the gate)

Tyler's real library, this UI, daily:

- All library types browse and open the right CTA (Play / Listen / Read).
- Resume survives refresh and hash-change (video unmount already flushes).
- Matching inbox + apply-episodes used on the actual TV shows.
- Admin can add **and remove** a library without SQL.
- Cover grid shows progress.
- A non-browser-playable movie still plays in the web UI (HLS or an honest
  “open in player” — pick HLS; do not ship a dead Play button).

Until that gate, do not expand faces.

---

## 7. Faces (niceties)

Honesty rule unchanged: a README cell says **works** only after recorded
client traffic replays and a live client pass. Today nothing says works.
On-disk corpus: one synthetic ABS ping.

Do not grow a face because a PLAN table listed it. Grow a face when:

- the web gate is green, **and**
- Tyler actually uses that client (phone ABS, KOReader, JMP on the TV), **and**
- the delta is a thin adapter over `core`, not a DeviceProfile engine.

Current adapters stay mounted. Bugs that affect *our* model (auth header,
owner checks, HLS playlist URIs) get fixed because they are cheap. A
general Jellyfin DeviceProfile engine, ABS socket.io, Kavita protocol, and
`/Items/{id}/Similar` are **not** on the web-first path.

If a living-room client is required after the web gate, the order is:
Jellyfin Media Player (already pointed at us in the README recipe) →
Android TV. Not Infuse, not Roku, not Swiftfin.

---

## 8. Scanner & metadata

Keep: fsnotify + sweep, xxhash head+tail+size, hash re-link on move,
ffprobe chapters, NFO + sidecar art, providers behind one interface
(TMDB, Audible, MusicBrainz, OpenLibrary, ComicVine), auto-apply only on
single ≥0.85 match else inbox.

Drop from the old list (not built, not needed for the web gate): Google
Books, iTunes Search. Podcasts are RSS URL + OPML, which is enough.

---

## 9. Sequence (gates, not hopes)

**Gate W — web daily-use** (current; founder-owned on a real library)

Code for the bar in §6 landed (DECISIONS 19). Then Tyler uses it for a week
on a real library. If he still opens Jellyfin web, the gap is the next code
item — not a face.

**Gate U — unification visible**

Work→Editions is how you *find* a title, not an admin trick. Resume-per-work
is the default on the work page (already prefers in-progress edition).
Importer from ABS/Kavita is how a switcher arrives; dry-run stays.

**Gate F — one inherited client, optional**

Only after W. Pick the client Tyler actually launches. Record traffic.
Pin the version in DECISIONS. One green cell in the README. Stop.

Old slices 0–4 (ABS → Jellyfin → OPDS → Subsonic → unify) described the
emulation-first path. Codeable scope of those slices is largely in-tree;
their *exit criteria* (phone, JMP, KOReader) are demoted to Gate F.

---

## 10. Verification

- **Gate W** is judged by daily use, not by corpus. Synthetic `data/demo`
  is for browser smoke, not the gate.
- Benchmarks stay as tests: idle RSS < 100 MB (23 MB now), 10k cold scan
  < 10 min, warm rescan near-instant, transcode p95 < 2s if HLS ships in
  the web player.
- Face corpus + CI replay remain the method **if** Gate F is invoked.
  Do not block web work on an empty `testcorpus/`.
- CI: shipped (.github/workflows/ci.yml — go vet/test + web tsc/build).
  Verify the Forgejo runner picks it up; corpus replay hooks in if Gate
  F is invoked.

---

## 11. Security

Local-first, zero telemetry. Bind `:<port>`, Tailscale/LAN intended, no
default public auth. Per-device tokens with revocation. Admin vs user
surfaces. All file bytes through the `files` table path, never a
client-supplied path.

---

## 12. Risks

| Risk | Mitigation |
|---|---|
| Web is “fine” but he still opens Jellyfin for TV | Gate W includes in-browser play for non-H.264; if still failing, Gate F on JMP — not a native app |
| Solo bandwidth | Faces are niceties; nothing enters a gate after it is set |
| Scope creep (three products + a UI) | §2; Work→Editions is the only novel surface; no series tree, no people DB |
| Incumbents are free | Wedge is one install + one work across editions, not parity |
| Faces rot while ignored | Leave them mounted; do not promise cells; README stays honest |

---

## 13. Relationships

- **media-hub** (parked): superseded. Archive when convenient (§14).
- **Teploy**: optional deploy template. Bare systemd first.
- **Neutron**: dogfood. neutron-go server + Neutron static UI. No Neutron DB.
- **jellyfin-plugins concept**: separate; libteca never grows acquisition.

---

## 14. Open (founder owes)

0. Games library type: **SHIPPED (G1-G4, 2026-09-13)** — see `PLAN-GAMES.md`


1. media-hub: **KEEP PARKED** (decided 2026-09-11 — no urgency).
2. Public: **PUBLIC NOW** (decided 2026-09-11 — Subsonic face
   live-client-verified, demo corpus proves the story; GitHub mirror
   live and public at github.com/libteca/libteca, MIT).
3. License: **MIT** (decided 2026-09-11, LICENSE committed).
4. In-browser HLS vs “Play fails, download/open” for non-browser codecs.
   Recommendation: HLS, because a dead Play button loses to Jellyfin web.

Closed: podcasts are in (first-party). ABS-app podcasts are a Gate F item,
not a web item.
