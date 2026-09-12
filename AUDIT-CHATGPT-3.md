# libteca - Audit Pass 3 (2026-09-12)

## Verdict

**8 new findings meet the bar: 4 HIGH, 4 MEDIUM.** None repeats the 12 Pass 1 or 3 Pass 2 findings. The targeted sweep rechecked the hardened podcast egress transport, podcast deletion transactions/locking, auth context keys and token propagation, HLS session lifecycle, pagination helpers/callers, scanning/import paths, and settings mutation. The `updateAppSettings` identifier is not present in the supplied non-test Go source; the settings paths that are present were reviewed instead. This is a source audit of the supplied attachment; no build/test execution is claimed here.

## Findings

### 1. [HIGH] Podcast-library deletion can delete a subscription created after its lock snapshot

**File:** `internal/podcast/podcast.go` (`Service.DeleteLibraryPodcasts`, `Service.Subscribe`); `internal/api/core/libraries.go` (`deleteLibrary`)

**Problem:** `DeleteLibraryPodcasts` snapshots the library's podcast IDs and removable paths first, then acquires single-flight slots only for that frozen ID set. The store operation it subsequently calls is broader: it deletes every podcast matching `library_id`. `Subscribe`, meanwhile, inserts the new podcast row before acquiring that new podcast's single-flight slot. A concrete race is therefore: deletion snapshots podcast A; a concurrent subscribe inserts podcast B and acquires B's slot; deletion checks/locks only A, then `DB.DeleteLibraryPodcasts(libID)` deletes A **and B** while B is actively applying its feed/downloads. B was absent from the deletion path/cover snapshot, so bytes it writes can be left orphaned, and the subscribe can fail after its row has disappeared. There is a second creation window after `Service.DeleteLibraryPodcasts` returns/releases its slots but before core separately calls `DB.DeleteLibrary(id)`. fileciteturn4file2L112-L164 fileciteturn2file1L51-L71 fileciteturn7file0L9-L22

**Fix with code:** add a podcast-library lifecycle gate around row creation and library deletion, and keep that gate held through deletion of the library row. The database side should expose one transaction that removes podcast children and the library row so the service does not reopen a gap between two commits.

```go
type Service struct {
    DB      *store.DB
    DataDir string
    // ...
    lifecycleMu sync.Mutex // serializes subscription creation vs library deletion
    mu          sync.Mutex // existing per-podcast inflight map
}

func (s *Service) Subscribe(ctx context.Context, feedURL string, auto bool, max int) (*store.Podcast, error) {
    feedURL = normalizeFeedURL(feedURL)
    feed, _, etag, lastModified, err := s.fetcher.FetchFeed(ctx, feedURL, "", "")
    if err != nil {
        return nil, err
    }

    // Network fetch stays outside the gate; publication of a new subscription
    // and acquisition of its single-flight slot are atomic w.r.t. library delete.
    s.lifecycleMu.Lock()
    libID, err := s.DB.EnsurePodcastsLibrary(filepath.Join(s.DataDir, "podcasts"))
    if err != nil {
        s.lifecycleMu.Unlock()
        return nil, err
    }
    p := &store.Podcast{LibraryID: libID, FeedURL: feedURL, Title: feed.Title,
        AutoDownload: auto, MaxEpisodes: max}
    p.ID, err = s.DB.AddPodcast(p)
    if err == nil && !s.acquire(p.ID) {
        err = ErrRefreshBusy
    }
    s.lifecycleMu.Unlock()
    if err != nil {
        return nil, err
    }
    defer s.release(p.ID)

    s.DB.UpdatePodcastFetch(p.ID, nilOrEmpty(etag), nilOrEmpty(lastModified), nowMs())
    // existing cover/applyFeed work...
    return s.DB.Podcast(p.ID)
}

func (s *Service) DeletePodcastLibrary(libID int64) error {
    s.lifecycleMu.Lock()
    defer s.lifecycleMu.Unlock()

    ids, paths, covers, err := s.snapshotLibraryDelete(libID)
    if err != nil {
        return err
    }
    if !s.acquireAll(ids) { // one s.mu critical section; no partial acquisition
        return ErrRefreshBusy
    }
    defer s.releaseAll(ids)

    // One store transaction: capture file IDs, delete progress/episodes/files/
    // podcasts, then delete the library row before commit.
    if err := s.DB.DeletePodcastLibrary(libID); err != nil {
        return err
    }
    for _, p := range paths { _ = os.Remove(p) }
    for _, p := range covers { _ = os.Remove(p) }
    return nil
}
```

Core should call `DeletePodcastLibrary` and return directly for podcast libraries rather than performing a later, separately synchronized `DB.DeleteLibrary`.

### 2. [HIGH] Any Jellyfin user can enumerate another user's PlaySessionId and stop or remote-control that session

**File:** `internal/api/jellyfin/jellyfin.go` (`sessionsList`, `sessionStopped`, `hlsMaster`); `internal/api/jellyfin/ws.go` (`sessionDTOs`, `deviceForPlaySession`, `forwardCommand`); `internal/transcode/transcode.go` (`Manager.Get`, `Manager.Close`)

**Problem:** `/Sessions` is under `jfAuth` but returns the global hub snapshot to every authenticated user. Each DTO contains the owning user, device ID, and `PlaySessionId`. WebSocket command forwarding likewise resolves a supplied `DeviceId` or `PlaySessionId` globally and forwards the command without checking that the target belongs to the sender. Separately, `sessionStopped` accepts a query `PlaySessionId` and calls `TC.Close` by string alone. A concrete attack is: victim starts a transcoded Jellyfin playback and reports `Sessions/Playing`; ordinary user B calls `/Sessions`, learns victim A's `PlaySessionId`, then either sends a `Playstate`/`Play` WebSocket command to A's device or posts `POST /Sessions/Playing/Stopped?PlaySessionId=<victim>` with `{}`. The latter closes A's ffmpeg session server-side even though B does not own it. fileciteturn2file2L83-L85 fileciteturn4file4L256-L280 fileciteturn9file0L9-L17 fileciteturn9file1L31-L45 fileciteturn9file2L58-L65

**Fix with code:** make transcode sessions owner-bound, filter session enumeration, and enforce the same owner on WebSocket targeting. Do not let a colliding caller kill an existing session owned by someone else.

```go
// internal/transcode/transcode.go
type Session struct {
    // existing fields...
    OwnerUserID int64
}

var ErrSessionOwned = errors.New("transcode session belongs to another user")

func (m *Manager) Get(id string, ownerID, editionID int64, source string, start float64) (*Session, error) {
    if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
        return nil, fmt.Errorf("invalid transcode session id")
    }
    m.mu.Lock()
    defer m.mu.Unlock()

    if s, ok := m.sessions[id]; ok {
        if s.OwnerUserID != ownerID {
            return nil, ErrSessionOwned // never kill another user's session
        }
        if s.Edition == editionID {
            s.Touch()
            return s, nil
        }
        s.kill()
        delete(m.sessions, id)
    }
    // create with OwnerUserID: ownerID ...
}

func (m *Manager) CloseForUser(id string, ownerID int64) bool {
    m.mu.Lock()
    defer m.mu.Unlock()
    s, ok := m.sessions[id]
    if !ok || s.OwnerUserID != ownerID {
        return false
    }
    s.kill()
    delete(m.sessions, id)
    return true
}
```

```go
// Jellyfin callers
s, err := a.TC.Get(sessionID, uid(r), ed.ID, ed.Files[0].Path, start)
// ...
a.TC.CloseForUser(sid, uid(r))

// WebSocket clients already store uidv; expose it on the interface.
type wsClient interface {
    enqueue([]byte) bool
    shutdown()
    device() string
    userID() int64
}
func (c *socketConn) userID() int64 { return c.uidv }

// Before sendTo, resolve the target liveSession and require:
if target.UserID != from.userID() {
    return
}
```

`/Sessions` should similarly return only the requester's sessions unless an explicit admin-only policy authorizes global visibility.

### 3. [MEDIUM] The egress dial guard gives up after the first public DNS address fails

**File:** `internal/podcast/podcast.go` (`publicHTTPClient`)

**Problem:** the hardened transport correctly disables proxies and resolves the destination itself, but its `DialContext` immediately returns the result of dialing the **first** allowed IP. It never tries the remaining public DNS answers after a connect failure. A concrete failure is a dual-stack feed/CDN whose resolver returns a public IPv6 address first on a host with broken/no IPv6 routing and a healthy IPv4 address second: the first dial fails and libteca rejects the feed, cover, or enclosure even though another validated address is reachable. The same occurs with multi-A hosts during partial endpoint failure. fileciteturn8file0L10-L33

**Fix with code:** keep the SSRF classification for every candidate, but continue across allowed addresses until one connects.

```go
tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
    host, port, err := net.SplitHostPort(address)
    if err != nil {
        return nil, err
    }
    ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
    if err != nil {
        return nil, err
    }

    var lastErr error
    sawPublic := false
    for _, ip := range ips {
        if !publicIP(ip) {
            continue
        }
        sawPublic = true
        conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
        if err == nil {
            return conn, nil
        }
        lastErr = err
    }
    if sawPublic && lastErr != nil {
        return nil, lastErr
    }
    return nil, fmt.Errorf("podcast URL resolves only to non-public addresses")
}
```

Keep `tr.Proxy = nil`, the RFC 6598 rejection, and the existing redirect scheme/host checks unchanged.

### 4. [MEDIUM] Production enclosure downloads are accidentally capped at 30 seconds

**File:** `internal/podcast/podcast.go` (`New`, `NewWithClient`); `internal/podcast/download.go` (`downloadEpisode`, `downloadTimeout`)

**Problem:** `Service.DLClient` is documented as the enclosure client with no whole-request timeout, because `downloadEpisode` applies its own size-derived read deadline with a 90-second floor. Production `New`, however, constructs one `publicHTTPClient(30*time.Second)` and `NewWithClient` assigns that same client to both `Client` and `DLClient`. `http.Client.Timeout` includes reading the response body, so the outer 30-second timeout wins over the intended enclosure deadline. A 100 MiB episode arriving at 2 MiB/s takes roughly 50 seconds and will consistently fail around 30 seconds despite the 90+ second downloader budget. fileciteturn8file1L38-L55 fileciteturn3file0L6-L15

**Fix with code:** production needs two equally guarded transports with different total timeouts; retain the single-client constructor only as the loopback test seam.

```go
func New(db *store.DB, dataDir string) *Service {
    return newService(
        db, dataDir,
        publicHTTPClient(30*time.Second), // feeds/covers
        publicHTTPClient(0),              // enclosures; downloadEpisode owns deadline
    )
}

func NewWithClient(db *store.DB, dataDir string, client *http.Client) *Service {
    // Test seam: fixtures deliberately inject one unrestricted client.
    return newService(db, dataDir, client, client)
}

func newService(db *store.DB, dataDir string, feedClient, dlClient *http.Client) *Service {
    return &Service{
        DB: db, DataDir: dataDir,
        Client: feedClient, DLClient: dlClient,
        fetcher: Fetcher{Client: feedClient},
        inflight: map[int64]bool{},
        sem: make(chan struct{}, workerPool),
        dlReadFloor: 90 * time.Second,
    }
}
```

### 5. [MEDIUM] Subsonic token authentication bypasses the login limiter and exposes a cheap password oracle

**File:** `internal/api/subsonic/subsonic.go` (`API.authenticate`)

**Problem:** the Subsonic `t`/`s` token branch runs before the only `LoginLimiter.Allow` call, and bad token attempts never call `Failure`. Once a user has completed a successful plain/`enc:` login, libteca caches the plaintext password specifically so token auth can compute `md5(password+salt)`. An attacker who knows the username can choose a salt and submit `t=md5(candidate+salt)` for each password candidate. Wrong candidates are rejected by the cheap MD5 comparison without Argon2 and without incrementing the lockout; the correct candidate reaches the Argon2 verification and authenticates. That makes the token endpoint an unlimited online password oracle despite the shared five-failure limiter on ordinary login. fileciteturn9file3L85-L105 fileciteturn4file1L62-L95

**Fix with code:** apply the limiter before either credential form and count every credential-level token failure. Using an IP+username key also prevents unrelated accounts behind one address from sharing a counter.

```go
func (a *API) authenticate(r *http.Request) (int64, bool) {
    name := r.Form.Get("u")
    if name == "" {
        return 0, false
    }
    key := auth.ClientIP(r) + "|" + strings.ToLower(name)
    if a.LoginLimiter != nil {
        if ok, _ := a.LoginLimiter.Allow(key); !ok {
            return 0, false
        }
    }
    fail := func() (int64, bool) {
        if a.LoginLimiter != nil {
            a.LoginLimiter.Failure(key)
        }
        return 0, false
    }

    u, err := a.DB.UserByName(name)
    if err != nil {
        return fail()
    }
    if token := r.Form.Get("t"); token != "" {
        salt := r.Form.Get("s")
        secret, has := a.DB.GetSetting(subsonicSecretKey(u.ID))
        if salt == "" || !has {
            return fail()
        }
        sum := md5.Sum([]byte(secret + salt))
        if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])),
            []byte(strings.ToLower(token))) != 1 {
            return fail()
        }
        if !auth.Verify(secret, u.PasswordHash) {
            _ = a.DB.DeleteSetting(subsonicSecretKey(u.ID))
            return fail()
        }
        if a.LoginLimiter != nil { a.LoginLimiter.Success(key) }
        return u.ID, true
    }

    // Existing plain/enc path, using fail() on every credential failure.
    // ...
}
```

### 6. [HIGH] A file disappearing during a scan can panic the whole server process

**File:** `internal/scan/books.go` (`scanBooksLibrary`); `internal/scan/scan.go` (`scanAudioLibrary`); `internal/scan/video.go` (`scanVideoLibrary`, `scanMusicLibrary`); `internal/api/core/core.go` (`scanLibrary`)

**Problem:** all four scanner walkers call `fi, _ := d.Info()` and immediately dereference `fi`. `os.DirEntry.Info` can fail even when the `WalkDir` callback's `err` argument is nil—for example, another process deletes or atomically replaces a file after the directory entry was enumerated but before `Info` performs its stat. In that case `fi` is nil and `fi.Size()`/`fi.ModTime()` panics. HTTP-triggered scans run in a bare background goroutine (`go a.runScan(...)`), outside Neutron's request recovery middleware, so this is not merely a 500: an unhandled panic in that goroutine terminates the Go process. fileciteturn1file2L74-L86 fileciteturn1file2L95-L113 fileciteturn1file2L117-L136 fileciteturn1file2L139-L158 fileciteturn7file2L63-L67

**Fix with code:** handle `Info` failures at every walker site instead of dereferencing a nil result. A vanished entry can safely be skipped and reconsidered on the next scan.

```go
fi, err := d.Info()
if err != nil {
    // Entry vanished or became unreadable after WalkDir enumerated it.
    // Do not mark it seen; reconciliation / the next scan decides its state.
    return nil
}
files = append(files, bookFile{
    path:  path,
    // ...
    size:  fi.Size(),
    mtime: fi.ModTime().Unix(),
})
```

Apply the same checked pattern in `scanBooksLibrary`, `scanAudioLibrary`, `scanVideoLibrary`, and `scanMusicLibrary`.

### 7. [HIGH] Book scanning defeats its own size cap and can OOM on large PDFs or CBR pages

**File:** `internal/scan/books.go` (`pdfPageCount`, `cbrExtract`)

**Problem:** `pdfPageCount` reads the entire PDF into memory and then converts the full byte slice to a string merely for a best-effort marker count. A multi-gigabyte PDF can therefore exhaust a small media-server process during a routine scan. The CBR cover path has a nominal 20 MiB `cbrMaxCoverBytes`, but both extraction branches enforce it only **after** buffering the full decompressed page: `unrar` uses `Command.Output()` and the `unar` branch uses `os.ReadFile`, then slices the already-allocated result. A CBR whose first image decompresses to hundreds of MiB can likewise exhaust memory before the cap is consulted. fileciteturn10file0L8-L24 fileciteturn10file1L41-L58 fileciteturn11file0L20-L34

**Fix with code:** because PDF page counting is explicitly best-effort, skip the raw scan above a bounded size (or implement a streaming parser). For CBR, enforce the byte limit while reading, not after allocation.

```go
const pdfRawCountMax = 64 << 20

func pdfPageCount(p string) int {
    fi, err := os.Stat(p)
    if err != nil || fi.Size() > pdfRawCountMax {
        return 0
    }
    data, err := os.ReadFile(p)
    if err != nil {
        return 0
    }
    n := bytes.Count(data, []byte("/Type /Page")) -
        bytes.Count(data, []byte("/Type /Pages"))
    if n < 0 { return 0 }
    return n
}

func readBounded(r io.Reader, max int64) ([]byte, error) {
    b, err := io.ReadAll(io.LimitReader(r, max+1))
    if err != nil { return nil, err }
    if int64(len(b)) > max {
        return nil, fmt.Errorf("extracted image exceeds %d bytes", max)
    }
    return b, nil
}

func cbrExtract(tool, archive, name string) ([]byte, error) {
    if tool == "unrar" {
        cmd := exec.Command(tool, "p", "-inul", archive, name)
        stdout, err := cmd.StdoutPipe()
        if err != nil { return nil, err }
        if err := cmd.Start(); err != nil { return nil, err }
        data, rerr := readBounded(stdout, cbrMaxCoverBytes)
        if rerr != nil {
            _ = cmd.Process.Kill()
            _ = cmd.Wait()
            return nil, rerr
        }
        if err := cmd.Wait(); err != nil { return nil, err }
        return data, nil
    }

    // For unar's extracted temp file, stat first and reject before os.ReadFile.
    // ...
}
```

### 8. [MEDIUM] ABS import can desynchronize file metadata from inserted file IDs and panic while importing progress

**File:** `internal/importer/importer.go` (`applyFiles`, `locate`); `internal/importer/abs.go` (`ABS`)

**Problem:** `applyFiles` receives the planned `[]fileSpec`, silently skips any file whose `os.Stat` now fails, and returns a compacted `[]int64` of only the successfully inserted file IDs. `ABS` then stores an `edRef` containing the **original** file slice beside that compacted ID slice. `locate` iterates the original files and indexes `ids[i]` without checking that the slices remain aligned. A concrete race is a three-track audiobook planned successfully, then track 2 is removed before apply: `files` still has three entries while `ids` contains only track 1 and track 3. Progress that lands in track 2 can be linked to track 3's ID; progress that lands in track 3 reaches `ids[2]` and panics. By then the importer has already committed user/library/work/edition/file writes, so the recovered request can leave a partial import behind. fileciteturn9file4L112-L129 fileciteturn5file2L77-L81 fileciteturn7file1L36-L53 fileciteturn5file1L46-L65

**Fix with code:** return only the `fileSpec` values that actually produced IDs, so the two slices are constructed together and cannot drift. Keep a defensive bound in `locate` as a final guard.

```go
func applyFiles(db *store.DB, editionID int64, files []fileSpec) ([]fileSpec, []int64, error) {
    kept := make([]fileSpec, 0, len(files))
    ids := make([]int64, 0, len(files))
    for i, f := range files {
        fi, err := os.Stat(f.Path)
        if err != nil {
            continue
        }
        fr := &store.FileRec{
            EditionID: editionID, Path: f.Path, Seq: i + 1,
            SizeBytes: fi.Size(), MtimeSecs: fi.ModTime().Unix(),
            DurationSecs: f.Duration, Chapters: "[]",
        }
        if err := db.UpsertFile(fr); err != nil {
            return nil, nil, err
        }
        kept = append(kept, f)
        ids = append(ids, fr.ID)
    }
    return kept, ids, nil
}

// ABS caller
kept, ids, err := applyFiles(db, eid, b.files)
if err != nil { return nil, err }
bookEds[b.b.itemID] = &edRef{id: eid, files: kept, ids: ids}

func locate(files []fileSpec, ids []int64, pos float64) (*int64, float64) {
    if len(ids) == 0 { return nil, 0 }
    n := min(len(files), len(ids))
    cum := 0.0
    for i := 0; i < n; i++ {
        f := files[i]
        cum += f.Duration
        if pos < cum || i == n-1 {
            off := max(0, pos-(cum-f.Duration))
            id := ids[i]
            return &id, off
        }
    }
    return nil, 0
}
```

[AUDIT-CHATGPT-3.md](sandbox:/mnt/data/AUDIT-CHATGPT-3.md)
