# libteca - Audit Pass 4 (2026-09-12)

## Verdict

**Not clean: 10 demonstrable defects remain: 3 HIGH, 5 MEDIUM, 2 LOW.** This was a confirmation sweep of the supplied current non-test Go source; no build/test execution is claimed.

The Pass-3 egress changes were re-verified: `publicHTTPClient` disables proxy inheritance, rejects the intended non-public ranges including RFC 6598, and now tries every public DNS answer before failing. The production podcast constructor also correctly splits the 30-second feed client from the no-total-timeout enclosure client. All four scanner `DirEntry.Info()` sites now guard errors, and `applyFiles` now fails on a vanished planned file instead of compacting the ID slice. Those fixes hold.

The remaining defects are concentrated in incomplete lifecycle/session/recovery/resource fixes, plus two previously-audited pagination/atomicity classes that still have uncovered callers.

## Findings

### 1. [HIGH] Podcast-library deletion still has two lifecycle races and can orphan newly-created media

**File:** `internal/podcast/podcast.go` (`Service.DeleteLibraryPodcasts`, `Service.Subscribe`); `internal/api/core/libraries.go` (`deleteLibrary`); `internal/store/queries.go` (`DB.DeleteLibrary`)

`lifecycleMu` closes the original subscribe-vs-snapshot window, but it does not cover the complete destructive operation, and the existing per-podcast locks are acquired too late.

First, `DeleteLibraryPodcasts` snapshots covers and `FilesForPodcast` paths **before** it acquires the library's per-podcast `inflight` slots. A concrete interleaving is: deletion snapshots podcast A's old paths; an already-running refresh downloads and links a new enclosure (or writes a new cover), then finishes and releases A's slot; deletion subsequently checks `inflight`, now sees A idle, acquires it, and `DB.DeleteLibraryPodcasts` deletes the new file row. The post-commit disk cleanup uses the stale path/cover snapshot, so the newly-created bytes remain orphaned.

Second, `DeleteLibraryPodcasts` releases `lifecycleMu` when it returns, while core only then calls `DB.DeleteLibrary(id)`. A subscribe whose feed fetch is already complete can acquire the gate in that gap, `EnsurePodcastsLibrary` can return the still-existing library, and `AddPodcast` can publish/acquire the new subscription. Core then deletes that podcast and the library underneath the active subscribe. A cover written after that deletion is orphaned because `fetchCover` ignores `SetPodcastCover` failure. If an episode file was linked before the final library transaction, the defense-in-depth branch in `DB.DeleteLibrary` also fails to remove it: it deletes `podcast_episodes` first and only afterwards selects `file_id` from `podcast_episodes`, so that file subquery is necessarily empty.

The per-podcast slots need to be acquired before path/cover snapshotting, and the lifecycle gate must stay held through deletion of the library row. The store-side podcast-library delete should capture file IDs before deleting episode rows and delete podcast children plus the library row in the same gated operation.

### 2. [HIGH] Jellyfin session ownership is still bypassable across users

**File:** `internal/api/jellyfin/jellyfin.go` (`ownsPlaySession`, `hlsMaster`, `sessionStopped`); `internal/api/jellyfin/ws.go` (`handleText`, `sessionsMessage`, `deviceOwnedBy`, `sendTo`, `ReportPlayback`); `internal/transcode/transcode.go` (`Manager.Get`, `Manager.Close`)

The HTTP `/Sessions` list is filtered, but WebSocket `SessionsStart` is not: every authenticated socket gets `sessionsMessage()`, and that message is built from the global `sessionDTOs()` snapshot. It exposes other users' `DeviceID` and `PlaySessionId` values.

That leak is directly exploitable through two unchecked transcode paths. `sessionStopped` applies `ownsPlaySession` only to the **query-string** `PlaySessionId`; when the ID is supplied only in the JSON body, `saveFromSession` returns it and `sessionStopped` calls `TC.Close` without an ownership check. An ordinary user can therefore take a victim ID learned from WebSocket `SessionsStart` and stop the victim's ffmpeg session. Likewise, `hlsMaster` accepts any syntactically valid `PlaySessionId` and passes it straight to `Manager.Get`; if the attacker requests a different edition under the victim ID, `Manager.Get` kills the victim's existing session before replacing it.

The `deviceOwnedBy` fix is also poisonable because ownership is taken from `hub.sessions`, keyed only by client-supplied `DeviceID`, rather than from the target socket. An attacker can learn victim device D from the global WebSocket snapshot, report their own playback over HTTP while claiming `DeviceId=D`, thereby replacing `sessions[D]` with the attacker's user ID, then send a command from a different attacker socket targeting D. `deviceOwnedBy` now passes, and `sendTo(D, ...)` can deliver the command to the victim socket whose actual device is D.

WebSocket session snapshots must be filtered by the authenticated socket user, every supplied/generated play-session ID must be owner-bound at the transcode manager (not only parsed by prefix at selected handlers), and command authorization must verify the actual target connection's authenticated user rather than mutable playback state keyed by `DeviceID`.

### 3. [MEDIUM] The Subsonic token oracle can still bypass lockout by resetting the shared IP bucket

**File:** `internal/api/subsonic/subsonic.go` (`API.authenticate`); `internal/auth/limiter.go` (`Limiter.Success`); `internal/server/server.go` (`Server.Handler`)

The `t`/`s` branch now calls `Allow`, `Failure`, and `Success`, but the limiter key is still only `ClientIP`, and `Success(ip)` deletes the entire bucket. The same limiter instance is shared by all protocol faces.

Concrete bypass: an attacker with any valid libteca account makes four wrong Subsonic token guesses against victim V (below the five-failure lockout), then performs one successful login as attacker A from the same IP. That successful login calls `Success(ip)` and erases V's four failures. Repeating `4 victim guesses -> 1 own success` permits unlimited MD5 candidate tests against any victim whose Subsonic plaintext secret has been cached, preserving the cheap online password oracle that Pass 3 was meant to bound.

Use a principal-aware key such as `IP + normalized username` for credential failures/successes (optionally alongside a separate coarse IP bucket), so a success for A cannot erase failures against V.

### 4. [MEDIUM] `runScan` recovers a panic in memory but leaves the persistent scan job permanently `running`

**File:** `internal/api/core/core.go` (`API.runScan`, `API.scanLibrary`, `API.TriggerScan`); `internal/watch/watch.go` (`Watcher.waitForJob`, `Watcher.scanInFlight`)

The new recovery defer calls `run.finish("error", ...)`, but it never calls `DB.FinishScanJob`. On any panic reaching that defer, the normal persistence path is skipped. The other defer removes the run from `a.runs`, while the `scan_jobs` row remains `status='running'`.

The concrete aftermath is stable: watcher `waitForJob` polls that row forever; HTTP `scanLibrary` sees the stale running row and returns 409; `TriggerScan` returns `ErrScanRunning`; and the library cannot be scanned again until process restart invokes `FailRunningScanJobs` (or the database is repaired manually). The recovery path must persist terminal `error` status/counts before finishing the in-memory run.

### 5. [HIGH] The CBR 20 MiB extraction cap can deadlock `unrar`, while the `unar` path is not actually capped during extraction

**File:** `internal/scan/books.go` (`cbrExtract`)

For `unrar`, the code starts a child with `StdoutPipe`, reads exactly `cbrMaxCoverBytes` through `io.LimitReader`, then immediately calls `cmd.Wait()`. If the first page expands beyond 20 MiB by more than the OS pipe buffer, the parent stops reading at 20 MiB while `unrar` blocks trying to write the remainder; `cmd.Wait()` waits for that blocked child forever. Because this uses `exec.Command` rather than `CommandContext`, scan cancellation cannot break the deadlock. For a smaller overage that happens to fit in the pipe, the function instead silently returns a truncated image.

For `unar`, the limit is applied only **after** `unar` has fully extracted the selected entry into a temporary directory. A decompression-bomb first page can therefore consume unbounded disk before the 20 MiB read limit is consulted, and a successful oversized extraction is again silently truncated rather than rejected.

The extractor must detect `max+1`, reject oversize output, and terminate/drain the child safely under the scan context; the `unar` path needs an extraction strategy/resource limit that applies before an arbitrarily large temporary file is materialized.

### 6. [MEDIUM] Pagination overflow panics remain in two audited callers

**File:** `internal/api/abs/abs.go` (`API.items`); `internal/api/jellyfin/jellyfin.go` (`API.nextUp`)

The overflow-safe helpers exist, but these callers still perform raw integer arithmetic before slicing.

On the supported 64-bit release targets, an ABS request with `page=9223372036854775807&limit=20` makes `start := page * limit` wrap to `-20`; the subsequent `results[start:end]` slice panics. Jellyfin `NextUp` has the same class: with at least one result, `StartIndex=1&Limit=9223372036854775807` makes `end := start + limit` wrap negative and the slice panics. Global request recovery converts these to repeatable authenticated 500s rather than process death, but the Pass-1 pagination fix is not complete.

`API.items` should use `pageWindow`, and `API.nextUp` should use `sliceWindow` (or equivalent overflow-safe clamping) before slicing.

### 7. [MEDIUM] Book-image size caps still accept and persist/serve truncated images

**File:** `internal/scan/epub.go` (`parseEPUB`); `internal/scan/books.go` (`probeCBZ`); `internal/api/opds/cbz.go` (`readZipPage`); `internal/api/opds/opds.go` (`psePage`)

These paths use `io.ReadAll(io.LimitReader(..., 20<<20))` with no `+1` overflow check. `LimitReader` reports clean EOF at the artificial limit, so an oversized image is indistinguishable from an exactly-20-MiB image.

For EPUB and CBZ scanning, a cover larger than 20 MiB is truncated and then `writeBookCover` persists the truncated bytes as a valid cover. For OPDS-PSE, a CBZ page larger than 20 MiB is returned with HTTP 200 and `Content-Length` equal to the truncated prefix, so the reader receives a corrupt image rather than a complete page or an explicit size error. This is the same truncation class already fixed for provider and podcast covers, but these book paths were missed.

Read one byte past the cap and reject oversize covers/pages, or stream the full PSE page under an explicit policy instead of silently truncating it.

### 8. [MEDIUM] Subsonic `updatePlaylist` can return an error after permanently applying earlier mutations

**File:** `internal/api/subsonic/subsonic.go` (`API.updatePlaylist`); `internal/store/playlists.go` (`RemovePlaylistItem`, `AddPlaylistItem`, `RenamePlaylist`)

The handler commits removals first, validates appended `songId` values afterwards, appends one row at a time, and renames last. These operations are separate transactions.

Concrete failure: a playlist contains song A; the request asks for `songIndexToRemove=0` and also supplies a nonexistent `songId`. `RemovePlaylistItem` commits deletion of A, then `resolveSongIDs` reports "Song not found" and the request fails. The client receives a failed update even though its playlist was already modified. Later add/rename failures can similarly leave a partially-applied request.

Resolve/validate the complete requested change first and apply removals, additions, and rename in one store transaction.

### 9. [LOW] The streamed PDF overlap logic double-counts some page markers at chunk boundaries

**File:** `internal/scan/books.go` (`pdfPageCount`)

The streaming rewrite retains the last `len("/Type /Pages")` bytes of every chunk and prepends that tail to the next chunk, but it counts all matches in the combined string each time. If a complete `"/Type /Page"` marker lies wholly within the final 12 bytes of a 1 MiB chunk, it is counted once in that chunk and then counted again from the carried tail on the next iteration. A PDF constructed with one such marker therefore reports two pages from one raw marker.

The overlap should carry only what is necessary to detect matches that actually cross the boundary, without recounting matches wholly contained in the previous chunk.

### 10. [LOW] Provider-key updates can commit a setting and then return 400 for the same request

**File:** `internal/api/core/settings.go` (`API.providerKeysPut`)

The handler validates and mutates in one iteration over the decoded `map[string]string`. Go map iteration order is unspecified. For a request such as `{"keys":{"tmdb":"new-key","audible":"x"}}`, an execution that visits `tmdb` first commits the new TMDB key, then visits `audible`, rejects it as a provider that needs no key, and returns HTTP 400. The same partial side effect occurs with a valid key followed by an unknown key.

Validate the entire key set before any write, then apply the validated changes atomically so an error response cannot hide a committed partial update.
