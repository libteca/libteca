# libteca — Slice 0 engineering spec

Companion to `PLAN.md` (product plan). This file is the build contract for Slice 0:
the spine plus the Audiobookshelf face. Tasks are ordered; each has a done-check.
DECISIONS.md records pinned choices and their dates.

Scope recap (from PLAN §9): scan audiobooks, web UI (browse/play/progress/admin),
ABS API deep enough for the unmodified official mobile apps, direct play only.
Exit gate: Tyler's phone connects, browses, plays, syncs progress both directions,
against a golden corpus that replays in CI.

---

## 1. Module and layout

Module `github.com/libteca/libteca`. Repo root is this folder (`Sides/libteca/`,
which also holds the product docs and the gitignored `libteca-site/`).

```
libteca/
├── go.mod
├── cmd/libteca/main.go        # flags: --data, --port, --dev; subcommand: scan
├── internal/
│   ├── server/                # chi router, middleware, static web mount, /health
│   ├── auth/                  # users, tokens, password hashing (argon2id)
│   ├── store/                 # sqlc queries + goose migrations (embedded)
│   │   └── migrations/
│   ├── scan/                  # walker, audiobook detector, hasher
│   ├── audio/                 # ffprobe wrapper, chapters, cover extraction
│   ├── meta/                  # provider interface + cache; Slice 0: tags/folder only
│   ├── api/
│   │   ├── core/              # first-party REST (our web UI is the only client)
│   │   └── abs/               # Audiobookshelf emulation (mounted at /)
│   └── progress/              # progress domain: per-edition, file+offset, sync rules
├── web/                       # Neutron app (static preset, Preact), client-side only —
│                              # no loaders, all data via fetch('/api/...'); built to
│                              # web/dist, embedded via embed.FS (DECISIONS 5)
├── testcorpus/abs/            # sanitized recorded traffic fixtures
├── tools/record/              # mitmproxy capture + sanitize scripts (not shipped)
└── Makefile                   # build, web, test, corpus, bench
```

Dependencies (all decided, see DECISIONS.md): chi, modernc.org/sqlite (CGo-free),
goose (embedded), sqlc, go-socket.io (deferred, task 11). ffmpeg/ffprobe are
exec'd from PATH; absence is detected at startup and reported in /health.

## 2. Data model (goose migration 0001)

```sql
CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,          -- argon2id
  is_admin INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE tokens (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id),
  label TEXT NOT NULL,                  -- e.g. "Pixel 8 · ABS app"
  value TEXT NOT NULL UNIQUE,           -- random 32B hex
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER,
  revoked_at INTEGER
);

CREATE TABLE libraries (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('movies','tv','music','audiobooks','books','comics','podcasts')),
  path TEXT NOT NULL,                   -- absolute, canonical
  created_at INTEGER NOT NULL
);
-- Slice 0 creates type='audiobooks' only; other types exist in the enum now so
-- later migrations never touch this table.

CREATE TABLE works (
  id INTEGER PRIMARY KEY,
  library_id INTEGER NOT NULL REFERENCES libraries(id),
  title TEXT NOT NULL,
  subtitle TEXT,
  author TEXT,                          -- display author; Slice 0: single author
  description TEXT,
  cover_path TEXT,                      -- path under data/covers/
  provider TEXT,
  provider_id TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_works_match ON works(library_id, lower(title), lower(coalesce(author,'')));

CREATE TABLE editions (
  id INTEGER PRIMARY KEY,
  work_id INTEGER NOT NULL REFERENCES works(id),
  format TEXT NOT NULL CHECK (format IN ('m4b','mp3','epub','pdf','cbz','cbr','video')),
  title TEXT NOT NULL,                  -- edition title (may differ from work)
  language TEXT,
  abridged INTEGER NOT NULL DEFAULT 0,
  duration_secs REAL,                   -- sum of files
  position INTEGER NOT NULL DEFAULT 0,  -- manual sort
  created_at INTEGER NOT NULL
);

CREATE TABLE files (
  id INTEGER PRIMARY KEY,
  edition_id INTEGER NOT NULL REFERENCES editions(id),
  path TEXT NOT NULL UNIQUE,            -- canonical absolute
  seq INTEGER NOT NULL,                 -- play order within edition
  size_bytes INTEGER NOT NULL,
  mtime_secs INTEGER NOT NULL,
  hash TEXT,                            -- xxhash128 head+tail+size; audio: full-file
  codec TEXT, container TEXT, bitrate INTEGER, channels INTEGER, sample_rate INTEGER,
  duration_secs REAL NOT NULL,
  chapters TEXT NOT NULL DEFAULT '[]',  -- JSON [{start,end,title}] from ffprobe
  embedded_meta TEXT NOT NULL DEFAULT '{}',
  missing INTEGER NOT NULL DEFAULT 0,
  probed_at INTEGER NOT NULL
);

CREATE TABLE progress (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id),
  edition_id INTEGER NOT NULL REFERENCES editions(id),
  file_id INTEGER REFERENCES files(id),
  file_offset_secs REAL NOT NULL DEFAULT 0,
  edition_position_secs REAL NOT NULL DEFAULT 0,  -- cumulative, for display/sync
  duration_secs REAL,
  is_finished INTEGER NOT NULL DEFAULT 0,
  device TEXT,
  updated_at INTEGER NOT NULL,
  UNIQUE (user_id, edition_id)
);

CREATE TABLE playback_sessions (
  id TEXT PRIMARY KEY,                  -- uuid; this IS the ABS session id
  user_id INTEGER NOT NULL REFERENCES users(id),
  edition_id INTEGER NOT NULL REFERENCES editions(id),
  file_id INTEGER REFERENCES files(id),
  started_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  position_secs REAL NOT NULL DEFAULT 0,
  time_listened_secs REAL NOT NULL DEFAULT 0,
  device_info TEXT NOT NULL DEFAULT '{}',
  closed_at INTEGER
);

CREATE TABLE provider_cache (
  provider TEXT NOT NULL,
  key TEXT NOT NULL,
  response TEXT NOT NULL,
  fetched_at INTEGER NOT NULL,
  PRIMARY KEY (provider, key)
);

CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
```

Progress semantics (PLAN §5, made exact): resume state is (file, offset) — exact.
`edition_position_secs` is the derived cumulative value for display and ABS sync.
Work-level resume = edition with max(updated_at). No cross-format mapping.

## 3. Scanner — audiobook detection rules

Walk each `audiobooks` library top-down:

1. **A folder containing exactly one `.m4b`** (plus non-audio files) → one edition
   (format m4b), one file. Chapters from ffprobe `-show_chapters`.
2. **A folder with ≥2 audio files among `.mp3`/`.m4b`/`.m4a`** → one edition
   (format mp3), files ordered by: disc → track → natural filename sort
   (natural sort: `2.mp3 < 10.mp3`). Chapters: per-file ID3 chapters if present,
   else one chapter per file titled from embedded title or filename.
3. Nested folders are treated as separate books (no series grouping in Slice 0;
   `Book/CD01/*.mp3` collapses: audio files in subfolders roll up to the nearest
   ancestor folder that contains non-audio siblings or is a direct child of the
   library root — implemented as: group by top-most folder under library root,
   recurse for audio).
4. Ignore: images (`cover.jpg` → candidate cover), playlists, pdf/epub present in
   the same folder (kept on disk, linked as an edition of the same work if a work
   title matches — slice 0 may skip this; record as follow-up, do not block).
5. Work/edition metadata precedence: embedded tags → folder/filename parse
   (`Author - Title` patterns) → manual edit via web UI. **No provider network
   calls in Slice 0 scanning** (provider interface exists, Audible lands with
   Slice 1 prep).
6. Cover precedence: embedded (extract first video frame via ffmpeg) →
   `cover.jpg`/`folder.jpg` in folder → generated initial-letter tile (same
   Penguin-style generator as the marketing site).
7. Re-scan: mtime+size unchanged → skip probe. Moved file (same hash) → re-link.
   Missing file → `missing=1`, never auto-delete; UI offers cleanup.
8. Scanner runs in-process (bounded worker pool, 2×GOMAXPROCS), triggered by API
   and by `libteca scan`. Progress events via SSE to the admin UI. No fsnotify
   in Slice 0 (poll/manual — fsnotify arrives with Slice 1 watch).

## 4. ABS face — endpoint surface (Slice 0)

The contract is the recorded corpus (§6), not this table; the table is the build
checklist. Mounted so the official apps' base-URL config works unchanged.

| # | Endpoint | Notes |
|---|---|---|
| 1 | `POST /login` | `{username,password}` → `{user, userToken}`; also `GET /ping` → `{success}` (app startup probe) |
| 2 | `GET /healthcheck`, `GET /status` | app version gate (see corpus — returns min-server-version) |
| 3 | `GET /me` | user + `mediaProgress[]` + player settings shape |
| 4 | `GET /libraries` | id/name/mediaType='book' (ABS calls audiobooks 'book') |
| 5 | `GET /libraries/{id}/items` | paged; sort/filter params the app sends (from corpus) |
| 6 | `GET /libraries/{id}/items/{itemId}` | full item: media.chapters, media.tracks, addedAt, progress embedded |
| 7 | `GET /me/progress/{itemId}` | ABS progress shape (currentTime, progress 0-1, isFinished, lastUpdate, serverTime) |
| 8 | `PATCH/POST /me/progress/{itemId}` | upsert; both verb variants the app uses |
| 9 | `DELETE /me/progress/{itemId}` | reset |
| 10 | `POST /items/{itemId}/play` | open session → `{sessionId, libraryItem, userMediaProgress, audioTracks[]}` |
| 11 | `GET /s/{sessionId}/t/{trackIndex}` | audio stream, HTTP Range required, mime from probe |
| 12 | `POST /session/{id}/sync` | currentTime/timeListened/duration ticks |
| 13 | `POST /session/{id}/close` | close + final progress write |
| 14 | `GET /libraries/{id}/items/{id}/cover` | thumb + `?width=` sizing |
| 15 | socket.io `/socket` | **deferred — task 11**; app degrades to polling without it |

ID mapping: ABS `libraryItemId` == our edition id (stringified). ABS has no
work/edition split; a multi-edition work serves one ABS item per edition. That
mapping is written down here so Slice 4 never breaks it silently.

Auth: `Authorization: Bearer {token}` (mobile) — token issued by /login. Rate
limit login attempts (5/min/IP).

## 5. Core API + web UI (Slice 0 surface)

Core (first-party, under `/api/`, token auth): `POST /api/login`, `GET /api/me`,
libraries CRUD + `POST /api/libraries/{id}/scan`, `GET /api/libraries/{id}/works`,
`GET /api/works/{id}` (with editions), `GET/POST /api/progress/{editionId}`,
`GET /api/stream/{fileId}` (Range), `GET /api/covers/{...}`, users + tokens admin.

Web SPA (Preact + Vite, embedded):
- `/login`
- `/{library}` — work grid (cover, title, author, progress bar)
- `/{library}/{work}` — detail: editions, chapter list, description, resume/play
- player — full-bleed bottom bar: chapter seek, ±30s, speed 0.5-3.0, sleep timer,
  progress writes on pause/unload/every 15s
- `/admin` — libraries (add path/scan), users, tokens, scan log

House style: no emojis, SVG glyphs, same visual language as libteca.com.

## 6. Golden corpus — capture harness (task 10, built early in parallel)

1. Reference stack on the LAN: real Audiobookshelf server + 2-3 test books;
   official Android + iOS apps pointed at a mitmproxy whose upstream is ABS.
2. `tools/record/` — mitmproxy addon script: writes each request/response pair
   as JSON (`{method, path, headers-safitized, body}`) into `testcorpus/abs/raw/`.
3. Sanitizer: strips tokens/hostnames/user ids; maps to fixture ids.
4. Replay: `go test ./internal/api/abs/ -run Corpus` — each fixture becomes
   (request, expected-response-subset) assertion. Fuzzy match: exact on status +
   shape, subset on JSON fields (timestamps vary).
5. Corpus files are named by the app flow: `login.json`, `libraries.json`,
   `items_page1.json`, `item_detail.json`, `play_open.json`, `sync.json`,
   `close.json`, `progress_roundtrip.json`. CI runs the replay on every push.

## 7. Tasks (order matters; estimates in focused sessions)

| # | Task | Done when | Est |
|---|---|---|---|
| 1 | Scaffold: go.mod, cmd, chi, /health, embedded web shell, Makefile | `make build` → binary serves /health + empty SPA, `--dev` flag works | 1 |
| 2 | Store: modernc sqlite + goose 0001 (§2) + sqlc config + first queries | migration up/down clean; queries compiled; roundtrip test | 1 |
| 3 | Auth: users/tokens/argon2id, `POST /api/login`, middleware | curl login → token → authorized 200 / unauthorized 401 | 1 |
| 4 | Scanner v1 (§3 rules 1-3, 7): detect/probe/order/hash/upsert + `libteca scan` | 20-book synthetic tree (tools/seed) → correct works/editions/files rows; re-scan is a no-op probe-wise | 2-3 |
| 5 | Covers + audio probe details (§3 rules 6, ffprobe wrapper) | covers served at 3 sizes; chapters JSON stored; m4b+mp3 both covered | 1 |
| 6 | Core API (§5) + progress domain | curl can drive: list → detail → progress post → resume | 1-2 |
| 7 | Web UI (§5) | browser login → browse → play m4b w/ chapter seek → progress survives reload | 3-4 |
| 8 | ABS face pass 1 (§4 rows 1-9) | corpus-less smoke: login/me/libraries/items/item/progress via curl, shapes reviewed against app source | 2-3 |
| 9 | ABS playback sessions (rows 10-13) + Range streaming | curl play → stream with Range → sync → close; progress lands in DB | 2 |
| 10 | Corpus harness (§6) | real traffic recorded + sanitized; replay green; wired into `make test` | 1-2 |
| 11 | socket.io minimal (row 15) — stretch | second device sees progress update live; may slip past gate with note | 1-2 |
| 12 | Gate: phone e2e | official ABS app (unmodified) connects/browses/plays/syncs both directions on LAN; idle RSS < 100 MB recorded; PLAN §9 Slice 0 exit checklist signed | 0.5 |

Gates for the founder (per daw loop protocol): **G1 after task 4** (scanner
correct on your real library — metadata quality is the visible risk), **G2 after
task 7** (web player usable on your books), **G3 = task 12** (phone). Kill-switch
review at each gate per PLAN §12.

## 8. Slice 0 non-goals (explicit)

No transcode (audio direct play only — mp3/m4b/m4a; unsupported codec = clear
error), no fsnotify, no providers/network metadata, no multi-user UI polish
beyond create/revoke, no podcasts, no OPDS, no backups tooling (document
`data/` copy), no Docker image (bare binary + systemd doc).

## 9. Current state (2026-09-09, end of session)

**Done and smoke-verified** (synthetic libraries over curl, both faces):

- Tasks 1-9 complete: scaffold (neutron-go), store + migrations 0001/0002,
  argon2id auth, audiobook scanner (m4b chapters, multi-mp3, covers), core API
  (Range streaming), ABS face (login/me/libraries/items/personalized/progress,
  play sessions, /s/{sid}/t/{i} tracks), embedded Neutron web UI.
- Slice 1 core, built ahead of plan (DECISIONS 11): movie/TV/music scanners
  (SxxExx + season-folder + album/track parsing), Jellyfin face v1 (Views,
  Items with full type mapping, Seasons/Episodes, PlaybackInfo with
  direct-play vs HLS decision, /Videos stream + Range, ffmpeg HLS transcode
  sessions with reaper, /Audio universal, Sessions progress, Images),
  first-party players in the web UI (video w/ resume + next-episode, TV
  episode list w/ progress ticks, music track bar, audiobook chapters).

**Owed, in order:**

1. Corpus capture (task 10) — mitmproxy against a real ABS server + official
   apps; verify every shape this build guessed from client source.
2. Same treatment for the Jellyfin face: point a real Jellyfin client
   (JMP desktop, then Android TV) at the server; record and fix.
3. Jellyfin websocket (`/socket`) for remote control; /Shows/NextUp real;
   trickplay tiles.
4. Per-library scan locking (currently one global scan at a time) and a scan
   job API with progress events.
5. Slice 0 exit gates G1-G3 (real library, web player, phone) — then the
   PLAN §14 public/launch decisions.

**Known warts:** episode titles depend on filename quality (no providers yet);
single global scan slot; no fsnotify; `--init-admin` first-run only; transcoded
HLS starts cold (no segment prebuffer tuning).
