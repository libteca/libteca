# libteca fresh-eyes adversarial audit — pass C (2026-10-06)

Revision audited: `bd8c80c` (HEAD, main) — post-audit12 decision landing
(cookie shrunk to read-only, Bearer mutations, migration 0016 + sha256
backfill). Method: read `AGENTS.md`, `AUDIT_OPEN.md` (all standing
deferrals honored — nothing deferred was re-reported), `DECISIONS.md`,
`AUDIT-CHATGPT-12.md`; diffed `bd8c80c` plus the audit10/11/12 landings
(`a15c17f`, `2fdef07`, `7ea80e3`); then swept the current tree with
emphasis on surfaces the GPT rounds covered thinly (admin ops, settings,
scan/meta job machinery, error handling, concurrency).

## Priority table

| ID | Severity | Area | Finding | Confidence |
|----|----------|------|---------|------------|
| C-01 | HIGH | scan/games/store | Full-ROM sha256 is computed INSIDE the `BEGIN IMMEDIATE` write transaction in `storeGame`; for multi-GB ROMs every concurrent writer (progress saves, other libraries' scan transactions, podcast writes) fails with `SQLITE_BUSY` after the 5s busy_timeout. Empirically reproduced. | Certain (source + repro) |
| C-02 | MEDIUM | store/scan | A changed file whose re-hash fails keeps its OLD sha256 forever (`coalesce(?, sha256)`), contradicting the omilator contract's "value changes only when the bytes change"; the documented retry path does not exist for updated rows. | High (code-path verified; trigger is a transient read failure) |
| C-03 | LOW | core/games | Lazy sha256 backfill runs synchronously in the `GET /works/{id}` handler with no request-context cancellation, no concurrency budget, and no singleflight; abandoned/slow requests keep hashing multi-GB files, and a failed `SetFileSHA256` re-hashes on every later view. | Certain |
| C-04 | LOW | core/podcasts | `podcastDetailBody` returns `{"error": ...}` inside HTTP 200/201 responses on DB failure and swallows the episode-progress error entirely — a false-success shape the repo eliminated elsewhere (audit6 F38). | Certain |
| C-05 | LOW | core jobs | `refresh-meta` and OPML import run under `context.WithoutCancel`, so their cancel branches are dead code and server shutdown blocks in `WaitJobs()` until the whole pass finishes (potentially many minutes). | Certain |
| C-06 | LOW | core/subtitles | Embedded-subtitle extraction buffers ffmpeg's entire stdout via `cmd.Output()` with no byte cap (30s deadline only) — the cap pattern pass-7 F08 added to ffprobe was never applied here. | Certain |

The headline cookie/Bearer split, migration 0016 shape, keepalive-fetch
beacon semantics, and the audit10-12 fix sites were re-verified and found
correct; details under "Verified clean" below.

---

## C-01 — full-ROM hashing holds SQLite's single write lock

**Severity:** HIGH (availability regression introduced at `bd8c80c`)
**Files:** `internal/scan/games.go:214-268`, `internal/store/store.go:29,72-79`

### Evidence

`storeGame` opens the write transaction first, then hashes inside the
closure (`internal/scan/games.go:216,248-252`):

```go
func storeGame(db *store.DB, lib *store.Library, d *gameDoc, coversDir string, tr *tracker) error {
	var workID int64
	err := db.Update(func(tx *store.Tx) error {
		...
		hash := hashFile(d.path, d.size)
		var fileSHA *string
		if sum := SHA256File(d.path); sum != "" {
			fileSHA = &sum
		}
```

`db.Update` begins IMMEDIATE via the DSN (`internal/store/store.go:29`
`q.Set("_txlock", "immediate")`; `store.go:72-79` — `Update` calls
`d.Begin()` before `fn`). `SHA256File` reads the entire file
(`internal/scan/scan.go:402-417`). SQLite has one writer, and the DSN
sets `busy_timeout(5000)` (`store.go:32`).

Empirical repro (scratch test against the real store, run and deleted;
tree left clean): a `db.Update` closure sleeping 8s while a concurrent
single-row `UPDATE` executes produces

```text
concurrent write: err=database is locked (5) (SQLITE_BUSY) elapsed=5.05s
```

### Failure mode

A games scan touching a new/changed ROM larger than what the storage can
hash in <5s (roughly >1-2 GB on spinning/network disks; PS2/GC/PS3 ISOs
routinely 1.4-50 GB) holds the write lock for the hash duration:

- Reader/audio progress saves (`POST /progress/{id}`), ABS session
  writes, podcast episode progress — 500s after 5s.
- Worse: other libraries' scans. The watcher's sweep and boot
  reconciliation start scans for ALL libraries in parallel
  (`internal/watch/watch.go:225-243` — `scanAll` fires `TriggerScan`
  per library, which is async), and every scanner writes per-file/per-book
  transactions. The first transaction that lands inside a long hash window
  gets SQLITE_BUSY and THE WHOLE SCAN JOB FAILS with "database is locked"
  (`runScan` treats any scanner error as terminal). One big ROM being
  hashed can kill an unrelated music scan.
- The pre-existing sampled `hashFile` (128 KiB reads) was safely inside
  the tx; `bd8c80c` added the full-content read to the same closure,
  multiplying the lock-hold time by ~4 orders of magnitude.

### Fix sketch

Hoist both hashes out of the transaction — compute `hash` and
`SHA256File` before `db.Update`, pass them into the closure. The reads
then happen lock-free; the transaction stays millisecond-scale like every
other scanner. Optional hardening: skip re-hash when only mtime changed
and size/sha256-compatible evidence exists (out of scope; identity model
is the deferred F17 family).

### Regression test spec

- Unit test with a hash seam (mirror the `hashRequest` pattern in
  `internal/api/core/users.go:74`): inject a `sha256File func(string) string`
  that blocks on a channel; while blocked, issue `db.PutSetting`/a
  progress write from another goroutine with a short deadline; it must
  succeed. On current code it times out with SQLITE_BUSY.
- Integration: seed a games library with one large file, start the scan,
  and concurrently run a small second library's scan; assert the second
  job reaches `done`, not `error`.

---

## C-02 — stale sha256 survives a failed re-hash of changed bytes

**Severity:** MEDIUM (omilator contract integrity; silent and permanent)
**Files:** `internal/store/queries.go:438-451`, `internal/scan/games.go:202,248-252`, `internal/scan/scan.go:398-401`

### Evidence

The update path keeps the old value when the new one is absent
(`internal/store/queries.go:439`):

```go
_, err := q.Exec(`UPDATE files SET ... sha256 = coalesce(?, sha256) ...`, ...)
```

The lazy backfill only fills NULL rows (`queries.go:448-451`):

```go
func (d *DB) SetFileSHA256(id int64, sum string) error {
	_, err := d.Exec(`UPDATE files SET sha256 = ? WHERE id = ? AND sha256 IS NULL`, sum, id)
	return err
}
```

`SHA256File` returns "" on any read error and the caller deliberately
tolerates it (`scan.go:398-401`):

```go
// ... Empty string means "could not read"; callers leave the column
// NULL so a later scan or the lazy payload backfill can retry.
```

But the scanner's warm-skip (`games.go:202`) matches on the NEW
size/mtime that `updateFileRow` just persisted:

```go
if size, mtime, mtimeNs, ok, serr := db.FileStatByPath(d.path); serr == nil && ok && size == d.size && mtime == d.mtime && mtimeNs == d.mtimeNs && mtimeNs != 0 {
	continue
}
```

### Failure mode

A file's content changes; the scan re-processes it; `hashFile` (128 KiB
sample) succeeds but the full read fails past the sample — transient
I/O error, NFS/spotlight hiccup, file truncated mid-scan. Then:

1. `updateFileRow` writes the new size/mtime/sampled-hash, but
   `coalesce(NULL, sha256)` retains the OLD content hash.
2. Every later scan warm-skips the row (size/mtime match).
3. `backfillSHA256` skips non-NULL values.

The row permanently carries the hash of bytes that no longer exist.
`docs/omilator-client-contract.md` (the `sha256` guarantee added in
`bd8c80c`) promises "The value changes only when the bytes change" and
omilator verifies downloads against it — the client now fails
verification for that file until the bytes change again. The code
comment's claimed retry path ("a later scan or the lazy payload
backfill can retry") exists only for inserted rows, not updated ones.

### Fix sketch

Make update semantics explicit: when the scanner reprocesses a changed
file (size or mtime differ — i.e., we are in `updateFileRow` with fresh
stat evidence), write `sha256 = ?` directly (NULL when hashing failed),
not `coalesce`. That preserves the insert-time behavior (NULL stays
backfillable) and makes a failed re-hash re-tryable by the next scan
(warm-skip must then also treat NULL-sha256 games rows with a present
file as re-hashable — e.g., include `sha256 IS NULL` in the games
warm-skip condition, bounded to the games scanner).

### Regression test spec

- Seed a games file with a known sha256; rewrite the file with different
  bytes and a bumped mtime; run a scan with the hash seam forced to fail
  once → assert row's sha256 is NULL (after fix; currently still the old
  value) and size/mtime are new.
- Re-run the scan with the seam succeeding → assert sha256 equals the
  hash of the new bytes.
- Backfill interplay: a row left NULL by the failed update is filled by
  `GET /works/{id}` (existing test covers only the never-hashed insert
  path — `internal/api/core/games_face_test.go:183-193`).

---

## C-03 — lazy backfill blocks the request with no cancellation or budget

**Severity:** LOW (resource/latency hardening; the once-ever cost is a
documented design choice)
**Files:** `internal/api/core/core.go:808-833,915-917`

### Evidence

```go
func backfillSHA256(db *store.DB, root string, editions []store.EditionView) {
	for i := range editions {
		for j := range editions[i].Files {
			f := &editions[i].Files[j]
			if f.SHA256 != nil { continue }
			fh, err := mediafs.Open(root, f.Path)
			...
			sum := scan.SHA256Content(fh)
```

Called from the `work` handler before any response work
(`core.go:915-917`). `r.Context()` is never consulted; there is no
size cap, no deadline, and no singleflight — unlike every other
expensive path in the repo (2-slot KDF budget `auth.go:62`, 2-slot
subtitle budget `subtitles.go:37`, 2-slot trickplay budget).

### Failure mode

- Client abandons the request (navigation, timeout): the server keeps
  reading a multi-GB ROM to completion; N parallel detail views of
  different works hash N files concurrently (disk thrash).
- If `SetFileSHA256` keeps failing (e.g., read-only DB, sustained
  SQLITE_BUSY — see C-01), every subsequent view re-hashes the same
  files: the "each file is read at most once ever" comment
  (`core.go:810-811`) does not hold.

### Fix sketch

Thread `r.Context()` into the read (wrap the file in a context-reader or
check `ctx.Err()` per file; skip the rest on cancel), add a
process-wide small budget channel (mirror `subtitleSlots`), and
singleflight per file id so concurrent views share one hash.

### Regression test spec

- Handler test with a context that is cancelled mid-hash (hash seam
  blocking on a channel): assert the handler returns promptly and later
  requests still succeed.
- Concurrency test: two simultaneous `GET /works/{id}` for the same
  un-hashed work perform exactly one file read (count via seam).

---

## C-04 — podcast detail reports DB failures as HTTP 200 success

**Severity:** LOW (protocol hygiene)
**Files:** `internal/api/core/podcasts.go:111-131` (also reached by
subscribe/refresh/patch paths)

### Evidence

```go
func (a *API) writePodcastDetail(w http.ResponseWriter, r *http.Request, status int, p *store.Podcast) {
	writeJSON(w, status, a.podcastDetailBody(p, auth.UserID(r)))
}

func (a *API) podcastDetailBody(p *store.Podcast, userID int64) map[string]any {
	eps, err := a.DB.PodcastEpisodes(p.ID)
	if err != nil {
		return map[string]any{"error": "internal error"}
	}
	progs, _ := a.DB.EpisodeProgressByPodcast(userID, p.ID)
```

`status` is 200 (detail/refresh/patch) or 201 (subscribe). On DB failure
the client receives `200 {"error":"internal error"}` — exactly the
false-success shape audit6 F38 fixed for `api()` consumers — and the
episode-progress error is silently dropped (progress markers vanish from
a "successful" render).

### Failure mode

A transient DB failure during podcast detail/refresh/patch renders as a
successful response with no episodes; callers using plain `api()` see
error-shaped data, `apiChecked` callers see a status-0 APIError instead
of a 500.

### Fix sketch

Have `podcastDetailBody` return `(map[string]any, error)`; handlers map
the error to 500 as every other read path does. Surface (or at least
log) the `EpisodeProgressByPodcast` failure.

### Regression test spec

Inject a failing `PodcastEpisodes` (seam or closed DB) and assert
`GET /podcasts/{id}`, `POST /podcasts/{id}/refresh`, `PATCH` answer 500
with `{"error":"internal error"}` and no 2xx.

---

## C-05 — background meta/OPML jobs ignore shutdown and cannot cancel

**Severity:** LOW (shutdown latency; dead code)
**Files:** `internal/api/core/providers.go:830,907-943`,
`internal/api/core/podcasts.go:270,297+`

### Evidence

```go
if !a.launchJob(func() { a.runRefreshMeta(context.WithoutCancel(r.Context()), id, run) }) {
```

and inside the loop:

```go
for i := range inbox {
	if ctx.Err() != nil {
		run.finish(metaSnap{Status: "error", ... Error: "canceled"})
```

`WithoutCancel` makes `ctx.Err()` permanently nil, so the cancel branch
is unreachable; the same shape is in `runOPMLImport`
(`podcasts.go:270`). Scans, by contrast, use the shutdown context
(`core.go:367` `a.runScan(a.scanCtx(), ...)`).

### Failure mode

`main` joins all launchJob goroutines in `srv.Core.WaitJobs()`
(`cmd/libteca/main.go:217`). A refresh-meta pass over a large inbox
(500 works x provider searches at 10s timeout each) or an OPML import of
200 feeds keeps the process alive arbitrarily long after SIGTERM; there
is no bound and no cancellation despite the code clearly intending one.

### Fix sketch

Use `a.scanCtx()` (the shutdown context) for these jobs instead of
`WithoutCancel(r.Context())` — the 202 response is already written
before the job starts, so detaching from the request is preserved by
launchJob itself, not by WithoutCancel.

### Regression test spec

Start a refresh-meta/OPML job with a blocking provider seam; cancel the
shutdown context; assert the job finishes with status "canceled"/"error"
within a short bound and `WaitJobs` returns.

---

## C-06 — embedded-subtitle extraction has no output byte cap

**Severity:** LOW (memory hardening; content requires an admin-scanned
hostile file, consistent with the recorded trust boundary)
**Files:** `internal/api/core/subtitles.go:29-34,182-195`

### Evidence

```go
ffmpegRun = func(ctx context.Context, name string, files []*os.File, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.ExtraFiles = files
	return cmd.Output()
}
```

`extractEmbedded` adds only a 30s deadline; the extracted WebVTT bytes
are buffered unbounded in memory. Pass-7 F08 established the house
pattern — ffprobe got a "4 MiB output cap that kills the child" — and
this path (reachable by any authenticated user via
`GET /subtitles/{fileId}`) never received it.

### Failure mode

A crafted MKV with an enormous subtitle stream makes ffmpeg emit
unbounded stdout for up to 30s; concurrent requests multiply the buffer.

### Fix sketch

Replace `cmd.Output()` with an `io.LimitReader` on `cmd.StdoutPipe`
(kill the child on overflow), mirroring the ffprobe cap.

### Regression test spec

Fake-ffmpeg seam (existing `ffmpegRun` var) writing >cap bytes: assert
the handler 503/404s instead of buffering, and the child is reaped.

---

## Verified clean (this pass)

- **Cookie/Bearer split (`bd8c80c`):** `MediaRequest` is GET/HEAD-only
  (`mediaauth.go:15-20`); the middleware takes the Authorization header
  first, refuses query tokens only on media routes, and consumes the
  cookie only on media routes (`auth.go:308-323`) — mutations
  (`POST /progress`, `DELETE /hls`) require the header. The
  documented consequence that the two mutation routes fall back to
  `?token=` query auth (AUDIT_OPEN post-audit12 note) was confirmed and
  is not re-reported. Web beacon (`web/src/reader/shared.tsx:238-248`)
  is a keepalive fetch with Bearer; body is tiny (no 64 KiB keepalive
  concern); persisted-queue replay covers the no-token/failed-delivery
  cases; `video.tsx:193-198` HLS stop carries Bearer (empty token
  degrades to a caught 401). Cookie set/clear sites (login, `/me`,
  logout) are consistent; `rewriteHLSPlaylist` embeds no token.
- **Audit12 fixes at `7ea80e3`:** `sessionDuration` resolves the bound
  before validation in both sync and close; `timeListened` fully
  validated at handler and store layers (`abs.go:691-699,744-752`).
- **Migration 0016:** additive column; both file INSERT sites use
  explicit column lists (`queries.go:359`, `podcasts.go:505`);
  `fileCols`/`scanFile` updated in lockstep; no `SELECT *` on files.
  Importers create rows with NULL sha256 (games-only field, by
  construction). Down migration is a plain DROP COLUMN.
- **Admin ops:** library add (overlap-checked inside the insert tx),
  delete (podcast gating), linking (cross-library rejection), import
  (validated whole-transaction per LT-120), settings/providers
  (validate-before-write) — no new defects found beyond C-04.
- **Scan/SSE/job machinery:** run guards, panic recovery with terminal
  persistence, dedup window, subscriber lifecycles, admin masking of
  paths/errors — sound.
- **`GET /s/{sid}/t/{index}` without middleware** (server.go:97) is the
  standing pass-6/7 ABS playback-URL credential-lifecycle deferral —
  not re-reported.

## What was run

- `go test ./... -count=1 -timeout 600s` — all green (24 packages with
  tests; corpusutil/assets/bench have none). Matches the recorded
  baseline (23 pkgs + growth).
- `go vet`-equivalent implicit via build; `gofmt` not re-run (no code
  changed).
- Web: `npm ci` (node_modules was absent in this checkout), then
  `npx tsc --noEmit` (clean) and `npx vitest run` — **324/324 tests
  pass, 24 files**, matching the recorded baseline.
- One scratch verification test (C-01 repro) was created inside
  `internal/store`, executed, and deleted; `git status` is clean and no
  commits were made.
