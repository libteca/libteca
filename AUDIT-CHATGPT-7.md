# libteca source-code audit

**Repository:** [libteca/libteca](https://github.com/libteca/libteca)  
**Audited revision:** [`b85e1c3b167c1b9c5b38e64a9087212072004d97`](https://github.com/libteca/libteca/commit/b85e1c3b167c1b9c5b38e64a9087212072004d97), `main` as retrieved on **September 17, 2026**.  
**Deliverable:** Findings, proposed implementations, regression cases, validation results, and review coverage in this one file.

## Executive assessment

The highest-priority problems are not cosmetic. They concern loss of backup generations, overwriting downloaded episodes, exposure of files outside a configured library under a specific filesystem threat model, plaintext storage of a user's main password, unbounded work on network-facing routes, and corruption of playback/reading state.

Several existing protections are effective and should be preserved: the shared Argon2 concurrency limit; password-bound token issuance; transactional last-administrator protection; the podcast HTTP client's DNS/dial-time private-address checks; transcode session-ID path validation; bounded CBZ page extraction; and the SQLite WAL/foreign-key configuration. This report does **not** claim that those already-addressed vulnerabilities remain open.

The report is a broad, risk-based review of actual source files retrieved through the GitHub connector, not a guarantee that every possible defect has been found. Implementation files were read across the server, authentication, storage, scanners, podcasts, playback, protocol adapters, readers, watcher, importer, build, and deployment paths. Some large files were reviewed in ranges; the coverage ledger at the end identifies those limits. Previous audit documents in the repository were not used as evidence.

### Evidence and implementation status

**Static-confirmed** means the problematic control flow or data operation is directly present in the pinned source. **Isolated reproduction** means a small local program reproduced that mechanism, not that the entire application was executed. **Conditional** identifies a necessary deployment, concurrency, or input condition. **Hardening** is a recommendation rather than a demonstrated exploit.

A clone attempt failed because the execution environment could not resolve GitHub; direct GitHub connector reads succeeded. The local Go toolchain is **1.23.2**, while the pinned `go.mod` requires **1.26.6**. Consequently, **`go test ./...`, the repository race suite, the frontend build, end-to-end browser tests, and a dependency vulnerability scan were not run**. Isolated checks used Python/SQLite, Node 22.16.0, Go 1.23.2 standard-library programs, GNU Make, and FFmpeg 7.1.5.

The code below is a set of **proposed integration patches**, not a claim that an integrated patchset has already compiled or passed the repository suite. Small replacements identify their enclosing function; larger changes identify schema and caller work that must accompany them. Shared fixes are cross-referenced rather than duplicated. New migrations must receive distinct sequential numbers and be tested against a copy of a real database. No remote repository changes were made.

### Severity interpretation

**High:** material data loss, credential exposure, substantial availability risk, or core state corruption. **Medium:** significant correctness, reliability, isolation, or operational failure under stated conditions. **Low:** narrower interoperability, validation, or documentation failure. Severity is not a CVSS score, and a conditional finding is not represented as an unauthenticated remote exploit.

## Finding index

**50 findings: 12 High, 34 Medium, and 4 Low.** Conditions and evidence strength remain part of each entry; the severity tally is not an exploit count.

| ID | Severity | Finding |
|---|---|---|
| [F01](#f01) | High | Compatibility login handlers accept unbounded JSON bodies |
| [F02](#f02) | High | Subsonic authentication persists the primary password in plaintext |
| [F03](#f03) | High, conditional | Library file paths are not confined to the library at open time |
| [F04](#f04) | High | Concurrent retention passes can remove every backup |
| [F05](#f05) | High | Backup generations share mutable cover files and publish too early |
| [F06](#f06) | High | The last podcast filename fallback can overwrite another episode |
| [F07](#f07) | High | CBR listing can deadlock after reaching its output cap |
| [F08](#f08) | Medium | Several media probes and extractors are not cancellable or output-bounded |
| [F09](#f09) | High, conditional on hostile/oversized artwork | Cover handling can allocate unbounded compressed or decoded image data |
| [F10](#f10) | High | Trickplay generation bypasses the transcode concurrency limit |
| [F11](#f11) | High (progress correctness) | Server-side HLS resume loses the absolute playback timeline |
| [F12](#f12) | Medium | Signing out does not revoke the browser's server token |
| [F13](#f13) | Medium | A stale self-service password change can overwrite a newer reset |
| [F14](#f14) | Medium | ABS playback URLs remain valid after token revocation and have no expiry |
| [F15](#f15) | Medium | Principal-only login limiting can be bypassed by changing usernames |
| [F16](#f16) | Medium, conditional on a shared cache | Authenticated OPDS assets are explicitly marked publicly cacheable |
| [F17](#f17) | High (library integrity) | Display titles and directory grouping collapse distinct media identities |
| [F18](#f18) | Medium | Audiobook disc directories are interleaved by basename sorting |
| [F19](#f19) | Medium | Alternate audiobook encodings are concatenated and M4A is mislabeled |
| [F20](#f20) | Medium | A warm music rescan gives new tracks the wrong album position |
| [F21](#f21) | Medium | Updating a game file moves it to the end of its edition |
| [F22](#f22) | High when combined with missing-file reconciliation | Failed directory walks and failed probes can be reported as successful scans |
| [F23](#f23) | Medium | Manual and CLI rescans do not consistently reconcile deleted files |
| [F24](#f24) | Medium | Partial-file hashes are used as though they established file identity |
| [F25](#f25) | Medium | Relinking treats non-not-found stat failures as disappearance |
| [F26](#f26) | Medium | Duplicate or overlapping library roots conflict with global file-path identity |
| [F27](#f27) | Medium | Scan invalidation misses same-second changes and sidecar-only edits |
| [F28](#f28) | Medium | A killed transcode launch can skip Wait and leave cleanup racing the child |
| [F29](#f29) | Medium | An expired HLS session can be silently recreated with a different origin |
| [F30](#f30) | Medium | Direct MP4 resume is disabled on browsers that support native HLS |
| [F31](#f31) | Medium | HLS.js failures can leave a blank player with no actionable error |
| [F32](#f32) | Medium | Rewinding audio suppresses periodic progress saves |
| [F33](#f33) | Medium | HTTP errors can be shown as “saved,” and failed progress is discarded |
| [F34](#f34) | Medium | Closing the PDF reader can undo “Mark finished” |
| [F35](#f35) | Medium | CBZ page ordering and supported extensions differ between clients |
| [F36](#f36) | Low | Unguarded localStorage access can prevent application startup |
| [F37](#f37) | Medium | ABS session close commits before final progress, making retries ineffective |
| [F38](#f38) | Medium | ABS track offsets truncate fractional durations and chapter IDs repeat |
| [F39](#f39) | Medium | Jellyfin's season-zero filter also means “no filter” |
| [F40](#f40) | Medium | Jellyfin session stop can write two JSON documents into one response |
| [F41](#f41) | Medium | Compatibility progress endpoints lack core-equivalent numeric validation |
| [F42](#f42) | Medium | The importer’s natural comparator orders 10 before 2 |
| [F43](#f43) | Low | The foreign-database URI is built from an unescaped filesystem path |
| [F44](#f44) | Medium | Derived assets can be served before completion and remain stale after source changes |
| [F45](#f45) | Medium | Parallel release builds can embed stale or missing web assets |
| [F46](#f46) | Medium | Unknown-length podcast downloads receive an unnecessarily short total deadline |
| [F47](#f47) | Low | Administrative user creation applies inconsistent validation and error mapping |
| [F48](#f48) | Medium | Watcher overflow is logged without guaranteeing reconciliation |
| [F49](#f49) | Low | The documented quick-start password is rejected by initialization |
| [F50](#f50) | Medium | Metadata operations can claim success after failed database or cover updates |

The separate [hardening section](#additional-hardening-and-engineering-recommendations) contains H01–H08. See [validation](#validation-performed) and [coverage](#review-coverage-and-boundaries) before treating the proposed patches as production-ready.

## Findings and implementations

<a id="f01"></a>

### F01 — Compatibility login handlers accept unbounded JSON bodies

**Severity:** High · **Evidence:** Static-confirmed · **Exposure:** Unauthenticated HTTP requests.

**Sources:** [server route construction](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/server/server.go), [ABS `Login`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/abs/abs.go), [Jellyfin `authenticate`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/jellyfin/jellyfin.go).

The 4 MiB body limit is mounted on the core groups only. `/login` and `/Users/AuthenticateByName` decode JSON without that limit. A large string must be allocated before password-length validation or the Argon2 semaphore helps. The Jellyfin handler also ignores the decoding error, allowing processing of partially decoded data. A read timeout limits elapsed time, not the number of bytes a fast sender can allocate.

**Implementation:** Apply an outer request-body ceiling, retain tighter login limits, and make decoding failure terminate every handler. This helper deliberately permits unknown fields for protocol compatibility but rejects a second JSON value.

```go
// New package internal/httpinput; imports: encoding/json, errors, io, net/http.
func LimitBody(n int64) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if r.Body != nil {
                r.Body = http.MaxBytesReader(w, r.Body, n)
            }
            next.ServeHTTP(w, r)
        })
    }
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
    r.Body = http.MaxBytesReader(w, r.Body, limit)
    dec := json.NewDecoder(r.Body)
    err := dec.Decode(dst)
    if err == nil {
        var extra any
        if e := dec.Decode(&extra); e != io.EOF {
            if e == nil { e = errors.New("multiple JSON values") }
            err = e
        }
    }
    if err == nil { return true }
    status := http.StatusBadRequest
    var tooLarge *http.MaxBytesError
    if errors.As(err, &tooLarge) { status = http.StatusRequestEntityTooLarge }
    http.Error(w, http.StatusText(status), status)
    return false
}
```

In `Server.buildHandler`, return `httpinput.LimitBody(4 << 20)(app.Handler())`. In both compatibility login functions, replace the decoder call with `if !httpinput.DecodeJSON(w, r, &body, 32<<10) { return }`. Apply equivalent checked decoding to mutating ABS/Jellyfin handlers, preserving their response envelope where required.

**Regression:** Send oversized known and unknown string fields, malformed JSON, and two JSON documents to every login face. Expect 413/400 without a KDF call, a user change, or a token insert. Check that normal clients with additional harmless fields still authenticate.

<a id="f02"></a>

### F02 — Subsonic authentication persists the primary password in plaintext

**Severity:** High · **Evidence:** Static-confirmed · **Exposure:** Read access to the service database or its backups.

**Source:** [Subsonic password and token authentication](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/subsonic/subsonic.go).

Successful password authentication stores the supplied password under `settings["subsonic.pw.<userID>"]` to support the Subsonic `MD5(password + salt)` flow. That is the user's primary libteca password, not an independent application secret. The Argon2 hash in `users` therefore does not provide the expected protection against an offline database read once this protocol is used. Hashing this cached value with Argon2 would not preserve the legacy MD5 protocol: the server needs the original secret for that computation.

**Implementation, secure baseline:** Stop writing this cache, stop accepting `t`/`s` against the primary password, and require password authentication over TLS until a separate application-password feature exists. Remove existing cache entries in a migration:

```sql
-- +goose Up
DELETE FROM settings WHERE key GLOB 'subsonic.pw.*';
```

```go
// In the Subsonic authentication branch, before the old t/s verification:
if r.Form.Get("t") != "" || r.Form.Get("s") != "" {
    // Emit the adapter's normal authentication-error envelope.
    // Do not perform a lookup in settings or recreate a password cache.
    a.unauthorized(w, r) // Use the existing adapter error writer/signature.
    return
}
// Keep the checked p=/enc: password flow, but remove its SetSetting call.
```

The error-writer line is an **integration point**, not an assertion that the adapter currently has this exact helper. The security-relevant implementation is removal of both plaintext storage and its authentication consumer. For clients that require MD5, introduce a separately generated, narrowly scoped application password; never silently substitute the main password. Store that recoverable app secret encrypted with a separately managed key, expire it, and show the compatibility/security tradeoff in the UI.

Existing backups can still contain deleted credentials. Treat them as sensitive and rotate affected primary passwords; do not silently destroy backup history to conceal this condition.

**Regression:** Authenticate through password and encoded-password Subsonic flows; verify no `subsonic.pw.*` row exists afterward. Verify an old MD5 token no longer authenticates and document the intentional client compatibility change.

<a id="f03"></a>

### F03 — Library file paths are not confined to the library at open time

**Severity:** High, conditional · **Evidence:** Static-confirmed; isolated filesystem reproduction.

**Sources:** [game scanner](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/games.go), [book scanner](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/books.go), [core file serving](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/core/core.go), [edition acquisition lookup](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/reading.go).

The scanners accept extension-matching entries without consistently rejecting symlinks/non-regular files, and serving opens the database path directly. A `.nes` symlink inside a games library can point outside the library. Games do not require successful media probing before registration; opening that recorded path follows the link. An authenticated reader can then obtain the target through an edition download.

**Required condition:** Someone can create or replace entries in a configured library, and the server process can read the target. This is not a claim that an unauthenticated HTTP request can create such a link. Shared download directories, writable media shares, and a root-running container make the boundary important. FIFO/special-file entries also create blocking risks.

**Implementation:** Reject non-regular entries while scanning and use a retained `os.Root` when opening media, rather than a check-then-reopen path sequence. The repository's required Go version supports this API; [Go's traversal-resistant API description](https://go.dev/blog/osroot) explains why lexical checks alone are insufficient.

```go
// Package internal/mediafs. Go 1.24+; imports: errors, os, path/filepath.
type LibraryRoot struct {
    path string
    root *os.Root
}

func OpenLibraryRoot(path string) (*LibraryRoot, error) {
    absolute, err := filepath.Abs(path)
    if err != nil { return nil, err }
    canonical, err := filepath.EvalSymlinks(absolute)
    if err != nil { return nil, err }
    root, err := os.OpenRoot(canonical)
    if err != nil { return nil, err }
    return &LibraryRoot{path: canonical, root: root}, nil
}

func (l *LibraryRoot) Close() error { return l.root.Close() }

func (l *LibraryRoot) OpenRecorded(path string) (*os.File, error) {
    rel, err := filepath.Rel(l.path, path)
    if err != nil || !filepath.IsLocal(rel) {
        return nil, errors.New("media path outside library")
    }
    f, err := l.root.Open(rel)
    if err != nil { return nil, err }
    st, err := f.Stat()
    if err != nil { f.Close(); return nil, err }
    if !st.Mode().IsRegular() {
        f.Close()
        return nil, errors.New("media is not a regular file")
    }
    return f, nil
}
```

Resolve the library by the edition/work relationship, not from a client-supplied root. Use the returned descriptor with `http.ServeContent`; do not call `os.Open` again. Keep the root handle for that library's lifetime. At scan time, reject `d.Type()&os.ModeSymlink != 0` and any `Info().Mode()` that is not regular. Apply the same boundary to sidecars and externally invoked probes; an `EvalSymlinks` check followed by a later path open is not a complete race-resistant fix. Root confinement does not prevent access through deliberately created hard links or privileged mount manipulation; retain least-privilege filesystem permissions.

**Regression:** Escaping symlink, in-root symlink policy, ancestor-directory replacement, FIFO, directory with a media extension, and normal range requests. Tests must verify that the bytes served come from the validated descriptor.

<a id="f04"></a>

### F04 — Concurrent retention passes can remove every backup

**Severity:** High · **Evidence:** Static-confirmed; isolated deletion-schedule reproduction.

**Source:** [`Snapshot` / `pruneBackups`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/backup.go).

`pruneBackups` protects its caller's new snapshot while choosing other files to delete. With `keep=1`, two concurrent processes can each enumerate both new files, protect their own file, and select the other process's file for deletion. Both removes succeed; zero backups remain. A mutex attached to one `DB` instance would not protect separate CLI processes.

**Implementation:** Take a cross-process lock around the entire snapshot/publication/retention operation. The following is suitable for the Linux/macOS targets used by the release Makefile:

```go
// New backup_lock_unix.go in package store.
// Imports: fmt, os, path/filepath, syscall.
func withBackupLock(dir string, fn func() error) error {
    if err := os.MkdirAll(dir, 0o700); err != nil { return err }
    f, err := os.OpenFile(filepath.Join(dir, ".backup.lock"), os.O_CREATE|os.O_RDWR, 0o600)
    if err != nil { return err }
    defer f.Close()
    if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
        return fmt.Errorf("another backup is active: %w", err)
    }
    defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
    return fn()
}
```

Wrap the public snapshot operation with this lock, and move the current body into a private unlocked helper to avoid recursively locking. Keep the lock file; deleting it while another process has it open can create two independent locks. Document that the target filesystem must support the locking semantics; otherwise refuse concurrent backups or use a different lock service.

**Regression:** Run two processes against one backup directory with a barrier immediately before retention enumeration. Assert at least one complete generation remains and that a second simultaneous operation receives an explicit busy error.

<a id="f05"></a>

### F05 — Backup generations share mutable cover files and publish too early

**Severity:** High · **Evidence:** Static-confirmed.

**Sources:** [backup implementation](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/backup.go), [existing backup tests](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/backup_test.go).

The database snapshot is linked into its final name before `copyTree(coversDir, backupDir/covers)` completes. All database generations share the same covers directory, and copying truncates/replaces files there. An interrupted or failed later backup can therefore leave a visible database snapshot with incomplete assets, and can alter assets used by an older backup. A successful SQLite snapshot alone does not make that multi-resource backup self-contained.

**Implementation:** Store a database and its covers under one staging directory and publish the directory only after all writes succeed. Reuse the existing copying logic but add durability checks. This is the central replacement shape; `syncTree` and generation pruning follow immediately below.

```go
// Package store. Additional imports: context, os, path/filepath, strings, time.
func (d *DB) SnapshotGeneration(ctx context.Context, coversDir, backupDir string, keep int) (string, error) {
    var published string
    err := withBackupLock(backupDir, func() error {
        stage, err := os.MkdirTemp(backupDir, ".pending-")
        if err != nil { return err }
        defer os.RemoveAll(stage)
        if _, err := d.ExecContext(ctx, `VACUUM INTO ?`, filepath.Join(stage, "libteca.db")); err != nil {
            return err
        }
        if err := copyTree(coversDir, filepath.Join(stage, "covers")); err != nil { return err }
        if err := os.WriteFile(filepath.Join(stage, "COMPLETE"), []byte("libteca backup v2\n"), 0o600); err != nil {
            return err
        }
        if err := syncTree(stage); err != nil { return err }
        name := "libteca-" + time.Now().UTC().Format("20060102T150405.000000000Z") +
            "-" + strings.TrimPrefix(filepath.Base(stage), ".pending-")
        target := filepath.Join(backupDir, name)
        if err := os.Rename(stage, target); err != nil { return err }
        if err := syncDirectory(backupDir); err != nil { return err }
        published = target
        return pruneGenerations(backupDir, keep, target)
    })
    return published, err
}

func syncDirectory(path string) error {
    f, err := os.Open(path)
    if err != nil { return err }
    defer f.Close()
    return f.Sync()
}

func syncTree(root string) error {
    var dirs []string
    err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
        if walkErr != nil { return walkErr }
        if entry.IsDir() { dirs = append(dirs, path); return nil }
        f, err := os.Open(path)
        if err != nil { return err }
        syncErr := f.Sync()
        closeErr := f.Close()
        if syncErr != nil { return syncErr }
        return closeErr
    })
    if err != nil { return err }
    for i := len(dirs)-1; i >= 0; i-- {
        if err := syncDirectory(dirs[i]); err != nil { return err }
    }
    return nil
}

func pruneGenerations(dir string, keep int, protect string) error {
    if keep < 1 { return nil }
    entries, err := os.ReadDir(dir) // Name-sorted.
    if err != nil { return err }
    var candidates []string
    for _, e := range entries {
        p := filepath.Join(dir, e.Name())
        if !e.IsDir() || !strings.HasPrefix(e.Name(), "libteca-") || p == protect { continue }
        if _, err := os.Stat(filepath.Join(p, "COMPLETE")); err != nil { continue }
        candidates = append(candidates, p)
    }
    for len(candidates) > keep-1 {
        if err := os.RemoveAll(candidates[0]); err != nil { return err }
        candidates = candidates[1:]
    }
    return syncDirectory(dir)
}
```

Update the CLI's restore instructions to the new directory layout. Do not delete legacy flat snapshots automatically. Reject symlinks/special files in `copyTree`; optionally add a checksummed manifest and verify it during restore. **This fixes generation isolation and incomplete publication, but not an instantaneous database-plus-cover snapshot:** that additionally requires immutable content-addressed cover files, or coordination with every cover writer while the database snapshot and asset copy are taken. State that consistency boundary rather than calling a directory rename a cross-resource transaction.

**Regression:** Inject failures before/after each copy and before publication; previous generations must remain unchanged. Restore every retained generation independently. Test retention with clock rollback and with a failed incomplete staging directory.

<a id="f06"></a>

### F06 — The last podcast filename fallback can overwrite another episode

**Severity:** High · **Evidence:** Static-confirmed; isolated POSIX publication reproduction.

**Sources:** [podcast download and filename selection](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/podcast/podcast.go), [path ownership / file linking](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/podcasts.go).

When both the title-based filename and the title-plus-episode-ID filename collide, the code falls back to the numeric episode ID without another ownership check. Existing `Example.mp3`, `Example-4.mp3`, and `4.mp3` therefore provide a concrete collision for episode 4. On POSIX, publishing with `os.Rename` replaces the destination. `InsertPodcastFile` can then reuse the existing path's file row, worsening cross-episode mis-linking. Errors from ownership checks must not be treated as proof that a name is unused.

**Implementation:** Use service-generated names and publish with a no-replace primitive. This helper assumes the staged file and final directory are on the same filesystem; create staging files there accordingly.

```go
// Imports: crypto/rand, encoding/hex, fmt, os, path/filepath, strings.
func publishEpisode(part, dir string, episodeID int64, extension string) (string, error) {
    switch strings.ToLower(extension) {
    case ".mp3", ".m4a", ".m4b", ".aac", ".ogg", ".opus", ".flac", ".wav":
    default:
        extension = ".bin"
    }
    var nonce [12]byte
    if _, err := rand.Read(nonce[:]); err != nil { return "", err }
    dst := filepath.Join(dir, fmt.Sprintf("episode-%d-%s%s", episodeID, hex.EncodeToString(nonce[:]), extension))
    f, err := os.Open(part)
    if err != nil { return "", err }
    err = f.Sync()
    closeErr := f.Close()
    if err != nil { return "", err }
    if closeErr != nil { return "", closeErr }
    if err := os.Link(part, dst); err != nil { return "", err } // Never replaces dst.
    return dst, nil
}
```

Remove the staging link only after handling the returned result. Insert the new file row and link its episode in a **single database transaction**; on failure, delete only this operation's newly published path. Check affected-row counts. A crash between filesystem publication and the transaction still needs an orphan reconciliation job; do not solve collisions by silently adopting someone else's file row.

**Regression:** Pre-create all three old fallback names with distinct contents and database owners. Download the colliding episode, force an ownership-query error, and force a link transaction failure. None of the pre-existing bytes or links may change.

<a id="f07"></a>

### F07 — CBR listing can deadlock after reaching its output cap

**Severity:** High · **Evidence:** Static-confirmed; Go subprocess reproduction.

**Source:** [`cbrList`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/books.go).

`io.ReadAll(io.LimitReader(stdout, 4<<20))` stops draining the child's stdout, then `cmd.Wait()` waits for the child to exit. A listing larger than the cap can fill the pipe and block the child forever. The nearby extraction code kills an oversized child, but the listing path does not. The local check used a child producing 6 MiB; after the parent read 4 MiB, `Wait` remained blocked until the watchdog killed the child.

**Implementation:** Read cap+1, kill on overflow or read error, and call `Wait` exactly once. Also add a timeout so a child that produces no output cannot stall a scan.

```go
// cbrList should receive the scan context. Imports: context, fmt, io, os/exec, time.
func cappedListing(parent context.Context, name string, args ...string) ([]byte, error) {
    ctx, cancel := context.WithTimeout(parent, 30*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, name, args...)
    pipe, err := cmd.StdoutPipe()
    if err != nil { return nil, err }
    if err := cmd.Start(); err != nil { return nil, err }
    const limit = 4 << 20
    data, readErr := io.ReadAll(io.LimitReader(pipe, limit+1))
    oversized := len(data) > limit
    if oversized || readErr != nil { _ = cmd.Process.Kill() }
    waitErr := cmd.Wait()
    if oversized { return nil, fmt.Errorf("archive listing exceeds %d bytes", limit) }
    if readErr != nil { return nil, readErr }
    if ctx.Err() != nil { return nil, ctx.Err() }
    if waitErr != nil { return nil, waitErr }
    return data, nil
}
```

Use this for both `unrar lb` and `lsar`, and pass context through `probeBook`/`probeCBR`/`cbrList`. A child process tree may need process-group cancellation, using the corrected lifecycle discussed in F28.

**Regression:** Exactly limit bytes, limit+1 bytes, an infinite writer, an idle child, cancellation, and a nonzero exit. Every case must reap the child and return within a bounded interval.

<a id="f08"></a>

### F08 — Several media probes and extractors are not cancellable or output-bounded

**Severity:** Medium · **Evidence:** Static-confirmed.

**Sources:** [audio `Probe`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/audio/probe.go), [scanner commands](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/scan.go), [CBR extraction](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/books.go), [hardware probing](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/transcode/transcode.go).

`exec.Command(...).Output()` and uncancelled extraction commands can outlive a canceled scan, block shutdown, or buffer very large metadata output. The scanner's context checks between files cannot interrupt a command already running. The `unar` branch also checks extracted size **after** the process has written its output, so its nominal cover cap is not a disk-space limit during extraction.

**Implementation:** Make context part of the probe interface, keep compatibility wrappers only for callers that explicitly choose a timeout, and use a common bounded runner. This runner drains output rather than deadlocking a pipe; it cancels on overflow and limits retained stderr too.

```go
// Package internal/command. Imports: bytes, context, errors, io, os/exec, time.
var ErrOutputLimit = errors.New("command output limit exceeded")

type boundedOutput struct {
    bytes.Buffer
    limit int
    cancel context.CancelFunc
    overflow bool
}
func (b *boundedOutput) Write(p []byte) (int, error) {
    room := b.limit - b.Len()
    if room < 0 { room = 0 }
    n := len(p)
    if n > room {
        b.overflow = true
        if room > 0 { _, _ = b.Buffer.Write(p[:room]) }
        b.cancel()
        return n, nil // Continue draining until CommandContext stops the child.
    }
    _, _ = b.Buffer.Write(p)
    return n, nil
}

func Output(parent context.Context, timeout time.Duration, maxBytes int, name string, args ...string) ([]byte, error) {
    if maxBytes <= 0 || timeout <= 0 { return nil, errors.New("invalid command budget") }
    ctx, cancel := context.WithTimeout(parent, timeout)
    defer cancel()
    stdout := &boundedOutput{limit: maxBytes, cancel: cancel}
    stderr := &boundedOutput{limit: 64 << 10, cancel: cancel}
    cmd := exec.CommandContext(ctx, name, args...)
    cmd.Stdout, cmd.Stderr, cmd.Stdin = stdout, stderr, nil
    cmd.WaitDelay = 2 * time.Second
    err := cmd.Run() // Run includes exactly one Wait after successful Start.
    if stdout.overflow || stderr.overflow { return nil, ErrOutputLimit }
    if ctx.Err() != nil { return nil, ctx.Err() }
    if err != nil { return nil, err }
    return stdout.Bytes(), nil
}
```

Remove the unused `io` import if copying this file exactly. Call it from `ProbeContext(ctx, path)` with explicit timeout/output budgets; route scanner and podcast callers through that method. For disk-writing extraction, prefer the bounded streaming `unrar p` path. When only `unar` is available, either disable extraction for untrusted archives or run it in a quota-limited sandbox; checking disk usage after it exits is not an enforceable quota.

**Regression:** Inject a fake executable that hangs, emits oversized JSON/stderr, or keeps descendants holding stdout open. Cancellation must release the scan job and all owned processes. Verify normal large chapter lists within the configured budget still work.

<a id="f09"></a>

### F09 — Cover handling can allocate unbounded compressed or decoded image data

**Severity:** High, conditional on hostile/oversized artwork · **Evidence:** Static-confirmed.

**Sources:** [OPDS `coverThumb`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/opds/opds.go), [book cover loading](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/books.go), [game cover loading](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/games.go).

The OPDS thumbnail path calls `os.ReadFile` and then `image.Decode` without a pixel-dimension preflight. A compressed-byte limit elsewhere does not bound decoded dimensions. Some scanner sidecar loaders also read the complete file before checking its size, or have no such check. Multiple concurrent thumbnail requests multiply the allocation. The threat includes artwork obtained from a feed/provider as well as locally supplied covers; the attacker still needs that artwork to reach the relevant path.

**Implementation:** Bound bytes during reading, validate dimensions before decoding, and limit simultaneous decoders. Use this before scaling:

```go
// Imports: bytes, errors, image, io, os. Keep existing JPEG/PNG/GIF registrations.
func readSmallFile(path string, limit int64) ([]byte, error) {
    f, err := os.Open(path)
    if err != nil { return nil, err }
    defer f.Close()
    st, err := f.Stat()
    if err != nil { return nil, err }
    if !st.Mode().IsRegular() { return nil, errors.New("not a regular file") }
    b, err := io.ReadAll(io.LimitReader(f, limit+1))
    if err != nil { return nil, err }
    if int64(len(b)) > limit { return nil, errors.New("image byte limit exceeded") }
    return b, nil
}

func decodeCover(data []byte) (image.Image, error) {
    cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
    if err != nil { return nil, err }
    const maxPixels = 16_000_000
    if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 16384 || cfg.Height > 16384 ||
        cfg.Width > maxPixels/cfg.Height {
        return nil, errors.New("image dimensions exceed budget")
    }
    img, _, err := image.Decode(bytes.NewReader(data))
    return img, err
}
```

Acquire a shared two-slot decoder semaphore before calling `decodeCover`, and return a controlled 503/busy response when capacity is exhausted. Use bounded reads in every sidecar loader, not only OPDS. Respect F03's confined-open requirement when adapting `readSmallFile` to library-owned paths. Re-encode validated cover bytes to the actual stored format rather than merely giving arbitrary bytes a `.jpg` suffix.

**Regression:** A tiny file with huge declared dimensions, oversized sidecar, malformed image, portrait and landscape images near the limit, and concurrent requests. Reject the huge-dimension case using `DecodeConfig`; do not deliberately allocate the oversized image in the test process.

<a id="f10"></a>

### F10 — Trickplay generation bypasses the transcode concurrency limit

**Severity:** High · **Evidence:** Static-confirmed · **Exposure:** Authenticated requests.

**Sources:** [trickplay generator](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/trickplay/trickplay.go), [Jellyfin trickplay routes](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/jellyfin/jellyfin.go).

Trickplay deduplicates by `(itemID, width)` but has no global worker cap. Different requested widths or items spawn independent full-video FFmpeg jobs. Widths up to 3840 are accepted and tiled 10×10, making very large intermediate sheets possible. `transcode.MaxSessions` does not protect these processes. Core and Jellyfin instantiate separate generators, so a per-instance budget would still allow combined oversubscription.

**Implementation:** Share a process-wide budget (or one injected service), constrain supported widths and pixel budgets, add a deadline, and bound stderr through F08.

```go
// In package trickplay; shared by all Generator instances.
var generationSlots = make(chan struct{}, 2)
var ErrBusy = errors.New("trickplay capacity exhausted")

func supportedWidth(width int) bool {
    return width == 160 || width == 320
}

// At the beginning of generate, after checking the completed cache:
if !supportedWidth(width) { return ErrBadWidth }
select {
case generationSlots <- struct{}{}:
    defer func() { <-generationSlots }()
default:
    return ErrBusy
}
ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
defer cancel()
```

Map `ErrBusy` to 503 with `Retry-After`, and unsupported widths to 400; update clients to request an advertised supported width. Also cap the computed sheet pixel count after reading source dimensions, particularly for extreme aspect ratios. The two-minute budget is an example policy, not a guarantee all long videos finish that quickly; make it configurable and surface timeout failures. F44 addresses completion-safe cache publication; H05 addresses disk budgets.

**Regression:** Request many distinct widths/items through both API faces concurrently. Observe at most two generation processes, bounded output, explicit rejections, and recovery after canceled jobs.

<a id="f11"></a>

### F11 — Server-side HLS resume loses the absolute playback timeline

**Severity:** High (progress correctness) · **Evidence:** Static-confirmed; real FFmpeg fixture.

**Sources:** [video player boot/save/seek](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/players/video.tsx), [core HLS handler](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/core/hls.go), [FFmpeg argument builder](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/transcode/hwaccel.go).

The player adds `start=floor(savedPosition)` to the HLS URL. FFmpeg seeks into the source, but output time starts near zero. The browser then saves `video.currentTime` as if it were the absolute edition position and uses the same unadjusted value for UI, chapters, and seeking. Resuming at 600 seconds and playing another 15 seconds can consequently save a position near 15 rather than 615. Seeking before the truncated stream's start is also not implemented correctly.

**Executed check:** A 12-second synthetic source transcoded with `-ss 6` and the relevant repository HLS arguments yielded a six-second playlist whose first timestamp was **0.058667**, not six seconds. This establishes the timestamp-reset mechanism; the browser's exact MSE normalization was not tested end-to-end.

**Implementation, correctness-first patch:** Remove the server-side start optimization for the web player so the HLS resource represents the full edition. Set resume in the player against that full timeline. Do not combine client-side absolute seek with a truncated source.

```ts
// In video.tsx, when constructing the HLS URL:
const hlsURL = info.url; // Do not append &start=<savedPosition>.
const resume = Number.isFinite(ed.position) && !ed.isFinished
  ? Math.max(0, ed.position || 0) : 0;

// In the existing dynamic hls.js branch:
const hls = new Hls({ startPosition: resume });
hls.loadSource(hlsURL);
hls.attachMedia(video);

// For direct/native playback, record the actual selected mode in a ref.
// On loadedmetadata/canplay, retry this until the target is seekable:
function restoreWhenSeekable(video: HTMLVideoElement, target: number): boolean {
  for (let i = 0; i < video.seekable.length; i++) {
    if (target >= video.seekable.start(i) && target <= video.seekable.end(i)) {
      video.currentTime = target;
      return true;
    }
  }
  return target === 0;
}
```

Adapt the local variable names to the existing `boot` function; the patch deliberately changes the timeline contract, not just the displayed clock. Keep edition duration as the authoritative total; a growing HLS playlist's currently available duration is not the whole edition.

**Tradeoff:** Encoding from the beginning can make a far-ahead resume slow. A performant follow-up must return an explicit absolute source origin and media timestamp origin with each immutable HLS session and use `absolute = sourceOrigin + mediaTime - mediaOrigin` for *every* save, chapter, subtitle, and seek operation. Seeking outside that session's range must create a fresh session. A small reusable implementation is:

```ts
export function absoluteTime(media: number, sourceOrigin: number, mediaOrigin: number): number {
  if (![media, sourceOrigin, mediaOrigin].every(Number.isFinite)) throw new Error("Invalid timeline");
  return Math.max(0, sourceOrigin + media - mediaOrigin);
}
```

Do not ship that optimized variant until the origins are actually measured/returned by the server and browser tests cover native HLS as well as hls.js. F29 fixes the separate session-expiry/recreation problem.

**Regression:** Resume at 600, play 15, save approximately 615; seek backward across the resume origin; pause/reopen; switch episodes; complete a resumed video; and test both native HLS and hls.js with the same absolute timeline assertions.

<a id="f12"></a>

### F12 — Signing out does not revoke the browser's server token

**Severity:** Medium · **Evidence:** Static-confirmed.

**Sources:** [application sign-out handler](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/app.tsx), [token API](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/core/users.go), [token lookup](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/auth/auth.go).

The UI deletes local storage and reloads, but leaves the server token valid. Tokens have no expiry, so an old copied token or previously generated token-bearing media URL remains usable after sign-out. Clearing a browser variable is not server-side logout.

**Implementation:** Add a current-token logout route on the authenticated core group. Do not ask the browser to discover a token ID first.

```go
// In package core; mount with r.HandleFunc("POST /logout", a.logout).
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
    _, err := a.DB.Exec(`UPDATE tokens SET revoked_at = ?
        WHERE user_id = ? AND value = ? AND revoked_at IS NULL`,
        time.Now().UnixMilli(), auth.UserID(r), auth.Token(r))
    if err != nil {
        writeJSON(w, 500, map[string]string{"error": "logout could not be completed"})
        return
    }
    w.Header().Set("Cache-Control", "no-store")
    w.WriteHeader(http.StatusNoContent)
}
```

```ts
// Use apiChecked from F33, not a call that treats an error object as success.
async function signOut(): Promise<void> {
  await apiChecked('/logout', { method: 'POST' });
  setToken('');
  try { localStorage.removeItem('libteca-token'); } catch {}
  location.reload();
}
```

Display an error when online revocation fails; a separate explicitly labeled “clear this device while offline” action may still clear storage, but must not claim revocation. F14 binds ABS capabilities to the revoked token; H01 addresses token expiry and scope.

**Regression:** Retain the raw token, sign out, then retry a core request and a new media request with it. Both must fail. Another explicitly issued device token should remain usable unless “sign out all devices” was selected.

<a id="f13"></a>

### F13 — A stale self-service password change can overwrite a newer reset

**Severity:** Medium · **Evidence:** Static-confirmed; SQLite schedule reproduction.

**Sources:** [password-change handler](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/core/users.go), [`RotatePassword`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/users.go).

The handler verifies the current password before the store transaction, but `RotatePassword` updates by user ID only. A paused request that verified the old hash can therefore resume after an administrator reset or another successful change and replace that newer password. Deleting the Subsonic secret outside the rotation transaction also permits partially completed credential cleanup.

**Implementation:** Compare against the hash that was actually verified, atomically rotate/revoke, and distinguish explicit administrative resets. Close old ABS sessions and delete the legacy secret within the same transaction.

```go
// In package store. Additional imports: errors, strconv.
var ErrCredentialsChanged = errors.New("credentials changed; authenticate again")

func (d *DB) RotatePasswordChecked(id int64, expected *string, replacement string) error {
    return d.Update(func(tx *Tx) error {
        now := nowMilli()
        res, err := tx.Exec(`UPDATE users SET password_hash = ?, updated_at = ?
            WHERE id = ? AND (? IS NULL OR password_hash = ?)`,
            replacement, now, id, expected, expected)
        if err != nil { return err }
        n, err := res.RowsAffected()
        if err != nil { return err }
        if n != 1 { return ErrCredentialsChanged }
        if _, err := tx.Exec(`UPDATE tokens SET revoked_at = ?
            WHERE user_id = ? AND revoked_at IS NULL`, now, id); err != nil { return err }
        if _, err := tx.Exec(`UPDATE playback_sessions SET closed_at = ?
            WHERE user_id = ? AND closed_at IS NULL`, now, id); err != nil { return err }
        _, err = tx.Exec(`DELETE FROM settings WHERE key = ?`, "subsonic.pw."+strconv.FormatInt(id, 10))
        return err
    })
}
```

For a self-service change, pass `&user.PasswordHash` from the successful verification. Only the separately authorized administrative-reset branch passes `nil`. Map `ErrCredentialsChanged` to 409 and require reauthentication. Use the same secret cleanup in both user-deletion variants; generic `settings` rows have no user foreign key and otherwise outlive deleted users.

**Regression:** Barrier a self-change after verification, complete an administrator reset, then release the old request. The old request must fail, not overwrite the reset. Inject failure in token/secret/session cleanup and assert the password change rolls back too.

<a id="f14"></a>

### F14 — ABS playback URLs remain valid after token revocation and have no expiry

**Severity:** Medium · **Evidence:** Static-confirmed.

**Sources:** [unprotected session-track route](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/server/server.go), [ABS `SessionTrack` / `play`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/abs/abs.go), [session storage](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/progress.go).

`/s/{sid}/t/{index}` is intentionally a bearer-capability URL: it has no normal auth middleware. Its session ID is random, so this is **not** an ID-guessing claim. The defect is lifecycle: the route checks only that the session exists and is not closed. It is not tied to the issuing token and has no expiry. Revoking that token or rotating the password does not by itself invalidate an unclosed URL in the current implementation.

**Implementation:** Keep capability-based playback for clients that cannot send headers, but bind each session to its issuing token and expiry. Fail closed for old sessions with no binding.

```sql
-- New migration; existing sessions must be recreated by clients.
ALTER TABLE playback_sessions ADD COLUMN parent_token_id INTEGER REFERENCES tokens(id);
ALTER TABLE playback_sessions ADD COLUMN expires_at INTEGER;
UPDATE playback_sessions SET closed_at = COALESCE(closed_at, updated_at)
WHERE parent_token_id IS NULL;
```

```go
// Store-side creation should be INSERT ... SELECT, not lookup then unguarded INSERT.
res, err := tx.Exec(`INSERT INTO playback_sessions
    (id, user_id, edition_id, started_at, updated_at, position_secs,
     time_listened_secs, device_info, parent_token_id, expires_at)
    SELECT ?, t.user_id, ?, ?, ?, ?, ?, ?, t.id, ? FROM tokens t
    WHERE t.value = ? AND t.user_id = ? AND t.revoked_at IS NULL`,
    s.ID, s.EditionID, s.StartedAt, s.UpdatedAt, s.PositionSecs,
    s.TimeListened, s.DeviceInfo, nowMilli()+int64((12*time.Hour)/time.Millisecond),
    parentToken, s.UserID)
if err != nil { return err }
if n, err := res.RowsAffected(); err != nil { return err } else if n != 1 { return ErrNotFound }
```

Before serving a track, query the session through this predicate:

```sql
SELECT s.id FROM playback_sessions s
JOIN tokens t ON t.id = s.parent_token_id
WHERE s.id = ? AND s.closed_at IS NULL
  AND s.expires_at > ? AND t.revoked_at IS NULL;
```

Use the same store method from every session reader rather than relying on one handler's check. A twelve-hour absolute lifetime is an example policy; active clients should obtain a replacement session explicitly, not extend an old leaked capability forever. Do not promise revocation of bytes already sent or of a request already streaming; enforcing immediate termination of existing streams requires tracked cancellation as well.

**Regression:** Revoke the parent token, rotate the password, expire a session, and try a legacy unbound session. New track requests must fail. An unrelated user's valid session must not be affected.

<a id="f15"></a>

### F15 — Principal-only login limiting can be bypassed by changing usernames

**Severity:** Medium · **Evidence:** Static-confirmed.

**Sources:** [limiter](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/auth/limiter.go), [Subsonic authentication](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/subsonic/subsonic.go), [core login](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/core/core.go), [OPDS authentication](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/opds/opds.go).

Most callers key the limiter by `IP|username`. Changing the username creates a fresh bucket. In Subsonic, the unknown-user dummy verification occurs before the limiter path and does not register a failure. The global two-slot KDF semaphore is valuable, but it bounds simultaneous work rather than preventing a sender from repeatedly occupying it and making legitimate logins receive 429.

**Implementation:** Keep the principal bucket and add a separate per-IP aggregate bucket; ensure unknown users pass through the same checks and failure accounting before any KDF call. Do not clear the aggregate failure history after a single successful login.

```go
// Shared flow, adapted to each protocol's response envelope.
ipKey := "ip|" + auth.ClientIP(r)
userKey := "principal|" + auth.ClientIP(r) + "|" + strings.ToLower(strings.TrimSpace(username))
for _, key := range []string{ipKey, userKey} {
    if ok, retry := limiter.Allow(key); !ok {
        auth.WriteRetryAfter(w, retry)
        http.Error(w, "too many attempts", http.StatusTooManyRequests)
        return
    }
}
// Look up the user and perform real or dummy verification here.
// On bad credentials, including an unknown username:
limiter.Failure(ipKey)
limiter.Failure(userKey)
// On success, call Success(userKey), not Success(ipKey).
```

Factor the checks and result accounting into an authentication service rather than copying branches that later diverge. Configure aggregate limits separately from per-principal limits to avoid locking out a whole household/proxy too aggressively. A request-admission budget before uncached KDF work is also appropriate for repeated valid-password abuse; keep OPDS's inexpensive validated-proof cache fast path. Preserve the current refusal to trust arbitrary `X-Forwarded-For`; reverse-proxy support needs an explicit trusted-proxy configuration.

**Regression:** Hundreds of distinct nonexistent usernames from one IP must hit an aggregate limit without indefinitely starving a second IP. Include Subsonic's unknown-user branch and successful requests that must not reset the aggregate counter.

<a id="f16"></a>

### F16 — Authenticated OPDS assets are explicitly marked publicly cacheable

**Severity:** Medium, conditional on a shared cache · **Evidence:** Static-confirmed.

**Source:** [OPDS covers, thumbnails, and CBZ page responses](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/opds/opds.go).

These Basic-auth-protected URLs return `Cache-Control: public, max-age=86400`. The `public` directive explicitly permits shared-cache reuse of an authenticated response; see [RFC 9111, authenticated responses and public caching](https://httpwg.org/specs/rfc9111.html). A caching reverse proxy can consequently reuse a fetched cover/page without another origin authorization check. This is not an origin middleware bypass in a deployment with no intermediary cache.

**Implementation:** For user-private media, prefer no shared caching and, where logout privacy matters, no storage at all:

```go
// Set before writing protected cover/page/thumbnail responses.
w.Header().Set("Cache-Control", "private, no-store")
w.Header().Set("Referrer-Policy", "no-referrer")
```

Apply a consistent policy across protocol adapters. Browser-only caching may use `private, max-age=...` if retaining media after logout is an accepted product decision. Purge existing intermediary caches during rollout; changing headers does not recall responses already cached elsewhere. Do not rely on `Vary: Authorization` as a substitute for a deliberate private-media policy.

**Regression:** Put the service behind a test shared cache, fetch an asset with valid credentials, then request that URL anonymously and with another principal. The cache must not supply the authenticated body.

<a id="f17"></a>

### F17 — Display titles and directory grouping collapse distinct media identities

**Severity:** High (library integrity) · **Evidence:** Static-confirmed.

**Sources:** [video/music grouping](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/video.go), [book edition creation](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/books.go), [`UpsertEdition` / `UpsertFile`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/queries.go), [edition acquisition](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/reading.go).

Several distinct failures share the same underlying identity problem. Root-level movies are grouped under the library's directory identity and can become files of one work/edition; playback selects the first file. Books with the same title/author/format reuse an edition despite the scanner comment promising one edition per file. Music tracks with the same display title can similarly reuse one edition. Conversely, a filename/tag-derived title change can create a new edition and reparent the file while progress remains keyed to the previous edition. Preserving the file ID alone does not preserve edition-scoped progress.

**Implementation:** Separate stable scanner identity from mutable display metadata. Add explicit source keys and use them for new scanner upserts; for existing paths, first preserve their current work/edition relationship. A migration foundation is:

```sql
ALTER TABLE works ADD COLUMN source_key TEXT;
ALTER TABLE editions ADD COLUMN source_key TEXT;
CREATE UNIQUE INDEX idx_work_source ON works(library_id, source_key)
WHERE source_key IS NOT NULL;
CREATE UNIQUE INDEX idx_edition_source ON editions(work_id, source_key)
WHERE source_key IS NOT NULL;
```

Use root-relative paths for independent files, album/book directory plus variant for grouped editions, and `(series, season, episode, variant)` for TV. Do not use the title as a fallback *identity* when a stable key is available. Correct root-level movie grouping directly:

```go
func movieSourceKey(root, path string) (string, error) {
    rel, err := filepath.Rel(root, path)
    if err != nil || !filepath.IsLocal(rel) { return "", fmt.Errorf("invalid movie path") }
    return "movie:" + filepath.ToSlash(rel), nil
}
```

Inside the store transaction, preserve existing membership before considering new metadata:

```sql
SELECT f.edition_id, e.work_id
FROM files f JOIN editions e ON e.id = f.edition_id
JOIN works w ON w.id = e.work_id
WHERE f.path = ? AND w.library_id = ?;
```

For a genuinely new independent edition, insert a new `editions` row instead of calling the title-matching `UpsertEdition`; record `source_key` in that insert. The existing schema does not impose an edition-title unique index. Work titles **do** have the `idx_works_match` unique index: a complete source-identity migration must replace that constraint with a non-unique search index before representing two genuinely distinct same-title works. Do not drop it without updating all import/provider/upsert callers together.

Existing collapsed records need a **reviewable repair migration**, not a blind split: users may intentionally group editions, and assigning historical progress to one of several files requires evidence. A move detected by a verified content match should update the source key without changing the edition ID. Do not automatically copy one old progress row to every split edition.

**Regression:** Two root-level movies; two same-title EPUB editions; two “Intro” tracks; an edited title; a moved file; alternate encodings; and a deliberate multi-file audiobook. Distinct identities stay distinct, while intended groups and progress survive rescans.

<a id="f18"></a>

### F18 — Audiobook disc directories are interleaved by basename sorting

**Severity:** Medium · **Evidence:** Static-confirmed.

**Source:** [audiobook grouping and sort](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/scan.go).

Tracks are sorted by basename alone. `CD1/01.mp3`, `CD1/02.mp3`, `CD2/01.mp3`, and `CD2/02.mp3` can therefore be ordered by track number across discs instead of disc then track. Files with equal basenames also lack a deterministic directory tie-break. This changes the concatenated timeline, chapters, and resume mapping.

**Implementation:** Sort by natural **root-relative path**, or explicitly by parsed disc and track numbers with the path as a stable tie-break. The relative-path version is the minimal correction:

```go
sort.Slice(files, func(i, j int) bool {
    a, errA := filepath.Rel(bookDir, files[i].path)
    b, errB := filepath.Rel(bookDir, files[j].path)
    if errA != nil || errB != nil { return files[i].path < files[j].path }
    a, b = filepath.ToSlash(a), filepath.ToSlash(b)
    if natLess(a, b) { return true }
    if natLess(b, a) { return false }
    return a < b
})
```

Use the actual scanner slice's field names and retain one shared comparator; F35/F42 address other inconsistent ordering implementations. Recompute file sequence and derived chapter offsets as one coherent update, and map existing progress via stable file ID plus file offset rather than retaining an obsolete absolute offset blindly.

**Regression:** Multiple discs, leading zeros, repeated basenames, disc 2 versus disc 10, and a rescan with unchanged media. Assert exact file order and edition timeline.

<a id="f19"></a>

### F19 — Alternate audiobook encodings are concatenated and M4A is mislabeled

**Severity:** Medium · **Evidence:** Static-confirmed.

**Source:** [audio extension collection and edition-format selection](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/scan.go).

The group format becomes `m4b` if any member is M4B, otherwise `mp3`, even for an M4A-only group. A directory containing a complete M4B and the same book split into MP3 tracks is treated as one timeline instead of alternate editions. Duration and chapter lists can effectively contain the book twice.

**Implementation:** Partition source groups into explicit variants before building an edition. Preserve a compatible logical format separately from the file's actual codec/container; the existing format constraint cannot simply be given a new arbitrary value without a migration.

```go
func audioVariant(path string) (key, logicalFormat string, err error) {
    switch strings.ToLower(filepath.Ext(path)) {
    case ".m4b":
        return "m4b-single:" + filepath.Base(path), "m4b", nil
    case ".m4a":
        return "m4a-tracks", "m4b", nil
    case ".mp3":
        return "mp3-tracks", "mp3", nil
    default:
        return "", "", fmt.Errorf("unsupported audiobook extension")
    }
}
```

This is a safe **default partition**, not an inference engine: some books legitimately have several sequential M4B volumes, and some track sets mix encodings. Support an explicit sidecar/group manifest for those cases. Store the variant key using F17, retain probed container/MIME information, and do not merge two variants solely because the display title matches.

**Regression:** M4A-only book; MP3 plus a complete M4B alternative; two independent M4B editions; and an explicitly configured multi-volume book. Playback must include exactly the selected edition, not all alternatives.

<a id="f20"></a>

### F20 — A warm music rescan gives new tracks the wrong album position

**Severity:** Medium · **Evidence:** Static-confirmed; isolated ordinal reproduction.

**Sources:** [music scan loop](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/video.go), [edition update columns](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/queries.go).

The scanner excludes unchanged tracks before assigning positions. When tracks 1 and 2 already exist and track 3 is added, the new track is index zero of the changed subset and receives position 1. `UpsertEdition` also does not update position for an existing edition, so later correct ordering cannot reliably repair this.

**Implementation:** Determine the ordinal from the complete ordered track list before deciding whether to probe, and update position even when media bytes are unchanged.

```go
// Replace enumeration over only probedTracks with enumeration over all tracks.
for ordinal, track := range orderedTracks {
    position := ordinal + 1
    // Reuse stored probe data when the file is unchanged, but retain this ordinal.
    // In the same per-album transaction, after resolving its stable edition ID:
    if _, err := tx.Exec(`UPDATE editions SET position = ? WHERE id = ?`, position, editionID); err != nil {
        return err
    }
}
```

The new `orderedTracks` flow must resolve every track's edition via F17; the snippet is the position write within that loop, not a standalone function. Preserve metadata on unchanged files instead of forcing all tracks through a costly probe merely to renumber them.

**Regression:** Add track 3 after a warm scan, insert a new track before track 2, reorder via tags, and rescan unchanged data. Positions must match the complete album order and remain stable.

<a id="f21"></a>

### F21 — Updating a game file moves it to the end of its edition

**Severity:** Medium · **Evidence:** Static-confirmed; SQLite reproduction.

**Source:** [`storeGame`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/games.go).

The scanner computes `max(seq)+1` even when `UpsertFile` is updating an existing path. Changing the first of two ROM/disc files therefore changes its sequence from 1 to 3. That can change the default selected file or multi-disc order.

**Implementation:** Preserve the existing sequence on a same-edition update; assign a new ordinal only for a genuinely new file. For full natural-order rescans, use an explicit complete ordering pass rather than repeated append semantics.

```go
var seq int
err := tx.QueryRow(`SELECT seq FROM files WHERE path = ? AND edition_id = ?`, d.path, editionID).Scan(&seq)
if errors.Is(err, sql.ErrNoRows) {
    err = tx.QueryRow(`SELECT coalesce(max(seq), 0) + 1 FROM files WHERE edition_id = ?`, editionID).Scan(&seq)
}
if err != nil { return err }
// Use seq in the FileRec passed to UpsertFile.
```

**Regression:** Modify size/mtime of the first file, then rescan twice. Sequence and selected default must remain unchanged. Also test a genuinely new disc and an explicit full-order repair.

<a id="f22"></a>

### F22 — Failed directory walks and failed probes can be reported as successful scans

**Severity:** High when combined with missing-file reconciliation · **Evidence:** Static-confirmed.

**Sources:** [audio scanner](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/scan.go), [video/music scanner](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/video.go), [book scanner](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/books.go), [game scanner](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/games.go), [watcher reconciliation](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/watch/watch.go).

Several `WalkDir` callbacks return `nil` for filesystem errors, including an unavailable root. Some per-file probe/storage errors are logged and skipped while the enclosing scan returns success. The watcher interprets a `done` job as permission to reconcile missing files. An unavailable library mount can therefore be reported as a clean empty scan and have its file rows marked missing. This does not delete the original media bytes, but it hides valid library entries and makes scan status unreliable.

**Implementation:** Fail before scanning an unavailable root; propagate traversal errors or return an explicit partial result. Never authorize global missing-file reconciliation from a partial/failed enumeration.

```go
func validateScanRoot(root string) error {
    st, err := os.Stat(root)
    if err != nil { return fmt.Errorf("library root unavailable: %w", err) }
    if !st.IsDir() { return fmt.Errorf("library root is not a directory") }
    return nil
}

// In every WalkDir callback, before using d:
if err != nil { return fmt.Errorf("scan %s: %w", p, err) }
if err := ctx.Err(); err != nil { return err }

// For per-file failures that should not stop discovery of the remaining files:
var failures []error
// ... append fmt.Errorf("probe %s: %w", path, probeErr) rather than only logging.
// At the end of the scan:
return count, errors.Join(failures...)
```

Use the pinned root handle from F03 to avoid evaluating a different filesystem between enumeration and serving/reconciliation. Distinguish `complete`, `partial`, `failed`, and `canceled` in job state; report counts and affected paths without exposing secrets. A probe failure can be partial metadata failure even when enumeration succeeded, but the distinction must be explicit and tested rather than inferred from `nil`.

**Regression:** Missing/unmounted root, unreadable subdirectory, disappearing file, corrupt media, canceled scan, and a normal empty library. Only the last case is an unqualified successful empty scan.

<a id="f23"></a>

### F23 — Manual and CLI rescans do not consistently reconcile deleted files

**Severity:** Medium · **Evidence:** Static-confirmed.

**Sources:** [watcher's `reconcileMissing`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/watch/watch.go), [core scan jobs](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/core/core.go), [CLI scan path](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/cmd/libteca/main.go).

Removal reconciliation lives in the watcher after `triggerAndWait`, not in the shared scanner. With watching disabled, a successful manual/CLI rescan does not necessarily mark deleted rows missing. Users receive different library state depending on which entry point initiated the same scan.

**Implementation:** Move reconciliation to a shared, post-success scan operation and remove the duplicate watcher-only call. Collect missing candidates, check `rows.Err`, close the result set, and then apply updates transactionally. Preserve the conservative error handling from F22/F25.

```go
// A shared store helper may receive the root-confined existence check.
func (d *DB) ReconcileLibraryFiles(ctx context.Context, libraryID int64,
    exists func(string) (bool, error)) error {
    rows, err := d.QueryContext(ctx, `SELECT f.id, f.path FROM files f
        JOIN editions e ON e.id=f.edition_id JOIN works w ON w.id=e.work_id
        WHERE w.library_id=? AND f.missing=0`, libraryID)
    if err != nil { return err }
    var missing []int64
    for rows.Next() {
        var id int64
        var path string
        if err := rows.Scan(&id, &path); err != nil { rows.Close(); return err }
        present, err := exists(path)
        if err != nil { rows.Close(); return err }
        if !present { missing = append(missing, id) }
    }
    scanErr := rows.Err()
    closeErr := rows.Close()
    if scanErr != nil { return scanErr }
    if closeErr != nil { return closeErr }
    if err := ctx.Err(); err != nil { return err }
    return d.Update(func(tx *Tx) error {
        for _, id := range missing {
            if _, err := tx.Exec(`UPDATE files SET missing=1 WHERE id=? AND missing=0`, id); err != nil { return err }
        }
        return nil
    })
}
```

The supplied `exists` callback must return false only for a genuine not-found result under the retained library root. Invoke this after a complete enumeration in the common scan entry point used by all callers. For stronger crash/race semantics, use a scan generation and “seen in generation” records rather than unrelated per-path updates.

**Regression:** With the watcher disabled, delete a file and rescan through CLI and HTTP separately. Both must hide the missing file while preserving its recoverable metadata/progress, and must restore it when it returns.

<a id="f24"></a>

### F24 — Partial-file hashes are used as though they established file identity

**Severity:** Medium · **Evidence:** Static-confirmed; deterministic collision-input reproduction.

**Sources:** [`hashFile`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/scan.go), [relinking by hash](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/queries.go).

The hash samples the first and last 64 KiB rather than the whole file. Two equal-length files with identical ends and different middle contents feed the same sampled bytes into the hash; no cryptographic attack is needed. Relinking can then attach the wrong file identity and associated state. Read/seek errors are also ignored in the signature helper. The local reproduction used two 192 KiB files whose middle thirds differed: sampled inputs matched while complete SHA-256 hashes differed.

**Implementation:** Treat the sample as a candidate filter only. Before relinking, verify a full, error-checked content hash. Version the stored hash scheme so legacy sample hashes are never compared as equivalent to full hashes.

```go
// Imports: context, crypto/sha256, encoding/hex, io, os.
// f must already be opened through the media boundary from F03.
func fullFileHash(ctx context.Context, f *os.File) (string, error) {
    if _, err := f.Seek(0, io.SeekStart); err != nil { return "", err }
    h := sha256.New()
    buf := make([]byte, 128<<10)
    for {
        if err := ctx.Err(); err != nil { return "", err }
        n, err := f.Read(buf)
        if n > 0 { _, _ = h.Write(buf[:n]) }
        if err == io.EOF { break }
        if err != nil { return "", err }
    }
    return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
```

Compute full hashes outside the SQLite write transaction, verify the file's size/version did not change while reading, and then perform the short conditional relink transaction. Do not make routine warm scans hash every large file unnecessarily; verify candidates when identity would otherwise change. Legacy hashes can be refreshed lazily, but must not authorize a destructive relink on their own.

**Regression:** Identical ends/different middle, equal full copies, a read error, a file modified during hashing, and an old unversioned hash. Only a stable full-content match may be used as identity evidence.

<a id="f25"></a>

### F25 — Relinking treats non-not-found stat failures as disappearance

**Severity:** Medium · **Evidence:** Static-confirmed.

**Source:** [file relinking](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/queries.go).

The old path can be considered eligible for relinking when `os.Stat` fails for reasons other than nonexistence. Permission errors and transient filesystem I/O failures are not proof that a file moved. Candidate matching is also not sufficiently scoped to the destination library, so equal signatures can reparent state across independent libraries.

**Implementation:** Check actual existence even when a stale `missing` flag is set, accept only `fs.ErrNotExist` as disappearance, and constrain candidates by library.

```go
_, statErr := os.Stat(oldPath) // Replace with the confined-root stat in F03.
if statErr == nil {
    continue // The old file still exists; this is a duplicate, not a move.
}
if !errors.Is(statErr, fs.ErrNotExist) {
    return false, fmt.Errorf("cannot verify old file %s: %w", oldPath, statErr)
}
// Only now consider a verified full-hash candidate for relinking.
```

```sql
SELECT f.id, f.path FROM files f
JOIN editions e ON e.id=f.edition_id JOIN works w ON w.id=e.work_id
WHERE f.hash=? AND f.size_bytes=? AND w.library_id=?;
```

Derive the library ID from the destination edition inside the transaction; do not accept it as an arbitrary client selector. If several eligible identical files remain, report ambiguity instead of selecting one arbitrarily. Cross-library moves should be an explicit operation with progress/ownership semantics, not a side effect of a hash lookup.

**Regression:** EACCES, EIO, a stale missing flag with an existing file, identical files in two libraries, and a genuine same-library move. Only the last case should relink automatically.

<a id="f26"></a>

### F26 — Duplicate or overlapping library roots conflict with global file-path identity

**Severity:** Medium · **Evidence:** Static-confirmed.

**Sources:** [library creation](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/core/core.go), [global unique file paths](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/migrations/0001_init.sql), [watch root ownership map](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/watch/watch.go).

The API accepts overlapping roots, while files are globally unique by path and the watcher maps each directory to one library ID. A second library can skip “unchanged” files already owned by the first, or a later scan can reparent them. The UI does not have a sound many-library-membership model behind this configuration.

**Implementation, minimal supported policy:** Reject duplicate/ancestor/descendant roots transactionally when adding a library. Normalize absolute paths and resolve root symlinks before comparison.

```go
func pathContains(parent, child string) bool {
    rel, err := filepath.Rel(parent, child)
    return err == nil && filepath.IsLocal(rel)
}

func rootsOverlap(a, b string) bool {
    return pathContains(a, b) || pathContains(b, a)
}
```

Perform the library-list check and insert under one store transaction so two simultaneous requests cannot both pass. Use `os.SameFile` as an additional duplicate-root check on case-insensitive filesystems. Existing overlaps need an administrative diagnostic and a repair choice; do not silently delete a library or its state. Supporting intentional overlap instead requires library-scoped membership tables and watcher fan-out to all owning libraries.

**Regression:** Same path, trailing separator, root symlink alias, ancestor/descendant roots, distinct siblings, and concurrent creation attempts.

<a id="f27"></a>

### F27 — Scan invalidation misses same-second changes and sidecar-only edits

**Severity:** Medium · **Evidence:** Static-confirmed.

**Sources:** [file stat lookup](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/reading.go), [scan cache decisions](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/scan.go), [video/music scan](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/video.go), [books scan](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/books.go).

A file is treated as unchanged by size and integer-second mtime. A same-size rewrite within that granularity can be missed. NFO/cover/other sidecar changes do not invalidate that media-file comparison, and some unchanged paths skip cover or metadata repair entirely.

**Implementation:** Store a higher-resolution version and a separate sidecar fingerprint. A schema extension and comparison are:

```sql
ALTER TABLE files ADD COLUMN mtime_ns INTEGER NOT NULL DEFAULT 0;
ALTER TABLE files ADD COLUMN sidecar_hash TEXT NOT NULL DEFAULT '';
```

```go
type ScanVersion struct {
    Size int64
    MtimeNS int64
    Sidecars string
}
func sameVersion(old, current ScanVersion) bool {
    return old.MtimeNS != 0 && old.Size == current.Size &&
        old.MtimeNS == current.MtimeNS && old.Sidecars == current.Sidecars
}
```

Populate `MtimeNS` with `fi.ModTime().UnixNano()` and hash the bounded, ordered set of relevant sidecars. An empty legacy version forces one refresh. Separate media probing from metadata/cover reconciliation so a changed sidecar need not invoke FFprobe again. Nanosecond storage cannot manufacture precision on a coarse filesystem; retain a full-content verification mode or periodic verification for that case.

**Regression:** Same-size same-second rewrite, sidecar title change, new cover beside unchanged media, deleted sidecar, and a legacy database row. Each must trigger the relevant work without unnecessarily reprobeing all unchanged media.

<a id="f28"></a>

### F28 — A killed transcode launch can skip Wait and leave cleanup racing the child

**Severity:** Medium · **Evidence:** Static-confirmed; Go fake-process reproduction.

**Source:** [`Session.launch`, `kill`, hardware fallback](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/transcode/transcode.go).

After a successful `p.start`, `launch` checks `s.killed` and can return without starting the waiter or closing `done`. The race is reachable when session teardown overlaps software fallback startup. Separately, `kill` removes the output directory without waiting for process exit. A child may still be writing, or the newly launched process may never be reaped. The local model confirmed that the killed branch calls `kill` but zero `wait` calls.

**Implementation:** Serialize launch/teardown lifecycle, start a waiter after every successful start, and remove output only after exit. Add `lifecycle sync.Mutex` to `Session`, then replace the two methods:

```go
func (s *Session) launch(startSecs float64, accel string) error {
    s.lifecycle.Lock()
    defer s.lifecycle.Unlock()
    s.mu.Lock()
    closed := s.killed
    s.mu.Unlock()
    if closed { return ErrClosed }
    p := s.spawn(buildArgs(accel, s.Source, s.Dir, startSecs, DefaultVideoBitrate))
    if err := p.start(); err != nil { return err }
    done := make(chan struct{})
    s.mu.Lock()
    s.proc, s.done, s.exitErr = p, done, nil
    s.mu.Unlock()
    go func() {
        err := p.wait()
        s.mu.Lock()
        if s.done == done { s.exitErr = err }
        s.mu.Unlock()
        close(done)
    }()
    return nil
}

func (s *Session) kill() {
    s.lifecycle.Lock()
    defer s.lifecycle.Unlock()
    s.mu.Lock()
    s.killed = true
    p, done := s.proc, s.done
    s.mu.Unlock()
    if p != nil && done != nil {
        select {
        case <-done:
        default:
            p.kill()
        }
        select {
        case <-done:
        case <-time.After(2*time.Second):
            slog.Warn("transcode exit pending; preserving output directory", "session", s.ID)
            return
        }
    }
    if err := os.RemoveAll(s.Dir); err != nil {
        slog.Warn("transcode cleanup failed", "session", s.ID, "err", err)
    }
}
```

The timeout protects shutdown from a process stuck in kernel I/O; preserve its directory and schedule explicit orphan cleanup rather than claiming it exited. For the directly spawned FFmpeg process, use `cmd.Process.Kill()` instead of deriving a process group from a possibly reaped/reused numeric PID. If process-tree termination is required, retain a process identity/lifecycle-aware supervisor; `Getpgid(oldPID)` after `Wait` is not a reliable identity check.

**Regression:** Block the fake process in `start`, overlap close/fallback, and release the barrier. Every successful start has exactly one wait; no fallback starts after close; output is removed only after exit. Run these tests under `-race` after integration.

<a id="f29"></a>

### F29 — An expired HLS session can be silently recreated with a different origin

**Severity:** Medium · **Evidence:** Static-confirmed.

**Sources:** [core HLS ticket/segment handling](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/core/hls.go), [manager idle expiry and Get](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/transcode/transcode.go).

Tickets live longer than the 90-second idle transcode lifetime. Segment requests also call the creating `Manager.Get`. After a long pause/reap, a segment request can recreate the session at default start zero because its URL lacks the original start parameter, while reusing the old segment namespace. This can serve a different timeline under URLs the player believes identify the previous stream. `Get` also reuses an existing same-edition session without comparing start/source parameters.

**Implementation:** Separate lookup from creation. Segments must never create an encoder. Return 410 for an expired session and have the client bootstrap a fresh session from its saved **absolute** position.

```go
func (m *Manager) Existing(id string, editionID int64) (*Session, bool) {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.closed { return nil, false }
    s, ok := m.sessions[id]
    if !ok || s.Edition != editionID { return nil, false }
    s.Touch()
    return s, true
}

// In core hlsSegment, after ticket/user validation:
s, ok := a.TC.Existing(sessionID, editionID)
if !ok {
    w.Header().Set("Cache-Control", "no-store")
    http.Error(w, "playback session expired", http.StatusGone)
    return
}
```

Use the handler's ticket fields for `sessionID`/`editionID`, not client-supplied ownership data. Store the origin and source version on the ticket/session at creation; a master request with mismatched parameters must create a new generation or fail explicitly. Do not rewrite an old segment namespace in place. F31's error handling must recognize the expiry and trigger a bounded fresh bootstrap.

**Regression:** Pause longer than idle expiry, request an old segment, and assert no new FFmpeg process starts. Resume must obtain a new ID and continue at the saved absolute time. Test seeking with a reused session ID and a different start value.

<a id="f30"></a>

### F30 — Direct MP4 resume is disabled on browsers that support native HLS

**Severity:** Medium · **Evidence:** Static-confirmed; JavaScript condition reproduction.

**Source:** [video `onLoadedMetadata`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/players/video.tsx).

The resume condition checks whether the browser can play HLS rather than whether the current resource is HLS. On a native-HLS-capable browser, that condition also blocks restoring the position of a direct MP4. Browser capability and the selected playback mode are different facts.

**Implementation:** Record the mode selected by the playback response, and restore a direct resource regardless of the browser's unrelated HLS capability.

```ts
const playbackMode = useRef<'direct' | 'hls'>('direct');
// In boot, after validating info:
playbackMode.current = info.mode;

// In the loadedmetadata handler, replacing the capability-based guard:
if (playbackMode.current === 'direct' && ed.position && !ed.isFinished) {
  const last = Number.isFinite(v.duration) ? Math.max(0, v.duration - 0.1) : ed.position;
  v.currentTime = Math.min(ed.position, last);
}
```

For HLS, apply the timeline-aware resume path in F11, not this direct-file branch. Reset the mode and resume state when the edition changes.

**Regression:** Stub native HLS capability to a nonempty string while loading direct MP4. Resume must still run. Repeat in WebKit/Safari during integration testing, including a finished item that should intentionally start over.

<a id="f31"></a>

### F31 — HLS.js failures can leave a blank player with no actionable error

**Severity:** Medium · **Evidence:** Static-confirmed.

**Source:** [video boot, hls.js setup, and error rendering](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/players/video.tsx).

The hls.js branch attaches MediaSource without setting the `src` state used by error/loading UI. There is no `Hls.Events.ERROR` listener, and `void boot()` does not catch a rejected dynamic import. Fatal HLS/network/demux errors can therefore bypass the visible native-media error path. Session expiry from F29 is also not handled as a fresh-bootstrap event.

**Implementation:** Catch bootstrap failures, attach a bounded HLS error handler, and make loading/error UI independent of whether a literal `src` was assigned.

```ts
let networkRetries = 0;
let mediaRetries = 0;
hls.on(Hls.Events.ERROR, (_event, data) => {
  if (!alive || !data.fatal) return;
  if (data.type === Hls.ErrorTypes.NETWORK_ERROR && networkRetries++ < 1) {
    hls.startLoad();
    return;
  }
  if (data.type === Hls.ErrorTypes.MEDIA_ERROR && mediaRetries++ < 1) {
    hls.recoverMediaError();
    return;
  }
  hls.destroy();
  if (hlsRef.current === hls) hlsRef.current = null;
  setWaiting(false);
  setFatal('Playback failed. Reload playback to create a new session.');
});

void boot().catch((error: unknown) => {
  if (!alive) return;
  setWaiting(false);
  setFatal(error instanceof Error ? error.message : 'Playback could not start');
});
```

Add an explicit retry action that starts a **new** playback session, using the last absolute position. An expired-session response must take that path, not repeatedly retry old segment URLs. Do not allow unlimited `recoverMediaError` loops. Remove `src` as a prerequisite for showing `waiting`/`fatal` in the hls.js path. Confirm the event/response handling against the hls.js version in the lockfile during integration.

**Regression:** Failed dynamic import, manifest failure, segment 410, fatal decode error, exhausted retry, and component unmount during failure. The player must end in either working playback or a visible recoverable error, not a silent blank state.

<a id="f32"></a>

### F32 — Rewinding audio suppresses periodic progress saves

**Severity:** Medium · **Evidence:** Static-confirmed; isolated time-sequence reproduction.

**Source:** [audio time-update handler](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/players/audio.tsx).

Periodic persistence is gated by `position - lastSent > 10`. After rewinding from 3600 to 300 seconds, periodic saves stop until playback passes the old position again. Pause/unmount handlers still save, so normal testing can conceal the defect; a crash or abrupt tab loss during the intervening period loses the newer position.

**Implementation:** Use elapsed wall-clock time for the save cadence, and save after completed seeks. Do not use monotonic forward playback position as a clock.

```ts
const lastSentAt = useRef(0);
function maybeSave(position: number, force = false): void {
  if (!Number.isFinite(position) || position < 0) return;
  const now = performance.now();
  if (!force && now - lastSentAt.current < 10_000) return;
  lastSentAt.current = now;
  props.onPos?.(position);
}
// Call maybeSave(absolutePosition) from timeupdate;
// call maybeSave(absolutePosition, true) from seeked and pause.
```

Reset cadence state when switching the playback queue/edition. F33 handles delivery failures; an attempted request is not itself proof of persistence.

**Regression:** Play to 3600, seek to 300, continue for 30 seconds, and verify several saves near the new position. Test forward seeks, track transitions, pause immediately after seek, and playback-rate changes.

<a id="f33"></a>

### F33 — HTTP errors can be shown as “saved,” and failed progress is discarded

**Severity:** Medium · **Evidence:** Static-confirmed; JavaScript error-object reproduction.

**Sources:** [API helper return contract](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/api.ts), [reader progress saver](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/reader/shared.tsx), [video save/deduplication](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/players/video.tsx).

Except for 401/network failure, `api` returns an error object instead of throwing. The reader awaits it and then sets “saved” even for HTTP 500. Its queue is cleared before sending and is not retained for a retry. Video also advances its deduplication watermark before a save is acknowledged, so a failed write can suppress a same-position retry. Independent in-flight requests can finish out of order.

**Implementation:** Preserve the existing `api` contract for callers that inspect error objects, but introduce a checked wrapper for mutations whose completion means success. Use one serialized latest-value queue per edition.

```ts
// Add to api.ts.
export class APIError extends Error {
  constructor(message: string, readonly status: number) { super(message); }
}
export async function apiChecked<T = unknown>(path: string, opts: RequestInit = {}): Promise<T> {
  const value = await api(path, opts);
  if (value && typeof value === 'object' && typeof value.error === 'string') {
    throw new APIError(value.error, typeof value.status === 'number' ? value.status : 0);
  }
  return value as T;
}
```

```ts
// New pure helper; a queue instance belongs to one immutable edition identity.
export function progressQueue<T>(
  send: (value: T) => Promise<unknown>,
  state: (value: 'saving' | 'saved' | 'error') => void,
) {
  let pending: { value: T; version: number } | undefined;
  let active: typeof pending;
  let version = 0;
  let running = false;
  let disposed = false;
  const delay = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms));

  async function run(): Promise<void> {
    if (running || disposed) return;
    running = true;
    try {
      while (pending && !disposed) {
        const job = pending;
        pending = undefined;
        active = job;
        state('saving');
        let delivered = false;
        for (let attempt = 0; attempt < 3 && !disposed; attempt++) {
          try {
            await send(job.value);
            delivered = true;
            break;
          } catch (error) {
            if (pending && pending.version > job.version) break;
            if (error instanceof APIError && error.status >= 400 && error.status < 500 && error.status !== 429) break;
            if (attempt < 2) await delay(250 * 2 ** attempt);
          }
        }
        active = undefined;
        if (disposed) return;
        if (!delivered) {
          if (pending && pending.version > job.version) continue;
          pending = job; // Retain failed state for retry/flush, rather than dropping it.
          state('error');
          return;
        }
        if (!pending) state('saved');
      }
    } finally {
      running = false;
    }
  }
  return {
    enqueue(value: T) { pending = { value, version: ++version }; void run(); },
    retry() { void run(); },
    pendingValue(): T | undefined { return pending?.value ?? active?.value; },
    dispose() { disposed = true; },
  };
}
```

Wire `send` to `apiChecked` with the edition ID captured when the queue is created, not a ref that might later identify another edition. Only advance video deduplication state after acknowledgment. Before disposing the queue, its latest unacknowledged value can be used for the existing best-effort beacon. A beacon's acceptance does not prove server persistence; a visible retry indicator is still needed. This serializes one client's writes, not conflicts between multiple devices; cross-device conflict resolution needs a separate versioning policy.

**Regression:** 500/429/400 responses, offline mode, invalid JSON response, a newer progress event during a slow request, unmount with an in-flight request, retry at the identical position, and switching editions. Never display “saved” for a rejected request or deliver edition A's payload to edition B.

<a id="f34"></a>

### F34 — Closing the PDF reader can undo “Mark finished”

**Severity:** Medium · **Evidence:** Static-confirmed; SQLite/default-value reproduction.

**Sources:** [PDF reader save/cleanup](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/reader/pdf.tsx), [core progress decoding](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/core/core.go), [reading upsert](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/reading.go).

`Mark finished` sends `finished:true`, but the PDF page/unmount update sends only page/percent. The core decoder's plain `bool` turns omitted `finished` into false, and the upsert overwrites `is_finished`. Even opening a previously finished PDF with a restored page can mark it dirty and then clear completion on close.

**Implementation:** Give progress updates patch semantics: omission preserves completion, while an explicit false reopens an item. This must be atomic in the upsert, not a read-old-state-then-write race.

```go
// Change the core request field:
Finished *bool `json:"finished"`

// While constructing the existing Progress value:
if body.Finished != nil { p.IsFinished = *body.Finished }
// Pass body.Finished != nil to the new patch-aware store method.
```

In a patch-aware copy of the existing reading upsert, replace its completion assignment with:

```sql
is_finished = CASE WHEN ? THEN excluded.is_finished ELSE progress.is_finished END
```

Append `finishedProvided` as the corresponding final argument. Keep the existing value for inserts (`false` when omitted). Preserve the old `SetReadingProgress` method as a wrapper that passes `true`, so callers that intentionally send a complete `Progress` record retain their contract; the core JSON PATCH-style path uses the new method. This change is limited to the current query's completion assignment and argument list, not a new invented schema.

Also make the PDF reader dirty only after an actual user edit; restoring a page from the server is not an edit. Completion should not be lost even if an unload event arrives after the “finished” request.

**Regression:** Mark finished and close; reopen a finished PDF and close without edits; send page only; send explicit `finished:false`; race a page update with a completion update. Only the explicit false should reopen the item.

<a id="f35"></a>

### F35 — CBZ page ordering and supported extensions differ between clients

**Severity:** Medium · **Evidence:** Static-confirmed; Node comparison reproduction.

**Sources:** [browser page enumeration](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/reader/shared.tsx), [OPDS CBZ enumeration](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/opds/cbz.go), [scan-time CBZ counting](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/books.go).

The browser uses a case-insensitive numeric `Intl.Collator`; Go uses a case-sensitive bytewise natural comparison. For `a2.jpg` and `A10.jpg`, the browser puts `a2` first and Go puts `A10` first. The browser also accepts an extension not accepted by the scanner/OPDS list (`.jxl`). Page numbers and saved progress can identify different images depending on the client.

**Implementation:** Define one versioned ordering/extension contract. Ideally return an ordered page manifest from the server and have every client use it. When local ZIP enumeration must remain, use the same byte-based comparator in Go and TypeScript, not locale-dependent collation. Move this Go implementation to `internal/natural` and use it in scan, OPDS, and import:

```go
package natural

import "strings"

func digit(c byte) bool { return c >= '0' && c <= '9' }
func Less(a, b string) bool {
    i, j := 0, 0
    for i < len(a) && j < len(b) {
        if digit(a[i]) && digit(b[j]) {
            ei, ej := i, j
            for ei < len(a) && digit(a[ei]) { ei++ }
            for ej < len(b) && digit(b[ej]) { ej++ }
            x, y := strings.TrimLeft(a[i:ei], "0"), strings.TrimLeft(b[j:ej], "0")
            if len(x) != len(y) { return len(x) < len(y) }
            if x != y { return x < y }
            i, j = ei, ej
            continue
        }
        if a[i] != b[j] { return a[i] < b[j] }
        i++; j++
    }
    if len(a)-i != len(b)-j { return len(a)-i < len(b)-j }
    return a < b // Deterministic tie-break for numerically equivalent names.
}
```

Matching TypeScript:

```ts
const encoder = new TextEncoder();
export function comparePages(left: string, right: string): number {
  const a = encoder.encode(left), b = encoder.encode(right);
  const digit = (n: number) => n >= 48 && n <= 57;
  let i = 0, j = 0;
  while (i < a.length && j < b.length) {
    if (digit(a[i]) && digit(b[j])) {
      let ei = i, ej = j;
      while (ei < a.length && digit(a[ei])) ei++;
      while (ej < b.length && digit(b[ej])) ej++;
      let si = i, sj = j;
      while (si < ei && a[si] === 48) si++;
      while (sj < ej && b[sj] === 48) sj++;
      if (ei-si !== ej-sj) return (ei-si)-(ej-sj);
      for (let k = 0; k < ei-si; k++) {
        if (a[si+k] !== b[sj+k]) return a[si+k]-b[sj+k];
      }
      i = ei; j = ej;
    } else {
      if (a[i] !== b[j]) return a[i]-b[j];
      i++; j++;
    }
  }
  if (a.length-i !== b.length-j) return (a.length-i)-(b.length-j);
  for (let k = 0; k < Math.min(a.length, b.length); k++) {
    if (a[k] !== b[k]) return a[k]-b[k];
  }
  return a.length-b.length;
}
```

Use the same extension set (`jpg/jpeg/png/gif/webp/bmp/avif`) until a new format is supported consistently. Define handling of duplicate/noncanonical ZIP names and reject ambiguous entries. Store a page filename/manifest version as a locator when possible, so changing the ordering implementation does not silently reinterpret historical progress.

**Regression:** Mixed case, 2/10, leading zeros, nested paths, Unicode, duplicate names, and each accepted extension. Assert identical ordered names and page counts in all three implementations.

<a id="f36"></a>

### F36 — Unguarded localStorage access can prevent application startup

**Severity:** Low · **Evidence:** Static-confirmed.

**Sources:** [app initialization](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/app.tsx), [audio rate initialization](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/players/audio.tsx), [already guarded API cleanup](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/api.ts).

Some storage accesses are protected, but boot-time token reading and audio rate initialization are not consistently guarded. Storage can throw in restricted contexts. Invalid saved rate values can also reach `playbackRate` without normalization. A settings/storage failure should not leave the application unable to become ready.

**Implementation:** Centralize safe access and validate typed preferences:

```ts
export function readPreference(key: string, fallback = ''): string {
  try { return localStorage.getItem(key) ?? fallback; } catch { return fallback; }
}
export function writePreference(key: string, value: string): boolean {
  try { localStorage.setItem(key, value); return true; } catch { return false; }
}
export function readPlaybackRate(key: string): number {
  const value = Number(readPreference(key, '1'));
  return Number.isFinite(value) && value >= 0.25 && value <= 3 ? value : 1;
}
```

Use the application's existing preference keys. Keep an in-memory token when persistence is unavailable and notify the user that it will not survive a reload; do not make persistence failure equivalent to authentication failure.

**Regression:** Throwing `getItem`/`setItem`, empty storage, `NaN`, infinity, negative rate, and an out-of-range value. The login screen/player must still render and use a valid rate.

<a id="f37"></a>

### F37 — ABS session close commits before final progress, making retries ineffective

**Severity:** Medium · **Evidence:** Static-confirmed; SQLite retry-state reproduction.

**Sources:** [ABS `sessionClose`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/abs/abs.go), [session/progress persistence](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/progress.go).

The handler closes the session before saving final progress. If the latter write fails, it returns an error, but retry sees the session already closed and skips the final update. A normal retry cannot repair the lost final position. Related sync operations should also avoid separately committed session/progress updates.

**Implementation:** Make close and final progress one idempotent transaction. This uses the existing progress columns and leaves reading-specific columns untouched:

```go
func (d *DB) CloseSessionWithProgress(sessionID string, userID int64, p *Progress) error {
    return d.Update(func(tx *Tx) error {
        var owner, edition int64
        var closed sql.NullInt64
        err := tx.QueryRow(`SELECT user_id, edition_id, closed_at FROM playback_sessions WHERE id=?`, sessionID).
            Scan(&owner, &edition, &closed)
        if errors.Is(err, sql.ErrNoRows) { return ErrNotFound }
        if err != nil { return err }
        if owner != userID || p.UserID != owner || p.EditionID != edition { return ErrNotFound }
        if closed.Valid { return nil }
        now := nowMilli()
        _, err = tx.Exec(`INSERT INTO progress
          (user_id, edition_id, file_id, file_offset_secs, edition_position_secs,
           duration_secs, is_finished, device, updated_at) VALUES (?,?,?,?,?,?,?,?,?)
          ON CONFLICT(user_id, edition_id) DO UPDATE SET
           file_id=excluded.file_id, file_offset_secs=excluded.file_offset_secs,
           edition_position_secs=excluded.edition_position_secs,
           duration_secs=excluded.duration_secs, is_finished=excluded.is_finished,
           device=excluded.device, updated_at=excluded.updated_at`,
           p.UserID, p.EditionID, p.FileID, p.FileOffsetSecs, p.EditionPositionSecs,
           p.DurationSecs, p.IsFinished, p.Device, now)
        if err != nil { return err }
        _, err = tx.Exec(`UPDATE playback_sessions SET closed_at=?, updated_at=?, position_secs=?
            WHERE id=? AND closed_at IS NULL`, now, now, p.EditionPositionSecs, sessionID)
        return err
    })
}
```

Validate the payload and derive the edition from the owned session before calling this helper. Extract the shared progress SQL into a transaction-capable helper during integration so it does not drift from `SetProgress`. Only return success after commit. For incremental listening-time updates, include them in the same transaction and define request-id/idempotency semantics rather than blindly adding the same delta after a network retry.

**Regression:** Force the progress write or close write to fail, retry, and assert final position is saved exactly once. Concurrent close requests must not double-count listening time or partially commit.

<a id="f38"></a>

### F38 — ABS track offsets truncate fractional durations and chapter IDs repeat

**Severity:** Medium · **Evidence:** Static-confirmed; duration arithmetic reproduction.

**Source:** [ABS playback response construction](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/abs/abs.go).

Playback track offsets accumulate `int(f.DurationSecs)`, truncating each track separately. With 99 preceding tracks of 60.9 seconds, track 100 is advertised 89.1 seconds early. Another response path uses floating-point accumulation, so the API can disagree with itself. Chapter IDs restart from 1 for each file, producing duplicate IDs in a multi-file book.

**Implementation:** Retain floating-point seconds throughout the cumulative timeline and assign chapter IDs once across the complete response:

```go
cum := 0.0
nextChapterID := int64(1)
for _, f := range ed.Files {
    // Existing track map:
    track["startOffset"] = cum
    // Existing chapter loop:
    for _, chapter := range fileChapters {
        chapterMap["id"] = nextChapterID
        chapterMap["start"] = cum + chapter.Start
        chapterMap["end"] = cum + chapter.End
        nextChapterID++
        // Append chapterMap to the response's shared chapter list.
    }
    cum += f.DurationSecs
}
```

Apply these changes to the existing map-building loops; do not create an extra detached `track` map. Round only at a field boundary whose protocol actually requires integer units. Use stable composite IDs instead if the client needs chapter identity across reordering.

**Regression:** Many fractional-duration files, mixed whole/fractional durations, and multiple files with embedded chapters. Track offsets must agree with the player's full timeline and chapter IDs must be unique.

<a id="f39"></a>

### F39 — Jellyfin's season-zero filter also means “no filter”

**Severity:** Medium · **Evidence:** Static-confirmed; filter reproduction.

**Source:** [Jellyfin episodes/season filtering](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/jellyfin/jellyfin.go).

The integer zero sentinel means “no season filter,” but zero is also the valid specials season. Applying the filter only when `seasonFilter > 0` makes a specials request include ordinary seasons as well.

**Implementation:** Represent absence separately from the parsed season number:

```go
func seasonMatches(requested *int, actual int) bool {
    return requested == nil || *requested == actual
}
```

Change the local filter variable to `*int`. After the existing SeasonId parser successfully produces a season number, retain a pointer to that number, including zero. Use `seasonMatches` in the episode loop and return 400 for malformed season identifiers instead of silently widening the query.

**Regression:** No season filter, specials (0), season 1, malformed ID, and a valid empty season. Specials must return only season zero.

<a id="f40"></a>

### F40 — Jellyfin session stop can write two JSON documents into one response

**Severity:** Medium · **Evidence:** Static-confirmed; JSON parse reproduction.

**Source:** [`sessionStopped` / `saveFromSession`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/jellyfin/jellyfin.go).

`saveFromSession` already writes the response. Ownership-rejection branches in `sessionStopped` then call `write` again, producing concatenated documents such as `{}{"ok":true}`. That is not valid JSON even when the HTTP status is 200.

**Implementation, minimal replacement:** Remove the second response writes while preserving the existing ownership checks:

```go
func (a *API) sessionStopped(w http.ResponseWriter, r *http.Request) {
    sid := a.saveFromSession(w, r) // This currently writes the only response.
    if q := qget(r, "PlaySessionId"); q != "" {
        if !ownsPlaySession(r, q) { return }
        sid = q
    }
    if sid != "" && !ownsPlaySession(r, sid) { return }
    if sid != "" { sid = bindPlaySession(r, sid) }
    if a.TC != nil && sid != "" { a.TC.Close(sid) }
}
```

A cleaner follow-up makes `saveFromSession` return a result/error without writing, then writes once at the outer handler after all decisions. Keep response ownership in one layer.

**Regression:** Query-only and body-only foreign session IDs, owned session, malformed body, and a database failure. Assert one JSON document, one status decision, and no teardown of another user's session.

<a id="f41"></a>

### F41 — Compatibility progress endpoints lack core-equivalent numeric validation

**Severity:** Medium · **Evidence:** Static-confirmed.

**Sources:** [ABS progress/sync/close](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/abs/abs.go), [Jellyfin progress conversion](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/jellyfin/jellyfin.go), [core validation for comparison](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/core/core.go).

Compatibility handlers can accept negative/extreme values or derive non-finite values by multiplication even when the JSON number itself was finite. Core validation does not protect independently mounted adapters. Silent parse failures and success responses on unresolved items further conceal lost or invalid progress.

**Implementation:** Validate percentages before multiplication, then validate derived positions against the server-known duration. Share the validation helper across protocol faces.

```go
func normalizedPosition(position, total float64) (float64, error) {
    if math.IsNaN(position) || math.IsInf(position, 0) || position < 0 {
        return 0, fmt.Errorf("invalid position")
    }
    if math.IsNaN(total) || math.IsInf(total, 0) || total < 0 {
        return 0, fmt.Errorf("invalid duration")
    }
    if total > 0 {
        if position > total+5 { return 0, fmt.Errorf("position exceeds duration") }
        return math.Min(position, total), nil // Small end-of-track rounding tolerance.
    }
    if position > 30*24*60*60 { return 0, fmt.Errorf("unknown-duration position exceeds policy limit") }
    return position, nil
}
```

For a fractional `progress` field, reject values outside `[0,1]` and non-finite values **before** multiplying by duration. Treat the thirty-day unknown-duration bound as a configurable policy, not a format specification. Validate listening deltas independently. Return explicit 400/404/500 protocol errors and never continue after malformed JSON (F01).

**Regression:** Negative values, finite-but-overflowing percentage input, positions past the duration, zero/unknown duration, negative Jellyfin ticks, malformed JSON, and a valid rounded end position.

<a id="f42"></a>

### F42 — The importer’s natural comparator orders 10 before 2

**Severity:** Medium · **Evidence:** Static-confirmed; comparator reproduction.

**Source:** [`natLess` / `natNum` / `audioPathsIn`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/importer/importer.go).

The importer converts digit runs to strings and compares them lexically without comparing their lengths first. Thus `10.mp3` sorts before `2.mp3`. Imported file sequence and absolute-to-file progress mapping can differ from a later normal scan.

**Implementation:** Delete the importer-specific comparator and use the shared `internal/natural.Less` implementation in F35:

```go
import "github.com/libteca/libteca/internal/natural"

// In audioPathsIn:
sort.Slice(names, func(i, j int) bool { return natural.Less(names[i], names[j]) })
```

Use the same ordering contract for importer and scanner, including disc directories (F18). Repair existing misordered imports using stable file IDs and offsets, not by retaining an absolute position against a different sequence.

**Regression:** 1/2/10, 02/2, disc 2/disc 10, a long digit run that would overflow an integer parser, and imported progress near a file boundary.

<a id="f43"></a>

### F43 — The foreign-database URI is built from an unescaped filesystem path

**Severity:** Low · **Evidence:** Static-confirmed.

**Source:** [`openForeign`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/importer/importer.go).

The code concatenates `"file:" + path + "?mode=ro..."`. Characters such as `?` and `#` in a valid filename become URI delimiters, so the importer can open the wrong path or interpret filename text as connection options. This is a path/URI-boundary defect; no claim is made that the importer otherwise executes client-supplied SQL.

**Implementation:** Accept a filesystem path, canonicalize it to an absolute path, and let `net/url` encode the URI. Keep read-only mode explicit.

```go
func foreignDSN(path string) (string, error) {
    absolute, err := filepath.Abs(path)
    if err != nil { return "", err }
    u := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
    q := url.Values{}
    q.Set("mode", "ro")
    q.Add("_pragma", "busy_timeout(5000)")
    u.RawQuery = q.Encode()
    return u.String(), nil
}
// Use the returned DSN in sql.Open("sqlite", dsn); preserve Ping/error cleanup.
```

**Regression:** Paths containing spaces, `?`, `#`, `%`, and Unicode. Assert the intended file is opened and a write through the foreign connection fails.

<a id="f44"></a>

### F44 — Derived assets can be served before completion and remain stale after source changes

**Severity:** Medium · **Evidence:** Static-confirmed.

**Sources:** [trickplay cache](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/trickplay/trickplay.go), [OPDS thumbnail cache](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/opds/opds.go), [cover writes](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/scan/books.go).

Trickplay treats the existence of `0.jpg` as completion, although FFmpeg may still be generating later sheets, or may have crashed after creating the first one. `Tile` also bypasses the in-flight generation wait as soon as its path exists. Thumbnail/trickplay keys do not consistently include the source version, and direct file writes expose partially written cache files. Replacing media or artwork can therefore retain stale derived content indefinitely.

**Implementation:** Use versioned cache keys and publish only complete generations. For single-file assets, atomically replace the destination after a complete write:

```go
func writeAssetAtomically(dst string, data []byte) error {
    dir := filepath.Dir(dst)
    if err := os.MkdirAll(dir, 0o700); err != nil { return err }
    f, err := os.CreateTemp(dir, ".asset-")
    if err != nil { return err }
    tmp := f.Name()
    defer os.Remove(tmp)
    if _, err = f.Write(data); err != nil { f.Close(); return err }
    if err = f.Sync(); err != nil { f.Close(); return err }
    if err = f.Close(); err != nil { return err }
    if err = os.Rename(tmp, dst); err != nil { return err }
    return syncDirectory(dir) // Shared helper from F05, placed in an internal fs utility package.
}
```

For trickplay, render into a hidden unique directory, wait for FFmpeg success, validate the output/manifest, write a completion marker, and rename the whole directory to a key derived from `(source content version, width, generation profile)`. Cache reads must require that marker, not merely `0.jpg`. Use a shared per-key single-flight service across API instances; handle another publisher winning by validating its completed generation rather than deleting it. The backup staging pattern in F05 supplies the publication mechanics.

Do not mutate an immutable-version URL's bytes. For stable URLs without an embedded version, use private revalidation/ETags or no-store (F16), so the browser cannot keep an old result after server-side invalidation.

**Regression:** Abort after the first sheet, crash before the completion marker, request a tile while generation is running, change source content with the same edition ID, replace a cover, and run two generator instances for one key. No reader should see a partially completed generation.

<a id="f45"></a>

### F45 — Parallel release builds can embed stale or missing web assets

**Severity:** Medium · **Evidence:** Static-confirmed; GNU Make dependency reproduction.

**Source:** [release Makefile](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/Makefile).

`release` lists `web` and the platform releases as sibling prerequisites. With `make -j`, siblings run concurrently: a Go embed build can start before the web build/copy finishes. Merely listing `web` first does not establish dependency order. The local miniature graph reproduced a stale artifact; adding the direct dependency fixed it.

**Implementation:** Make every platform build depend on `web` itself:

```make
release-%: web
	# Keep the existing per-platform build/archive recipe here unchanged.
```

This is the prerequisite change on the existing pattern rule, not an additional empty rule that replaces its recipe. Keep checksums dependent on all archives. Avoid marking expanded pattern targets phony in a way that disables their implicit pattern recipes. Use `npm ci` in the web recipe as discussed in H03.

**Regression:** Start from a clean tree, run `make -j8 release`, and inspect each binary's embedded assets for a build marker from the same commit. Test a second incremental build after changing the UI.

<a id="f46"></a>

### F46 — Unknown-length podcast downloads receive an unnecessarily short total deadline

**Severity:** Medium · **Evidence:** Static-confirmed.

**Source:** [podcast download timeout calculation](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/podcast/download.go).

A response with unknown Content-Length follows the minimum timeout, approximately 90 seconds, even if it is a legitimate chunked download making steady progress. The size-derived allowance used for known-length responses is unavailable, so otherwise healthy longer downloads can repeatedly fail.

**Implementation:** Distinguish an unknown total size from an idle connection. Retain the existing two-GiB byte limit, give unknown-length downloads the configured maximum overall deadline, and apply an idle-read timeout separately.

```go
func downloadDeadline(contentLength int64) time.Duration {
    const floor = 90 * time.Second
    const ceiling = 2 * time.Hour
    if contentLength < 0 { return ceiling }
    const minBytesPerSecond int64 = 32 << 10
    seconds := contentLength / minBytesPerSecond
    if contentLength%minBytesPerSecond != 0 { seconds++ }
    if seconds >= int64(ceiling/time.Second) { return ceiling }
    d := time.Duration(seconds) * time.Second
    if d < floor { return floor }
    return d
}
```

The deadline must cover body consumption, not only receipt of headers. Ensure an earlier `http.Client.Timeout` does not still cancel the body after 90 seconds. Use a response-body idle watchdog that closes a stalled body, with a configurable interval, or a dedicated transport implementation with equivalent per-request semantics; do not set a shared HTTP/2 connection deadline that disrupts unrelated streams. An overall maximum remains necessary even if a hostile server sends occasional bytes.

**Regression:** A chunked download lasting longer than 90 seconds with steady data, a stalled body, a declared oversize body, a streaming body that exceeds the actual byte limit, and cancellation during transfer. Use injected clocks/timeouts rather than making every test wait minutes.

<a id="f47"></a>

### F47 — Administrative user creation applies inconsistent validation and error mapping

**Severity:** Low · **Evidence:** Static-confirmed.

**Sources:** [core user creation/password changes](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/core/users.go), [initial-admin validation](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/auth/auth.go).

The initialization path trims and bounds usernames, while administrative creation accepts a much weaker set of names. Excessive password length is rejected by hashing but can be reported as “server busy”/429 instead of a client validation error. This yields inconsistent account behavior and misleading retry advice.

**Implementation:** Apply one validator before database/KDF work; do not trim passwords.

```go
func validateCredentials(name, password string) (string, error) {
    name = strings.TrimSpace(name)
    if name == "" || len(name) > 128 || !utf8.ValidString(name) {
        return "", fmt.Errorf("username must be valid UTF-8 and 1-128 bytes")
    }
    for _, r := range name {
        if unicode.IsControl(r) { return "", fmt.Errorf("username contains a control character") }
    }
    if len(password) < 8 || len(password) > 1024 {
        return "", fmt.Errorf("password must be 8-1024 bytes")
    }
    return name, nil
}
```

Reuse the same username policy in initialization, API creation, and imported-user creation, with an explicit migration/report for already existing incompatible names rather than silently renaming accounts. Map validation errors to 400, actual KDF capacity errors to 429, and unexpected internal errors to 500.

**Regression:** Whitespace-only name, leading/trailing whitespace, control characters, long multibyte names, too-short/too-long password, and a genuine capacity rejection. Check case-insensitive duplicate handling remains correct.

<a id="f48"></a>

### F48 — Watcher overflow is logged without guaranteeing reconciliation

**Severity:** Medium · **Evidence:** Static-confirmed.

**Source:** [watch error handling and `watchTree`](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/watch/watch.go).

A filesystem event overflow means changes were lost. Logging it does not recover those changes; without a later sweep they can remain invisible. In addition, returning early because a root is already watched prevents revisiting child directories whose watch installation previously failed.

**Implementation:** On overflow, mark all configured libraries dirty in a bounded, coalescing queue. Retry failed reconciliation without spawning a goroutine for every event. A reusable queue is:

```go
// Imports: context, sync, time.
type DirtyLibraries struct {
    mu sync.Mutex
    ids map[int64]struct{}
    wake chan struct{}
}
func NewDirtyLibraries() *DirtyLibraries {
    return &DirtyLibraries{ids: make(map[int64]struct{}), wake: make(chan struct{}, 1)}
}
func (d *DirtyLibraries) Mark(id int64) {
    d.mu.Lock(); d.ids[id] = struct{}{}; d.mu.Unlock()
    select { case d.wake <- struct{}{}: default: }
}
func (d *DirtyLibraries) Run(ctx context.Context, scan func(context.Context, int64) error) {
    retry := time.NewTicker(10*time.Second)
    defer retry.Stop()
    for {
        select { case <-ctx.Done(): return; case <-d.wake: case <-retry.C: }
        d.mu.Lock()
        batch := d.ids
        d.ids = make(map[int64]struct{})
        d.mu.Unlock()
        for id := range batch {
            if ctx.Err() != nil { return }
            if err := scan(ctx, id); err != nil {
                d.mu.Lock(); d.ids[id] = struct{}{}; d.mu.Unlock()
                // No immediate wake here: persistent failures wait for retry/event.
            }
        }
    }
}
```

Inject the shared complete scan/reconcile operation from F22/F23 as `scan`; a busy/skipped job must not be reported as completed. In the overflow error branch, call `Mark` once per configured library. Rewalk already watched roots during recovery and skip only duplicate `watcher.Add` calls, not traversal of their children. Retain a periodic full sweep as a safety net.

**Regression:** Inject `fsnotify.ErrEventOverflow`, fail a child watch then restore permissions, and trigger repeated overflow while a scan runs. There must be bounded queue state, no goroutine explosion, and an eventual complete reconciliation.

<a id="f49"></a>

### F49 — The documented quick-start password is rejected by initialization

**Severity:** Low · **Evidence:** Static-confirmed.

**Sources:** [README quick start](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/README.md), [initial-admin minimum length](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/auth/auth.go).

The example uses `admin:secret`; `secret` has six characters, while initialization requires at least eight bytes. A new user following the main example gets an avoidable failure.

**Implementation:** Replace the example with a valid, clearly non-production placeholder and explicitly instruct the user to choose a unique secret:

```sh
./libteca --data ./data --init-admin 'admin:replace-with-a-unique-long-password'
```

Do not present the literal placeholder as a safe deployed password. H01 recommends a password-file initialization option so a long-running server command does not expose the initial password in its process arguments.

**Regression:** Add a documentation smoke test that uses generated disposable credentials satisfying the same validator, starts the service, and verifies authenticated access against a temporary data directory.

<a id="f50"></a>

### F50 — Metadata operations can claim success after failed database or cover updates

**Severity:** Medium · **Evidence:** Static-confirmed.

**Sources:** [provider application/cover updates](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/core/providers.go), [work cover assignment](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/queries.go).

Several episode-title/description and cover-link writes discard errors while incrementing success summaries. The cover downloader also treats an existing destination as “nothing to do,” which cannot repair a previous successful file write followed by a failed database link. A partial/corrupt existing destination can make subsequent attempts appear permanently ineffective.

**Implementation:** Make success reflect confirmed persistence, and distinguish “valid asset already available” from “not applied.” Use F09 validation and F44 atomic file writes, then check the database link:

```go
// After a validated cover is present, whether newly downloaded or already existing:
if err := a.DB.SetWorkCover(workID, coverName); err != nil {
    return false, fmt.Errorf("link cover to work: %w", err)
}
return true, nil
```

Apply this pattern in a small `applyCover` helper with the appropriate enclosing signature, rather than returning from an unrelated metadata function. In episode updates, check every `SetEpisodeTitle`/description write and increment the updated count only after it succeeds. For a multi-field per-episode update, use one store transaction:

```sql
UPDATE editions SET title=? WHERE id=?;
-- Apply any associated episode metadata write in the same transaction.
```

If the operation intentionally permits partial progress across many episodes, return an explicit partial result with per-item failures; do not falsely imply all-or-nothing success. On retry, validate an existing cover and link it if usable; replace an invalid cover atomically rather than trusting existence alone.

**Regression:** Disk full, invalid existing cover, failed `SetWorkCover` after file publication, failed episode update, and cancellation midway through a batch. Successful counts and UI summaries must match the rows actually persisted.

## Additional hardening and engineering recommendations

These eight recommendations are separate from the 50 findings above. They identify deployment-dependent risks or improvements whose benefit is supported by the reviewed design, but are not represented as eight additional demonstrated exploits.

<a id="h01"></a>

### H01 — Reduce the lifetime and exposure of bearer credentials

**Sources:** [token storage schema](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/migrations/0001_init.sql), [token lookup/issuance](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/auth/auth.go), [media URL construction](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/api.ts), [CLI initialization](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/cmd/libteca/main.go).

Tokens are stored as recoverable values, and the web client attaches the same credential to media URLs. Such URLs can reach request logs, copied links, diagnostics, and browser storage/history depending on how they are used. This does not imply every browser sends every URL in a Referer header, nor that a token is guessable. The concern is the authority and lifetime of a credential after an accidental disclosure.

**Implementation:** Hash high-entropy bearer tokens at rest and enforce an explicit expiry. A SHA-256 digest is appropriate for an independently generated random token; this is not a recommendation to hash human passwords with SHA-256.

```go
// Imports: crypto/sha256, encoding/hex.
func tokenDigest(raw string) string {
    digest := sha256.Sum256([]byte(raw))
    return hex.EncodeToString(digest[:])
}
```

Introduce a unique `token_digest` column and an `expires_at` column; backfill digests from existing values in a controlled migration, change every lookup and issuance path, and only then remove the recoverable values. Replace cache invalidation keyed by raw stored values with invalidation keyed by token/user identity. Do not merely hash newly issued tokens while leaving old lookup/revocation code unchanged. A deployment may instead choose an explicit forced logout and token-table replacement during maintenance, avoiding a dual-format transition.

For media, issue short-lived, resource-scoped tickets bound to the parent token and read method rather than placing a full API credential in each URL. A ticket for one cover must not authorize a progress write, administrative operation, or another edition. Apply expiry and parent-revocation checks on every use. An HttpOnly-cookie redesign is another option, but requires an explicit CSRF and cross-origin policy; it is not a safe mechanical substitution for the current bearer design.

Also support initial credentials from a file rather than leaving a password in the long-running process's argument vector. A Unix-oriented reader can reject overly permissive files and unbounded input:

```go
// Imports: errors, fmt, io, os, strings.
func readInitialPassword(path string) (string, error) {
    f, err := os.Open(path)
    if err != nil { return "", err }
    defer f.Close()
    st, err := f.Stat()
    if err != nil { return "", err }
    if !st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 {
        return "", errors.New("password file must be regular and private (mode 0600 or stricter)")
    }
    data, err := io.ReadAll(io.LimitReader(f, 1025))
    if err != nil { return "", err }
    if len(data) > 1024 { return "", errors.New("password file too large") }
    password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
    if len(password) < 8 { return "", fmt.Errorf("password must contain at least 8 bytes") }
    return password, nil
}
```

Wire this to a separate username plus password-file option, reject conflicting initialization options, use the common validator in F47, and never log the secret. Test expiry boundaries, revocation, migration rollback, and permissions failures.

<a id="h02"></a>

### H02 — Run the container as a dedicated unprivileged user

**Sources:** [Dockerfile](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/Dockerfile), [systemd unit](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/deploy/systemd/libteca.service).

The final container image does not declare `USER`; the supplied systemd unit already has substantial isolation and `DynamicUser=yes`. Bring the container default closer to that least-privilege model. Running FFmpeg, archive utilities, and the server with broad filesystem privileges magnifies the impact of a parsing or path-handling defect.

**Implementation:** Add the following to the final Alpine stage after installing runtime packages and before the existing entrypoint:

```dockerfile
RUN addgroup -S -g 10001 libteca \
 && adduser -S -D -H -u 10001 -G libteca libteca \
 && mkdir -p /data \
 && chown 10001:10001 /data
USER 10001:10001
```

Existing bind mounts need an explicit ownership/ACL migration; the image's `chown` does not change an externally mounted directory. Mount media read-only unless a documented feature needs writes. Grant only the required render-device access for hardware acceleration, not blanket privilege. Test a fresh volume, an existing volume, software transcoding, and the supported hardware paths. Preserve the systemd hardening already present.

<a id="h03"></a>

### H03 — Make release inputs reproducible and add dependency assurance

**Sources:** [Dockerfile dependency install](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/Dockerfile), [Makefile web target](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/Makefile), [CI workflow](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/.github/workflows/ci.yml), [Go dependency manifest](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/go.mod), [web dependency manifest](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/package.json).

The CI web job uses `npm ci`, but Docker and Make use `npm install`. Align these paths so a release fails on manifest/lockfile disagreement instead of silently resolving a different dependency graph.

```diff
- RUN npm install --no-fund --no-audit
+ RUN npm ci --no-fund --no-audit
```

```make
# In the existing web recipe; preserve its type-check, build, and copy steps.
cd web && npm ci --no-fund --no-audit && npx tsc --noEmit && npm run build
```

Pin container base digests and GitHub Actions to reviewed commit SHAs, with automated update pull requests; do not replace tags with invented or unverified hashes. Audit the lockfile, not only the dependency ranges. Useful checks for a compatible checkout include:

```sh
go mod verify
go vet ./...
go test -race ./... -timeout 600s
# One-time discovery of the current vulnerability checker:
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
(cd web && npm ci && npm audit)
go version -m ./libteca
```

Pin the vulnerability checker's resolved version in CI after evaluation; `@latest` above is a one-time discovery command, not a reproducible CI recommendation. Vulnerability scan results require review of reachability and mitigation rather than blind automatic upgrades. **No specific dependency CVE or clean vulnerability bill of health is asserted by this audit, because those scans were not executed.**

<a id="h04"></a>

### H04 — Add automated state-transition and release tests for the web application

**Sources:** [CI jobs](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/.github/workflows/ci.yml), [web scripts](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/package.json), [audio player](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/players/audio.tsx), [video player](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/players/video.tsx), [reader progress saver](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/web/src/reader/shared.tsx).

The reviewed workflow runs Go vet/race tests, TypeScript checking, and a web build. It does not run browser tests, and the reviewed web manifest has no test script. A type-correct player can still corrupt progress or mishandle an HTTP response.

**Implementation:** Commit a browser-test dependency and its resolved lockfile, add a `test:e2e` script, and add a CI job that boots the built server against disposable data before running it. For example, after adding Playwright and the corresponding configuration:

```json
{
  "scripts": {
    "test:e2e": "playwright test"
  }
}
```

Merge this script into the existing manifest; do not replace its build/dev scripts. The CI steps then include:

```yaml
- run: npm ci
  working-directory: web
- run: npx playwright install --with-deps chromium firefox webkit
  working-directory: web
- run: npm run test:e2e
  working-directory: web
```

Configure a fixture-owned server with generated credentials, deterministic media, and automatic teardown. Add state-transition cases from F11, F12, F30–F36 first: backward seek, HLS resume, direct playback in a native-HLS-capable browser, failed progress writes, mark-finished then close, and cross-edition navigation. Add unit tests for the shared queue/comparator and a `make -j release` artifact smoke test. Native Safari/iOS playback still deserves device testing beyond a desktop WebKit engine. No nonexistent test file or currently working browser integration is assumed here.

<a id="h05"></a>

### H05 — Enforce CPU, memory, and disk budgets across media generation

**Sources:** [transcode manager capacity](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/transcode/transcode.go), [FFmpeg arguments](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/transcode/hwaccel.go), [trickplay generation](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/trickplay/trickplay.go).

A limit on the number of processes is not a limit on their aggregate output size or memory. Long HLS encodes and persistent trickplay caches need disk quotas, admission control, and cleanup policy. F10 supplies an immediate trickplay concurrency/width bound; it is not a complete storage budget.

**Implementation:** Use one shared reservation budget for estimated output bytes, with overflow-safe arithmetic:

```go
// Imports: errors, sync.
type ByteBudget struct {
    mu sync.Mutex
    limit, used int64
}
func NewByteBudget(limit int64) (*ByteBudget, error) {
    if limit <= 0 { return nil, errors.New("budget must be positive") }
    return &ByteBudget{limit: limit}, nil
}
func (b *ByteBudget) Reserve(n int64) (func(), bool) {
    b.mu.Lock()
    if n <= 0 || n > b.limit-b.used {
        b.mu.Unlock()
        return nil, false
    }
    b.used += n
    b.mu.Unlock()
    var once sync.Once
    return func() {
        once.Do(func() { b.mu.Lock(); b.used -= n; b.mu.Unlock() })
    }, true
}
```

Estimate from validated duration/bitrate and refuse work when space is insufficient. Hold the reservation until the process has exited and its temporary output is removed or transferred to accounted persistent storage. Enforce a real filesystem/project quota or isolated volume for hard disk limits; polling free space and estimates alone cannot guarantee that the volume will not fill. Use service/container resource limits for child CPU/memory and an age/size-bounded cache eviction policy. Test simultaneous jobs, inaccurate duration estimates, disk-full errors, and cancellation while output is being written.

<a id="h06"></a>

### H06 — Reuse the podcast egress policy for provider-supplied artwork

**Sources:** [podcast guarded HTTP client](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/podcast/fetch.go), [provider cover downloader](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/core/providers.go).

The podcast implementation already protects outbound requests at DNS/dial time. The metadata artwork downloader uses an ordinary `http.Client` and a provider-supplied URL. Its exposure depends on who controls provider responses and administrative matching actions; this is **not** presented as a demonstrated unauthenticated arbitrary-URL endpoint.

**Implementation:** Move the existing guarded transport policy into an internal shared package, export a factory, and use it for metadata art as well. Preserve validation on redirects and the actual dial target, not just a preliminary hostname lookup. Adapt the downloader to accept context:

```go
// New shared factory must preserve the implementation already in podcast/fetch.go.
// Example caller after extracting that implementation into internal/netpolicy:
client := netpolicy.NewPublicHTTPClient(coverTimeout)
req, err := http.NewRequestWithContext(ctx, http.MethodGet, coverURL, nil)
if err != nil { return false, err }
req.Header.Set("User-Agent", providerUA)
resp, err := client.Do(req)
if err != nil { return false, err }
defer resp.Body.Close()
// Continue with checked status, capped read, image validation, and atomic publish.
```

`netpolicy.NewPublicHTTPClient` is a proposed extracted API, not an existing repository function. Avoid duplicating a partial substitute for the already reviewed policy. Keep a separately configured, explicit allowlist for installations that intentionally use a private artwork server. Test redirect-to-private-address and DNS rebinding using controlled fixtures, without contacting real internal services.

<a id="h07"></a>

### H07 — Claim exclusive ownership of a live data directory before destructive startup cleanup

**Sources:** [server/transcode construction](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/server/server.go), [`transcode.New` cleanup](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/transcode/transcode.go), [startup ordering](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/cmd/libteca/main.go).

Starting another process with the same data directory can delete the existing transcode tree during manager construction. A later port-bind failure does not undo that deletion. This is an operational concurrency risk, not an authentication bypass.

**Implementation:** Acquire a nonblocking lifetime server lock before constructing the transcode manager or performing cleanup:

```go
// Imports: fmt, os, path/filepath, syscall. Linux/macOS targets.
func withServerDataLock(dataDir string, serve func() error) error {
    f, err := os.OpenFile(filepath.Join(dataDir, ".server.lock"), os.O_CREATE|os.O_RDWR, 0o600)
    if err != nil { return err }
    defer f.Close()
    if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
        return fmt.Errorf("data directory already belongs to a running server: %w", err)
    }
    defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
    return serve()
}
```

Create/validate the private data directory first. Keep the lock file rather than unlinking it. Claim the lock for the full service lifetime, before `server.New`/handler construction. Keep backup's separate lock from F04 so a supported online backup does not incorrectly require stopping the service. Mutating offline imports/scans need an explicit coordination policy. Test two processes using the same directory, including one that would otherwise fail to bind its port. Document filesystem lock support.

<a id="h08"></a>

### H08 — Scope expensive reads and make development gates deterministic

**Sources:** [core work/progress assembly](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/api/core/core.go), [user-wide reading lookup](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/internal/store/reading.go), [gate script](https://github.com/libteca/libteca/blob/b85e1c3b167c1b9c5b38e64a9087212072004d97/gate.sh).

Where a work detail response only needs progress for that work's editions, fetching every progress row for the user increases work with account history rather than the requested object. Add work-scoped store methods and preserve complete column decoding/error checks:

```sql
SELECT p.edition_id, p.edition_position_secs, p.duration_secs, p.is_finished,
       p.page, p.percent, p.locator
FROM progress p
JOIN editions e ON e.id=p.edition_id
WHERE p.user_id=? AND e.work_id=?;
```

Use the existing `(user_id, edition_id)` uniqueness/indexing and inspect the actual query plan before adding redundant indexes. Benchmark large user histories and libraries; no measured production latency is claimed here.

The development `gate.sh` also relies on fixed sleeps and builds JSON by interpolating a filesystem path. Quotes/backslashes in the supplied path can produce invalid JSON, and a slow start/scan can be mistaken for a failed gate. Serialize the payload instead:

```sh
payload=$(python3 - "$LIB_PATH" <<'PY'
import json, sys
print(json.dumps({"name": "Gate Library", "type": "audiobooks", "path": sys.argv[1]}))
PY
)
# Use --data "$payload" in the existing authenticated library-creation request.
```

Replace fixed startup sleeps with bounded health polling and fixed scan sleeps with the returned job ID's terminal status. Add an exit trap that cleans up the server only when the script owns that PID; do not kill unrelated processes listening on the same port. Treat command/HTTP/JSON failures as failed gates with a useful message. These changes make the development tool more reliable; they do not establish production compatibility by themselves.

## Recommended remediation order

First, make a protected, verified offline copy before any identity/schema migration. Close F01/F02/F03 and fix backup publication/retention (F04/F05) and episode overwrite (F06). Preserve the effective existing password/KDF, DNS, and transcode-path protections while doing so.

Next, unify authentication/session lifecycle (F12–F16), process/resource bounds (F07–F10/F28), and playback/progress semantics (F11/F29–F41). Implement the checked request helper before wiring new progress queues; implement patch-aware persistence before relying on reader cleanup behavior.

Then address scanner identity, ordering, and reconciliation (F17–F27/F42/F48), cache publication (F44), downloads/imports, and build fixes. Identity migrations require a diagnostic/dry-run report and explicit recovery mapping for already merged editions; a schema addition does not reconstruct lost identity automatically. Finish with least-privilege deployment, egress policy reuse, repeatable builds, and the regression suite.

Treat these as coherent change sets with tests, not fifty unrelated copy/paste edits. Several fixes deliberately share helpers or change a common API, and should land together to avoid inconsistent behavior across protocol adapters.

## Validation performed

**21 isolated mechanism demonstrations reproduced the expected failure behavior.** These are deliberately small programs that model the exact operation or branch identified in a source finding, or exercise that behavior in the relevant standard library/tool. They do not boot libteca, invoke its real HTTP routes, load its complete migrations, or measure production exploitability. A deterministic deletion schedule demonstrates a possible race outcome; it does not measure how often the scheduling occurs in practice.

| Local demonstration | Finding | Observed result |
|---|---|---|
| Escaping media symlink | F03 | Opening a recorded `.nes` path read a temporary file outside the temporary library. |
| Two protected keep-one deletion plans | F04 | Each deleted the other's backup, leaving neither snapshot. |
| Same size and same sampled file ends | F24 | Sampled bytes were identical, but complete SHA-256 digests differed. No cryptographic collision was needed. |
| Final podcast name collision | F06 | POSIX replacement overwrote the existing numeric fallback file. |
| Rewind progress watermark | F32 | No periodic save occurred during ten minutes after the modeled backward seek. |
| Importer's numeric encoding | F42 | `10.mp3` compared before `2.mp3`. |
| Fractional ABS track durations | F38 | The hundredth track's advertised offset was 89.1 seconds early. |
| Specials-season filter | F39 | A zero-season request selected seasons zero, one, and two. |
| Omitted finished field | F34 | The page-only update cleared an existing finished flag. |
| Stale password change | F13 | Unconditional update overwrote a newer reset; an expected-hash predicate rejected it. |
| Close-before-progress retry | F37 | A committed closed state caused the modeled retry to skip progress. |
| Changed game sequence | F21 | Updating the first file with max-sequence-plus-one moved it to the end. |
| Warm music scan ordinal | F20 | The newly seen third track received position one. |
| Duplicate JSON response | F40 | Concatenated response documents failed JSON parsing. |
| Parallel release prerequisites | F45 | The original dependency graph built before web completion; the amended dependency ordered it correctly. |
| Capped archive listing then Wait | F07 | A six-MiB writer blocked after four MiB were consumed; the watchdog had to kill it. |
| Killed launch without waiter | F28 | The original branch made zero Wait calls; the corrected model waited once. |
| Browser/Go comic page order | F35 | `a2.jpg` and `A10.jpg` appeared in opposite orders. |
| HTTP error treated as saved | F33 | A resolved HTTP-500-shaped result advanced the modeled UI state to saved. |
| Direct resume gated on HLS capability | F30 | A direct MP4 resume was skipped when native HLS capability was truthy. |
| Actual FFmpeg resumed HLS timeline | F11 | A 12-second source, resumed at six seconds, produced a six-second stream starting at 0.058667 seconds. |

### Checks of proposed helper implementations

The new TypeScript progress queue and comparator passed `tsc --strict` with ES2022/DOM libraries. Runtime checks covered two retryable failures followed by acknowledgment, retaining a permanent failure for explicit retry, coalescing newer pending writes behind an in-flight write, numeric ordering, tie-breaking, and comparator antisymmetry.

The proposed Go natural comparator and bounded-command runner were extracted into an isolated standard-library-only module and passed `go test -race -v ./...` under the local Go 1.23.2 toolchain. The runner tests covered normal output, output-cap rejection, and deadline cancellation. The Go and TypeScript comparators produced the same order for the tested ASCII, accented, leading-zero, and emoji fixture names.

During validation, the bounded-output draft was corrected to use a named buffer field: embedding `bytes.Buffer` would promote `ReadFrom` and allow an `io.Copy` optimization to bypass the limiting `Write` method. The final implementation shown in F08 is the corrected, tested form. These successful helper checks are **not** a claim that the whole repository or all proposed snippets were compiled. In particular, `os.Root` integration, real store migrations, protocol compatibility, the full frontend, and browser playback still require the repository's compatible toolchain and integration tests.

### Required integration acceptance gates

Apply patches on a branch against the pinned revision, resolve all shared API/schema changes together, and run the existing Go suite plus new regressions under the repository-required Go version. Run real browser tests against the built server, including native-HLS-capable clients. Exercise backups through an actual restore, not only an existence check. Test interrupted downloads/scans, real SQLite transaction failures, and overlapping shutdown/fallback operations. Review migration output against an existing database before touching the production copy.

No claim is made that the reviewed commit's CI was green, that dependencies are vulnerability-free, that a production deployment was penetration-tested, or that unreviewed paths are safe.

## Review coverage and boundaries

All paths below refer to the pinned revision. “Substantial selected ranges” is intentionally different from “every line reviewed.” Generated files, repository tree listings, or filenames alone were not treated as evidence of implementation behavior.

| Area | Implementation files actually retrieved and inspected | Review depth |
|---|---|---|
| Startup/authentication | `cmd/libteca/main.go`, `internal/server/server.go`, `internal/auth/auth.go`, `internal/auth/limiter.go` | Full or near-full source reads. |
| Core HTTP API | `internal/api/core/core.go`, `users.go`, `hls.go`, `providers.go` | Users/HLS read broadly; core and provider handlers read in substantial ranges, not every unrelated endpoint. |
| Store and schema | `internal/store/store.go`, `users.go`, `queries.go`, `backup.go`, `progress.go`, `reading.go`, `podcasts.go`, `migrations/0001_init.sql` | Full/near-full named files except selected podcast-store ranges. Other migration filenames were inventoried, not all migration bodies reviewed. |
| Scan and media inspection | `internal/scan/scan.go`, `video.go`, `books.go`, `games.go`, `internal/audio/probe.go` | Full/near-full scan/video/books/probe; substantial game-scanner range. |
| Background/media work | `internal/podcast/fetch.go`, `download.go`, `podcast.go`, `internal/watch/watch.go`, `internal/transcode/transcode.go`, `hwaccel.go`, `internal/trickplay/trickplay.go` | Full or near-full named source reads. |
| Compatibility APIs | `internal/api/abs/abs.go`, `internal/api/jellyfin/jellyfin.go`, `internal/api/opds/opds.go`, `cbz.go`, `internal/api/subsonic/subsonic.go` | Substantial ABS/Jellyfin/OPDS ranges; Subsonic authentication-focused range; full OPDS CBZ helper. |
| Import | `internal/importer/importer.go` | Full shared importer helper; not the complete foreign-schema translators. |
| Browser state/playback/readers | `web/src/api.ts`, `app.tsx`, `players/audio.tsx`, `players/video.tsx`, `reader/shared.tsx`, `reader/epub.tsx`, `reader/pdf.tsx` | Full API/PDF and substantial selected app/player/shared/EPUB ranges. |
| Build/deployment/documentation | `go.mod`, `web/package.json`, `.github/workflows/ci.yml`, `Dockerfile`, `Makefile`, `deploy/systemd/libteca.service`, `gate.sh`, `README.md` | Full named files. Dependency versions were inspected as source inputs, not independently certified. |
| Existing tests | `internal/store/backup_test.go`; test filenames and associated implementation/test seams elsewhere | Backup tests read; not a complete test-suite audit or execution. |

Not exhaustively reviewed: provider-specific metadata adapters; complete ABS/Kavita import translators; the Jellyfin WebSocket implementation; remaining store/query/playlist modules; every admin view, reader component, service worker, CSS/accessibility behavior, and routing path; all schema migrations; generated assets; complete dependency/lockfile transitive contents; client traffic corpora and real-device protocol behavior; deployment-specific proxies, filesystem permissions, container runtime policies, and production configuration.

Consequently, this report identifies the problems found within the reviewed paths; it does not certify that the remainder is defect-free. Further test failures may expose additional defects, and some proposed policy choices—especially legacy Subsonic authentication, overlapping libraries, media-ticket lifetimes, and identity repair—require deliberate compatibility decisions.

## Reproducible local checks embedded in this file

The following scripts are the local mechanism demonstrations described above. Save each block under the indicated filename and run it in a disposable working directory. They use synthetic temporary files and an in-memory SQLite database, not real credentials, a production media library, or an exposed service. The Python graph check requires GNU Make; the subprocess proof requires Go; the HLS proof requires FFmpeg/FFprobe with libx264/AAC support. The scripts are evidence for the narrow mechanisms, not substitutes for repository regression tests.

### `models.py`

Run: `python3 models.py`.

```python
"""Small reproductions of source-level invariants, not the repository test suite."""
import hashlib, json, os, sqlite3, subprocess, tempfile
from pathlib import Path

results = []
def passed(name, details):
    results.append({"name": name, "result": "reproduced", "details": details})

with tempfile.TemporaryDirectory(prefix="libteca-audit-") as tmp:
    root = Path(tmp)
    # An os.Open of the stored game path follows an escaping symlink.
    (root / "library").mkdir()
    (root / "secret").write_bytes(b"outside-library-test-data")
    link = root / "library" / "sample.nes"
    link.symlink_to(root / "secret")
    assert link.read_bytes() == b"outside-library-test-data"
    passed("library_path_symlink", "A .nes path inside the library opens bytes outside that library.")

    # If each pruner computes its deletion set while both files exist, each
    # can protect its own file and select the other's for deletion.
    a, b = root / "a.db", root / "b.db"
    a.touch(); b.touch()
    prune_a, prune_b = [b], [a]
    for p in prune_a + prune_b:
        p.unlink()
    assert not a.exists() and not b.exists()
    passed("concurrent_retention_schedule", "Two keep=1 deletion plans, each protecting its own snapshot, leave zero snapshots.")

    # No hash collision is needed: the bytes fed into any windowed hash
    # are identical, even though the complete files are different.
    prefix, suffix = b"P" * 65536, b"S" * 65536
    first, second = prefix + b"A" * 65536 + suffix, prefix + b"B" * 65536 + suffix
    assert len(first) == len(second)
    assert first[:65536] + first[-65536:] == second[:65536] + second[-65536:]
    assert hashlib.sha256(first).digest() != hashlib.sha256(second).digest()
    passed("windowed_file_identity", "Same length and identical first/last 64 KiB; different full SHA-256 hashes.")

    # The third podcast fallback must not be treated as unused.
    for name in ["Example.mp3", "Example-4.mp3", "4.mp3"]:
        (root / name).write_bytes(b"existing-episode")
    name = "Example.mp3"
    if (root / name).exists():
        name = "Example-4.mp3"
        if (root / name).exists():
            name = "4.mp3"
    stage = root / "download.part"
    stage.write_bytes(b"new-episode")
    os.replace(stage, root / name)
    assert (root / "4.mp3").read_bytes() == b"new-episode"
    passed("podcast_last_fallback", "The unchecked numeric fallback replaces an existing file on POSIX.")

    # Audio code bases its save cadence on forward movement, not elapsed time.
    last_sent = 3600
    saves = [p for p in range(300, 901, 10) if p - last_sent > 10]
    assert saves == []
    passed("rewind_progress_watermark", "No periodic saves during ten minutes after rewinding from 3600 s to 300 s.")

    # The imported comparator's 'numeric' string encoding still sorts 10 < 2.
    def encoded(s):
        end = 0
        while end < len(s) and s[end].isdigit(): end += 1
        return s[:end].lstrip("0") + "|" + s[end:]
    assert encoded("10.mp3") < encoded("2.mp3")
    passed("importer_natural_order", "natNum('10.mp3') sorts before natNum('2.mp3').")

    # Observed ABS play() integer accumulation.
    ds = [60.9] * 100
    advertised = sum(int(x) for x in ds[:-1])
    actual = sum(ds[:-1])
    assert abs(actual - advertised - 89.1) < 1e-8
    passed("abs_fractional_offsets", f"Track 100 starts {actual-advertised:.1f} s too early in the advertised timeline.")

    # Conditional filtering uses zero both as a valid season and 'no filter'.
    season_filter = 0
    selected = [s for s in [0, 1, 2] if not (season_filter > 0 and s != season_filter)]
    assert selected == [0, 1, 2]
    passed("jellyfin_specials_filter", "A season-zero filter selects seasons 0, 1, and 2.")

    db = sqlite3.connect(":memory:")
    db.executescript("CREATE TABLE progress (id INTEGER PRIMARY KEY, page INTEGER, is_finished INTEGER); INSERT INTO progress VALUES (1, 12, 1);")
    body = json.loads('{"page":12}')
    db.execute("UPDATE progress SET page=?, is_finished=? WHERE id=1", (body["page"], bool(body.get("finished", False))))
    assert db.execute("SELECT is_finished FROM progress").fetchone()[0] == 0
    passed("pdf_finish_omission", "A page-only update with the current bool-default semantics clears finished=true.")

    db.executescript("CREATE TABLE users (id INTEGER PRIMARY KEY, password_hash TEXT); INSERT INTO users VALUES (1, 'old');")
    verified_hash = "old"
    db.execute("UPDATE users SET password_hash='admin-reset' WHERE id=1")
    db.execute("UPDATE users SET password_hash='stale-self-change' WHERE id=1")
    assert db.execute("SELECT password_hash FROM users").fetchone()[0] == "stale-self-change"
    db.execute("UPDATE users SET password_hash='admin-reset' WHERE id=1")
    n = db.execute("UPDATE users SET password_hash='stale-self-change' WHERE id=1 AND password_hash=?", (verified_hash,)).rowcount
    assert n == 0
    passed("password_change_cas", "An unconditional update overwrites a later reset; the expected-hash predicate rejects it.")

    db.executescript("CREATE TABLE sessions (id TEXT PRIMARY KEY, closed INTEGER); INSERT INTO sessions VALUES ('s', 0);")
    db.execute("UPDATE sessions SET closed=1 WHERE id='s'"); db.commit()
    # Simulate failure of the subsequent progress write and the retry guard.
    retry_saves = db.execute("SELECT closed FROM sessions WHERE id='s'").fetchone()[0] == 0
    assert not retry_saves
    passed("abs_close_retry", "After closing commits but progress fails, the closed-session retry branch skips progress.")

    db.executescript("CREATE TABLE files (id INTEGER PRIMARY KEY, seq INTEGER); INSERT INTO files VALUES (1,1),(2,2);")
    new_seq = db.execute("SELECT coalesce(max(seq),0)+1 FROM files").fetchone()[0]
    db.execute("UPDATE files SET seq=? WHERE id=1", (new_seq,))
    assert db.execute("SELECT id FROM files ORDER BY seq").fetchall() == [(2,), (1,)]
    passed("changed_game_reordering", "Re-upserting the first of two files with max(seq)+1 moves it to the end.")

    unchanged = {"track1", "track2"}
    probed = [x for x in ["track1", "track2", "track3"] if x not in unchanged]
    positions = dict((name, i + 1) for i, name in enumerate(probed))
    assert positions["track3"] == 1
    passed("warm_music_ordinal", "The third track receives position 1 when unchanged tracks are excluded before enumeration.")

    payload = "{}" + '{"ok":true}'
    try:
        json.loads(payload)
    except json.JSONDecodeError:
        passed("jellyfin_double_response", "Two response writes concatenate JSON documents, which JSON.parse rejects.")
    else:
        raise AssertionError("expected invalid combined JSON")

    # A minimal graph with the same dependency shape as the release Makefile.
    m = root / "Makefile"
    m.write_text(".PHONY: release web checksums\nrelease: web release-linux checksums\nchecksums: release-linux\n\t@test -f artifact\nweb:\n\t@sleep 0.3; touch web-ready\nrelease-%:\n\t@if test -f web-ready; then echo fresh > artifact; else echo stale > artifact; fi\n")
    subprocess.run(["make", "-j4", "release"], cwd=root, check=True, capture_output=True)
    assert (root / "artifact").read_text().strip() == "stale"
    (root / "artifact").unlink(); (root / "web-ready").unlink()
    m.write_text(m.read_text().replace("release-%:\n", "release-%: web\n"))
    subprocess.run(["make", "-j4", "release"], cwd=root, check=True, capture_output=True)
    assert (root / "artifact").read_text().strip() == "fresh"
    passed("parallel_release_graph", "Original graph embeds before web finishes; release-%: web orders the build correctly.")

print(json.dumps(results, indent=2))
```

### `process.go`

Run: `go run process.go`.

```go
package main

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "os/exec"
    "sync/atomic"
    "time"
)

type result struct { Name string `json:"name"`; Reproduced bool `json:"reproduced"`; Details string `json:"details"` }

type fakeProcess struct { killed atomic.Bool; waits atomic.Int32 }
func (p *fakeProcess) start() error { return nil }
func (p *fakeProcess) kill() { p.killed.Store(true) }
func (p *fakeProcess) wait() error { p.waits.Add(1); return nil }
func launchModel(killed, fixed bool, p *fakeProcess) <-chan struct{} {
    if err := p.start(); err != nil { panic(err) }
    done := make(chan struct{})
    if killed && !fixed { p.kill(); return done }
    go func() { _ = p.wait(); close(done) }()
    if killed { p.kill() }
    return done
}

func main() {
    if len(os.Args) > 1 && os.Args[1] == "writer" {
        _, _ = io.Copy(os.Stdout, bytes.NewReader(make([]byte, 6 << 20)))
        return
    }
    var out []result
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, os.Args[0], "writer")
    stdout, err := cmd.StdoutPipe(); if err != nil { panic(err) }
    if err = cmd.Start(); err != nil { panic(err) }
    b, err := io.ReadAll(io.LimitReader(stdout, 4 << 20)); if err != nil { panic(err) }
    if len(b) != 4<<20 { panic("short listing") }
    waited := make(chan error, 1)
    go func() { waited <- cmd.Wait() }()
    blocked := false
    select {
    case err := <-waited: panic(fmt.Sprintf("expected wait to block; got %v", err))
    case <-time.After(200*time.Millisecond): blocked = true
    }
    _ = cmd.Process.Kill()
    <-waited
    out = append(out, result{"cbr_listing_cap_wait", blocked, "Child writing 6 MiB blocks after reader stops at 4 MiB; Wait only completes after watchdog kills child."})

    before := &fakeProcess{}
    done := launchModel(true, false, before)
    select { case <-done: panic("unexpected closed channel"); default: }
    if before.waits.Load() != 0 || !before.killed.Load() { panic("unexpected model") }
    after := &fakeProcess{}
    fixedDone := launchModel(true, true, after)
    select { case <-fixedDone: case <-time.After(time.Second): panic("fixed waiter stuck") }
    if after.waits.Load() != 1 { panic("wait count") }
    out = append(out, result{"killed_launch_waiter", true, "Current killed-after-start branch never calls Wait or closes done; revised ordering waits exactly once."})
    enc := json.NewEncoder(os.Stdout); enc.SetIndent("", "  "); _ = enc.Encode(out)
}
```

### `browser.mjs`

Run: `node browser.mjs`.

```js
import assert from 'node:assert/strict';
const out = [];
const names = ['a2.jpg', 'A10.jpg'];
const browserOrder = [...names].sort(new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' }).compare);
const goByteOrder = [...names].sort(); // First characters differ, so this is natLess's decision here.
assert.notDeepEqual(browserOrder, goByteOrder);
out.push({ name: 'cbz_cross_client_order', browserOrder, goByteOrder });
let state = 'saving';
async function apiModel() { return {error: 'Internal server error', status: 500}; }
await apiModel(); state = 'saved';
assert.equal(state, 'saved');
out.push({name: 'progress_false_saved', state, httpStatus: 500});
const position = 120;
const nativeHLSCapability = 'probably';
const shouldResume = position > 0 && !nativeHLSCapability;
assert.equal(shouldResume, false);
out.push({name: 'direct_resume_capability_condition', directMP4ResumeRuns: shouldResume});
console.log(JSON.stringify(out, null, 2));
```

### `hls-proof.sh`

Run: `sh hls-proof.sh`.

```sh
#!/bin/sh
# Synthetic fixture only; does not read a library or contact a server.
set -eu
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
cd "$work"
ffmpeg -y -v error \
  -f lavfi -i 'color=c=black:s=160x90:r=25' \
  -f lavfi -i 'sine=frequency=440:sample_rate=48000' \
  -t 12 -c:v libx264 -preset ultrafast -pix_fmt yuv420p -c:a aac source.mp4
ffmpeg -y -v error -ss 6 -i source.mp4 \
  -map 0:v:0 -map '0:a:0?' -c:v libx264 -preset veryfast -crf 21 \
  -c:a aac -b:a 192k -ac 2 -muxdelay 0 \
  -f hls -hls_time 4 -hls_init_time 2 -hls_list_size 0 \
  -hls_flags independent_segments -hls_segment_filename 'seg%05d.ts' index.m3u8
printf 'Source duration:\n'
ffprobe -v error -show_entries format=duration -of json source.mp4
printf 'Resumed HLS start/duration:\n'
ffprobe -v error -show_entries format=start_time,duration -of json index.m3u8
```


## Source-reference note

Each finding links directly to the implementation at the audited commit rather than a moving branch. Function names and behavior descriptions identify the relevant portion when a link targets a complete file. The external technical references used for general semantics were [Go's traversal-resistant file API](https://go.dev/blog/osroot), [FFmpeg's option documentation](https://ffmpeg.org/ffmpeg.html), and [HTTP caching semantics in RFC 9111](https://httpwg.org/specs/rfc9111.html). Repository code, not an earlier audit report, is the evidence for repository-specific findings.

**End of report.**
