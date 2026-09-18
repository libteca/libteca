# libteca repository audit and implementation report

**Repository:** [libteca/libteca](https://github.com/libteca/libteca)  
**Audited commit:** [`b95620bc6bce313bf32d40837ed7464e090542f2`](https://github.com/libteca/libteca/commit/b95620bc6bce313bf32d40837ed7464e090542f2)  
**Snapshot:** `main` resolved to this commit during the review; commit timestamp 2026-09-17 17:53:07 UTC.  
**Audit date:** September 17, 2026.  
**Deliverable:** Source findings, proposed code changes, regression tests and validation limits in one Markdown file. No repository changes were made.

## Executive assessment

This review identified **35 prioritized findings**, ranging from conditional filesystem/capability exposure and durable-state corruption to playback failures, cleanup/backup defects and missing behavioral coverage. Evidence links point to the exact audited commit. Several underlying filesystem, SQL and FFmpeg behaviors were reproduced with benign local fixtures. The complete application was not built or tested in this environment.

**The highest priorities are:** consistent confinement for every media consumer; removing writable subtitle caches from library directories; tying public playback capabilities to revocation and expiry; avoiding plaintext primary-password capture; preserving identities and progress during scans/patches; protecting the running instance and immutable backups.

“High” indicates substantial confidentiality, integrity or operational impact under the stated preconditions. It is not a claim of an unauthenticated remote exploit. “Medium” covers meaningful correctness, isolation, reliability or deployment gaps; “Low” covers narrower configuration behavior. “Confirmed” means the inspected code supports the mechanism; the validation column distinguishes local primitive/model tests from whole-application execution. Design hardening and unresolved hardware questions are explicitly identified.

**Completeness:** This is a broad, source-based audit, not proof that every possible bug was found. The coverage matrix lists partial and unreviewed areas. No critical unauthenticated exploit is established by this review. Proposed implementations have differing validation levels and must be integrated/tested with the complete repository before deployment.

## Findings index

| ID | Priority | Finding |
|---|---|---|
| [F01](#f01) | High | Library confinement is bypassed by book downloads, OPDS and Subsonic |
| [F02](#f02) | High | Embedded subtitle caching permits out-of-root reads and writes |
| [F03](#f03) | High | HLS, trickplay and embedded extraction reopen unconfined processor inputs |
| [F04](#f04) | High | ABS streaming capabilities outlive revocation of the token that created them |
| [F05](#f05) | High | Subsonic silently stores the primary account password in plaintext |
| [F06](#f06) | Medium | Authenticated covers and thumbnails are explicitly share-cacheable |
| [F07](#f07) | Medium | Authentication turns database failures into logout-worthy 401 responses |
| [F08](#f08) | Medium | Overlapping library roots can pass concurrent validation |
| [F09](#f09) | Medium | A symlink library root is accepted but scanned as an empty root |
| [F10](#f10) | High | Mutable title-based scanner identity can strand progress and playlists |
| [F11](#f11) | Medium | A normal one-scan rename does not reach the relinking path |
| [F12](#f12) | High | The sampled file hash is unsafe as an identity proof and discards I/O errors |
| [F13](#f13) | Medium | Incremental scans ignore sidecar and group-membership changes |
| [F14](#f14) | Medium | Scan failures and late cancellation can be reported as successful completion |
| [F15](#f15) | High | A second server using the same data directory can damage the running instance |
| [F16](#f16) | Medium | Audio-only HLS fails because the transcoder requires a video stream |
| [F17](#f17) | Medium | HLS uses the first file while validating an edition-wide timeline |
| [F18](#f18) | Medium | HLS ignores small start offsets and silently reuses an incompatible session |
| [F19](#f19) | Medium | Hardware capability probes can block all transcode management indefinitely |
| [F20](#f20) | Low | Explicit hardware auto selection can lose to an environment override |
| [F21](#f21) | Medium | Core and Jellyfin race independent trickplay generators over the same cache |
| [F22](#f22) | Medium | Derived caches can survive replacement of their underlying media |
| [F23](#f23) | Medium | Jellyfin HLS segment access does not enforce session ownership or item binding |
| [F24](#f24) | Medium | Podcast download errors prevent retention enforcement |
| [F25](#f25) | Medium | Podcast purging discards unlink errors and can lose cleanup tracking |
| [F26](#f26) | Medium | Podcast URL normalization changes resource identity without consent |
| [F27](#f27) | High | All database backups share one mutable cover directory |
| [F28](#f28) | High | Partial progress updates reset fields the client did not send |
| [F29](#f29) | High | The reader progress queue loses patches on overwrite/failure and permits reordering |
| [F30](#f30) | Medium | Metadata cover downloads are non-atomic and use a weaker outbound-fetch policy |
| [F31](#f31) | Medium | Watcher recovery leaves stale descendant watches and treats recent failed scans as fresh |
| [F32](#f32) | Medium | The container runs the media server and processors as root |
| [F33](#f33) | Medium | CI does not exercise frontend behavior or the cross-adapter failure cases found here |
| [F34](#f34) | Medium | ABS masks database failures as empty progress or successful deletion |
| [F35](#f35) | Medium | Importer file discovery accepts nonregular/unconfined entries and hides traversal errors |

**Totals:** 11 high, 23 medium, 1 low. Findings sharing a root cause are cross-referenced rather than presented as unrelated exploits.

## Detailed findings


<a id="f01"></a>

### F01 — Library confinement is bypassed by book downloads, OPDS and Subsonic

**Priority:** High  
**Evidence level:** Confirmed source defect; filesystem primitive reproduced

**Source:** [`internal/api/core/reading.go` — `editionDownload`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/reading.go); [`internal/api/opds/opds.go` — `download / psePage`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/opds/opds.go); [`internal/api/subsonic/subsonic.go` — `stream`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/subsonic/subsonic.go#L477-L505); [`internal/mediafs/mediafs.go` — `OpenWithin`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/mediafs/mediafs.go).

**Problem and impact.** Direct core/ABS/Jellyfin streaming uses `mediafs.OpenWithin`, but the core edition download calls the unconfined `serveFile`; OPDS opens the stored path directly, including `zip.OpenReader`; Subsonic streaming also calls `os.Open`. A path originating in the database is not permanently safe: a library file or ancestor can be replaced by a symlink after scanning. An authenticated requester can then retrieve bytes outside the configured library through these alternate routes. An imported out-of-root path creates the same mismatch.

**Threat boundary.** This is not an unauthenticated arbitrary-path query vulnerability. It requires control over library filesystem contents/ancestors, or a dangerous imported database path, plus access to the affected media route. The service's filesystem privileges determine the damage. A benign temporary-file model returned an outside sentinel through a symlink.

**Implementation.** Replace direct opens with the existing rooted helper and serve the *same opened descriptor*. `LibraryRootForEdition` and core `serveConfined` already exist; reuse them rather than adding another competing path policy. For core `editionDownload`, replace its last `serveFile` call with:

```go
root, err := a.DB.LibraryRootForEdition(id)
if err != nil {
    writeJSON(w, 404, map[string]string{"error": "edition not found"})
    return
}
serveConfined(w, r, root, f.Path)
```

For OPDS download, import `internal/mediafs`, obtain the root for `eid`, and replace `os.Open` and the separate `Stat` with `mediafs.OpenWithin(root, f.Path)`. Preserve its content-type and response handling. For OPDS PSE:

```go
root, err := a.DB.LibraryRootForEdition(eid)
if err != nil { http.NotFound(w, r); return }
fh, fi, err := mediafs.OpenWithin(root, f.Path)
if err != nil { http.NotFound(w, r); return }
defer fh.Close()
zr, err := zip.NewReader(fh, fi.Size())
if err != nil { http.NotFound(w, r); return }
// Keep the existing ZIP-entry selection/streaming logic and enforce an entry byte budget.
// zip.Reader itself has no Close method; fh owns the descriptor.
```

Subsonic uses `song.Edition.ID` to retrieve the root and `song.File.Path` for the confined open. Do not first check with `EvalSymlinks` and then call the old open: that reintroduces a time-of-check/time-of-use race.

**Regression tests.** Parameterize core download, OPDS download/PSE, Subsonic stream/download and the already-protected routes. Scan a benign fixture, replace the file and then a parent directory with an outside symlink, and assert rejection without outside bytes. Include imported outside paths, FIFOs and directories. Preserve valid in-root symlink behavior supported by `os.Root`.


<a id="f02"></a>

### F02 — Embedded subtitle caching permits out-of-root reads and writes

**Priority:** High  
**Evidence level:** Confirmed source defect; read/write primitive reproduced

**Source:** [`internal/api/core/subtitles.go` — `cachedVTT / embeddedVTT / extractEmbedded`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/subtitles.go).

**Problem and impact.** The SRT branch uses a confined open, but the embedded branch reads `<media>.libteca.vtt` using unbounded `os.ReadFile` and writes the same path with `os.WriteFile`. Both follow symlinks. A library writer can point this cache name at another service-readable file and satisfy the modification-time check to disclose it. When the cache is stale, successful extraction can instead truncate and overwrite a service-writable symlink target. The temporary-file experiment demonstrated both operations using a harmless sentinel.

Even without an attacker, writing generated files alongside media fails on the intended read-only media mounts. That error is discarded, causing repeated extraction. There is no shared extraction concurrency cap, and `.Output()` buffers all subtitle output.

**Implementation.** Delete `libtecaVTT`/`cachedVTT` as library-side cache mechanisms. Put cache files under a service-owned `DataDir/subtitles` directory, key them by source identity/version, use bounded reads and atomic publication, and share a small extraction semaphore. Use the descriptor-based processor implementation in **P1** and atomic writer in **P2** below. A concrete replacement core is:

```go
var subtitleSlots = make(chan struct{}, 2)

func (a *API) extractSubtitle(ctx context.Context, root, media, key string) ([]byte, error) {
    // key must be a server-generated hexadecimal digest, not request text.
    if len(key) != 64 || strings.Trim(key, "0123456789abcdef") != "" {
        return nil, fmt.Errorf("invalid cache key")
    }
    dir := filepath.Join(a.DataDir, "subtitles")
    if err := os.MkdirAll(dir, 0o700); err != nil { return nil, err }
    dst := filepath.Join(dir, key+".vtt")
    if b, err := readBoundedRegular(dst, 16<<20); err == nil {
        return b, nil
    } else if !errors.Is(err, os.ErrNotExist) {
        return nil, err
    }
    select {
    case subtitleSlots <- struct{}{}:
        defer func() { <-subtitleSlots }()
    case <-ctx.Done():
        return nil, ctx.Err()
    default:
        return nil, fmt.Errorf("subtitle extraction busy")
    }
    ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()
    data, err := runConfinedFFmpeg(ctx, root, media,
        []string{"-map", "0:s:0", "-f", "webvtt", "-"}, 16<<20)
    if err != nil { return nil, err }
    if err := atomicWritePrivate(dst, data); err != nil { return nil, err }
    return data, nil
}
```

This uses P1/P2 helpers and needs the corresponding imports. Add per-key singleflight/coalescing before expensive extraction so concurrent HEAD/GET requests do not duplicate work. Map capacity errors to 503 plus `Retry-After`, not a false “no subtitles” 404. The key should use the full-content version from F12 or an explicitly maintained source generation; hashing only the filename is insufficient.

**Regression tests.** Cache symlink read and write attempts, stale cache, read-only library, oversized cached/extracted subtitles, simultaneous HEAD/GET, extraction cancellation, and disk-full publication. Tests must assert outside files remain unchanged.


<a id="f03"></a>

### F03 — HLS, trickplay and embedded extraction reopen unconfined processor inputs

**Priority:** High  
**Evidence level:** Confirmed source defect; descriptor-input feasibility tested

**Source:** [`internal/api/core/hls.go` — `hlsFile / editionThumbs / editionThumbTile`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/hls.go); [`internal/api/jellyfin/jellyfin.go` — `hlsMaster / trickplayTile / trickplayManifest`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/jellyfin/jellyfin.go#L1065-L1390); [`internal/transcode/transcode.go` — `launch`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/transcode/transcode.go); [`internal/trickplay/trickplay.go`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/trickplay/trickplay.go); [`internal/api/core/subtitles.go` — `extractEmbedded`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/subtitles.go).

**Problem and impact.** These paths hand the stored pathname to FFmpeg rather than using the library-root policy applied to direct streams. Fixing only the HTTP download routes in F01 would leave the same boundary bypass in media processors. A check followed by a subprocess reopening that pathname is still racy. Media demuxers can also open secondary resources; constraining only the initial pathname does not constrain those accesses.

**Implementation.** Give processors a rooted, already-opened regular file. On the audited Unix targets, attach it as `exec.Cmd.ExtraFiles[0]` and use FFmpeg input options `-protocol_whitelist fd -fd 3 -i fd:`. See **P1** for a bounded implementation used by subtitle extraction. Change transcode/trickplay process factories to accept the opened descriptor rather than a raw source string. Keep a session-owned descriptor alive until both initial processing and any hardware-to-software retry have completed; reset its offset before a sequential retry. Do not close it immediately after `Start` if it is needed for fallback.

The installed FFmpeg 7.1.5 successfully decoded a WAV with this descriptor input. This does **not** verify every distribution's FFmpeg build. Add a startup capability probe, and fail closed or use an equivalent sandboxed input method when `fd` is unavailable. Do not silently fall back to an unconfined pathname. A pipe is not a universal substitute because many formats need seeking.

Audit scanner/ffprobe/conversion input paths under the same policy when completing this refactor; their entire subprocess surface was not exhaustively reviewed here.

**Regression tests.** Swap an input symlink after lookup; use a media file referencing an external resource; verify no outside file or outbound resource is read. Test seekable MP4/MKV input, audio-only input, and fallback after an initial hardware failure with the same confined descriptor.


<a id="f04"></a>

### F04 — ABS streaming capabilities outlive revocation of the token that created them

**Priority:** High  
**Evidence level:** Confirmed lifecycle gap; no live-service exploit attempted

**Source:** [`internal/server/server.go` — `public session-track route`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/server/server.go); [`internal/api/abs/abs.go` — `play / SessionTrack`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/abs/abs.go#L483-L602); [`internal/store/progress.go` — `CreateSession / Session`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/progress.go); [`internal/store/users.go` — `RevokeToken / RevokeTokenByValue / RotatePasswordChecked`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/users.go).

**Problem and impact.** `/s/{sid}/t/{index}` is a public capability URL. Its lookup checks that a playback session exists and is not closed, but sessions have no parent-token binding or expiry. Token revocation updates `tokens` only. Consequently a previously obtained session URL remains usable after the originating login token is revoked or logged out. Password rotation does close playback sessions; that existing protection should be retained. Random session IDs prevent practical guessing, but do not solve revocation of a *known* capability.

**Implementation.** Add token binding and expiry to playback sessions. Apply the following as a new numbered migration, not an edit of a migration already deployed:

```sql
ALTER TABLE playback_sessions ADD COLUMN parent_token_id INTEGER REFERENCES tokens(id);
ALTER TABLE playback_sessions ADD COLUMN expires_at INTEGER;
CREATE INDEX playback_sessions_parent_token ON playback_sessions(parent_token_id);
-- Existing capabilities cannot be safely assigned to an originating token.
UPDATE playback_sessions
SET closed_at = CAST(strftime('%s','now') AS INTEGER)*1000
WHERE closed_at IS NULL;
```

Create a session with one `INSERT ... SELECT` conditional on the authenticated token still being active, preventing a create-versus-revoke race:

```sql
INSERT INTO playback_sessions
(id,user_id,edition_id,started_at,updated_at,position_secs,time_listened_secs,
 device_info,parent_token_id,expires_at)
SELECT ?,t.user_id,?,?,?,?,?,?,t.id,?
FROM tokens t
WHERE t.value=? AND t.user_id=? AND t.revoked_at IS NULL;
```

Bind arguments in order to session ID, edition ID, start/update times, position, listened time, device JSON, expiry, `auth.Token(r)`, and user ID. Require `RowsAffected()==1`. Extend `Session`'s type and accessors. The public track lookup must join its token and require `t.revoked_at IS NULL`, `s.closed_at IS NULL`, and `s.expires_at > now`; update the expiry only on authenticated synchronization, using an explicit maximum lifetime. A proposed initial policy is a 24-hour absolute lifetime with reauthorization for longer sessions. Close a token's child sessions in the same transaction as revocation as cleanup; the lookup join supplies the immediate enforcement even if cleanup is delayed.

**Regression tests.** Create/play, revoke originating token, then fetch the old URL and require 404/401. Exercise logout, password change, user deletion, expiry, another active token, and concurrent session creation/revocation. Do not log capability URLs.


<a id="f05"></a>

### F05 — Subsonic silently stores the primary account password in plaintext

**Priority:** High  
**Evidence level:** Confirmed design/security risk, explicitly described in source

**Source:** [`internal/api/subsonic/subsonic.go` — `authenticate / subsonicSecretKey`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/subsonic/subsonic.go#L153-L268); [`internal/store/users.go` — `RotatePasswordChecked`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/users.go).

**Problem and impact.** A successful plain/hex Subsonic login saves the same account password in `settings` under `subsonic.pw.<uid>`. That primary password, otherwise protected by Argon2, becomes recoverable from the database and its backups. Supporting the legacy `md5(password + salt)` protocol explains the implementation; it does not make persisting the primary password harmless. This is a documented tradeoff that deserves an explicit product/security decision, not an allegation of an unknown cryptographic bypass.

**Immediate implementation.** Stop capturing primary passwords and disable the legacy token branch until separate app credentials are implemented. Preserve primary-password verification over a trusted TLS transport:

```go
// Early in authenticate, before the old t+s branch:
if r.Form.Get("t") != "" {
    return 0, false, ErrLegacyTokenDisabled
}
// Remove the successful-login block that calls:
// a.DB.SetSetting(subsonicSecretKey(u.ID), pass)
```

Define `ErrLegacyTokenDisabled = errors.New("legacy Subsonic token authentication disabled")`; handle it in `wrap` with the existing error envelope and a clear message that a dedicated app credential is required, rather than logging it as a database failure. Migration:

```sql
DELETE FROM settings WHERE key LIKE 'subsonic.pw.%';
```

This is an intentionally compatibility-breaking containment patch. The compatible long-term solution is a separately generated, revocable Subsonic app secret, never the primary password. To support `t+s`, store that secret encrypted with an authenticated cipher and a server key managed separately from ordinary database backups; bind it to the user and app credential ID. Use hashed high-entropy bearer keys when clients support them. Merely hashing the stored primary password cannot implement arbitrary `md5(password + salt)` challenges.

**Regression tests.** Successful Subsonic login must not persist the primary password anywhere. Verify credential revocation, rotation, migration, absence from logs, and documented client behavior for unsupported legacy authentication. Old backups already containing plaintext secrets need protected handling and password rotation; deleting current rows does not sanitize historical backups.


<a id="f06"></a>

### F06 — Authenticated covers and thumbnails are explicitly share-cacheable

**Priority:** Medium  
**Evidence level:** Confirmed response policy defect; proxy replay not tested

**Source:** [`internal/api/core/core.go` — `cover / fanart`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/core.go); [`internal/api/core/hls.go` — `editionThumbTile`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/hls.go); [`internal/api/jellyfin/jellyfin.go` — `image / trickplayTile`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/jellyfin/jellyfin.go); [`internal/api/subsonic/subsonic.go` — `getCoverArt`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/subsonic/subsonic.go).

**Problem and impact.** Several authenticated image routes set `Cache-Control: public, max-age=86400`. That explicitly authorizes shared caching of protected responses. This is especially problematic for header-authenticated URLs whose cache key does not identify the user. OPDS's private cover policy is already better. The service worker's current allowlist also correctly excludes authenticated/query/range traffic; the old service-worker cache bug is **not** reported here.

**Implementation.** Replace every protected image route's `public` directive with `private, max-age=86400`, or `private, no-store` for stronger post-logout confidentiality. For credential-bearing HLS playlists and session responses use:

```go
w.Header().Set("Cache-Control", "private, no-store")
w.Header().Set("Referrer-Policy", "no-referrer")
```

Make this a route policy applied before writes. Do not rely on `Vary: Authorization` as a substitute for prohibiting shared storage of private responses. Choose whether browser-local caching across logout is acceptable; `private` alone still permits it.

**Regression tests.** For every protected image/playlist endpoint, assert the final header contains no `public` directive. Run a caching-proxy test with two users and an unauthenticated request, covering header and query authentication.


<a id="f07"></a>

### F07 — Authentication turns database failures into logout-worthy 401 responses

**Priority:** Medium  
**Evidence level:** Confirmed error-contract defect

**Source:** [`internal/auth/auth.go` — `UserForToken / Middleware`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/auth/auth.go#L211-L261); [`web/src/api.ts` — `API 401 handling`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/web/src/api.ts); [`internal/api/jellyfin/jellyfin.go` — `jfAuth`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/jellyfin/jellyfin.go).

**Problem and impact.** `UserForToken` reduces every query error to `(nil,false)`. Middleware therefore returns 401 for a temporarily unavailable/locked/broken database as well as for an invalid token. The frontend treats 401 as a reason to clear stored authentication. A transient backend failure can unnecessarily log users out and conceal the actual operational problem.

**Implementation.** Introduce an error-returning resolver and update *all* callers, including Jellyfin and WebSocket authentication:

```go
func LookupTokenUser(db *store.DB, value string) (*store.User, error) {
    if value == "" || len(value) > 256 { return nil, store.ErrNotFound }
    var u store.User
    err := db.QueryRow(`SELECT u.id,u.name,u.password_hash,u.is_admin,u.created_at,u.updated_at
        FROM tokens t JOIN users u ON u.id=t.user_id
        WHERE t.value=? AND t.revoked_at IS NULL`, value).
        Scan(&u.ID,&u.Name,&u.PasswordHash,&u.IsAdmin,&u.CreatedAt,&u.UpdatedAt)
    if errors.Is(err, sql.ErrNoRows) { return nil, store.ErrNotFound }
    if err != nil { return nil, err }
    return &u, nil
}
```

Middleware returns 401 only for `store.ErrNotFound`; other errors get a generic 503 and a server-side diagnostic. Keep token activity accounting best-effort, separate from authentication. In the frontend, capture the token used for the request and clear storage on a 401 only when it still equals the current token, so an old request cannot invalidate a newer login.

**Regression tests.** Inject a query failure and expect 503 without credential deletion; separately test revoked/unknown tokens produce 401 and invalidate only the matching current credential.


<a id="f08"></a>

### F08 — Overlapping library roots can pass concurrent validation

**Priority:** Medium  
**Evidence level:** Confirmed check/insert race in source

**Source:** [`internal/api/core/core.go` — `addLibrary / rootsOverlap`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/core.go); [`internal/store/queries.go` — `AddLibrary`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/queries.go); [`internal/store/store.go`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/store.go); [`internal/importer/importer.go` — `ensureLibrary`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/importer/importer.go).

**Problem and impact.** The HTTP path reads existing roots and checks overlap separately from `AddLibrary`'s insert. Two concurrent requests adding a parent and child can both pass. Importers also call the raw insert rather than enforcing the same invariant. Overlapping libraries make ownership, watcher routing, and path-based file upserts ambiguous.

**Implementation.** Put the invariant in a store method used by every creation/path-update path. Canonicalize and validate the directory using P3 first. The transaction below uses the repository's immediate-write transaction configuration:

```go
func (d *DB) AddLibraryChecked(name, typ, root string) (int64, error) {
    var id int64
    err := d.Update(func(tx *Tx) error {
        rows, err := tx.Query(`SELECT path FROM libraries`)
        if err != nil { return err }
        var roots []string
        for rows.Next() {
            var p string
            if err := rows.Scan(&p); err != nil { rows.Close(); return err }
            roots = append(roots, p)
        }
        scanErr := rows.Err()
        closeErr := rows.Close()
        if scanErr != nil { return scanErr }
        if closeErr != nil { return closeErr }
        for _, p := range roots {
            if pathContains(root,p) || pathContains(p,root) {
                return fmt.Errorf("overlapping library root")
            }
        }
        res, err := tx.Exec(`INSERT INTO libraries(name,type,path,created_at)
                            VALUES(?,?,?,?)`, name, typ, root, nowMilli())
        if err != nil { return err }
        id, err = res.LastInsertId()
        return err
    })
    return id, err
}

func pathContains(root, path string) bool {
    rel, err := filepath.Rel(root, path)
    return err == nil && (rel == "." || filepath.IsLocal(rel))
}
```

Ensure legacy stored roots are canonicalized before comparing them; otherwise aliases evade the invariant. Integrate any additional library columns/defaults used by the creation path. Placeholders used by importers should be explicitly disabled/unresolved libraries, not fictitiously validated real roots. Validate name/type as before and map the overlap error to 409.

**Regression tests.** Start concurrent parent/child and alias/same-target creates with a barrier. Exactly one succeeds. Test import and update paths too. Fixtures deliberately creating overlapping libraries must opt into a test-only raw insertion seam rather than weakening production validation.


<a id="f09"></a>

### F09 — A symlink library root is accepted but scanned as an empty root

**Priority:** Medium  
**Evidence level:** Go filesystem behavior reproduced locally

**Source:** [`internal/scan/scan.go` — `validateScanRoot / Library`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/scan/scan.go); [`internal/scan/video.go` — `scanVideoLibrary`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/scan/video.go); [`internal/scan/books.go` — `scanBooksLibrary`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/scan/books.go); [`internal/watch/watch.go` — `watchTree`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/watch/watch.go).

**Problem and impact.** `os.Stat(root)` follows a symlink and accepts its target as a directory. `filepath.WalkDir(root)` does not follow that root symlink, so the scanners skip it as a nonregular file instead of walking the library. The watcher has the same assumption. The local Go reproduction printed `Stat IsDir=true`, then walked one non-directory symlink and found zero regular files without error.

**Implementation.** Use P3 to persist a canonical, real directory root at creation and to validate roots on use. Do not rewrite existing paths casually: already-imported/scanned file paths may use the old alias. Migrate their prefixes in a single transaction, preserving file/edition IDs, after checking that every relative path remains local and that no unique-path conflict or overlapping root is introduced. Alternatively keep an explicit logical-root/physical-root mapping and apply it consistently to scanning and rooted opens.

```go
walkRoot, err := canonicalLibraryRoot(lib.Path) // P3
if err != nil { return 0, err }
// Once legacy paths have been migrated, walk walkRoot, not lib.Path.
err = filepath.WalkDir(walkRoot, visit)
```

A smaller safe interim fix is to reject symlink roots clearly during creation/use rather than reporting a successful empty scan.

**Regression tests.** Real root, symlink root, relative root, symlink loop, symlink retargeting, and migration of an existing file with progress attached. Include watcher arming, not just the scanner.


<a id="f10"></a>

### F10 — Mutable title-based scanner identity can strand progress and playlists

**Priority:** High  
**Evidence level:** Confirmed data-model interaction in source

**Source:** [`internal/scan/video.go` — `scanVideoLibrary`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/scan/video.go); [`internal/scan/books.go` — `storeBook`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/scan/books.go); [`internal/store/queries.go` — `upsertFile / updateFileRow`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/queries.go#L278-L400); [`internal/store/reading.go` — `upsertEditionPages`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/reading.go); [`internal/store/progress.go` — `progress keyed by edition`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/progress.go).

**Problem and impact.** Work/edition upserts identify records partly by mutable title/author metadata. After a changed media file is reprobed with a new title/author, the scanner can create a new work/edition and update an existing file's `edition_id` to it. Progress and playlist references remain attached to the old edition. Preserving the file ID alone does not preserve the user's media identity. This also interacts with the rename workflow in F11.

**Implementation.** Resolve stable identities from existing source files before doing title-based creation. Refuse silent reassignment as an immediate containment measure; then implement explicit identity-preserving metadata updates. Add to `updateFileRow` before its UPDATE:

```go
var oldEdition int64
if err := q.QueryRow(`SELECT edition_id FROM files WHERE id=?`, id).
    Scan(&oldEdition); err != nil { return err }
if oldEdition != f.EditionID {
    return fmt.Errorf("file %d belongs to edition %d; explicit reassignment required", id, oldEdition)
}
```

This turns a silent data-loss operation into a visible scan failure; it is not the complete usability fix. In each scanner, look up existing group file paths with `files -> editions -> works`, and reuse their edition/work IDs while updating display metadata. For a new source, allocate identity once. If a proposed group maps to multiple existing identities, require an explicit merge policy instead of selecting one arbitrarily. Use the repository's linking/merge facilities only after separately reviewing their handling of all user state.

A concrete stable-identity update for a resolved existing book is:

```sql
UPDATE works SET title=?, author=?, description=?, updated_at=? WHERE id=?;
UPDATE editions SET title=?, language=?, page_count=? WHERE id=? AND work_id=?;
```

Run these and the existing file upsert in one transaction, with the preserved IDs. Handle title-uniqueness conflicts as a merge/conflict, not by moving files behind users' backs. A future schema should separate source-group identity from display metadata.

**Regression tests.** Scan, create reading/listening progress and playlist membership, change embedded title/author or NFO plus media modification time, rescan, and assert stable work/edition/file IDs and all state. Also test genuine edition splits/merges and multiple same-format editions with the same title.


<a id="f11"></a>

### F11 — A normal one-scan rename does not reach the relinking path

**Priority:** Medium  
**Evidence level:** Confirmed orchestration defect; existing test behavior checked

**Source:** [`internal/api/core/core.go` — `runScan`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/core.go#L463-L493); [`cmd/libteca/main.go` — `scan command`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/cmd/libteca/main.go); [`internal/store/queries.go` — `relinkableFileID`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/queries.go); [`internal/store/files_test.go` — `TestUpsertFileGonePathRelink`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/files_test.go); [`internal/store/reconcile.go` — `MarkMissingLibraryFiles`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/reconcile.go).

**Problem and impact.** Relinking intentionally requires an old row to be marked `missing=1` *and* its old path to be absent. The test explicitly expects an unmarked disappearance to insert a new row. However, normal scanning upserts the new path first and marks missing files only afterward. On the next scan the new path already exists, so that old ID is not recovered. This loses file identity for an ordinary rename; edition-level state may also be affected as described in F10.

**Implementation.** Keep the store's conservative relinking guard. Change the scan phases: complete and validate traversal; check cancellation/root availability; mark verified disappearances; then perform metadata/probe upserts and relinks. Do not mark missing before knowing that traversal succeeded, and do not simply remove the missing check.

```go
// After successful full WalkDir and before the first new-path upsert:
if err := ctx.Err(); err != nil { return 0, err }
if err := validateScanRoot(abs); err != nil { return 0, err }
if _, err := db.MarkMissingLibraryFiles(lib.ID); err != nil { return 0, err }
// Now process the collected files. Existing verified missing rows are eligible.
```

Apply that phase ordering to every library scanner and both HTTP/CLI entry points. For removable/network mounts, root existence alone is not proof that the intended volume is mounted; an optional volume-identity sentinel or explicit offline state prevents an empty mountpoint from being reconciled as wholesale deletion. Pair this with F10 so relinking preserves edition identity as well as file ID.

**Regression tests.** Rename a real fixture between exactly two scans, starting with `missing=0`, and assert the file ID is unchanged. Also test a live copy, ambiguous identical candidates, traversal failure, cancellation and an unavailable mount. Existing isolated store tests are insufficient to verify the orchestration.


<a id="f12"></a>

### F12 — The sampled file hash is unsafe as an identity proof and discards I/O errors

**Priority:** High  
**Evidence level:** Deterministic collision mechanism reproduced

**Source:** [`internal/scan/scan.go` — `hashFile`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/scan/scan.go); [`internal/store/queries.go` — `relinkableFileID`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/queries.go); [`internal/scan/books.go` — `storeBook`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/scan/books.go).

**Problem and impact.** Large files are identified using only their first and last 64 KiB plus size. Two files with identical ends and size but different middle content supply *exactly the same bytes* to the hash; no cryptographic attack or improbable hash collision is required. That value participates in identity-preserving relinks. Read/seek failures are also discarded, so an incomplete read can become an apparent identity. Books calculate this hash while holding a write transaction, unnecessarily extending database writer occupancy.

**Reproduction.** Two 192 KiB buffers, `H*64KiB + A*64KiB + T*64KiB` and the same with `B` in the middle, have equal sampled inputs and unequal full SHA-256 digests.

**Implementation.** P4 supplies a context-aware full-file SHA-256 implementation with errors and before/after metadata checks. Calculate it on a confined descriptor *before* the write transaction. Prefix stored values with `sha256:` and never compare legacy sampled values as though they were full digests:

```go
fh, _, err := mediafs.OpenWithin(lib.Path, path)
if err != nil { return err }
digest, err := hashComplete(ctx, fh) // P4 returns 64 hex characters
closeErr := fh.Close()
if err != nil { return err }
if closeErr != nil { return closeErr }
hash := "sha256:" + digest
// Only now begin the transaction and upsert FileRec{Hash: &hash, ...}.
```

Rehash legacy rows before they are eligible for content-based relinking. Keep size checking and ambiguity rejection. A full hash has an I/O cost; optimize unchanged-file reuse, not the correctness of identity evidence. The metadata checks detect ordinary concurrent writes, not a hostile writer deliberately preserving size/timestamps. Strong adversarial immutability requires snapshotting or exclusive ownership of the source during hashing.

**Regression tests.** The equal-ends/different-middle example, short reads, failed seeks, cancellation, concurrent mutation, empty files, legacy-hash migration and ambiguous duplicates. Assert no digest is persisted after any failed read.


<a id="f13"></a>

### F13 — Incremental scans ignore sidecar and group-membership changes

**Priority:** Medium  
**Evidence level:** Confirmed invalidation gaps in inspected scanners

**Source:** [`internal/scan/scan.go` — `scanBook fast path`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/scan/scan.go); [`internal/scan/video.go` — `anyChanged / ensureCoverVideo`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/scan/video.go); [`internal/scan/books.go` — `FileStatByPath fast path`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/scan/books.go).

**Problem and impact.** The fast paths mainly compare media size and modification times. They do not model NFO/cover dependencies or the ordered set of files in a multipart group. Editing an NFO or adding/replacing a cover without touching the media can be skipped. The video warm path derives a work lookup from the directory title/year rather than the previously applied NFO identity. Books return early before cover repair. A group losing a member can also bypass recomputation of stored edition metadata. Some API durations are recalculated from live files, so this does not imply every displayed duration is wrong.

**Implementation.** Separate cheap group/sidecar refresh from expensive media probing. Never put metadata, membership and cover work behind an “all media unchanged” early return. Store a scanner-versioned dependency fingerprint and compare it independently:

```go
type scanDependency struct {
    Path string `json:"path"`
    Size int64 `json:"size"`
    MtimeNS int64 `json:"mtime_ns"`
    Digest string `json:"digest,omitempty"` // full hash for small sidecars
}

func dependencyKey(deps []scanDependency) (string, error) {
    copyOfDeps := append([]scanDependency(nil), deps...)
    sort.Slice(copyOfDeps, func(i,j int) bool { return copyOfDeps[i].Path < copyOfDeps[j].Path })
    data, err := json.Marshal(struct {
        Version int `json:"version"`
        Entries []scanDependency `json:"entries"`
    }{Version: 1, Entries: copyOfDeps})
    if err != nil { return "", err }
    sum := sha256.Sum256(data)
    return hex.EncodeToString(sum[:]), nil
}
```

Include the complete ordered media membership separately when order is meaningful; sorting above is for a dependency *set*, not playback order. Include missing sidecars' absence so appearance/disappearance invalidates the key. Update metadata using preserved identities from F10. Upsert the fingerprint only after successful commit/publication. An immediate lower-risk fix is to always reread bounded sidecars and repair covers while still reusing unchanged media probe results.

**Regression tests.** Modify only NFO title/description, add/remove/replace a cover, delete one multipart file, rename a member, and change only scanner version. Require correct metadata/ordering without unnecessary reprobes of unchanged audio/video.


<a id="f14"></a>

### F14 — Scan failures and late cancellation can be reported as successful completion

**Priority:** Medium  
**Evidence level:** Confirmed error propagation defects

**Source:** [`internal/scan/scan.go` — `per-book error handling`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/scan/scan.go); [`internal/scan/video.go` — `probe error handling`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/scan/video.go); [`internal/scan/books.go` — `probeBook error handling`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/scan/books.go); [`internal/api/core/core.go` — `runScan`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/core.go#L463-L493).

**Problem and impact.** Per-item probe errors are logged and skipped. A scan in which all new items fail can finish as `done`, sometimes creating an empty work. Cancellation swallowed as an item error near the last iteration can likewise yield a nil final error. `runScan` only recognizes cancellation inside `if err != nil`, and logs reconciliation failure before still publishing `done`. The UI and restart logic therefore cannot distinguish a healthy scan from an incomplete one.

**Implementation.** Return cancellation immediately, collect item failures, and propagate reconciliation failure. A minimal change to each probe loop is:

```go
var itemErrors []error
// In the loop, after a failed probe:
if err := ctx.Err(); err != nil { return count, err }
itemErrors = append(itemErrors, fmt.Errorf("probe %s: %w", path, probeErr))
// Continue processing independent files; do not create a new empty work.
// At the end of the scan:
return count, errors.Join(append(itemErrors, ctx.Err())...)
```

Adapt the variable names to the scanner's actual `perr`/`err`. In `runScan`, check `ctx.Err()` even if the scan function returned nil, and treat reconciliation failure as an unsuccessful terminal result. A richer implementation should expose `partial` plus failed-item counts and retryable diagnostics. Until that status is modeled end-to-end, `error` is more honest than `done`. Do not reconcile disappearances after an incomplete traversal; F11's validated traversal boundary matters.

**Regression tests.** All probes fail; one succeeds and one fails; cancel during the final probe; reconciliation database failure; panic recovery; and failure to persist terminal status. Verify counters reflect committed work, not rolled-back attempts.


<a id="f15"></a>

### F15 — A second server using the same data directory can damage the running instance

**Priority:** High  
**Evidence level:** Confirmed startup ordering and destructive shared cleanup

**Source:** [`cmd/libteca/main.go` — `server startup`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/cmd/libteca/main.go); [`internal/api/core/core.go` — `New / startup job recovery`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/core.go); [`internal/transcode/transcode.go` — `New`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/transcode/transcode.go#L110-L123).

**Problem and impact.** There is no server-instance data-directory lock before startup recovery. Creating a transcode manager removes the whole shared `transcode` directory; core startup marks running scan jobs interrupted. A second process can perform those actions before its port bind fails, or can run concurrently on a different port. SQLite's transaction locking does not protect these filesystem/process lifecycle operations.

**Implementation.** Acquire a lifetime advisory lock before opening/migrating the database, constructing managers, recovering jobs or starting watchers. P5 provides the Unix implementation. After dispatching non-server CLI commands, wire it into the server path:

```go
release, err := lockServerDataDir(dataDir) // P5; before store.Open
if err != nil { return err }
defer release()
// Open store, recover jobs, construct managers, start server.
```

Keep the lock file inode; do not unlink it on release. Propagate cleanup/creation errors rather than ignoring them. Read-only backup operations need their existing backup lock and consistent-snapshot rules, not a blanket ban while the server is running. Mutating CLI commands need either this exclusive lock or explicit server-mediated coordination. Document that advisory locking requires a filesystem with the expected lock semantics.

**Regression tests.** Start two processes with the same directory and different ports; the second must fail *before* changing jobs or media caches. Also test the same-port case, abrupt termination/restart, and a legitimate concurrent backup.


<a id="f16"></a>

### F16 — Audio-only HLS fails because the transcoder requires a video stream

**Priority:** Medium  
**Evidence level:** FFmpeg failure and minimal fix reproduced locally

**Source:** [`internal/api/core/hls.go` — `editionPlayback / hlsFile`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/hls.go); [`internal/transcode/hwaccel.go` — `buildArgs`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/transcode/hwaccel.go).

**Problem and impact.** The playback decision can select HLS for audio-only content, but the generated command requires `-map 0:v:0`. A normal audio-only input has no such stream. The local three-second WAV test exited 234 and produced no playlist; replacing the video map with `0:v:0?` exited zero and produced a playlist.

**Implementation.** Make stream selection depend on probed streams. For audio-only content, do not initialize video hardware or add a video encoder/filter at all:

```go
// After the confined input arguments:
if hasVideo {
    args = append(args, "-map", "0:v:0", "-map", "0:a:0?")
    args = append(args, videoEncoderArgs...)
} else {
    args = append(args, "-map", "0:a:0", "-vn")
}
args = append(args, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
args = append(args, "-f", "hls", "-hls_time", "4", "-hls_init_time", "2", "-hls_list_size", "0",
    "-hls_segment_filename", filepath.Join(outDir, "seg%05d.ts"),
    filepath.Join(outDir, "index.m3u8"))
```

These are the relevant replacement command-building fragments; preserve the manager's existing lifecycle and any required HLS publication options. The one-character optional-map change is a proven *software-path* mitigation, not sufficient evidence that every hardware configuration supports audio-only input.

**Regression tests.** WAV/FLAC/MP3 audio-only, video without audio, ordinary video+audio, missing streams and unsupported audio. Assert an actual playlist and decodable segments, not just process startup success.


<a id="f17"></a>

### F17 — HLS uses the first file while validating an edition-wide timeline

**Priority:** Medium  
**Evidence level:** Confirmed source/timeline mismatch

**Source:** [`internal/api/core/hls.go` — `hlsFile`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/hls.go); [`internal/api/jellyfin/jellyfin.go` — `hlsMaster`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/jellyfin/jellyfin.go#L1065-L1139); [`internal/store/works.go` — `Locate / TotalDuration`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/works.go); [`web/src/players/video.tsx` — `boot`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/web/src/players/video.tsx#L93-L167).

**Problem and impact.** HLS uses `ed.Files[0].Path`, while positions/durations can describe the sum of all edition files. An edition-wide start beyond the first file is passed to FFmpeg against the wrong source, and an advertised full-edition stream cannot include later files. The current first-party video player deliberately resumes on a full timeline client-side, so the old “resume starts at zero” frontend bug should not be reintroduced; the multi-file server mismatch still exists.

**Implementation.** Either implement a complete multipart timeline or reject unsupported multipart HLS honestly. The safest immediate patch in both adapters is:

```go
if len(ed.Files) != 1 {
    http.Error(w, "multipart HLS is not supported; use per-file playback", http.StatusConflict)
    return
}
```

The complete solution needs `ed.Locate(position)` to select the file and file-relative offset, an explicit edition base offset in playback metadata, per-file transitions, and progress conversion back to edition time. Do not merely select the second file and report its `currentTime` as edition-absolute time. A concatenation-based implementation must use confined inputs for *every* member and preserve codecs/timestamps or transcode them consistently.

**Regression tests.** Two files of known lengths, positions before/at/after their boundary, playback to the next file, resume, subtitles and progress. Include a single-file test proving the current full-timeline resume behavior remains correct.


<a id="f18"></a>

### F18 — HLS ignores small start offsets and silently reuses an incompatible session

**Priority:** Medium  
**Evidence level:** Confirmed command/session-contract defects

**Source:** [`internal/transcode/hwaccel.go` — `buildArgs`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/transcode/hwaccel.go); [`internal/transcode/transcode.go` — `Get`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/transcode/transcode.go#L137-L165); [`internal/api/jellyfin/jellyfin.go` — `hlsMaster`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/jellyfin/jellyfin.go).

**Problem and impact.** The command only includes `-ss` when the requested start exceeds one second. A positive start at or below one second is ignored. `Manager.Get` returns an existing same-edition session without comparing source or start position; a client reusing a play-session ID for a seek or changed source can receive the old timeline. The source itself documents this requirement, but not every protocol caller guarantees a new ID.

**Implementation.** Validate finite/nonnegative starts, use `startSecs > 0`, and define session identity explicitly. Add `StartSecs float64` and a source-version field to `Session`; reject reuse with incompatible parameters rather than silently returning an old session:

```go
if math.IsNaN(startSecs) || math.IsInf(startSecs, 0) || startSecs < 0 {
    return nil, fmt.Errorf("invalid transcode start")
}
// In Get's existing-session branch:
if s.Edition != edition || s.Source != source || s.StartSecs != startSecs {
    return nil, ErrSessionParametersChanged
}
```

Define the new error and translate it into an explicit conflict/new-session response. Initialize `StartSecs` on creation and include the actual source generation, not just its path. For Jellyfin, validate `StartTimeTicks` using checked integer parsing and the edition duration; do not turn malformed/negative input into a usable offset. Mint fresh IDs with the existing cryptographic `NewSessionID`, including the fallback case, rather than a millisecond timestamp.

**Regression tests.** Starts at 0, 0.25, 1 and 1.25 seconds; invalid/negative/over-duration ticks; repeated identical requests; changed start/source with the same ID; two simultaneous viewers; and an explicit fresh-session seek. Exact float equality is appropriate for immutable request parameters only after canonicalizing their representation, for example to integer microseconds.


<a id="f19"></a>

### F19 — Hardware capability probes can block all transcode management indefinitely

**Priority:** Medium  
**Evidence level:** Confirmed missing timeout and lock interaction

**Source:** [`internal/transcode/transcode.go` — `New / Get`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/transcode/transcode.go#L110-L190); [`internal/transcode/hwaccel.go` — `accelMode / selectAccel`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/transcode/hwaccel.go).

**Problem and impact.** Capability probing uses `exec.Command(...).Output()` without a deadline. The first `Get` holds the manager mutex while selecting the acceleration mode. A hung FFmpeg/driver probe can therefore block other session requests and operations waiting for the same mutex, including shutdown. The probe output is also unbounded.

**Implementation.** Probe outside the session-map lock during controlled manager initialization, cache the result, and use the manager's lifetime context. The following is a concrete replacement probe runner using P1's bounded buffer:

```go
func probeFFmpeg(parent context.Context, argv []string) (string, error) {
    ctx, cancel := context.WithTimeout(parent, 5*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, "ffmpeg", argv...)
    cmd.WaitDelay = time.Second
    var out limitedBuffer
    out.limit = 1 << 20
    cmd.Stdout = &out
    cmd.Stderr = io.Discard
    err := cmd.Run()
    if ctx.Err() != nil { return "", ctx.Err() }
    if err != nil { return "", err }
    return out.buf.String(), nil
}
```

Five seconds and 1 MiB are proposed policy limits, not existing requirements. On a timed-out/failed probe, report the reason and choose an explicit safe software mode. Do not indefinitely hold `sync.Once` or the manager lock around a subprocess. Preserve process-group cleanup where a probe wrapper can spawn descendants.

**Regression tests.** Inject a probe that blocks, writes excessive output or errors; ensure request latency, other sessions and shutdown remain bounded. Exercise the test process factory rather than requiring a physical GPU.


<a id="f20"></a>

### F20 — Explicit hardware auto selection can lose to an environment override

**Priority:** Low  
**Evidence level:** Confirmed configuration-precedence ambiguity

**Source:** [`internal/transcode/hwaccel.go` — `SetHwAccel / accelMode / selectAccel`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/transcode/hwaccel.go); [`cmd/libteca/main.go` — `hardware acceleration flag`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/cmd/libteca/main.go).

**Problem and impact.** `SetHwAccel("auto")` is normalized to the same empty state used for “not configured,” after which selection consults the environment. This loses the distinction between an explicit CLI `auto` choice and no CLI override. That can force an unexpected backend when operators believe they requested detection.

**Implementation.** Preserve the sentinel and whether a value was explicitly supplied:

```go
func resolveAccelSetting(explicit *string, env string) string {
    if explicit != nil { return strings.ToLower(strings.TrimSpace(*explicit)) }
    if v := strings.ToLower(strings.TrimSpace(env)); v != "" { return v }
    return "auto"
}
```

Keep `"auto"` distinct from `"none"` and the unconfigured state, validate against the supported backend names, and pass the resolved mode to detection without rereading the environment. In the CLI use `flag.Visit` or an equivalent presence indicator; a flag's default string alone does not reveal whether it was supplied.

**Regression tests.** Explicit auto with forced environment, explicit none, explicit hardware backend, absent flag with environment, absent both, and invalid configuration. Document one precedence rule and test it.


<a id="f21"></a>

### F21 — Core and Jellyfin race independent trickplay generators over the same cache

**Priority:** Medium  
**Evidence level:** Confirmed shared-resource ownership defect

**Source:** [`internal/api/core/hls.go` — `trickplayer`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/hls.go#L38-L46); [`internal/api/jellyfin/jellyfin.go` — `trickplayer / trickplayTile / trickplayManifest`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/jellyfin/jellyfin.go); [`internal/trickplay/trickplay.go` — `generation/cache lifecycle`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/trickplay/trickplay.go); [`internal/server/server.go`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/server/server.go).

**Problem and impact.** Each API lazily creates its own `trickplay.Generator`, but both generate the same `e<editionID>/<width>` cache namespace. Per-generator maps/mutexes cannot serialize jobs started through the other instance. A global capacity of two allows precisely such concurrent work. Directory cleanup, tile writes and the completion marker can interleave, yielding failed, mixed or incomplete thumbnails.

**Implementation.** Construct one generator in `server.New` and inject it into both adapters before mounting/serving them. Add the following setter to each adapter, preserving its existing mutex:

```go
func (a *API) SetTrickplayGenerator(g *trickplay.Generator) {
    a.tpMu.Lock()
    defer a.tpMu.Unlock()
    a.tp = g
}
```

Call it with the *same pointer* on the core and Jellyfin instances. Keep lazy construction only for isolated tests, or require explicit construction. In the generator, build in a unique staging directory and publish atomically only after every expected tile/manifest is complete. Never delete a directory currently being served to another request. Use one shared per-key in-flight registry and source-versioned cache keys (F22). The server instance lock from F15 handles competing server processes; one injected object alone does not.

**Regression tests.** Concurrent core and Jellyfin requests for the same uncached edition/width; assert one generation, valid manifests and no partial tiles. Repeat while replacing the source, canceling a caller, and exhausting capacity.


<a id="f22"></a>

### F22 — Derived caches can survive replacement of their underlying media

**Priority:** Medium  
**Evidence level:** Confirmed invalidation defects in inspected caches

**Source:** [`internal/trickplay/trickplay.go` — `complete cache reuse`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/trickplay/trickplay.go); [`internal/api/opds/opds.go` — `coverThumb`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/opds/opds.go#L605-L649); [`web/src/reader/epub.tsx` — `locations cache`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/web/src/reader/epub.tsx).

**Problem and impact.** Trickplay completion is tied to item ID and width rather than source version. OPDS thumbnails are reused solely because `covers/thumb/<workID>.jpg` exists. The EPUB locations cache is tied to edition identity without a content generation. Replacing a video, cover or EPUB while preserving the entity ID can therefore keep obsolete thumbnails or reading-location indexes.

**Implementation.** Make cache identity include source generation and algorithm version. A reusable key function is:

```go
func derivedCacheKey(kind string, entityID int64, sourceVersion string, size int) string {
    value := fmt.Sprintf("v1\x00%s\x00%d\x00%s\x00%d", kind, entityID, sourceVersion, size)
    sum := sha256.Sum256([]byte(value))
    return hex.EncodeToString(sum[:])
}
```

Use the full-content hash or a maintained immutable generation as `sourceVersion`. Add that version to the edition API for the reader:

```ts
const locationsKey = `libteca:epub-locations:v2:${editionId}:${contentVersion}`;
```

Generate new artifacts under new names; clean old generations with a quota/age policy once no reader holds them. OPDS thumbnail publication must use P2's atomic writer, not direct `os.WriteFile`. A modification-time comparison is an acceptable interim fix for ordinary replacements, but is weaker than immutable content versioning and can miss same-size/same-time replacement.

**Regression tests.** Replace each source under the same ID, including same-size media, and verify regeneration. Test an interrupted cache build and a reader with an old locations index. Version schema/algorithm changes must also invalidate the cache.


<a id="f23"></a>

### F23 — Jellyfin HLS segment access does not enforce session ownership or item binding

**Priority:** Medium  
**Evidence level:** Confirmed isolation gap; impact bounded by shared-library access model

**Source:** [`internal/api/jellyfin/jellyfin.go` — `hlsMaster / hlsSegment`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/jellyfin/jellyfin.go#L1065-L1220); [`internal/api/core/hls.go` — `checkWebTicket`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/hls.go).

**Problem and impact.** `hlsMaster` binds IDs to a user, but `hlsSegment` only validates filename/session syntax, waits on that session and opens its output. It does not verify the caller owns it or that the URL item matches its edition. A known session ID, including one created through the core adapter, can be used through this weaker route by another authenticated user. Since the current product generally shares media libraries among users, this is principally a session-isolation/privacy and resource-lifetime bug, not evidence of bypassing an implemented per-library ACL.

**Implementation.** Store owner ID and edition ID in the transcode session itself, and expose a checked manager method. The syntax/prefix is not the authority:

```go
// Session gains OwnerID int64; initialize it from authenticated context.
func (m *Manager) OwnedSession(id string, owner, edition int64) (*Session, error) {
    m.mu.Lock()
    defer m.mu.Unlock()
    s, ok := m.sessions[id]
    if !ok || s.OwnerID != owner || s.Edition != edition {
        return nil, os.ErrNotExist
    }
    return s, nil
}
```

Before `WaitForSegmentFile`, resolve the URL's item/edition and call this check. Apply the same ownership to touch/stop/wait/file methods so a later unchecked lookup cannot bypass it. Make administrator access an explicit policy rather than inferring it from a session-ID string. Consider returning a handle whose checked lifetime and identity are used through the entire file operation.

**Regression tests.** Own session succeeds; another user's session, core session via Jellyfin, mismatched item ID and expired/stopped session fail. Unauthorized requests must not refresh the idle lease.


<a id="f24"></a>

### F24 — Podcast download errors prevent retention enforcement

**Priority:** Medium  
**Evidence level:** Confirmed control-flow defect

**Source:** [`internal/podcast/podcast.go` — `refresh / applyFeed`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/podcast/podcast.go); [`internal/podcast/download.go` — `enforceRetention`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/podcast/download.go).

**Problem and impact.** Both normal feed application and the not-modified refresh path return a download error before retention runs. One permanently failing enclosure can therefore prevent older successful downloads from being purged while other episodes continue arriving, defeating the configured retained-episode count.

**Implementation.** Run download and retention as independent steps and return both errors. The helper below is directly usable without changing either operation's signature:

```go
func downloadAndRetain(download func() error, retain func() error) error {
    downloadErr := download()
    retentionErr := retain()
    return errors.Join(downloadErr, retentionErr)
}
```

Replace each early-return sequence with this helper, passing closures around the existing calls. Record the combined error in refresh status. Cancellation policy can skip new downloads, but should not permanently suppress a separately scheduled cleanup pass. Apply retention when its configuration changes, not only after a successful new download.

**Regression tests.** A feed with successful new episodes and one permanently bad enclosure must retain at most the configured number of successfully downloaded episodes, while still reporting the bad enclosure. Repeat for HTTP 304 and retention-setting changes.


<a id="f25"></a>

### F25 — Podcast purging discards unlink errors and can lose cleanup tracking

**Priority:** Medium  
**Evidence level:** Confirmed error-discarding and split-state updates

**Source:** [`internal/podcast/download.go` — `enforceRetention`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/podcast/download.go#L202-L250); [`internal/podcast/helpers.go` — `osRemove`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/podcast/helpers.go).

**Problem and impact.** `osRemove` deliberately returns no error. Retention then marks the file missing and the episode purged even if deletion failed, or if the path was not purgeable. The on-disk file may remain indefinitely while the database no longer presents it as a downloaded episode eligible for normal retention. The two database updates also commit separately.

**Implementation.** Return unlink errors and keep the episode linked when deletion was not completed. A minimal patch in the purge loop is:

```go
path, ok := paths[*ep.FileID]
if !ok || !s.purgeablePath(path) {
    return fmt.Errorf("refusing to purge untracked/outside episode %d", ep.ID)
}
if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
    return fmt.Errorf("purge episode %d: %w", ep.ID, err)
}
// Only now update missing/file-link state; combine the two DB updates in one Tx.
```

The existing lexical `purgeablePath` is not sufficient against a retargeted parent directory; root the removal under a service-owned podcasts directory using the repository's Go version's rooted filesystem API. Do not delete the target of a symlink by resolving it first. For crash robustness, implement a persistent deletion outbox: record the intended file/episode cleanup transactionally, unlink idempotently, then finalize both database states in one transaction. Retry the outbox on startup and periodically.

**Regression tests.** Inject permission denial, missing file, out-of-root path, database failure between the two old updates, and process termination after unlink but before finalization. The next cleanup must retry safely without redownloading a deliberately purged episode.


<a id="f26"></a>

### F26 — Podcast URL normalization changes resource identity without consent

**Priority:** Medium  
**Evidence level:** Confirmed semantic URL rewriting

**Source:** [`internal/podcast/helpers.go` — `normalizeFeedURL`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/podcast/helpers.go).

**Problem and impact.** Normalization upgrades ordinary HTTP URLs to HTTPS and strips trailing path slashes. Those changes are not generally equivalent resources: an HTTP-only feed may not have TLS, and `/feed` and `/feed/` can have different representations or redirect behavior. A syntactically valid URL supplied by the user can become a different, unusable feed before any request is made.

**Implementation.** Validate scheme/host and remove the fragment, which is not part of the HTTP request, but preserve scheme, path, raw path and query. Decide transport policy explicitly instead of rewriting identity:

```go
func normalizeFeedURL(raw string) (string, error) {
    u, err := url.Parse(strings.TrimSpace(raw))
    if err != nil { return "", err }
    if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
        return "", fmt.Errorf("feed URL must be HTTP(S) with a host")
    }
    if u.User != nil { return "", fmt.Errorf("credentials in feed URL are not supported") }
    u.Fragment, u.RawFragment = "", ""
    return u.String(), nil
}
```

This changes the helper's signature, so update all callers to handle validation errors. HTTPS-only deployments should reject HTTP with an actionable message or require explicit opt-in; accepting HTTP where allowed should still use the existing public-destination/redirect protections. Preserve query tokens carefully and redact them from logs.

**Regression tests.** HTTP-only feed, distinct slash/no-slash endpoints, encoded path, query string, fragment, malformed URL, credentials and guarded redirects.


<a id="f27"></a>

### F27 — All database backups share one mutable cover directory

**Priority:** High  
**Evidence level:** Confirmed snapshot packaging defect

**Source:** [`internal/store/backup.go` — `Snapshot / copyTree / pruneBackups`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/backup.go#L44-L87).

**Problem and impact.** Each database snapshot has a unique name, but every snapshot copies covers into the same `backups/covers` directory. Later backups overwrite those cover bytes, so an older database snapshot no longer has its own matching asset set. A failed later copy can change some old backup assets even though no new database snapshot is published. `VACUUM INTO` provides the database snapshot; that part should be retained.

**Implementation.** Publish an immutable snapshot directory containing `snapshot.db`, `covers/` and a manifest, all staged together. **P6** provides a concrete replacement `Snapshot` and bundle-pruning helper using the existing `BackupTo`, `withBackupLock`, `copyTree` and `syncPath`. Update restore/CLI documentation to select the bundle's database and covers together. Keep legacy loose snapshots readable but never mutate their shared assets during migration.

**Consistency qualification.** An atomic directory rename solves publication and preservation of older backups. It does **not** by itself make a live database and mutable cover files a single point-in-time snapshot. For that, either make covers immutable/content-addressed and copy exactly the paths referenced by the backed-up database, or coordinate asset/metadata publication and snapshot creation with a cross-process lock. An in-process mutex alone cannot coordinate a separate backup CLI. Document the chosen guarantee and include a manifest/checksum validation step.

**Regression tests.** Backup A, replace a cover, backup B, then restore A and verify its original bytes. Force copy failure midway and assert A remains untouched. Verify pruning acts on complete bundles only, ignores staging directories, and never deletes the just-published snapshot. Test concurrent live metadata/cover updates under the chosen consistency model.


<a id="f28"></a>

### F28 — Partial progress updates reset fields the client did not send

**Priority:** High  
**Evidence level:** Exact SQL update behavior reproduced locally

**Source:** [`internal/api/core/core.go` — `setProgress`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/core.go#L1004-L1080); [`internal/store/reading.go` — `SetReadingProgressPatch`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/reading.go#L61-L87); [`internal/api/abs/abs.go` — `postProgress`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/abs/abs.go#L423-L480).

**Problem and impact.** Core decodes position/duration as plain zero-valued numbers while treating some other fields as a patch. A body containing only `finished`, page or locator can therefore overwrite playback position with zero, duration/device with nil and file offset with zero. ABS routes both POST and PATCH into similar non-presence-aware decoding. Its inferred completion can also override an explicitly supplied `isFinished:false` near the end.

**Reproduction.** Executing the inspected core UPSERT expressions in a minimal SQLite schema changed position `642` to `0`, duration `3600` to NULL, and device to NULL after a finished-only update. Reading page/percent survived because those columns use `coalesce`.

**Implementation.** Use pointer/presence fields and a single transactional merge or presence-aware SQL. **P7** supplies a concrete store method and request shape for the core patch path. It preserves omitted values, honors explicit zero/false, validates bounds, and computes file/offset only when position is supplied. Apply the same presence semantics to ABS PATCH; POST's documented full-state semantics can remain distinct only if clients and documentation explicitly agree. Keep malformed/oversized body checks.

Do not implement read-merge-write outside a transaction; two patches updating different fields would overwrite each other. Define explicit null-clearing separately—P7 treats null like omission and uses DELETE for resetting all progress. For known media, validate supplied position against the server's real duration rather than trusting an arbitrarily enlarged client duration.

**Regression tests.** Finished-only, explicit false near EOF, page-only, locator-only, position=0, omitted duration/device, invalid bounds, and concurrent disjoint patches. Verify audio state and reading state coexist rather than clobbering one another.


<a id="f29"></a>

### F29 — The reader progress queue loses patches on overwrite/failure and permits reordering

**Priority:** High  
**Evidence level:** Confirmed state-machine defects; minimal transitions reproduced

**Source:** [`web/src/reader/shared.tsx` — `useProgressSaver`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/web/src/reader/shared.tsx); [`web/src/players/video.tsx` — `save`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/web/src/players/video.tsx).

**Problem and impact.** The reading saver replaces the queued patch rather than merging it, clears the queue before the request succeeds, and records failure without putting the patch back. It does not serialize in-flight deliveries. Thus a page update followed by a completion update can lose page state; a failed last save can be forgotten; and an older request can overwrite a newer one. Unload handling only considers currently queued data, not already-cleared in-flight state. The video saver improves its acknowledgement watermark, but still does not supply a general ordering protocol.

**Implementation.** **P8** supplies a serial, merge-preserving, retrying TypeScript queue. Wire `save` to `enqueue`, call `flush` on pause/visibility changes, and keep dirty data until acknowledgement. It deliberately serializes within one queue and reinserts failed patches underneath newer patches so newer values win. It accepts a persistence callback so the hook can store unsent patches under a user-and-edition-specific key.

For reload/unload durability, persist before sending, and do not discard persisted data just because `sendBeacon` returned true; that is queue acceptance, not application acknowledgement. Use a session generation and monotonically increasing sequence/revision on the server if multiple tabs, beacons or devices can write concurrently. Apply compare-and-swap/revision checks to the same transaction as P7; return the accepted revision. Do not “fix” ordering by always taking the maximum position—legitimate backward seeks must remain possible.

**Regression tests.** Hold request A open while enqueueing B, reject A once, then resolve retries; final saved state must be B with any nonoverlapping fields from A. Test repeated failures, explicit zero/false, completion after page update, unload during an in-flight request, restored pending data, account switching and deliberate backwards seek. P8's isolated queue regression was executed locally; the full browser hook was not run.


<a id="f30"></a>

### F30 — Metadata cover downloads are non-atomic and use a weaker outbound-fetch policy

**Priority:** Medium  
**Evidence level:** Confirmed publication defect; outbound policy is defense in depth

**Source:** [`internal/api/core/providers.go` — `downloadCover / applyResult`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/providers.go#L519-L562); [`internal/podcast/fetch.go` — `guarded HTTP fetch implementation`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/podcast/fetch.go).

**Problem and impact.** The cover downloader writes directly to the final file and accepts any pre-existing file as success. An interrupted/disk-full write can leave a corrupt final file that future calls never repair. It accepts any nonempty body under the byte cap, without checking image format/dimensions. It also constructs an ordinary HTTP client instead of reusing the podcast fetcher's guarded destination/redirect behavior and does not propagate the request context. A provider-returned cover URL is less trusted than a local constant; compromised/malformed provider data should not obtain unrestricted internal-network access. No direct attacker-controlled cover-URL API was established in this review.

**Implementation.** Refactor the guarded public HTTP transport into a shared outbound package and use it for provider assets. Build requests with context, keep the existing byte/time caps, validate images with `image.DecodeConfig` plus a pixel/dimension budget, then publish using P2. Treat an existing final file as a cache hit only after validating its source generation and basic integrity:

```go
cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
if err != nil { return false, fmt.Errorf("invalid cover image: %w", err) }
if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 16384 || cfg.Height > 16384 ||
    int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
    return false, fmt.Errorf("cover dimensions exceed budget")
}
if format != "jpeg" && format != "png" && format != "gif" {
    return false, fmt.Errorf("unsupported cover format %q", format)
}
if err := atomicWritePrivate(dst, data); err != nil { return false, err }
return true, nil
```

Register the allowed image decoders; support additional formats explicitly rather than falsely naming arbitrary bytes JPEG. Store/serve the actual media type or transcode to JPEG with a bounded decoder. Do not silently ignore a cover failure in the apply summary: return a partial-result warning so operators can retry.

**Regression tests.** Disk-full/partial writes, invalid HTML response, decompression/pixel-budget image, cancellation, redirects to loopback/private addresses, changed cover URL and a pre-existing corrupt file. Validate the shared transport's DNS/redirect behavior independently; a hostname string check alone is not enough.


<a id="f31"></a>

### F31 — Watcher recovery leaves stale descendant watches and treats recent failed scans as fresh

**Priority:** Medium  
**Evidence level:** Confirmed bookkeeping/retry-policy gaps; filesystem integration not run

**Source:** [`internal/watch/watch.go` — `unwatchDir / syncLibraries / addDir / staleLibrary`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/watch/watch.go).

**Problem and impact.** Directory removal/rename removes only the exact directory from the watch map. Descendant entries can remain associated with old paths/inodes. `addDir` then skips a replacement directory if that path is still present in the map. Periodic sync mainly removes deleted libraries, not stale descendants. Separately, a recent failed/interrupted scan is treated as fresh until the staleness age expires, delaying restart recovery.

**Implementation.** Replace the single-directory unwatch with a subtree operation, remove stale bookkeeping even when the underlying watcher already dropped a watch, and periodically reconcile actual watched paths:

```go
func (w *Watcher) unwatchTree(root string) {
    w.mu.Lock()
    defer w.mu.Unlock()
    for dir := range w.dirs {
        rel, err := filepath.Rel(root, dir)
        if err == nil && (rel == "." || filepath.IsLocal(rel)) {
            delete(w.dirs, dir)
            if err := w.fw.Remove(dir); err != nil {
                // A removed watch may already be absent; reconcile bookkeeping regardless.
                fmt.Fprintf(os.Stderr, "libteca: watch removal %s: %v\n", dir, err)
            }
        }
    }
}
```

Use it for rename/remove events. In `staleLibrary`, treat terminal status other than `done` as requiring retry; keep a bounded backoff to avoid a tight loop on permanent failures. Also canonicalize roots under F09. Avoid holding the watcher mutex across any operation capable of waiting on another goroutine that needs it; the shown removal assumes fsnotify's ordinary non-callback removal operation.

**Regression tests.** Rename a directory containing watched children, recreate the original tree, modify a child file and require a scan. Repeat with root replacement, overflow recovery and library deletion. Restart after an interrupted/recent failed scan and verify retry rather than waiting 24 hours. Run on each supported OS because watcher event details differ.


<a id="f32"></a>

### F32 — The container runs the media server and processors as root

**Priority:** Medium  
**Evidence level:** Confirmed packaging hardening gap

**Source:** [`Dockerfile`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/Dockerfile); [`deploy/systemd/libteca.service`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/deploy/systemd/libteca.service).

**Problem and impact.** The Docker runtime stage has no `USER`, so the service and FFmpeg inherit root in the container. Mounted media/data permissions then have a much wider blast radius for the file-boundary problems above. The systemd unit already uses a substantially more restricted account and sandbox; do not remove those protections while aligning deployments.

**Implementation.** Replace the runtime stage with a non-root account and keep the data directory writable:

```dockerfile
FROM alpine:3.22
RUN apk add --no-cache ffmpeg \
    && addgroup -S -g 10001 libteca \
    && adduser -S -D -H -u 10001 -G libteca libteca \
    && mkdir -p /data \
    && chown 10001:10001 /data
COPY --from=build /out/libteca /libteca
USER 10001:10001
VOLUME /data
EXPOSE 8096
ENTRYPOINT ["/libteca", "--data", "/data", "--port", "8096"]
```

Existing bind mounts may need an explicit ownership migration; do not recursively chown arbitrary mounted media at startup. Document read-only media mounts, dropped capabilities and `no-new-privileges`. Hardware acceleration requires deliberate device/group access—grant only the required render device/group, not root. Use `npm ci --no-fund --no-audit` in the web build stage instead of `npm install` to enforce lockfile consistency; run dependency auditing in a separate checked job rather than treating `--no-audit` as security validation.

**Regression tests.** Container reports a nonzero UID, reads a read-only media mount, writes only its state/cache directories, restores from an existing data volume, and can access an explicitly granted render device. Test permission failures give actionable startup errors.


<a id="f33"></a>

### F33 — CI does not exercise frontend behavior or the cross-adapter failure cases found here

**Priority:** Medium  
**Evidence level:** Confirmed test-workflow coverage gap, not absence of all tests

**Source:** [`.github/workflows/ci.yml`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/.github/workflows/ci.yml); [`web/package.json`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/web/package.json); [`internal/store/files_test.go`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/store/files_test.go).

**Problem and impact.** CI has useful Go vet/race tests and frontend type checking/building, but no frontend behavioral test invocation. Type checking cannot catch the progress queue state machine or media lifecycle errors. Isolated store tests can pass while scan orchestration is wrong, as F11 demonstrates. Packaging and restore semantics also need runtime tests, not just a successful build.

**Implementation.** Add a committed frontend test script and test dependency, then run it in the web job. For example, after selecting/installing a compatible Vitest version and committing the updated lockfile:

```json
{
  "scripts": {
    "test": "vitest run"
  }
}
```

Merge the script into the existing package JSON; do not replace its existing scripts/dependencies. Add CI steps:

```yaml
- run: npm run test
- run: npx tsc --noEmit
- run: npm run build
```

Add integration suites around: all file-serving adapters (F01–F03), token/capability revocation (F04), real scan/rename flow (F10–F14), actual FFmpeg output (F16–F18), mixed-adapter trickplay concurrency (F21), failure-injected retention/backup (F24–F27), and browser progress lifecycle (F28–F29). Run the container as non-root in a smoke test. Add dependency/advisory scanning with committed tool versions and an explicit triage policy; this audit did not run `govulncheck` or an npm advisory audit and does not assert particular CVEs.

**Regression criterion.** Introduce a failing test for each fixed bug before accepting its patch; do not only add tests that reproduce the old expected behavior. Preserve existing `go test -race` coverage and use the repository-required Go toolchain.


<a id="f34"></a>

### F34 — ABS masks database failures as empty progress or successful deletion

**Priority:** Medium  
**Evidence level:** Confirmed ignored-error paths

**Source:** [`internal/api/abs/abs.go` — `userPayload / getProgress / deleteProgress`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/abs/abs.go#L135-L150); [`internal/api/abs/abs.go` — `progress handlers`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/abs/abs.go#L399-L493).

**Problem and impact.** `getProgress` returns a default zero-progress payload for every storage error, not just missing progress. `deleteProgress` ignores its delete error and returns success. `userPayload` ignores user lookup errors before dereferencing the result, allowing a concurrent deletion or database fault to cause a request panic. These behaviors mislead clients about durable state even when global recovery prevents a process crash.

**Implementation.** Distinguish missing rows from operational failures:

```go
p, err := a.DB.GetProgress(auth.UserID(r), ctx.ed.ID)
if err != nil && !errors.Is(err, store.ErrNotFound) {
    serverError(w, r, err)
    return
}
// Only ErrNotFound should build the existing default-zero response.
```

For deletion:

```go
if err := a.DB.DeleteProgress(auth.UserID(r), ctx.ed.ID); err != nil {
    serverError(w, r, err)
    return
}
write(w, 200, map[string]any{"success": true})
```

Make `userPayload` return `(map[string]any, error)`, check both the user and progress lookups, and update login/me callers to stop before writing a success response on error. Do not encode a nil payload as a successful authenticated user. Apply the same rule to ignored `Rows.Err`, metadata write errors and cleanup results encountered during future coverage of the remaining adapters.

**Regression tests.** Inject read/delete failures and delete a user between authentication and payload construction. Require a controlled generic 5xx/appropriate missing-user response, no false zero-progress success, and no panic.


<a id="f35"></a>

### F35 — Importer file discovery accepts nonregular/unconfined entries and hides traversal errors

**Priority:** Medium  
**Evidence level:** Confirmed in shared importer helpers; full import workflow not reviewed

**Source:** [`internal/importer/importer.go` — `audioPathsIn / applyFiles / applyUsers / ensureLibrary`](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/importer/importer.go).

**Problem and impact.** `audioPathsIn` discards traversal errors, selects entries by extension without requiring regular files, then follows paths using `os.Stat`. `applyFiles` also stats without checking regularity or enforcing the target library root. A source directory ending in an audio extension, a symlink outside a library, or a partially unreadable tree can become an accepted or silently incomplete import plan. This magnifies inconsistent serving policy in F01. Imports are administrative operations, so this is a validation/integrity boundary, not a demonstrated unauthenticated attack.

**Implementation.** Return discovery errors to the caller, explicitly reject nonregular entries and perform a confined descriptor open before committing a planned file:

```go
func discoverAudio(root string) ([]string, error) {
    var paths []string
    err := filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
        if walkErr != nil { return walkErr }
        if d.IsDir() {
            if p != root && strings.HasPrefix(d.Name(), ".") { return filepath.SkipDir }
            return nil
        }
        if _, ok := audioExts[strings.ToLower(filepath.Ext(d.Name()))]; !ok { return nil }
        info, err := d.Info()
        if err != nil { return err }
        if !info.Mode().IsRegular() { return fmt.Errorf("unsupported import entry: %s", p) }
        paths = append(paths, p)
        return nil
    })
    if err != nil { return nil, err }
    sort.Slice(paths, func(i,j int) bool { return natural.Less(paths[i],paths[j]) })
    return paths, nil
}
```

Extend `applyFiles` to receive/resolve the target root and use `mediafs.OpenWithin(root,f.Path)`; use its file info, capture nanosecond mtime, and retain error propagation if a planned file disappears. Route `ensureLibrary` through F08's checked creation policy. In `applyUsers`, create only after `errors.Is(err,store.ErrNotFound)`, not after arbitrary lookup failure. Decide and document partial-import rollback/resume semantics after reviewing the full ABS/Kavita import orchestrators; this review does not claim they were fully assessed.

**Regression tests.** Audio-suffixed directory, symlink outside root, FIFO, permission-denied subdirectory, vanished planned file, duplicate library alias, and a failing user lookup. Report partial discovery as an error/warning the administrator cannot mistake for a complete import.



## Shared implementation candidates

The following blocks supply the reusable implementations referenced above. They are **proposed changes**, not a patch already applied or compiled against the complete repository. New methods, fields and migrations require the call-site changes described in each finding. Keep helpers in a suitable common internal package when used from multiple packages; identical unexported helper names in separate packages do not automatically make them available across packages. The current Go toolchain requirement is the repository's Go 1.26.6, not the older Go used for isolated local experiments.

### P1 — Confined, bounded FFmpeg invocation

Put the shared buffer helper in the same package as the callers, or export it from an internal process package. The complete function below is suitable for finite-output extraction; long-running HLS needs the same descriptor ownership integrated into its session lifecycle rather than this whole-buffer API.

```go
package core

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "io"
    "os"
    "os/exec"
    "time"

    "github.com/libteca/libteca/internal/mediafs"
)

var errOutputLimit = errors.New("processor output limit exceeded")

type limitedBuffer struct {
    buf bytes.Buffer
    limit int64
    exceeded bool
    onLimit func()
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
    remaining := b.limit - int64(b.buf.Len())
    if remaining < int64(len(p)) {
        n := 0
        if remaining > 0 { n, _ = b.buf.Write(p[:int(remaining)]) }
        b.exceeded = true
        if b.onLimit != nil { b.onLimit() }
        return n, errOutputLimit
    }
    return b.buf.Write(p)
}

func runConfinedFFmpeg(parent context.Context, root, path string,
    outputArgs []string, maxOutput int64) ([]byte, error) {
    if maxOutput <= 0 { return nil, fmt.Errorf("invalid output budget") }
    f, _, err := mediafs.OpenWithin(root, path)
    if err != nil { return nil, err }
    defer f.Close()
    ctx, cancel := context.WithCancel(parent)
    defer cancel()
    args := []string{"-nostdin", "-v", "error", "-protocol_whitelist", "fd",
        "-fd", "3", "-i", "fd:"}
    args = append(args, outputArgs...)
    cmd := exec.CommandContext(ctx, "ffmpeg", args...)
    cmd.ExtraFiles = []*os.File{f}
    cmd.WaitDelay = time.Second
    stdout := limitedBuffer{limit: maxOutput, onLimit: cancel}
    stderr := limitedBuffer{limit: 64 << 10, onLimit: cancel}
    cmd.Stdout, cmd.Stderr = &stdout, &stderr
    err = cmd.Run()
    if stdout.exceeded || stderr.exceeded { return nil, errOutputLimit }
    if parent.Err() != nil { return nil, parent.Err() }
    if err != nil { return nil, fmt.Errorf("ffmpeg failed: %w: %s", err, stderr.buf.String()) }
    return stdout.buf.Bytes(), nil
}

var _ io.Writer = (*limitedBuffer)(nil)
```

Do not expose the detailed FFmpeg error text to ordinary API callers; return a generic error and log a redacted diagnostic. `outputArgs` must be server-generated. The initial `fd` input restriction intentionally rejects media that needs arbitrary secondary protocols. Broader format support needs a deliberate confined multi-resource design, not a relaxed global protocol whitelist.

### P2 — Atomic private-cache publication and bounded regular-file reads

These helpers assume a **service-owned private parent directory**. They are not a substitute for `mediafs.OpenWithin` in an attacker-writable library. Creating a directory with a restrictive mode does not repair an already-insecure directory automatically; verify/repair ownership and mode during startup.

```go
package core

import (
    "fmt"
    "io"
    "os"
    "path/filepath"
)

func atomicWritePrivate(dst string, data []byte) error {
    dir := filepath.Dir(dst)
    f, err := os.CreateTemp(dir, ".publish-*")
    if err != nil { return err }
    tmp := f.Name()
    defer os.Remove(tmp)
    if _, err := f.Write(data); err != nil { f.Close(); return err }
    if err := f.Sync(); err != nil { f.Close(); return err }
    if err := f.Close(); err != nil { return err }
    if err := os.Rename(tmp, dst); err != nil { return err }
    d, err := os.Open(dir)
    if err != nil { return err }
    defer d.Close()
    return d.Sync()
}

func readBoundedRegular(path string, max int64) ([]byte, error) {
    if max < 0 { return nil, fmt.Errorf("invalid read budget") }
    f, err := os.Open(path)
    if err != nil { return nil, err }
    defer f.Close()
    info, err := f.Stat()
    if err != nil { return nil, err }
    if !info.Mode().IsRegular() { return nil, fmt.Errorf("not a regular cache file") }
    if info.Size() > max { return nil, fmt.Errorf("cache exceeds budget") }
    b, err := io.ReadAll(io.LimitReader(f, max+1))
    if err != nil { return nil, err }
    if int64(len(b)) > max { return nil, fmt.Errorf("cache exceeds budget") }
    return b, nil
}
```

Callers must handle a failed directory sync as a durability failure even if the rename became visible. The shown permissions default to `0600` from `CreateTemp`; do not broaden them unless another explicitly authorized process needs access.

### P3 — Canonical library roots

```go
func canonicalLibraryRoot(raw string) (string, error) {
    if strings.TrimSpace(raw) == "" { return "", fmt.Errorf("empty library root") }
    abs, err := filepath.Abs(raw)
    if err != nil { return "", err }
    real, err := filepath.EvalSymlinks(abs)
    if err != nil { return "", err }
    fi, err := os.Stat(real)
    if err != nil { return "", err }
    if !fi.IsDir() { return "", fmt.Errorf("library root is not a directory") }
    return filepath.Clean(real), nil
}
```

Imports are `fmt`, `os`, `path/filepath`, and `strings`. This fixes root representation; it does not make future path opens race-resistant. Continue using `os.Root` through `mediafs` for actual access. The library root/ancestors must be administrator-controlled or held as a long-lived root capability if the threat model includes root replacement itself.

### P4 — Error-aware full-content hashing outside database transactions

```go
package scan

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "io"
    "os"
)

func hashComplete(ctx context.Context, f *os.File) (string, error) {
    before, err := f.Stat()
    if err != nil { return "", err }
    if !before.Mode().IsRegular() { return "", fmt.Errorf("not a regular media file") }
    if _, err := f.Seek(0, io.SeekStart); err != nil { return "", err }
    h := sha256.New()
    buf := make([]byte, 256<<10)
    var total int64
    for {
        if err := ctx.Err(); err != nil { return "", err }
        n, readErr := f.Read(buf)
        if n > 0 {
            if _, err := h.Write(buf[:n]); err != nil { return "", err }
            total += int64(n)
        }
        if readErr == io.EOF { break }
        if readErr != nil { return "", readErr }
        if n == 0 { return "", io.ErrNoProgress }
    }
    if err := ctx.Err(); err != nil { return "", err }
    after, err := f.Stat()
    if err != nil { return "", err }
    if total != before.Size() || after.Size() != before.Size() ||
        !after.ModTime().Equal(before.ModTime()) {
        return "", fmt.Errorf("media changed while hashing")
    }
    return hex.EncodeToString(h.Sum(nil)), nil
}
```

### P5 — Lifetime server-instance lock for the Unix deployment targets

```go
//go:build linux || darwin

package main

import (
    "fmt"
    "os"
    "path/filepath"
    "sync"
    "syscall"
)

func lockServerDataDir(dir string) (func(), error) {
    if err := os.MkdirAll(dir, 0o700); err != nil { return nil, err }
    f, err := os.OpenFile(filepath.Join(dir, ".server.lock"), os.O_CREATE|os.O_RDWR, 0o600)
    if err != nil { return nil, err }
    if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
        f.Close()
        return nil, fmt.Errorf("data directory already in use or cannot be locked: %w", err)
    }
    var once sync.Once
    return func() {
        once.Do(func() {
            _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
            _ = f.Close()
        })
    }, nil
}
```

If additional operating systems become supported, add a platform-specific equivalent rather than compiling an ineffective no-op lock. Keep this file separate from code that needs other build tags.

### P6 — Immutable backup bundle publication

Replace `Snapshot` and add `pruneBackupBundles` in `internal/store/backup.go`; add `encoding/json` to its imports. This changes snapshot packaging, so restore consumers must be updated in the same change. The manifest explicitly describes the remaining live-asset consistency limitation.

```go
func (d *DB) Snapshot(coversDir, backupsDir string, keep int) (string, error) {
    var published string
    err := withBackupLock(backupsDir, func() error {
        stage, err := os.MkdirTemp(backupsDir, ".libteca-stage-*")
        if err != nil { return err }
        defer os.RemoveAll(stage)
        dbPath := filepath.Join(stage, "snapshot.db")
        if err := d.BackupTo(dbPath); err != nil { return err }
        if err := syncPath(dbPath); err != nil { return err }
        if err := os.Mkdir(filepath.Join(stage, "covers"), 0o700); err != nil { return err }
        if err := copyTree(coversDir, filepath.Join(stage, "covers")); err != nil { return err }
        manifest, err := json.MarshalIndent(map[string]any{
            "formatVersion": 1,
            "createdAt": time.Now().UTC().Format(time.RFC3339Nano),
            "database": "snapshot.db",
            "covers": "covers",
            "assetConsistency": "best-effort-live-copy; see documented publication coordination",
        }, "", "  ")
        if err != nil { return err }
        mf := filepath.Join(stage, "manifest.json")
        if err := os.WriteFile(mf, manifest, 0o600); err != nil { return err }
        if err := syncPath(mf); err != nil { return err }
        if err := syncPath(filepath.Join(stage, "covers")); err != nil { return err }
        if err := syncPath(stage); err != nil { return err }
        suffix := strings.TrimPrefix(filepath.Base(stage), ".libteca-stage-")
        name := "libteca-" + time.Now().UTC().Format("20060102-150405.000000000") +
            "-" + suffix + ".bundle"
        dest := filepath.Join(backupsDir, name)
        if _, err := os.Lstat(dest); err == nil {
            return fmt.Errorf("snapshot destination already exists")
        } else if !os.IsNotExist(err) { return err }
        if err := os.Rename(stage, dest); err != nil { return err }
        published = filepath.Join(dest, "snapshot.db")
        if err := syncPath(backupsDir); err != nil { return err }
        return pruneBackupBundles(backupsDir, keep, dest)
    })
    return published, err
}

func pruneBackupBundles(dir string, keep int, protect string) error {
    if keep < 1 { return nil }
    entries, err := os.ReadDir(dir) // Names are sorted oldest-to-newest by timestamp prefix.
    if err != nil { return err }
    var candidates []string
    for _, e := range entries {
        if !e.IsDir() || !strings.HasPrefix(e.Name(), "libteca-") ||
            !strings.HasSuffix(e.Name(), ".bundle") { continue }
        path := filepath.Join(dir, e.Name())
        if filepath.Clean(path) == filepath.Clean(protect) { continue }
        candidates = append(candidates, path)
    }
    remove := len(candidates) - (keep - 1) // Published/protected bundle consumes one slot.
    for i := 0; i < remove; i++ {
        if err := os.RemoveAll(candidates[i]); err != nil { return err }
    }
    return syncPath(dir)
}
```

Once immutable cover generations or cross-process publication coordination are implemented, replace the best-effort manifest field with the actual enforced consistency guarantee. Add checksums for the DB and cover entries, and verify them before restoration; do not use a manifest statement as a substitute for the coordination itself.

### P7 — Transactional, presence-aware core progress patches

Add a store file with the following types and method. Existing handlers must decode this shape and call `ApplyProgressPatch` instead of building a zero-filled `ReadingProgress`. Its policy is: omitted/null fields are unchanged; explicit zero/false is applied; full reset uses the existing delete operation. This implementation intentionally does not add a cross-device revision protocol; F29 explains why that is a separate requirement.

```go
package store

import (
    "database/sql"
    "errors"
    "fmt"
    "math"
)

var ErrInvalidProgress = errors.New("invalid progress patch")

type ProgressPatch struct {
    Position *float64 `json:"position"`
    Duration *float64 `json:"duration"`
    Finished *bool `json:"finished"`
    Device *string `json:"device"`
    Page *int64 `json:"page"`
    Percent *float64 `json:"percent"`
    Locator *string `json:"locator"`
}

func (d *DB) ApplyProgressPatch(userID, editionID int64, patch ProgressPatch) error {
    finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
    if patch.Position != nil && (!finite(*patch.Position) || *patch.Position < 0) {
        return ErrInvalidProgress
    }
    if patch.Duration != nil && (!finite(*patch.Duration) || *patch.Duration < 0) {
        return ErrInvalidProgress
    }
    if patch.Percent != nil && (!finite(*patch.Percent) || *patch.Percent < 0 || *patch.Percent > 1) {
        return ErrInvalidProgress
    }
    if patch.Page != nil && *patch.Page < 0 { return ErrInvalidProgress }
    if patch.Device != nil && len(*patch.Device) > 256 { return ErrInvalidProgress }
    if patch.Locator != nil && len(*patch.Locator) > 8192 { return ErrInvalidProgress }
    return d.Update(func(tx *Tx) error {
        var exists int
        if err := tx.QueryRow(`SELECT 1 FROM editions WHERE id=?`, editionID).Scan(&exists); err != nil {
            if errors.Is(err, sql.ErrNoRows) { return ErrNotFound }
            return err
        }
        p := ReadingProgress{Progress: Progress{UserID: userID, EditionID: editionID}}
        err := tx.QueryRow(`SELECT file_id,file_offset_secs,edition_position_secs,duration_secs,
            is_finished,device,page,percent,locator FROM progress WHERE user_id=? AND edition_id=?`,
            userID, editionID).Scan(&p.FileID,&p.FileOffsetSecs,&p.EditionPositionSecs,&p.DurationSecs,
                &p.IsFinished,&p.Device,&p.Page,&p.Percent,&p.Locator)
        if err != nil && !errors.Is(err, sql.ErrNoRows) { return err }
        var total float64
        if err := tx.QueryRow(`SELECT COALESCE(SUM(duration_secs),0) FROM files
            WHERE edition_id=? AND missing=0`, editionID).Scan(&total); err != nil { return err }
        if !finite(total) || total < 0 { return fmt.Errorf("invalid stored media duration") }
        if patch.Position != nil {
            pos := *patch.Position
            if err := ValidPosition(pos, total); err != nil {
                return fmt.Errorf("%w: %v", ErrInvalidProgress, err)
            }
            if total > 0 && pos > total { pos = total } // Accepted small end-of-file tolerance.
            rows, err := tx.Query(`SELECT id,duration_secs FROM files
                WHERE edition_id=? AND missing=0 ORDER BY seq,id`, editionID)
            if err != nil { return err }
            var files []struct { id int64; dur float64 }
            for rows.Next() {
                var f struct { id int64; dur float64 }
                if err := rows.Scan(&f.id,&f.dur); err != nil { rows.Close(); return err }
                files = append(files, f)
            }
            scanErr, closeErr := rows.Err(), rows.Close()
            if scanErr != nil { return scanErr }
            if closeErr != nil { return closeErr }
            if len(files) == 0 { return ErrNotFound }
            base := 0.0
            for i, f := range files {
                if !finite(f.dur) || f.dur < 0 { return fmt.Errorf("invalid stored file duration") }
                if pos < base+f.dur || i == len(files)-1 {
                    id := f.id
                    p.FileID, p.FileOffsetSecs = &id, pos-base
                    break
                }
                base += f.dur
            }
            p.EditionPositionSecs = pos
        }
        if patch.Duration != nil {
            value := *patch.Duration
            if total > 0 { value = total } // Client duration cannot redefine a known edition.
            p.DurationSecs = &value
        }
        if patch.Finished != nil { p.IsFinished = *patch.Finished }
        if patch.Device != nil { p.Device = patch.Device }
        if patch.Page != nil { p.Page = patch.Page }
        if patch.Percent != nil { p.Percent = patch.Percent }
        if patch.Locator != nil { p.Locator = patch.Locator }
        _, err = tx.Exec(`INSERT INTO progress
            (user_id,edition_id,file_id,file_offset_secs,edition_position_secs,duration_secs,
             is_finished,device,updated_at,page,percent,locator)
            VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
            ON CONFLICT(user_id,edition_id) DO UPDATE SET
            file_id=excluded.file_id,file_offset_secs=excluded.file_offset_secs,
            edition_position_secs=excluded.edition_position_secs,duration_secs=excluded.duration_secs,
            is_finished=excluded.is_finished,device=excluded.device,updated_at=excluded.updated_at,
            page=excluded.page,percent=excluded.percent,locator=excluded.locator`,
            userID,editionID,p.FileID,p.FileOffsetSecs,p.EditionPositionSecs,p.DurationSecs,
            p.IsFinished,p.Device,nowMilli(),p.Page,p.Percent,p.Locator)
        return err
    })
}
```

Map `ErrInvalidProgress` to 400, `ErrNotFound` to 404 and genuine database failures to 500/503. Preserve the existing authentication and body-size limit. To support explicit clearing of nullable fields later, introduce a JSON field type that distinguishes absent from null rather than overloading pointers silently.

### P8 — Merge-preserving serial frontend progress queue

This TypeScript implementation passed an isolated compilation and Node regression test for retry/merge/order/zero/false behavior. It is **not** a browser lifecycle integration test. Supply nonthrowing `onError`/UI callbacks. Rehydrate only validated pending patches belonging to the current user, edition and session generation; create/stop one queue per such identity.

```ts
export type ProgressPatch = {
  position?: number; duration?: number; finished?: boolean;
  device?: string; page?: number; percent?: number; locator?: string;
};

type Options = {
  send: (patch: ProgressPatch) => Promise<void>;
  persist: (dirty: ProgressPatch) => void;
  onError: (error: unknown) => void;
  initial?: ProgressPatch;
  retryBaseMs?: number;
};

export class ProgressQueue {
  private pending: ProgressPatch;
  private inFlight: ProgressPatch | null = null;
  private running = false;
  private stopped = false;
  private failures = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;

  constructor(private readonly options: Options) {
    this.pending = { ...options.initial };
  }

  snapshot(): ProgressPatch {
    return { ...this.inFlight, ...this.pending };
  }

  private checkpoint(): void {
    try { this.options.persist(this.snapshot()); }
    catch (error) { this.options.onError(error); }
  }

  enqueue(patch: ProgressPatch): void {
    if (this.stopped) throw new Error("progress queue stopped");
    const clean = Object.fromEntries(
      Object.entries(patch).filter(([, value]) => value !== undefined),
    ) as ProgressPatch;
    this.pending = { ...this.pending, ...clean };
    this.checkpoint();
    void this.flush();
  }

  async flush(): Promise<void> {
    if (this.running || this.stopped || this.timer !== undefined) return;
    this.running = true;
    try {
      while (!this.stopped && Object.keys(this.pending).length > 0) {
        const batch = this.pending;
        this.pending = {};
        this.inFlight = batch;
        this.checkpoint();
        try {
          await this.options.send(batch);
        } catch (error) {
          this.pending = { ...batch, ...this.pending }; // Newer fields win.
          this.inFlight = null;
          this.checkpoint();
          this.options.onError(error);
          this.failures += 1;
          if (!this.stopped) {
            const base = Math.max(1, this.options.retryBaseMs ?? 1000);
            const delay = Math.min(30000, base * 2 ** Math.min(this.failures - 1, 8));
            this.timer = setTimeout(() => {
              this.timer = undefined;
              void this.flush();
            }, delay);
          }
          return;
        }
        this.inFlight = null;
        this.failures = 0;
        this.checkpoint(); // Clear persistence only after acknowledgement.
      }
    } finally {
      this.running = false;
    }
  }

  stop(): void {
    this.stopped = true;
    if (this.timer !== undefined) clearTimeout(this.timer);
    this.timer = undefined;
    this.checkpoint(); // An in-flight request remains dirty until acknowledged.
  }
}
```

Example hook wiring, using existing `apiChecked` and a fixed user/edition identity:

```ts
const key = `libteca:pending-progress:v1:${userId}:${editionId}:${sessionGeneration}`;
const queue = new ProgressQueue({
  initial: validatedPendingPatch,
  send: patch => apiChecked(`/progress/${editionId}`, {
    method: "POST", body: JSON.stringify(patch),
  }).then(() => undefined),
  persist: dirty => {
    if (Object.keys(dirty).length === 0) localStorage.removeItem(key);
    else localStorage.setItem(key, JSON.stringify(dirty));
  },
  onError: error => setSaveError(String(error)),
});
// Connect user progress changes to queue.enqueue(patch).
// On mount/online/visibility change, call void queue.flush().
// On unmount/account change call queue.stop(), retaining unacknowledged persistence.
```

Keep identity variables captured by the queue rather than reading “the current edition” from a mutable global at delivery time. A storage failure should visibly warn that offline durability is unavailable; network synchronization can still continue. Pause/stop retries on logout or permanent authorization failure instead of retrying forever. A durable client queue improves delivery but cannot by itself order independent devices or beacon requests; server-side revision enforcement remains necessary.


### P9 — Revision and idempotency guard for progress writes

This companion to P7 prevents an old independent request from silently overwriting a newer acknowledged state. Add a new migration:

```sql
ALTER TABLE progress ADD COLUMN revision INTEGER NOT NULL DEFAULT 0;
CREATE TABLE progress_write_receipts (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    edition_id INTEGER NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
    mutation_id TEXT NOT NULL,
    request_digest TEXT NOT NULL,
    revision INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY(user_id,edition_id,mutation_id)
);
CREATE INDEX progress_receipts_age ON progress_write_receipts(created_at);
```

Refactor the transaction body of P7 into `applyProgressPatchTx(tx,*args)` and pass it as `apply` below. Do **not** call `DB.ApplyProgressPatch` from inside this transaction; that would nest database operations incorrectly. This wrapper is the transaction owner:

```go
var ErrProgressConflict = errors.New("progress revision conflict")

func (d *DB) ApplyProgressMutation(userID, editionID, expected int64, mutationID, requestDigest string,
    apply func(*Tx) error) (int64, error) {
    if expected < 0 || len(mutationID) < 16 || len(mutationID) > 96 || len(requestDigest) != 64 || apply == nil {
        return 0, ErrInvalidProgress
    }
    var accepted int64
    err := d.Update(func(tx *Tx) error {
        var priorDigest string
        err := tx.QueryRow(`SELECT revision,request_digest FROM progress_write_receipts
            WHERE user_id=? AND edition_id=? AND mutation_id=?`,
            userID,editionID,mutationID).Scan(&accepted,&priorDigest)
        if err == nil {
            if priorDigest != requestDigest { return ErrInvalidProgress }
            return nil // Already committed: acknowledge without replaying.
        }
        if !errors.Is(err,sql.ErrNoRows) { return err }
        var current int64
        err = tx.QueryRow(`SELECT revision FROM progress WHERE user_id=? AND edition_id=?`,
            userID,editionID).Scan(&current)
        if err != nil && !errors.Is(err,sql.ErrNoRows) { return err }
        if current != expected { return ErrProgressConflict }
        if current == 1<<63-1 { return ErrInvalidProgress }
        if err := apply(tx); err != nil { return err }
        res, err := tx.Exec(`UPDATE progress SET revision=revision+1
            WHERE user_id=? AND edition_id=? AND revision=?`,userID,editionID,current)
        if err != nil { return err }
        n, err := res.RowsAffected()
        if err != nil { return err }
        if n != 1 { return ErrProgressConflict }
        accepted = current+1
        _, err = tx.Exec(`INSERT INTO progress_write_receipts
            (user_id,edition_id,mutation_id,request_digest,revision,created_at) VALUES(?,?,?,?,?,?)`,
            userID,editionID,mutationID,requestDigest,accepted,nowMilli())
        return err
    })
    return accepted, err
}
```

Expose `revision` on progress reads, and return the accepted revision on writes. Each logical request gets one random mutation ID which is reused for retries of the *same payload*. Compute `requestDigest` on the server as SHA-256 of the normalized patch plus expected revision; do not accept a client-asserted digest. The receipt comparison rejects reuse of an ID for a different payload. Bound receipt retention and client offline windows, and rate-limit creation so this does not introduce an unbounded auxiliary table. The wrapper rejects revision overflow. Deletion/reset must participate in the same revision contract: retain a tombstone/head revision rather than deleting it and resetting to zero, which could make old requests appear current again. This requires updating the delete path or moving the revision head to a separate persistent table before enabling the protocol.

For 409, the frontend must fetch current state and resolve a genuine conflict (for example keeping the active session's explicitly chosen position), not blindly take the numerically greatest position or silently retry against a new revision. Legacy adapters that cannot carry a revision need a documented fallback policy; this guard only protects clients whose writes participate in the same ordering contract. Sequence/revision migration is therefore a coordinated API-and-client change, not a drop-in server-only fix.


## Additional hardening and verification items

These are important follow-through items, but are not counted as independently demonstrated exploits in the 35 findings.

**Resource budgets beyond process counts.** `transcode.MaxSessions=8` and the trickplay semaphore bound concurrency, not total output bytes. HLS uses an unlimited playlist and can generate a whole long/high-bitrate source. Add per-user admission quotas, a private-cache volume quota, an aggregate cache-byte watermark and eviction that never deletes active sessions. A periodic directory-byte check should cancel an over-budget job rather than letting a process-count limit stand in for a storage limit. A compact check usable by a job monitor is:

```go
func directoryBytes(root string) (int64, error) {
    var total int64
    err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
        if err != nil { return err }
        if !d.Type().IsRegular() { return nil }
        st, err := d.Info()
        if err != nil { return err }
        total += st.Size()
        return nil
    })
    return total, err
}
```

Use server-owned directories, handle overflow for extreme sizes, and preserve quota failures as visible session errors. A monitor is a backstop, not as strict as a filesystem quota. Source: [transcode manager](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/transcode/transcode.go), [command construction](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/transcode/hwaccel.go), [trickplay](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/trickplay/trickplay.go).

**Successful Subsonic token requests still run Argon2.** The legacy `t+s` branch verifies the cached secret against the primary password hash after the MD5 comparison on every valid request. That can consume the bounded password-verification capacity during ordinary parallel client requests. Resolve F05 first. For dedicated app credentials, perform a cheap constant-time verification of the high-entropy app credential and its revocation/generation; for any cached expensive proof use a bounded TTL cache keyed by user, credential generation and proof digest, with deletion/revocation checks on every request. Do not cache a bare “user is authenticated” flag. Source: [Subsonic authentication](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/subsonic/subsonic.go).

**VAAPI filter-chain compatibility needs real hardware validation.** The command requests VAAPI output frames and then applies `format=nv12,hwupload`, a software-format/upload chain whose compatibility with already-hardware frames needs verification. No supported GPU was available, so this is not presented as a reproduced hardware defect. Test a hardware-frame chain such as `scale_vaapi=format=nv12` for hardware decode, and a separate software-decode `format=nv12,hwupload` path with an initialized device. Validate actual frame flow and fallback on representative media rather than accepting an encoder's presence as proof of working acceleration. Source: [hardware arguments](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/transcode/hwaccel.go).

**Metadata result application is only partially observable.** Cover, genre, chapter and episode enrichment failures can be suppressed while a match returns an apparently successful summary. Preserve per-operation warnings and counts of attempted/succeeded/failed updates, for example `warnings: [{step:"cover", error:"download failed"}]`, while keeping sensitive provider URLs out of responses/logs. Treat partial success as explicit, and make retries idempotent. Source: [applyResult/applyChapters/distributeChapters](https://github.com/libteca/libteca/blob/b95620bc6bce313bf32d40837ed7464e090542f2/internal/api/core/providers.go).

**Large single files and duplicated policy invite regressions.** The core/Jellyfin/ABS/Subsonic handlers duplicate authentication adaptation, file selection, progress conversion, cache policy and error translation. Extract small shared services with enforced invariants (`MediaAccess`, `ProgressService`, `PlaybackSessionService`) rather than merely moving handlers into more files. Use table-driven cross-adapter tests so a security fix cannot land in one face but miss another. This is an architectural recommendation grounded in the repeated discrepancies above, not a separate counted bug.

**Dependency and release hardening remains unverified.** This audit did not establish known dependency vulnerabilities, reproducible release provenance, action/image digest integrity, fuzz coverage or real-client certification. Add a pinned vulnerability-scanning tool, lockfile checks, SBOM/provenance generation and parser fuzzing. Do not invent image digests or claim a CVE without checking the exact dependency version and reachable code path. The README already calls out unverified real-client corpus behavior; that acknowledged limitation is not rebranded as a newly discovered defect.

## Validation performed

All dynamic checks below used locally generated benign fixtures or extracted/minimal logic. **The complete libteca server and its full test suite were not run.**

| Check | Actual result | What it establishes—and does not |
|---|---|---|
| Audio-only FFmpeg HLS mapping | Mandatory video map exited 234; no playlist. Optional video map exited 0; playlist created. | Establishes F16's argument-level defect; not end-to-end browser/hardware validation. |
| Go symlink-root traversal | `Stat IsDir=true`; WalkDir visited the symlink as non-directory; zero regular files and no walk error. | Establishes F09's filesystem behavior; does not exercise all scanner packages. |
| Sampled hash identity | Equal size and equal bytes fed to the sampled hash; unequal full SHA-256. | Deterministic F12 collision mechanism; not a probabilistic hash attack. |
| Core progress UPSERT | Finished-only model reset position 642→0, duration 3600→NULL, device→NULL; page/percent survived. | Uses inspected SQL expressions in a minimal SQLite schema, not a full migration-backed server. |
| Unconfined file/subtitle-cache operations | Temporary symlink returned outside sentinel; write replaced outside sentinel. | Establishes read/write primitives in F01/F02; no live-service exploitation. |
| FFmpeg descriptor input | `-protocol_whitelist fd -fd 3 -i fd:` decoded the local WAV successfully. | Supports P1's feasibility on installed FFmpeg 7.1.5, not every distribution/build. |
| Proposed TypeScript progress queue | Strict TypeScript compilation and Node assertions passed; maximum concurrent sends=1; retry preserved merged fields and explicit zero/false. | Tests P8 in isolation, not Preact effects, browser unload, storage quotas or cross-device revision integration. |
| Proposed atomic writer, full hasher and lock | Three isolated Go tests passed: atomic/bounded cache I/O, full-content hashing/cancellation, and exclusive/idempotently released instance locking. All 39 Go blocks also passed syntax-format parsing; that is not full type checking. | Isolated standard-library tests only; not compiled into the full application. |

Environment: local Go 1.23.2 versus repository requirement Go 1.26.6; Node 22.16; FFmpeg 7.1.5; Python SQLite 3.46.1. GitHub connector reads succeeded. A network clone/archive download into the execution container was unavailable, so no full checkout, full dependency resolution, `go test ./...`, `go test -race ./...`, browser suite, Docker build, hardware test or vulnerability scanner result is claimed.

### Reproduce the FFmpeg argument failure safely

This uses only generated audio in a temporary directory:

```sh
work="$(mktemp -d)"
ffmpeg -nostdin -v error -f lavfi -i 'sine=frequency=1000:duration=3' "$work/input.wav"
mkdir "$work/original" "$work/fixed"
# Expected failure: no video stream exists.
ffmpeg -nostdin -v error -i "$work/input.wav" -map 0:v:0 -map '0:a:0?' \
  -c:v libx264 -c:a aac -f hls -hls_time 4 -hls_list_size 0 \
  -hls_segment_filename "$work/original/seg%05d.ts" "$work/original/index.m3u8"
# Expected success for the software-path minimal mitigation.
ffmpeg -nostdin -v error -i "$work/input.wav" -map '0:v:0?' -map '0:a:0?' \
  -c:v libx264 -c:a aac -f hls -hls_time 4 -hls_list_size 0 \
  -hls_segment_filename "$work/fixed/seg%05d.ts" "$work/fixed/index.m3u8"
# Descriptor input feasibility; requires an FFmpeg build with the fd protocol.
ffmpeg -nostdin -v error -protocol_whitelist fd -fd 3 -i fd: -f null - 3<"$work/input.wav"
```

Use a shell without immediate exit-on-error for the intentionally failing command. Remove the temporary directory after inspecting results.

## Coverage and explicit limits

The repository was browsed through GitHub file/tree APIs at the pinned commit, not inferred from its README or previous audit reports. The following describes source areas actually inspected; “partial” means only the listed/relevant portions were read, not that the entire module was audited.

| Area | Actual files/coverage |
|---|---|
| Entrypoint/server/auth | `cmd/libteca/main.go`, `internal/server/server.go`, `internal/auth/auth.go`, `internal/mediafs/mediafs.go`. |
| Core API | `core.go` across several ranges covering initialization, libraries/scanning, work/progress and serving; `hls.go`, `reading.go`, `subtitles.go`; `providers.go` through matching/application/download/early inbox sections (partial). |
| Compatibility APIs | ABS `abs.go` 1–150 and 360–690 (partial); Jellyfin `jellyfin.go` 1–300 and 700–end (partial); OPDS `opds.go` 1–300 and 400–700 (partial); Subsonic `subsonic.go` 1–290 and 465–830 (partial). Their other source files were not all reviewed. |
| Store | `store.go`, `queries.go` including its tail, `reading.go`, `works.go`, `progress.go`, `users.go`, `reconcile.go`, `backup.go`; `files_test.go` identity tests. Other store modules and the complete migration history were not read. |
| Scanning | `scan.go`; `video.go` through the initial processing/fast paths; `books.go` through the main scan/probe/store and initial cover code (partial). Dedicated EPUB/archive/extractor/game parsers were not comprehensively reviewed. |
| Media processing | `hwaccel.go`, `trickplay.go`; `transcode.go` through the manager/start/fallback area (partial). |
| Podcasts/watch/import | Podcast `fetch.go`, `download.go`, `podcast.go`, `helpers.go`; `watch.go`; shared `importer/importer.go`. Provider-specific/foreign-database import orchestrators were not fully reviewed. |
| Frontend | `api.ts`, `public/sw.js`, `package.json`; `app.tsx` 1–260, `reader/shared.tsx` helper region, `reader/epub.tsx` 1–230, `players/audio.tsx` 1–250, `players/video.tsx` 1–285 (partial). Remaining views/readers/player code/styles were not comprehensively reviewed. |
| Packaging/docs | README, `go.mod`, Dockerfile, `.github/workflows/ci.yml`, systemd unit. Trees were enumerated for broader structure. Prior `AUDIT-*.md` files were not used as evidence of current bugs. |

Important unassessed areas include all provider-specific external API clients, full archive/XML/PDF/game parsing behavior, the full WebSocket implementation, every settings/admin/linking/playlist path, all migrations, full dependency advisories and installed-artifact/runtime behavior. The report therefore does not certify the repository or claim to have found every possible issue.

### Suspicions deliberately not promoted to confirmed findings

An empty edition causing progress to store file ID zero was considered and rejected: `EditionByID` returns `ErrNotFound` when no live files exist. The service worker now excludes the problematic authenticated/query/range cases, so its earlier privacy issue was not counted. The first-party video player now uses a full HLS timeline and client-side resume; the previous server-offset/frontend-relative-resume complaint was not repeated. A possible audio-player index/source mismatch was not promoted because the examined application routing can remount components and the remaining relevant call sites were not established. VAAPI hardware behavior and full import transaction semantics remain validation items, not asserted reproduced failures.

## Recommended implementation order

First close the cross-adapter file/processor/subtitle boundary gaps (F01–F03), revoke/expire playback capabilities (F04), decide the Subsonic credential policy (F05), and correct private caching (F06). Apply the server-instance lock and protect backup immutability before destructive lifecycle testing (F15/F27). Next preserve source identity, fix rename/hash/invalidation/scan status (F08–F14), and make progress patches/delivery reliable (F28–F29/P9). Then repair HLS/trickplay, retention, watcher recovery and packaging, adding each regression before the fix is considered complete.

Take a protected backup before schema/identity migrations. Test upgrade and rollback on copies containing real combinations of library aliases, existing progress, playlists, old sampled hashes, legacy sessions and shared-cover backups. Avoid bundling every refactor into one unreviewable change: this single report can drive a series of narrowly scoped patches.

## External primary references

These support implementation behavior rather than substituting for repository evidence:

- [Go filepath documentation — WalkDir and IsLocal](https://pkg.go.dev/path/filepath): WalkDir does not follow symbolic links; lexical containment alone is not an actual file-opening boundary.
- [Go: Traversal-resistant file APIs](https://go.dev/blog/osroot): rationale for rooted operations and avoiding check-then-reopen races.
- [FFmpeg command documentation](https://ffmpeg.org/ffmpeg.html): optional stream maps and input/stream-selection options.
- [FFmpeg protocol documentation — fd](https://ffmpeg.org/ffmpeg-protocols.html#fd): seekable descriptor input and its `fd` option. The descriptor number is not encoded as `fd:3`.

**Bottom line:** The most consequential pattern is inconsistent enforcement across multiple protocol adapters and between HTTP file serving, media processors and persisted user state. Fix shared invariants once, test them through every adapter, and verify the concrete deployment rather than equating passing unit tests with complete assurance.
