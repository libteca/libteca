# libteca - Audit (2026-09-12)

## Summary

| # | Severity | File | Issue |
|---|---|---|---|
| 1 | High | `internal/watch/watch.go` | Failed scans still run missing-file reconciliation, so a transiently unavailable library root can mark the entire library missing. |
| 2 | High | `internal/api/jellyfin/jellyfin.go`, `internal/api/core/hls.go`, `internal/transcode/transcode.go` | Deterministic HLS session IDs make concurrent viewers/tabs share one ffmpeg process, ignore independent seek positions, and tear each other down. |
| 3 | High | `internal/podcast/podcast.go`, `internal/podcast/download.go` | Podcast deletion is not serialized with refresh/download and can leave orphan files/rows or recreate state while deletion is in progress. |
| 4 | High | `internal/podcast/fetch.go`, `internal/podcast/podcast.go`, `internal/api/core/podcasts.go` | Podcast feeds can force server-side requests to loopback/private/link-local addresses through feed, cover, enclosure, and redirect URLs. |
| 5 | Medium | `internal/api/core/users.go`, `internal/store/users.go` | Last-admin protection is a check-then-delete race; two admins can concurrently delete each other and leave no administrator. |
| 6 | Medium | `internal/api/core/playlists.go`, `internal/api/subsonic/subsonic.go`, `internal/store/playlists.go` | Playlist create/replace operations are not atomic and can leave empty or partially populated playlists after an error. |
| 7 | Medium | `internal/api/jellyfin/jellyfin.go`, `internal/api/jellyfin/podcasts.go`, `internal/api/abs/podcasts.go` | Unbounded/negative pagination values can overflow indexes or produce negative slice bounds, causing request panics. |
| 8 | Medium | `internal/api/jellyfin/jellyfin.go` | A syntactically valid season ID for a nonexistent work dereferences a nil `Work`, causing a panic/500. |
| 9 | Medium | `internal/api/jellyfin/jellyfin.go`, `internal/api/core/hls.go`, `internal/auth/auth.go` | HLS playlists only propagate query-string tokens; header-authenticated playback can emit unauthenticated segment URLs and fail after the manifest. |
| 10 | Medium | `internal/api/core/providers.go`, `internal/podcast/fetch.go` | Cover size limits silently truncate oversized responses and then persist/advertise corrupt cover files. |
| 11 | Medium | `internal/store/queries.go`, `internal/podcast/podcast.go` | Deleting the podcasts library removes episode rows before podcast file rows are cleaned, leaving orphan file records and downloaded media on disk. |
| 12 | Low | `internal/transcode/transcode.go` | Every transcoder manager starts a reaper goroutine with no stop mechanism; `CloseAll` does not terminate it. |

## Findings

### 1. [HIGH] Failed scans can mark an entire unavailable library missing

- File: `internal/watch/watch.go` (`triggerAndWait`, `waitForJob`, `reconcileMissing`)
- Problem: `triggerAndWait` calls `reconcileMissing` whenever `waitForJob` returns and the context is still alive. `waitForJob` returns for every terminal job state, including `"error"`. If a library root or network mount temporarily disappears, the scan fails, but reconciliation then runs `os.Stat` against every stored path. `ENOENT` results are converted to `missing = 1`, potentially making an entire healthy library disappear from the server because of a transient mount failure.
- Fix: return the terminal job status from `waitForJob` and reconcile only after a successful `"done"` scan.

```diff
 func (w *Watcher) triggerAndWait(ctx context.Context, libID int64) {
 	jobID, err := w.scan.TriggerScan(ctx, libID)
 	if err != nil {
 		if w.scanInFlight(libID) {
 			w.markDirty(libID)
 		}
 		fmt.Fprintf(os.Stderr, "libteca: watch: scan for library %d skipped: %v\n", libID, err)
 		return
 	}
-	w.waitForJob(ctx, jobID)
-	if ctx.Err() == nil {
+	status := w.waitForJob(ctx, jobID)
+	if ctx.Err() == nil && status == "done" {
 		w.reconcileMissing(libID)
 	}
 }

-func (w *Watcher) waitForJob(ctx context.Context, jobID int64) {
+func (w *Watcher) waitForJob(ctx context.Context, jobID int64) string {
 	t := time.NewTicker(jobPollEvery)
 	defer t.Stop()
 	for {
 		select {
 		case <-ctx.Done():
-			return
+			return ""
 		case <-t.C:
 			j, err := w.db.GetScanJob(jobID)
-			if err != nil || j.Status != "running" {
-				return
+			if err != nil {
+				return ""
+			}
+			if j.Status != "running" {
+				return j.Status
 			}
 		}
 	}
 }
```

### 2. [HIGH] Deterministic HLS session IDs corrupt concurrent playback

- File: `internal/api/jellyfin/jellyfin.go` (`playbackInfo`), `internal/api/core/hls.go` (`webSessionID`, `editionPlayback`), `internal/transcode/transcode.go` (`Manager.Get`)
- Problem: Jellyfin always returns `PlaySessionId = "ps-<editionID>"`, while the web face uses `"web-<editionID>"`. `transcode.Manager.Get` keys sessions solely by that ID and, when the edition matches, returns the existing session without considering `startSecs`. Two users, devices, browser tabs, or seek/restart attempts for the same edition therefore share one ffmpeg process. The second caller's requested starting position is ignored, and either caller can stop/reap the shared process. This produces incorrect seeks and cross-client playback failures under ordinary concurrent use.
- Fix: make every playback instance carry an unpredictable unique suffix while retaining the edition ID for the core route's lookup.

```go
// shared helper, e.g. internal/transcode/sessionid.go
package transcode

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func NewSessionID(prefix string, editionID int64) (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%d-%s", prefix, editionID, hex.EncodeToString(b[:])), nil
}
```

```diff
 // Jellyfin playbackInfo
-	playSession := "ps-" + strconv.FormatInt(ed.ID, 10)
+	playSession, err := transcode.NewSessionID("ps-", ed.ID)
+	if err != nil {
+		write(w, 500, map[string]any{"error": "internal error"})
+		return
+	}
```

```diff
- func webSessionID(editionID int64) string {
-	return "web-" + strconv.FormatInt(editionID, 10)
- }
+func webSessionID(editionID int64) (string, error) {
+	return transcode.NewSessionID("web-", editionID)
+}

+func parseWebSessionID(s string) (int64, bool) {
+	if !strings.HasPrefix(s, "web-") {
+		return 0, false
+	}
+	rest := strings.TrimPrefix(s, "web-")
+	idText, suffix, ok := strings.Cut(rest, "-")
+	if !ok || suffix == "" {
+		return 0, false
+	}
+	id, err := strconv.ParseInt(idText, 10, 64)
+	return id, err == nil && id > 0
+}
```

```diff
 // editionPlayback
+	sid, err := webSessionID(ed.ID)
+	if err != nil {
+		writeJSON(w, 500, map[string]string{"error": "session creation failed"})
+		return
+	}
 	writeJSON(w, 200, map[string]any{
-		"mode": "hls", "fileId": fileID, "sessionId": webSessionID(ed.ID),
+		"mode": "hls", "fileId": fileID, "sessionId": sid,
 	})
```

```diff
 // hlsFile
-	eid := auth.Atoi64(strings.TrimPrefix(sid, "web-"))
-	if eid <= 0 {
+	eid, ok := parseWebSessionID(sid)
+	if !ok {
 		writeJSON(w, 400, map[string]string{"error": "bad session id"})
 		return
 	}
```

### 3. [HIGH] Podcast deletion races active refresh/download work

- File: `internal/podcast/podcast.go` (`DeletePodcast`, `RefreshPodcast`, `RefreshAll`), `internal/podcast/download.go` (`downloadEpisode`)
- Problem: refresh operations are serialized through `Service.acquire`, but `DeletePodcast` bypasses that guard. A delete can therefore remove the podcast and episode rows while another goroutine is fetching an enclosure or applying a feed. In `downloadEpisode`, the file is renamed into its final location and a `files` row is inserted before `LinkEpisodeFile`; if deletion removed the episode in between, linking fails and the newly downloaded file/row is not cleaned up. The race can leave orphan database rows and files on disk.
- Fix: make deletion participate in the same per-podcast single-flight guard, expose a busy result to the handler, and roll back downloaded file state if the final episode link fails.

```diff
 func (s *Service) DeletePodcast(id int64) error {
 	p, err := s.DB.Podcast(id)
 	if err != nil {
 		return err
 	}
+	if !s.acquire(id) {
+		return ErrRefreshBusy
+	}
+	defer s.release(id)
+
 	files, err := s.DB.FilesForPodcast(id)
 	if err != nil {
 		return err
 	}
 	// existing deletion...
 }
```

```diff
 // core podcastDelete
 	err := svc.DeletePodcast(auth.Atoi64(r.PathValue("id")))
 	if errors.Is(err, store.ErrNotFound) {
 		writeJSON(w, 404, map[string]string{"error": "podcast not found"})
 		return
 	}
+	if errors.Is(err, podcast.ErrRefreshBusy) {
+		writeJSON(w, 409, map[string]string{"error": "podcast refresh/download in progress"})
+		return
+	}
```

```diff
 	fileID, err := s.DB.InsertPodcastFile(path, size, time.Now().Unix(),
 		fmt.Sprintf("%x-%d", h.Sum64(), size), derefFlt(ep.DurationSecs), ext)
 	if err != nil {
 		os.Remove(path)
 		return err
 	}
-	return s.DB.LinkEpisodeFile(ep.ID, fileID)
+	if err := s.DB.LinkEpisodeFile(ep.ID, fileID); err != nil {
+		_ = s.DB.DeleteFilesByIDs([]int64{fileID})
+		_ = os.Remove(path)
+		return err
+	}
+	return nil
```

### 4. [HIGH] Podcast URLs provide an SSRF primitive

- File: `internal/api/core/podcasts.go` (`podcastSubscribe`), `internal/podcast/fetch.go` (`FetchFeed`, `FetchBytes`), `internal/podcast/download.go` (`downloadEpisode`), `internal/podcast/podcast.go` (`New`)
- Problem: subscription only verifies that the initial feed URL has an `http` or `https` scheme. The standard HTTP transports can connect to loopback, RFC1918/ULA, link-local, or otherwise internal addresses and follow redirects to them. A public podcast feed can also supply arbitrary cover and enclosure URLs. Subscribing to an untrusted feed can therefore make libteca issue GET requests to local services, cloud metadata endpoints, NAS/admin interfaces, or other hosts reachable only from the server.
- Fix: use an egress transport that resolves and rejects non-public IPs at connection time, and apply the same rule on redirects for both normal and enclosure clients.

```go
package podcast

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func publicHTTPClient(totalTimeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 30 * time.Second
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}

		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		return nil, fmt.Errorf("podcast URL resolves only to non-public addresses")
	}

	return &http.Client{
		Transport: tr,
		Timeout:   totalTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return validateRemoteURL(req.URL)
		},
	}
}

func validateRemoteURL(u *url.URL) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("invalid remote URL")
	}
	return nil
}

func publicIP(ip net.IP) bool {
	return ip != nil &&
		!ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsUnspecified() &&
		!ip.IsMulticast()
}
```

```diff
 func New(db *store.DB, dataDir string) *Service {
-	client := &http.Client{Timeout: 30 * time.Second}
-	tr := http.DefaultTransport.(*http.Transport).Clone()
-	tr.ResponseHeaderTimeout = 30 * time.Second
+	client := publicHTTPClient(30 * time.Second)
+	dlClient := publicHTTPClient(0)
 	return &Service{
 		DB:          db,
 		DataDir:     dataDir,
 		Client:      client,
-		DLClient:    &http.Client{Transport: tr},
+		DLClient:    dlClient,
 		fetcher:     Fetcher{Client: client},
 		// ...
 	}
 }
```

### 5. [MEDIUM] Last-admin protection has a concurrent deletion race

- File: `internal/api/core/users.go` (`userDelete`), `internal/store/users.go` (`CountAdmins`, `DeleteUser`)
- Problem: `userDelete` checks `CountAdmins()` separately from `DeleteUser()`. With two admins A and B, A can request deletion of B while B simultaneously requests deletion of A. Both requests can observe two admins and both deletes can subsequently commit, leaving the installation with zero administrator accounts.
- Fix: move the target lookup, last-admin test, token collection, and deletion into one immediate transaction.

```go
// internal/store/users.go
var ErrLastAdmin = errors.New("cannot delete last admin")

func (d *DB) DeleteUserGuarded(id int64) ([]string, error) {
	var values []string
	err := d.Update(func(tx *Tx) error {
		var isAdmin bool
		if err := tx.QueryRow(`SELECT is_admin FROM users WHERE id = ?`, id).Scan(&isAdmin); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}

		if isAdmin {
			var n int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1`).Scan(&n); err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastAdmin
			}
		}

		rows, err := tx.Query(`SELECT value FROM tokens WHERE user_id = ?`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return err
			}
			values = append(values, v)
		}
		if err := rows.Close(); err != nil {
			return err
		}

		if _, err := tx.Exec(`DELETE FROM playback_sessions WHERE user_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM progress WHERE user_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM tokens WHERE user_id = ?`, id); err != nil {
			return err
		}
		_, err = tx.Exec(`DELETE FROM users WHERE id = ?`, id)
		return err
	})
	return values, err
}
```

```diff
-	if target.IsAdmin {
-		if n, err := a.DB.CountAdmins(); err == nil && n <= 1 {
-			writeJSON(w, 400, map[string]string{"error": "cannot delete last admin"})
-			return
-		}
-	}
-
-	values, err := a.DB.DeleteUser(id)
+	values, err := a.DB.DeleteUserGuarded(id)
+	if errors.Is(err, store.ErrLastAdmin) {
+		writeJSON(w, 400, map[string]string{"error": "cannot delete last admin"})
+		return
+	}
```

### 6. [MEDIUM] Playlist create and replacement are not atomic

- File: `internal/api/core/playlists.go` (`playlistCreate`), `internal/api/subsonic/subsonic.go` (`createPlaylist`), `internal/store/playlists.go`
- Problem: core creates the playlist row first and then calls `AddPlaylistItem` once per requested edition. If a later edition does not exist, the request returns 404 but the playlist and any earlier items remain committed. Subsonic replacement similarly renames and clears an existing playlist before re-adding songs, so a later database error leaves the original playlist destroyed or partially replaced.
- Fix: perform validation and the whole create/replace operation in one store transaction.

```go
// internal/store/playlists.go
func (d *DB) CreatePlaylistWithItems(userID int64, name string, editionIDs []int64) (int64, error) {
	var playlistID int64
	err := d.Update(func(tx *Tx) error {
		for _, eid := range editionIDs {
			var exists int
			if err := tx.QueryRow(`SELECT 1 FROM editions WHERE id = ?`, eid).Scan(&exists); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrNotFound
				}
				return err
			}
		}

		now := nowMilli()
		res, err := tx.Exec(
			`INSERT INTO playlists (user_id, name, created_at, updated_at) VALUES (?,?,?,?)`,
			userID, name, now, now,
		)
		if err != nil {
			return err
		}
		playlistID, err = res.LastInsertId()
		if err != nil {
			return err
		}

		for i, eid := range editionIDs {
			if _, err := tx.Exec(
				`INSERT INTO playlist_items (playlist_id, edition_id, position, added_at)
				 VALUES (?,?,?,?)`,
				playlistID, eid, i+1, now,
			); err != nil {
				return err
			}
		}
		return nil
	})
	return playlistID, err
}

func (d *DB) ReplacePlaylist(id int64, name *string, editionIDs []int64) error {
	return d.Update(func(tx *Tx) error {
		for _, eid := range editionIDs {
			var one int
			if err := tx.QueryRow(`SELECT 1 FROM editions WHERE id = ?`, eid).Scan(&one); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrNotFound
				}
				return err
			}
		}
		if name != nil {
			if _, err := tx.Exec(`UPDATE playlists SET name = ? WHERE id = ?`, *name, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`DELETE FROM playlist_items WHERE playlist_id = ?`, id); err != nil {
			return err
		}
		now := nowMilli()
		for i, eid := range editionIDs {
			if _, err := tx.Exec(
				`INSERT INTO playlist_items (playlist_id, edition_id, position, added_at) VALUES (?,?,?,?)`,
				id, eid, i+1, now,
			); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`UPDATE playlists SET updated_at = ? WHERE id = ?`, now, id)
		return err
	})
}
```

### 7. [MEDIUM] Pagination parameters can panic Jellyfin/ABS handlers

- File: `internal/api/jellyfin/jellyfin.go` (`userItems`, `respondItems`), `internal/api/jellyfin/podcasts.go` (`podcastItems`), `internal/api/abs/abs.go` and `internal/api/abs/podcasts.go` (`pageWindow`/pagination)
- Problem: Jellyfin parses `StartIndex` and `Limit` directly into signed integers. `respondItems` only clamps `start > total`; a negative `StartIndex` therefore reaches `items[start:end]` and panics. Huge positive limits can also overflow `start + limit`. The podcast handler duplicates the unsafe arithmetic. ABS uses `page * limit`, which can overflow for attacker-controlled large integers and likewise produce an invalid slice index. Neutron recovery prevents process termination, but authenticated clients can reliably force 500s and panic paths.
- Fix: centralize overflow-safe pagination and clamp negative inputs.

```go
func sliceWindow(start, limit, total int) (int, int) {
	if total < 0 {
		total = 0
	}
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}

	remaining := total - start
	if limit <= 0 || limit > remaining {
		limit = remaining
	}
	return start, start + limit
}

func pageWindow(page, limit, total int) (int, int) {
	if page < 0 {
		page = 0
	}
	if limit <= 0 {
		limit = 20
	}
	if total <= 0 {
		return 0, 0
	}

	// Avoid page*limit overflowing.
	if page > total/limit {
		return total, total
	}
	return sliceWindow(page*limit, limit, total)
}
```

```diff
 func (a *API) respondItems(w http.ResponseWriter, items []map[string]any, sortBy string, limit, start int) {
 	// sorting...
 	total := len(items)
-	if start > total {
-		start = total
-	}
-	if limit <= 0 {
-		limit = total - start
-	}
-	end := start + limit
-	if end > total {
-		end = total
-	}
+	start, end := sliceWindow(start, limit, total)
 	// ...
 }
```

The same helper should replace the manual `start/end` arithmetic in `podcastItems`.

### 8. [MEDIUM] Nonexistent Jellyfin season IDs cause a nil dereference

- File: `internal/api/jellyfin/jellyfin.go` (`detailFor`)
- Problem: the season branch parses IDs such as `s999999-1`, calls `WorkByID`, discards its error, and immediately dereferences `wv.LibraryID`. Any authenticated client can therefore send a syntactically valid season ID referencing a missing work and trigger a panic. The server's recover middleware converts this into a 500, but this is still an input-driven crash path and protocol failure instead of a normal 404.
- Fix: handle the lookup error before dereferencing the work.

```diff
 	if m := reSeasonItem.FindStringSubmatch(id); m != nil {
-		wv, _ := a.DB.WorkByID(auth.Atoi64(m[1]))
+		wv, err := a.DB.WorkByID(auth.Atoi64(m[1]))
+		if err != nil || wv == nil {
+			return nil, false
+		}
 		n, _ := strconv.Atoi(m[2])
-		works, _ := a.DB.WorksInLibrary(wv.LibraryID)
+		works, err := a.DB.WorksInLibrary(wv.LibraryID)
+		if err != nil {
+			return nil, false
+		}
 		for i := range works {
 			if works[i].ID == wv.ID {
 				return a.seasonItem(&works[i], n), true
 			}
 		}
 		return nil, false
 	}
```

### 9. [MEDIUM] Header-authenticated HLS loses authentication on child requests

- File: `internal/api/jellyfin/jellyfin.go` (`jfAuth`, `playbackInfo`, `hlsMaster`, `hlsSegment`), `internal/api/core/hls.go` (`rewriteHLSPlaylist`), `internal/auth/auth.go` (`Middleware`)
- Problem: authentication accepts headers as well as query tokens, but generated HLS URLs only propagate `api_key`/`token` when the incoming request already contained that query parameter. A client authenticated with `X-Emby-Token`, `Authorization`, or core `Bearer` can successfully obtain playback metadata or a manifest and then receive segment URLs with no credentials. Media stacks typically fetch playlist children as independent requests and cannot be assumed to reproduce the original custom header, so transcoding can fail at the first segment despite successful authentication.
- Fix: retain the authenticated token in request context and use it whenever an HLS URL must carry credentials.

```go
// internal/auth/auth.go
type contextKey int

const (
	userIDKey contextKey = iota
	tokenKey
)

func Token(r *http.Request) string {
	v, _ := r.Context().Value(tokenKey).(string)
	return v
}

func Middleware(db *store.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			value := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if value == "" {
				value = r.URL.Query().Get("token")
			}
			user, ok := UserForToken(db, value)
			if !ok {
				// existing 401
				return
			}
			ctx := context.WithValue(r.Context(), userIDKey, user.ID)
			ctx = context.WithValue(ctx, tokenKey, value)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
```

For Jellyfin, preserve the token similarly in `jfAuth` and expose a helper:

```go
func requestToken(r *http.Request) string {
	if t := qget(r, "api_key"); t != "" {
		return t
	}
	if t, _ := r.Context().Value(tokenContextKey).(string); t != "" {
		return t
	}
	return ""
}
```

Then use `requestToken(r)` instead of `qget(r, "api_key")` when constructing `TranscodingUrl` and rewriting playlists. Core HLS should likewise pass `auth.Token(r)` when no query token was supplied.

### 10. [MEDIUM] Oversized covers are silently stored as truncated files

- File: `internal/api/core/providers.go` (`downloadCover`), `internal/podcast/fetch.go` (`FetchBytes`)
- Problem: provider covers are read through `io.LimitReader(resp.Body, coverMaxBytes)` and podcast covers through a similar exact limit. Reading exactly the configured limit is treated as success even when the HTTP body is larger. The code writes the truncated bytes and then records the cover path in the database. Large JPEG/WebP/etc. responses can therefore permanently create corrupt cover files that continue to be advertised until manually replaced.
- Fix: read one byte beyond the maximum and reject bodies that exceed the cap.

```go
func readLimited(body io.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		return nil, fmt.Errorf("invalid size limit")
	}
	data, err := io.ReadAll(io.LimitReader(body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("response exceeds %d-byte limit", max)
	}
	return data, nil
}
```

```diff
 // providers.go
-	data, err := io.ReadAll(io.LimitReader(resp.Body, coverMaxBytes))
+	data, err := readLimited(resp.Body, coverMaxBytes)
 	if err != nil {
 		return false, err
 	}
```

```diff
 // podcast/fetch.go
-	return io.ReadAll(io.LimitReader(resp.Body, limit))
+	return readLimited(resp.Body, limit)
```

### 11. [MEDIUM] Deleting the podcasts library leaves podcast file state behind

- File: `internal/store/queries.go` (`DeleteLibrary`), `internal/podcast/podcast.go` (`DeletePodcast`)
- Problem: podcast download rows live in `files` with `edition_id = NULL`. `DeleteLibrary`'s normal file deletion only targets files belonging to editions. In its podcasts branch it deletes `podcast_episode_progress`, then `podcast_episodes`, then `podcasts`, losing the only links to those NULL-edition file rows. The generic library-delete endpoint also does not remove the corresponding files from `data/podcasts`. Deleting the podcasts library therefore leaves orphan database rows and downloaded media on disk.
- Fix: route podcast-library deletion through the podcast service while the episode-to-file relationship still exists, then delete the now-empty library.

```diff
 // internal/api/core/libraries.go
 func (a *API) deleteLibrary(w http.ResponseWriter, r *http.Request) {
 	if !a.requireAdmin(w, r) {
 		return
 	}
 	id := auth.Atoi64(r.PathValue("id"))
+
+	lib, err := a.DB.Library(id)
+	if errors.Is(err, store.ErrNotFound) {
+		writeJSON(w, 404, map[string]string{"error": "library not found"})
+		return
+	}
+	if err != nil {
+		writeJSON(w, 500, map[string]string{"error": "internal error"})
+		return
+	}
+
+	if lib.Type == "podcasts" {
+		pods, err := a.DB.Podcasts()
+		if err != nil {
+			writeJSON(w, 500, map[string]string{"error": "internal error"})
+			return
+		}
+		for i := range pods {
+			if pods[i].LibraryID != id {
+				continue
+			}
+			if err := a.Podcasts.DeletePodcast(pods[i].ID); err != nil {
+				writeJSON(w, 409, map[string]string{"error": "podcast library is busy"})
+				return
+			}
+		}
+	}
+
-	err := a.DB.DeleteLibrary(id)
+	err = a.DB.DeleteLibrary(id)
 	// existing error handling...
 }
```

As defense in depth, `store.DeleteLibrary` should also delete any still-linked podcast `files` rows before deleting `podcast_episodes`, so database integrity does not depend solely on the API caller.

### 12. [LOW] Transcode reaper goroutines cannot be stopped

- File: `internal/transcode/transcode.go` (`New`, `reaper`, `CloseAll`)
- Problem: every `Manager` starts `go m.reaper()`. The reaper loops forever with `time.Sleep`; `CloseAll` only kills current ffmpeg sessions and does not stop the goroutine. Normal process termination hides this in the CLI, but server reconstruction, embedding, and repeated handler lifecycles leak one goroutine per manager and retain the manager/data-directory object graph indefinitely.
- Fix: give the manager an explicit stop channel and make shutdown idempotently terminate the reaper.

```go
type Manager struct {
	DataDir string

	mu       sync.Mutex
	sessions map[string]*Session

	// existing fields...

	stop     chan struct{}
	stopOnce sync.Once
}

func New(dataDir string) *Manager {
	m := &Manager{
		DataDir:  dataDir,
		sessions: map[string]*Session{},
		stop:     make(chan struct{}),
	}
	// existing spawn/probe initialization...
	os.RemoveAll(filepath.Join(dataDir, "transcode"))
	go m.reaper()
	return m
}

func (m *Manager) reaper() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()

	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.mu.Lock()
			for id, s := range m.sessions {
				if time.Since(time.Unix(0, s.lastHit.Load())) > idleSessionTTL {
					s.kill()
					delete(m.sessions, id)
				}
			}
			m.mu.Unlock()
		}
	}
}

func (m *Manager) CloseAll() {
	m.stopOnce.Do(func() { close(m.stop) })

	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		s.kill()
		delete(m.sessions, id)
	}
}
```
