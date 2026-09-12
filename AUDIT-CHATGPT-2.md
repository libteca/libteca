# libteca - Audit Pass 2 (2026-09-12)

## Verdict

**3 new findings.** Re-checked the current non-test Go source from scratch, with extra scrutiny on the new `publicHTTPClient` egress guard (DNS resolution, IPv4/IPv6 classification, redirects, proxy behavior), `DeleteUserGuarded`, `CreatePlaylistWithItems` / `ReplacePlaylist`, Jellyfin/ABS pagination clamps, unique HLS session IDs and reaping/close paths, podcast single-flight deletion, podcast-library deletion routing, token-in-context propagation, bounded cover reads, and the reaper stop channel. The 12 findings recorded as fixed in `AUDIT-CHATGPT.md` / `AUDIT_OPEN.md` are not repeated here. fileciteturn1file0L19-L34

## Findings

### 1. [HIGH] The podcast egress guard can still reach non-public destinations through proxies and shared-address space

**File:** `internal/podcast/podcast.go` (`publicHTTPClient`, `publicIP`)

**Problem:** `publicHTTPClient` clones `http.DefaultTransport` but never clears its `Proxy` function. The default transport honors `HTTP_PROXY` / `HTTPS_PROXY`; when a proxy is selected, `DialContext` receives the proxy address, so the custom resolver validates and connects to the proxy rather than validating the URL destination. A configured public proxy can therefore forward a podcast request to an internal target that the direct dial guard would have rejected. Separately, `publicIP` only excludes `IsPrivate`, loopback, link-local, multicast, and unspecified addresses; Go's `net.IP.IsPrivate` does not include RFC 6598 shared address space `100.64.0.0/10`, so Tailscale/shared-address destinations remain allowed even though they are not public Internet endpoints. That is especially relevant to libteca's documented Tailscale deployment model. fileciteturn3file0L25-L74

**Fix with concrete code:** disable proxy inheritance for this SSRF-sensitive transport and explicitly reject shared-address space (preferably centralize all denied special-use prefixes with `net/netip`):

```go
var sharedV4 = netip.MustParsePrefix("100.64.0.0/10")

func publicHTTPClient(totalTimeout time.Duration) *http.Client {
    dialer := &net.Dialer{Timeout: 10 * time.Second}
    tr := http.DefaultTransport.(*http.Transport).Clone()
    tr.Proxy = nil // never let an environment proxy bypass destination validation
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
    // existing Timeout / CheckRedirect...
}

func publicIP(ip net.IP) bool {
    if ip == nil || ip.IsLoopback() || ip.IsPrivate() ||
        ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
        ip.IsUnspecified() || ip.IsMulticast() {
        return false
    }
    if a, ok := netip.AddrFromSlice(ip); ok && sharedV4.Contains(a.Unmap()) {
        return false
    }
    return true
}
```

### 2. [HIGH] The new podcast deletion path is not failure-atomic and can partially destroy a library

**File:** `internal/api/core/libraries.go` (`deleteLibrary`); `internal/podcast/podcast.go` (`DeletePodcast`); `internal/store/podcasts.go` (`DeletePodcast`, `DeleteFilesByIDs`); `internal/store/queries.go` (`DeleteLibrary`)

**Problem:** the fix routes podcast-library deletion through `Service.DeletePodcast`, but does so one subscription at a time. If podcast A is deleted successfully and podcast B is currently refreshing/downloading, B returns `ErrRefreshBusy` and the handler returns HTTP 409 after A and its media have already been permanently removed. A failed library deletion can therefore be destructive and partial. fileciteturn6file0L36-L61

The single-podcast path has the same failure-atomicity problem at the database layer: `Service.DeletePodcast` first commits `DB.DeletePodcast` (removing episodes and the subscription) and only then calls `DeleteFilesByIDs` in a separate transaction. If that second database operation fails, the subscription is gone but its `files` rows are orphaned and the function returns an error. fileciteturn6file2L389-L427 fileciteturn9file1L174-L197

The new `store.DeleteLibrary` defense-in-depth path also orders its statements incorrectly for the store's own FK invariant: it deletes podcast `files` before deleting/clearing the `podcast_episodes.file_id` references, while `DeleteFilesByIDs` explicitly documents that callers must clear episode links first. Foreign keys are enabled for every connection, so the fallback is not a valid independent cleanup path for a library that still has downloaded episodes. fileciteturn3file3L591-L615 fileciteturn9file1L174-L197 fileciteturn9file0L46-L72

**Fix with concrete code:** make podcast row cleanup one store transaction and make library deletion acquire every per-podcast single-flight lock before deleting anything. Capture file paths/cover paths before the transaction; remove disk files only after commit.

```go
// store: delete subscription + episode rows + file rows atomically.
func (d *DB) DeletePodcastWithFiles(id int64) error {
    return d.Update(func(tx *Tx) error {
        rows, err := tx.Query(`SELECT file_id FROM podcast_episodes
            WHERE podcast_id = ? AND file_id IS NOT NULL`, id)
        if err != nil {
            return err
        }
        var fileIDs []int64
        for rows.Next() {
            var fid int64
            if err := rows.Scan(&fid); err != nil {
                rows.Close()
                return err
            }
            fileIDs = append(fileIDs, fid)
        }
        if err := rows.Close(); err != nil {
            return err
        }
        if err := rows.Err(); err != nil {
            return err
        }

        if _, err := tx.Exec(`DELETE FROM podcast_episodes WHERE podcast_id = ?`, id); err != nil {
            return err
        }
        if len(fileIDs) > 0 {
            q, args := inClause(fileIDs)
            if _, err := tx.Exec(`DELETE FROM files WHERE id IN (`+q+`)`, args...); err != nil {
                return err
            }
        }
        res, err := tx.Exec(`DELETE FROM podcasts WHERE id = ?`, id)
        if err != nil {
            return err
        }
        if n, _ := res.RowsAffected(); n == 0 {
            return ErrNotFound
        }
        return nil
    })
}
```

For a podcast library, add a service operation that gathers all podcast IDs, acquires all of their `inflight` slots under one `s.mu` critical section (or fails before deleting anything), gathers removable paths, executes one corrected `DB.DeleteLibrary` transaction, then releases the locks and removes disk files best-effort. In `DB.DeleteLibrary`, capture podcast file IDs before deleting episodes, then delete episodes first and file rows second; do not issue the current FK-invalid `DELETE FROM files ... SELECT file_id FROM podcast_episodes ...` while those episode references still exist.

### 3. [MEDIUM] Jellyfin podcast child URLs still drop header-authenticated tokens

**File:** `internal/api/jellyfin/podcasts.go` (`podcastEpisodeItem`); `internal/api/jellyfin/jellyfin.go` (`jfAuth`, `requestToken`)

**Problem:** the Pass 1 HLS fix correctly stores the authenticated Jellyfin token in request context and adds `requestToken`, which falls back to that context token when `api_key` is absent. `podcastEpisodeItem`, however, still builds its `TranscodingUrl` using only `qget(r, "api_key")`. A request authenticated with `X-Emby-Token`, `Authorization`, or `X-Emby-Authorization` can list a downloaded podcast episode successfully, but the returned `/Audio/podcast/.../stream` URL contains no credential. That child request is mounted under the same `jfAuth` group and can therefore fail with 401 when the client follows the URL without replaying the original custom header. fileciteturn9file3L318-L375 fileciteturn9file2L244-L282

**Fix with concrete code:** use the new token helper consistently for every generated Jellyfin child URL, not just HLS:

```go
url := "/Audio/podcast/" + epID + "/stream"
if key := requestToken(r); key != "" {
    url += "?api_key=" + neturl.QueryEscape(key)
}
```

Import `net/url` with an alias if needed to avoid the local `url` variable name. The same rule should be applied to any future Jellyfin response field that emits an authenticated child URL: always source the credential through `requestToken(r)` rather than directly from the query string.
