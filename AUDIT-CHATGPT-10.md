# libteca bug audit and implementation plan

**Repository:** `libteca/libteca`  
**Audit baseline:** `main` at `50fc9780ed6d85b2ab7f6be8729db0e382fd0b65`  
**Baseline commit date:** 2026-10-03 21:48:41 UTC  
**Audit date:** 2026-10-04  
**Method:** repository/source inspection through the GitHub connector, review of the repo's existing audit ledger and systematic issue register, targeted inspection of high-risk authentication/media/progress/import/scanner/transcode paths, current open PR/issue review, and current GitHub Actions status.

> This is a best-effort source audit, not a proof that every defect in a nontrivial codebase has been found. The repository already contains unusually extensive historical audits. I did **not** duplicate hundreds of issues that the repository says are fixed; this report focuses on defects and unresolved correctness/security/reliability gaps that are still present at the audit baseline, plus concrete implementation plans.

## Executive summary

The current baseline has a successful GitHub Actions `CI` run. The repository's own `docs/systematic-issue-register.md` also explicitly leaves several areas open: physical source identity/repair, content generations, durable media progress, multipart video/HLS, broader resource budgets, dependency acceptance, real-device/client testing, focused security review, and production/hardware acceptance.

I found **four small, concrete code defects/hardening bugs that can be fixed immediately** and **seven larger confirmed runtime/design gaps already visible in shipped code**. The highest-priority work is:

1. **Stop exposing the general bearer token in media URLs**; issue narrow, short-lived media credentials instead.
2. **Finish multipart video/HLS integration**; the current player/server intentionally uses only `files[0]`.
3. **Add authoritative media/content generations** and key derived caches/playback sessions by generation.
4. **Complete physical source identity and repair**, because the current serving root is inherited from `work.library_id`, not independently bound to each physical file.
5. **Make media progress durable and revision/intent aware**, particularly podcast progress, instead of relying on in-tab retries.
6. **Bound transcode/trickplay/archive resource consumption** based on measured workloads.
7. Fix the two ABS JSON/RNG error-handling bugs and the password-hash entropy panic.

---

# Findings

## AUD-01 — Long-lived general bearer credential is placed in media URLs

**Severity:** High security  
**Confidence:** High  
**Files:** `web/src/api.ts`, `internal/auth/auth.go`, `internal/api/core/hls.go`

### Evidence

The web helper constructs media URLs as:

```ts
return `/api/core${p}${p.includes("?") ? "&" : "?"}token=${encodeURIComponent(token)}`;
```

The server authentication middleware explicitly accepts `?token=...` as the same bearer credential used for normal API authentication. HLS playlist rewriting also propagates that token onto segment URLs.

This means the user's general account credential is transported in URLs. URLs are commonly observable in reverse-proxy/access logs, browser tooling/history-like surfaces, copied URLs, monitoring products, and same-origin request/referrer contexts. The repository's own historical audit already identifies this as an unresolved medium credential-lifecycle issue; in my assessment it deserves high priority because the token is a broad bearer credential rather than a media-only capability.

### Fix

Do **not** remove query authentication abruptly because `<img>`, `<audio>`, `<video>`, `<track>` and EventSource cannot reliably attach arbitrary Authorization headers. Replace the broad token with a **short-lived scoped media ticket**.

Recommended shape:

```text
POST /api/core/media-tickets
Authorization: Bearer <normal token>

{
  "resource": "stream",
  "fileId": 123
}

201
{
  "ticket": "<random 192+ bit value>",
  "expiresAt": "...",
  "url": "/api/core/stream/123?ticket=..."
}
```

Ticket properties:

- Store only a digest of the random ticket value.
- Bind to `user_id`.
- Bind to resource kind plus exact resource identifier (file/cover/subtitle/HLS session/etc.).
- 5–10 minute expiry, renewable only while the normal parent credential is still valid.
- One ticket cannot access arbitrary API routes.
- For HLS, issue a ticket for one playback session and allow only its manifest/segments.
- Explicitly reject normal `token=` query authentication on first-party core routes after migration.
- Keep legacy protocol faces separate if client compatibility requires their existing credential mechanism.
- `Cache-Control: private, no-store` for ticket-issuing responses.
- Never include either normal tokens or media tickets in application logs.

A simple in-memory ticket map like the existing HLS `webTicket` map is acceptable for an initial single-process implementation, but a restart will invalidate playback. A DB-backed digest table is preferable if seamless restart matters.

### Tests

Add tests proving:

- a ticket for file A cannot fetch file B;
- another user cannot reuse it;
- expired tickets fail;
- ticket URLs do not accept the normal general token once migration is complete;
- HLS playlist rewriting propagates only the scoped HLS ticket;
- revoking/deleting the parent user/token invalidates renewable access;
- media ticket values never appear in DB plaintext or logs.

---

## AUD-02 — Multipart video/HLS playback still ignores every file after the first

**Severity:** High functional correctness for multipart media  
**Confidence:** Certain  
**Files:** `internal/api/core/hls.go`, `internal/api/core/core.go`, `web/src/players/video.tsx`, `web/src/contracts/mediaTimeline.ts`, `docs/multipart-video-contract.md`

### Evidence

Current server code repeatedly selects `ed.Files[0]`:

- capability detection checks only the first file;
- `editionPlayback` returns the first file ID;
- `openEditionFile` opens the first file;
- HLS sessions use the first file's path;
- trickplay thumbnails use the first file.

The video player likewise uses the first file ID for its playback/progress path.

The repository already contains a well-designed pure reference contract in `web/src/contracts/mediaTimeline.ts` and explicitly states that it has **not** been integrated into runtime code.

### Failure mode

For an edition containing `part1.mkv`, `part2.mkv`, ...:

- playback ends after part 1;
- seeking to an edition position in a later file cannot select the correct file;
- HLS transcodes the wrong physical file;
- trickplay/subtitles are generated for part 1 rather than the active part;
- progress can be attributed to the wrong file/offset;
- an order change can make an in-flight player silently refer to a different timeline.

### Fix

Integrate the existing timeline contract rather than adding special cases.

#### Server data model/API

Expose an opaque **edition generation** and ordered timeline:

```json
{
  "editionId": 42,
  "generation": "g-...",
  "files": [
    {"fileId": 10, "durationSecs": 1800},
    {"fileId": 11, "durationSecs": 1900}
  ]
}
```

Replace the current GET-only playback negotiation with an explicit selected-file request, matching the repository's proposed contract:

```http
POST /api/core/editions/42/playback
```

```json
{
  "contractVersion": 1,
  "requestId": "...",
  "generation": "g-...",
  "fileId": 11,
  "fileOffsetSecs": 125.5,
  "mode": "auto"
}
```

The server must:

1. reload the authoritative edition timeline;
2. reject stale `generation` with 409;
3. verify `fileId` belongs to that edition;
4. validate `fileOffsetSecs` against that file;
5. negotiate direct vs HLS using the selected file, not `Files[0]`;
6. bind HLS ticket/session to `(user, edition, generation, fileId, fileOffset)`;
7. return the same tuple in the response.

#### Server helper refactor

Change:

```go
func (a *API) openEditionFile(ed *store.EditionView) ...
```

to a selected-file form:

```go
func (a *API) openEditionFile(ed *store.EditionView, fileID int64) (func() (*os.File, error), *store.FileRec, error)
```

Resolve the file strictly from `ed.Files` before opening it within the edition's authoritative physical source root.

Do the same for:

- `browserPlayable`
- `streamable`
- trickplay source
- subtitle endpoint selection
- HLS `TC.Get`

#### Web player

Use `createMediaTimeline`, `resolveEditionPosition`, `resolveFilePosition`, and `resolveEndedFile`.

On load/resume:

```ts
const target = resolveEditionPosition(timeline, generation, savedEditionPosition);
```

Play `target.fileId` at `target.fileOffsetSecs`.

On `ended`, use `resolveEndedFile`; if `next`, negotiate the next file and continue. Only mark the edition finished when the final file finishes.

All progress saves should include both:

- file ID + file-relative offset; and
- cumulative edition position.

Never calculate one from stale local file ordering when generation changed.

### Tests

Add server and browser integration tests for:

- 2- and 3-file direct-play editions;
- later file requiring HLS while earlier file does not;
- seek directly into file 2;
- automatic transition file 1 → file 2;
- final-file-only completion;
- stale generation during transition;
- reordered files during an in-flight negotiation;
- subtitles and trickplay for file 2;
- HLS session cannot be reused for another file.

---

## AUD-03 — Derived trickplay cache has no content/order generation and can serve stale images

**Severity:** Medium  
**Confidence:** High  
**Files:** `internal/api/core/hls.go`, `internal/trickplay/trickplay.go`

### Evidence

Trickplay is keyed as:

```go
itemID := "e" + strconv.FormatInt(ed.ID, 10)
```

and considered valid forever once:

```text
<data>/trickplay/<edition-id>/<width>/COMPLETE
```

exists.

The key contains no source-content generation, file ID, hash, file order, mtime, or scanner revision. The repository's own issue register explicitly leaves content/order generations unresolved.

### Failure mode

If an edition retains its ID but its physical video is replaced or reordered, old JPEG sheets remain `COMPLETE` and are served as if they represent the new content.

### Fix

First introduce the authoritative edition generation described in AUD-02/AUD-04. Then key derived assets by generation:

```text
trickplay/e<edition>-<generation>/<width>/...
```

or:

```text
trickplay/e<edition>/<generation>/<file-id>/<width>/...
```

The second form is better for multipart media.

Generation must be a server-owned opaque value that changes whenever any playback-relevant property changes:

- ordered file membership;
- physical-content identity;
- duration/timing map;
- selected subtitle/derived-content dependencies where relevant.

Do not derive correctness solely from display title, path, mtime, or file ID.

Garbage-collect obsolete generations asynchronously with a bounded retention policy.

### Tests

Generate trickplay; replace source under same edition ID; rescan; assert:

- generation changes;
- old `COMPLETE` marker is not consulted;
- new frames are generated;
- requests carrying old generation receive 409/404 rather than stale images.

---

## AUD-04 — Physical file ownership is still inferred through logical work/library ownership

**Severity:** High data integrity / serving correctness  
**Confidence:** High; repository marks repair as unimplemented  
**Files:** `internal/store/queries.go`, `internal/api/core/core.go`, `internal/sourceinventory/inventory.go`, `docs/source-inventory-command.md`

### Evidence

The serving root for an edition is currently:

```sql
SELECT l.path
FROM editions e
JOIN works w ON w.id = e.work_id
JOIN libraries l ON l.id = w.library_id
WHERE e.id = ?
```

A file row itself does not hold an authoritative source-library/root identity. The repository now prevents new cross-library reparenting, but its own source-inventory tooling explicitly warns that existing records may have `apparent_root_mismatch`, mixed roots, or collapsed editions, and that the repair/migration remains unimplemented.

### Failure mode

Historical or imported malformed rows can have a physical path belonging to one source while the logical work points at another library. Serving then resolves the file beneath the wrong root and can produce inaccessible media or incorrect logical grouping.

### Fix

Do not auto-repair by title/path prefix alone. Use the existing read-only inventory as the prerequisite.

Recommended schema:

```sql
CREATE TABLE media_sources (
  id INTEGER PRIMARY KEY,
  library_id INTEGER NOT NULL REFERENCES libraries(id),
  relative_path TEXT NOT NULL,
  content_identity BLOB,
  ...
);

ALTER TABLE files ADD COLUMN source_id INTEGER REFERENCES media_sources(id);
```

If every edition can be proven single-root, an edition-level source-library FK is simpler, but the existing inventory explicitly anticipates mixed/ambiguous records, so **per-file source ownership is safer**.

Migration procedure:

1. Take a consistent backup/snapshot.
2. Run `cmd/source-inventory` against a disposable copy and mapped copied roots.
3. Produce an operator-reviewed mapping for ambiguous files/editions.
4. Add schema without changing public IDs.
5. Populate unambiguous mappings automatically.
6. Refuse to migrate ambiguous rows without reviewed mapping.
7. Preserve file IDs, edition IDs, progress rows/revisions, playlists, sessions where meaningful.
8. Change all openers to resolve `(library root, relative path)` from `source_id`, never through `work.library_id`.
9. Add invariant checks: a live file must have exactly one valid source identity.
10. Rehearse rollback on a copy before touching production.

### Tests

Fixtures should include:

- two libraries with same title/author;
- historical wrong-root edition;
- mixed-root multipart edition;
- duplicate filenames and duplicate hashes;
- intentionally duplicated content;
- renamed source;
- source file missing;
- progress pointing at one of several collapsed files.

No migration should silently guess an ambiguous ownership mapping.

---

## AUD-05 — Audio/video progress durability is still in-tab only; podcast progress lacks revision/CAS semantics

**Severity:** Medium-High reliability / cross-device correctness  
**Confidence:** High  
**Files:** `web/src/players/audioProgress.ts`, `web/src/players/videoProgress.ts`, `internal/store/podcasts.go`, `docs/media-intent-contract.md`

### Evidence

Primary media progress retry state lives in JS memory. `AudioProgressSaver` retries with timers, and `VideoProgressSaver` serializes writes, but these objects do not provide durable replay after tab/process loss.

`podcast_episode_progress` updates are unconditional upserts and carry no revision/tombstone/intent protocol equivalent to the reader progress system.

The repository already contains a pure `mediaIntent` reference model, but it explicitly says no production player/API/storage integration imports it.

### Failure modes

- Close/crash/browser eviction during a failed save can lose the last position.
- A delayed write from another tab/device can overwrite a deliberate rewind/restart.
- "Maximum position wins" is not safe for media because backward seeks are intentional.
- Podcast progress cannot detect stale writers.

### Fix

Implement the existing media-intent design in production:

1. Add revision and tombstone fields to podcast progress.
2. Add operation/intent fields needed to distinguish heartbeat, seek, restart, finish, reset.
3. Persist outgoing operations before network transmission in user-scoped browser storage (IndexedDB is preferable to localStorage for structured queues).
4. Never store auth tokens in durable operation records or keys.
5. Send original base revision and immutable operation ID.
6. Server validates the complete timeline tuple and generation, then applies with CAS.
7. On ambiguous network failure, retain operation and reconcile before retry.
8. Do not silently merge stale explicit seeks/restarts/completions.
9. Add cross-tab replay ownership so two tabs do not race the same operation.
10. Fence login/account changes.

Use the existing `web/src/contracts/mediaIntent.ts` rules as the behavioral specification; wire them to a production adapter rather than rewriting the conflict logic ad hoc.

### Tests

Real browser/E2E tests should cover:

- offline pause and close;
- crash/reload after request sent but before response;
- storage quota denial;
- two tabs;
- two devices;
- account switch;
- backward seek vs remote completion;
- restart vs delayed completion;
- generation change while operation is uncertain;
- podcast equivalents.

---

## AUD-06 — Transcode/trickplay disk consumption is bounded by concurrency, not by bytes

**Severity:** Medium availability/resource exhaustion  
**Confidence:** High  
**Files:** `internal/transcode/transcode.go`, `internal/trickplay/trickplay.go`, `docs/resource-measurement.md`

### Evidence

Transcode limits sessions (`MaxSessions = 8`) and reaps idle sessions, which is good. Trickplay limits concurrent generation. However there is no aggregate byte quota for active HLS output or persistent trickplay generations. A small number of very long/high-bitrate videos can consume substantial disk even within the session-count limit, and trickplay caches persist.

The repository explicitly leaves this as an open resource-budget item.

### Fix

Do not choose arbitrary byte caps. Use the existing measurement harness and representative content, then implement:

- configured global cache/transcode high-water and low-water marks;
- per-session maximum bytes or maximum retained media duration, chosen from measurements;
- preflight free-space check;
- accounting while segments/sheets are created;
- stop/cleanup on quota breach;
- LRU eviction for derived trickplay generations;
- no eviction of HLS segments still referenced by the currently served playlist unless the playlist uses a rolling window;
- metrics/logging for current bytes and rejected work;
- startup reconciliation/cleanup of abandoned cache entries.

For HLS, the cleanest contract is a rolling playlist window plus a bounded back-buffer if arbitrary backwards seeking is not promised. If full-session seeking is promised, reserve/estimate disk and reject early when insufficient.

### Tests

- sparse/small filesystem fixture;
- ffmpeg producing output past quota;
- concurrent sessions competing for global quota;
- cleanup on cancellation, crash-like leftover, and failed ffmpeg;
- trickplay LRU never removes a generation currently being served.

---

## AUD-07 — ABS `/play` silently accepts malformed JSON and ignores RNG failure

**Severity:** Medium protocol correctness; Low-probability session-ID robustness  
**Confidence:** Certain  
**File:** `internal/api/abs/abs.go`

### Evidence

Current `play` does:

```go
json.NewDecoder(r.Body).Decode(&body)

raw := make([]byte, 16)
rand.Read(raw)
sid := hex.EncodeToString(raw)
```

Both errors are ignored.

Malformed JSON therefore falls through using zero-value fields. If `crypto/rand` ever fails, the zero-filled buffer can produce a deterministic all-zero session ID; repeated requests can collide or fail unpredictably.

### Fix

Use a shared strict body decoder and propagate RNG errors:

```go
if err := decodeJSONBody(w, r, 1<<20, &body); err != nil {
    writeABSBodyError(w, err)
    return
}

var raw [16]byte
if _, err := rand.Read(raw[:]); err != nil {
    serverError(w, r, err)
    return
}
sid := hex.EncodeToString(raw[:])
```

`decodeJSONBody` should:

- apply `http.MaxBytesReader`;
- return 413 for `*http.MaxBytesError`;
- reject malformed JSON with 400;
- optionally reject trailing non-whitespace JSON values;
- optionally `DisallowUnknownFields` only if real ABS clients tolerate it.

Do not make unknown-field rejection part of the first compatibility patch unless it has client corpus coverage.

### Tests

- malformed JSON → 400 and no session row;
- >1 MiB → 413 and no session row;
- valid `{}` remains accepted if that is required by ABS;
- inject/factor an RNG function so a forced entropy failure → 500 and no session row.

---

## AUD-08 — ABS `/session/{id}/close` silently treats malformed JSON as a zero-position close

**Severity:** Medium protocol/data correctness  
**Confidence:** Certain  
**File:** `internal/api/abs/abs.go`

### Evidence

`sessionSync` correctly checks JSON decoding, but `sessionClose` does:

```go
json.NewDecoder(r.Body).Decode(&body)
```

without checking the error.

A malformed/truncated body consequently becomes `CurrentTime == 0`, `Duration == 0`, `TimeListened == 0` and can close a valid session without recording the intended progress.

### Fix

Use the same shared bounded decoder proposed in AUD-07.

```go
if err := decodeJSONBody(w, r, 1<<20, &body); err != nil {
    writeABSBodyError(w, err)
    return
}
```

Only close the session after successful decoding and validation.

### Tests

Start a real session, submit truncated JSON to `/close`, assert:

- 400 response;
- session remains open;
- existing progress unchanged.

Then submit valid close and assert atomic progress/session closure.

---

## AUD-09 — Password hashing panics the whole process if OS entropy fails

**Severity:** Low probability / High blast radius  
**Confidence:** Certain  
**File:** `internal/auth/auth.go`

### Evidence

```go
func Hash(password string) string {
    salt := make([]byte, 16)
    if _, err := rand.Read(salt); err != nil {
        panic(err)
    }
    ...
}
```

`dummyHash` calls `Hash` during package initialization. User/admin password hashing also relies on it.

An entropy-source failure should be a controlled startup/request error, not a process panic from an otherwise recoverable function.

### Fix

Change the API to return an error:

```go
func Hash(password string) (string, error) {
    salt := make([]byte, 16)
    if _, err := rand.Read(salt); err != nil {
        return "", err
    }
    key := argon2.IDKey(...)
    return fmt.Sprintf(...), nil
}
```

Then:

```go
func HashRequest(...) (string, error) {
    ...
    return Hash(password)
}
```

For the dummy hash, avoid runtime randomness entirely. Use a checked-in **valid Argon2 string for a fixed dummy password/salt** solely for timing equalization, or initialize it in a startup function that can return an error. A fixed dummy hash is not a credential and does not need an unpredictable salt.

Update `InitAdmin` and every caller.

### Tests

Inject entropy reader or factor `readRandom`:

- forced failure returns an error;
- no panic;
- no user row created;
- login timing-equalization path still performs Argon2 work for unknown users.

---

## AUD-10 — Subsonic compatibility stores the user's plaintext password in application settings

**Severity:** High security if Subsonic is enabled/used  
**Confidence:** Certain; intentional compatibility design  
**File:** `internal/api/subsonic/subsonic.go`

### Evidence

Subsonic token auth requires `md5(password + salt)`. The implementation captures a successful plaintext/hex password and stores it in a setting:

```go
a.DB.SetSetting(subsonicSecretKey(u.ID), pass)
```

Later it retrieves that secret to recompute the Subsonic token.

This defeats the at-rest protection provided by Argon2 for that user's main password: compromise of the database/settings can expose the actual account password.

### Fix

The only robust fix is to separate credentials.

Recommended migration:

1. Add a per-user **Subsonic app password**/compatibility secret distinct from the main login password.
2. Generate it randomly server-side (or allow explicit user-generated secret).
3. Display it once or allow rotation.
4. Store it encrypted using an installation master key **or**, if Subsonic protocol truly requires recovering plaintext-equivalent bytes, document that reversibility is required. A one-way hash cannot compute `md5(secret + arbitrary_client_salt)`.
5. Never automatically copy the main account password into this field.
6. Existing users with cached `subsonic.pw.*` must be prompted/required to rotate to a dedicated app password; securely delete the old setting after migration.
7. Consider disabling legacy plain-password Subsonic login after setup, depending on client compatibility.
8. Apply rate limits to both plaintext and token-auth paths (the current code already has useful limiter work).

If the project refuses reversible at-rest secrets, do not support `t+s` authentication; support only protocol modes compatible with a stored verifier/app token.

### Tests

- main password never appears in settings;
- Subsonic app password can be rotated independently;
- rotating main account password does not expose or silently overwrite app secret;
- deleting Subsonic compatibility credential disables token auth only.

---

## AUD-11 — Current scan identity can still conflate logically separate works that share `(library, title, author)`

**Severity:** Medium-High data integrity  
**Confidence:** High; architectural family already acknowledged  
**File:** `internal/store/queries.go`, scanners

### Evidence

`UpsertWork` resolves a work with:

```sql
WHERE library_id = ?
  AND lower(title) = lower(?)
  AND lower(coalesce(author,'')) = lower(coalesce(?, ''))
```

That is display metadata, not physical identity. Different releases/works in one library with the same normalized title/author can therefore be folded into the same work unless higher scanner structure prevents it. The repository's source-identity proposal already recognizes title/author identity as an unresolved family.

### Fix

Treat work metadata as descriptive, not identity.

A good target model:

- physical `source` / `source_group` has stable ownership;
- scanner derives an edition from a source group;
- logical work linking is an explicit relation that may be suggested by metadata but not silently asserted;
- uniqueness indexes should be on stable source identity, not title/author;
- title/author index remains only for search and duplicate suggestions.

During scan:

1. resolve source identity first;
2. update/create its edition without considering display title uniqueness;
3. separately attach to an existing work only when an explicit durable link already exists;
4. otherwise create a distinct logical work or present a merge suggestion.

This should be implemented together with AUD-04 to avoid two migrations.

### Tests

- two books named “Collected Works” by same author in different source folders remain distinct;
- remaster/director's-cut/alternate edition remains distinguishable;
- rename/title metadata edit does not create a new physical identity;
- explicit same-library merge still works.

---

# Recommended implementation order

## Phase 1 — Small safe patches

Implement AUD-07, AUD-08 and AUD-09 first. They are narrow, low-risk changes with straightforward regression tests.

Suggested commits:

```text
fix(abs): reject malformed playback/session-close bodies
fix(abs): handle playback session entropy failures
fix(auth): return password hash entropy errors instead of panicking
```

## Phase 2 — Credential containment

Implement AUD-01 and AUD-10 before broad exposure/deployment.

Suggested sequence:

1. media-ticket endpoint + validation middleware/helper;
2. convert first-party media URL generation;
3. convert HLS playlist rewriting;
4. remove broad core `token=` fallback after compatibility transition;
5. dedicated Subsonic app credentials and migration.

## Phase 3 — Media generation + multipart video

Implement AUD-02 and AUD-03 together:

1. authoritative edition generation;
2. expose timeline;
3. selected-file playback negotiation;
4. server selected-file helpers;
5. first-party video state machine;
6. trickplay cache keyed by generation/file;
7. multipart tests.

Do not implement multipart HLS before generation semantics; otherwise reorder/replacement races remain ambiguous.

## Phase 4 — Source identity migration

Implement AUD-04 + AUD-11 as one schema/design project.

Use the already-shipped `source-inventory` command before touching real data. Preserve public IDs and progress revisions.

## Phase 5 — Durable media intent

Implement AUD-05 after source/timeline generation exists, because durable operations need a trustworthy generation and file tuple.

## Phase 6 — Resource budgets and acceptance

Implement AUD-06 using real measurements. Then run the repository's remaining acceptance gates: representative EPUB/archive/media corpus, browsers, ABS/Jellyfin/Subsonic clients, Docker/native/hardware acceleration, long-play disk behavior and production-sized libraries.

---

# Patch sketches

## Shared ABS decoder

Create e.g. `internal/api/abs/body.go`:

```go
package abs

import (
    "encoding/json"
    "errors"
    "io"
    "net/http"
)

var errTrailingJSON = errors.New("trailing json")

func decodeBody(w http.ResponseWriter, r *http.Request, max int64, dst any) error {
    r.Body = http.MaxBytesReader(w, r.Body, max)
    dec := json.NewDecoder(r.Body)
    if err := dec.Decode(dst); err != nil {
        return err
    }
    var extra any
    if err := dec.Decode(&extra); err != io.EOF {
        if err == nil {
            return errTrailingJSON
        }
        return err
    }
    return nil
}

func bodyErrorStatus(err error) (int, string) {
    var large *http.MaxBytesError
    if errors.As(err, &large) {
        return http.StatusRequestEntityTooLarge, "Request body too large"
    }
    return http.StatusBadRequest, "Invalid body"
}
```

Use it in `play`, `sessionSync`, `sessionClose`, progress mutations and any other ABS JSON handlers for consistent behavior.

## Selected-file server helper

```go
func fileInEdition(ed *store.EditionView, fileID int64) (*store.FileRec, error) {
    for i := range ed.Files {
        if ed.Files[i].ID == fileID {
            return &ed.Files[i], nil
        }
    }
    return nil, store.ErrNotFound
}

func (a *API) openEditionFile(
    ed *store.EditionView,
    fileID int64,
) (func() (*os.File, error), *store.FileRec, error) {
    f, err := fileInEdition(ed, fileID)
    if err != nil {
        return nil, nil, err
    }

    // After AUD-04 this should use f.SourceID, not the logical work's library.
    root, err := a.confinedRoot(ed.ID)
    if err != nil {
        return nil, nil, err
    }

    return func() (*os.File, error) {
        return mediafs.Open(root, f.Path)
    }, f, nil
}
```

Later replace `confinedRoot(ed.ID)` with an authoritative source-root lookup from `f.SourceID`.

## Generation table option

A transactionally bumped opaque integer is sufficient; it need not be a hash:

```sql
ALTER TABLE editions ADD COLUMN media_generation INTEGER NOT NULL DEFAULT 1;
```

Every transaction that changes any playback-relevant file membership/order/content/timing must:

```sql
UPDATE editions
SET media_generation = media_generation + 1
WHERE id = ?;
```

Advantages over deriving from path/mtime:

- exact server authority;
- cheap comparison;
- no accidental semantic meaning;
- easy CAS/409 behavior.

If a scanner transaction touches an edition but determines there is no semantic change, do not bump.

---

# Validation checklist after implementation

Run at minimum:

```sh
gofmt -w <changed-go-files>
go vet ./...
go test ./... -count=1 -timeout 600s
go test -race ./internal/store ./internal/api/core ./internal/api/abs ./internal/auth ./internal/transcode ./internal/trickplay

cd web
npm ci
npm run test
npx tsc --noEmit
npm run build
npm audit --omit=dev --audit-level=high
```

Then add acceptance that unit tests cannot substitute for:

- Chrome, Firefox, Safari/WebKit video transitions across multipart files;
- direct→HLS and HLS→next-file transitions;
- mobile/background/page-hide progress behavior;
- reverse proxy with access logging, confirming no general bearer token appears in URLs;
- real ABS and Subsonic clients;
- representative long media for disk-budget measurement;
- source-identity migration rehearsal on a disposable copy of a real library.

---

# Existing repository audit state I intentionally did not duplicate

The repository already contains `AUDIT_OPEN.md`, multiple `AUDIT-CHATGPT-*.md` passes, `docs/systematic-issue-register.md`, `docs/review-source-inventory.md` and contract/measurement documents. Those report a large number of previously repaired defects. This audit treated those fixes as historical context and inspected the current baseline instead of presenting old fixed findings as new work.

The repository's current systematic register explicitly leaves these broad gates open, all consistent with this audit:

- physical source identity and existing data repair;
- content/order generations and derived assets;
- durable offline media progress;
- multipart video/HLS;
- broader archive/decode/transcode budgets;
- runtime dependency acceptance;
- real library/browser/client compatibility;
- focused authentication/security review;
- production/container/hardware/performance acceptance.

No open GitHub issue was found during the audit. Several dependency update PRs are open; coordinate rather than duplicating them.

The baseline GitHub Actions `CI` workflow completed successfully, but a green CI run does not cover the runtime/design gaps above.

---

# Definition of done

I would consider this audit's implementation work closed only when:

- malformed ABS requests cannot mutate session/progress state;
- entropy failures never panic normal request/startup paths;
- first-party media URLs never contain the normal bearer token;
- Subsonic compatibility never stores the main account password reversibly;
- multipart video plays every ordered file with correct seek/progress/subtitles/trickplay;
- generation mismatch is detected rather than guessed through;
- derived caches cannot survive a source-generation change as current data;
- every physical file has explicit authoritative source ownership;
- title/author are no longer the primary physical identity key;
- media progress survives reload/crash and stale writers cannot silently overwrite explicit intent;
- disk/resource limits are based on measured representative workloads and fail cleanly;
- real-browser, real-client and production-like acceptance passes.

