# PLAN-GAMES — games as a fifth library type

Status: **PARKED** (DECISIONS #37). Design frozen 2026-09-13 so the shape is
settled before launch; nothing here is scheduled, and the reverse clause in
#37 stands: if omilator-as-RomM-client covers the need, this file becomes a
historical artifact and that is the preferred outcome.

Preconditions to build anything: G1–G3 gates passed, libteca launched.

## 1. Shape (why this is cheap)

The existing model already expresses a games library with **zero schema
changes**:

| Concept | Existing slot | Example |
|---|---|---|
| Game | `Work` (title, cover, description) | Super Mario World |
| Platform variant | `Edition` (`Format` carries the platform tag) | `snes`, `gba` ports of the same game |
| ROM dump | `FileRec` | `smw.sfc` |

The identity hedge from the DAT-fingerprinting deferral **already exists**:
`FileRec.Hash *string` is nullable and stays NULL for games unless
fingerprinting ever arrives (§6). No day-one migration is needed.

The scan pipeline is already staged the right way: `detect → probe → enrich`,
with `scan.Library` dispatching on `lib.Type` — games are one new case, no
probing stage (ROMs need no ffprobe), enrichment via the existing provider
interface (`meta.Provider`: `Name/Search/Fetch`, one new Kind).

## 2. Scope fenceposts (from DECISIONS #37, binding)

- **Gameyfin-simple only**: folder scan + extension/platform detection +
  provider metadata + covers. Never DAT fingerprinting (§6 documents the
  deferral and its reverse trigger).
- **No viewer app.** Mac desktop is omilator's lane; Linux is covered by
  Playnite-under-Wine (+ native port in progress), ES-DE, Lutris.
- **No faces.** No client protocol exists for games; games surface through
  the core API only. Jellyfin/ABS/Subsonic/OPDS face listings exclude the
  games type from day one (one filter per face, negative-tested).
- **Download-then-play, never streaming/transcode.** Existing file stream +
  Range endpoints serve ROMs; no transcode path is wired for games.

## 3. Phases

### Phase G1 — scanner + model (the library exists, browses, scans)

- `scan.Library`: `case "games"` → `scanGamesLibrary` (walk, extension
  filter, platform detect, upsert works/editions/files).
- Platform table lives in `internal/scan/games.go`: extension → platform
  tag, ported from omilator's `GameSystem` knowledge **including its
  documented shared-extension resolution** (`.bin/.iso/.cue` collide across
  systems; omilator already solved the disambiguation rules — copy them, do
  not re-derive).
- Web: add `"games"` to `TYPE_ORDER`; library + work views render games
  with the existing components (platform chip where books show format).
- Core API: no new endpoints; works/edition/file routes already serve any
  type. Add `platform` to the works filter params.
- Verification: `go test ./...`, web `tsc + build`, and a seeded games
  folder scanned in the demo instance.

### Phase G2 — metadata + covers

- `meta` package: new Kind `game`; two providers behind existing key
  gating (`providerKeyDefs` gains `thegamesdb` keyed, `igdb` keyed).
  TheGamesDB first (single key, generous limits); IGDB second if wanted.
- `Result.Extra["platform"]` carries the platform hint into Search.
- Covers flow through the existing cover pipeline (provider art preferred,
  local `<rom-name>.png` beside the ROM as fallback — same rule as books).
- Verification: provider tests mirroring `comicvine_test.go`; matching
  inbox (`matching.tsx`) works unchanged because it is provider-generic.

### Phase G3 — serving + the omilator client contract

- Document the minimal client contract on the core API (auth token; list
  libraries type=games; list works per platform; edition detail with file
  ids; file download with Range; progress POST per edition = playtime).
  This is the "face", just not a named protocol — omilator is the only
  client, so the core API is the contract.
- Rate/size guidance: ROM files can be CD/DVD-sized; confirm the stream
  endpoint's timeouts and Range behavior against >1 GB files (likely
  already fine; verify, do not assume).

### Phase G4 — omilator "libteca library" source (lives in omilator, listed
here only for sequencing)

- Network library source: server URL + token in settings; browse
  platforms → games with covers; download ROM to local cache (resumable);
  launch via existing core resolution (`GameSystem.preferredCore` +
  CoreDownloader already produce runnable cores); playtime POST back on
  exit. Reuses the pass-4-era atomic settings + downloader plumbing.
- **G4 before this file's G1 is also valid** (client-first against RomM
  instead) — that ordering is the reverse clause in action; decide at
  build time from real usage, not now.

## 4. Explicit non-goals

- DAT/No-Intro/Redump fingerprinting, collection completeness, revision
  identity, corruption detection (see §6).
- Any game streaming, cloud-save sync (RomM's lane), emulated playback in
  browser (EmulatorJS is RomM's lane).
- Multi-user per-game permissions beyond what libraries already have.
- A libteca-shipped desktop/mobile viewer beyond omilator itself.

## 5. Deferred decisions (made at phase time, recorded in DECISIONS)

- TheGamesDB-only vs +IGDB (G2).
- Platform taxonomy: the ported omilator table vs an external standard
  (G1; port first, external never unless needed).
- Multi-file games (`.m3u` discs) — likely "list files under one edition,
  defer playlists" (G1).
- Whether playtime belongs on editions (probably) vs works (G3).

## 6. The fingerprinting deferral, precisely

`FileRec.Hash` stays NULL for game files indefinitely. If, and only if, the
simple matcher proves insufficient on real libraries (the evidence gate is
the same session that runs G1 on real books — a real games folder browsed),
fingerprinting becomes an **additive background pass**: hash files → match
against fetched DAT sets → refine/merge editions. That is a future DECISIONS
entry with its own treadmill cost stated. Until then: filenames + providers,
95% of the value at 5% of the cost.

## 0. Build record

- **G1 shipped 2026-09-13** (`405b086`): scanner + migration 0011 + face
  exclusions + web; live-binary smoke green. Migration notes that cost real
  debugging: goose wraps migrations in a transaction where `PRAGMA
  foreign_keys` is a no-op — `-- +goose NO TRANSACTION` makes the rebuild's
  FK toggle real; the editions rebuild must preserve 0008's `description`
  column and re-create 0009's `idx_editions_work` (a table drop takes its
  indexes with it) or every later `goose down` in tests breaks.
- **G2 shipped 2026-09-13**: TheGamesDB provider (key-gated
  `LIBTECA_THEGAMESDB_KEY`, x-api-key header, platform tags mapped into
  `Extra["platform"]`, boxart as cover source), `game` kind wired through
  matching/apply, provider-key registry entry. IGDB deferred (DECISIONS
  candidate): TGDB alone covers the launch need.
- **G3 shipped 2026-09-13**: `docs/omilator-client-contract.md`; Range
  verified against a 1 GiB file on a live binary (206 head/tail exact, full
  200 exact byte count).
- **G4 shipped 2026-09-13 (desktop client core)**: omilator
  `data-library/.../LibtecaLibrarySource.kt` — contract client (discovery,
  paging, Range-resumed download cache keyed on fileId+size, covers,
  playtime POST) with a fake-server contract smoke on the client side.
  **Complete 2026-09-13** (omilator b156761): Server page in the library
  pager (browse + download with resume + play through the existing launch
  path), settings surface, LibtecaServerConnection adapter. All 4 targets
  compile green.

## 7. Size sketch (SPEC task-table style)

| Phase | Content | Est. (days) |
|---|---|---|
| G1 | scanner + web + filters + tests | 2–3 |
| G2 | meta kind + TGDB provider + covers | 1–2 |
| G3 | contract doc + big-file Range verification | 0.5 |
| G4 | omilator source (in omilator's repo) | 3–4 |
