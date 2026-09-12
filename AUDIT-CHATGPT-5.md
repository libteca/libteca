# libteca - Audit Pass 5 (2026-09-12)

## Verdict

**Not clean: 7 demonstrable defects remain: 2 HIGH, 4 MEDIUM, 1 LOW.** This was a source-only confirmation sweep of the supplied current non-test Go source; no build/test execution is claimed.

The Pass-4 changes are substantially present. Re-verified as holding: Subsonic's own limiter key is principal-aware (`IP|normalized username`); scan-panic recovery persists counts and calls `FinishScanJob(..., "error")` before finishing the in-memory run; the `unrar` CBR path reads max+1 and kills an oversized child; ABS/Jellyfin slicing now goes through `pageWindow`/`sliceWindow`; the book-image max+1 checks are present; the specific invalid-provider `providerKeysPut` partial-commit bug is closed because every provider name is validated before the first write; and the PDF overlap arithmetic subtracts markers wholly contained in the carried tail. fileciteturn3file4L136-L142 fileciteturn13file0L7-L20 fileciteturn7file0L18-L38 fileciteturn13file2L63-L69 fileciteturn13file3L100-L113 fileciteturn13file4L137-L145 fileciteturn7file4L438-L468 fileciteturn13file1L34-L49

The findings below are concrete source-level failure paths; corpus/compatibility uncertainty and hypothetical failures without a source-driven trigger are not counted.

## Findings (if any)

### 1. [MEDIUM] Podcast deletion still snapshots `CoverPath` before acquiring the per-podcast exclusion

**File:** `internal/podcast/podcast.go` (`Service.DeletePodcast`, `Service.DeleteLibraryPodcasts`, `Service.RefreshPodcast`, `Service.fetchCover`)

`DeletePodcast` loads the `Podcast` row before `s.acquire(id)`, and `DeleteLibraryPodcasts` calls `DB.Podcasts()` before acquiring any of the library's `inflight` slots. Both later use those pre-lock objects for cover cleanup. In the library path, the comment says the slots are acquired before any cover snapshot, but `all := s.DB.Podcasts()` has already snapshotted `CoverPath`. fileciteturn7file1L104-L135 fileciteturn7file1L144-L195

Concrete interleaving: refresh A already owns its single-flight slot while `cover_path` is empty; deletion reads A with an empty `CoverPath`; refresh writes `covers/podcast-A.jpg`, persists that path, and releases the slot; deletion then acquires the now-free slot, deletes the rows, and performs disk cleanup from its stale empty cover snapshot. The newly-written cover survives as an orphan. The enclosure-path half of the Pass-4 fix is correct because `FilesForPodcast` is read after slot acquisition; the cover half is not. fileciteturn8file1L44-L53 fileciteturn8file0L15-L28 fileciteturn8file2L73-L76

Acquire exclusion before reading cover state. For library deletion, snapshot only stable IDs under `lifecycleMu`, acquire every slot, then re-read covers/files under those slots before the transaction and post-commit cleanup.

### 2. [MEDIUM] Deleting an empty podcasts library returns success without deleting the library row

**File:** `internal/podcast/podcast.go` (`Service.DeleteLibraryPodcasts`); `internal/api/core/libraries.go` (`API.deleteLibrary`)

`DeleteLibraryPodcasts` returns `nil` immediately when the library has zero subscriptions. Core then deliberately skips `DB.DeleteLibrary` for every podcasts library because the service is expected to delete the library row itself. fileciteturn7file1L151-L163 fileciteturn8file3L92-L119

Concrete failure: subscribe once (creating the Podcasts library), delete that last podcast individually, then `DELETE /api/core/libraries/{id}`. The service sees zero podcast IDs and returns success; core returns HTTP 200; the library row remains. The zero-subscription case must still execute the gated `DeletePodcastsLibrary` transaction.

### 3. [HIGH] Jellyfin session isolation is still cross-user bypassable after the Pass-4 fixes

**File:** `internal/api/jellyfin/ws.go` (`API.handleText`, `API.ReportPlayback`, `API.forwardCommand`, `hub.deviceOwnedBy`, `hub.socketUser`, `hub.sendTo`, `clientInfo`, `API.handleSocket`); `internal/api/jellyfin/jellyfin.go` (`ownsPlaySession`, `API.hlsMaster`, `API.sessionStopped`); `internal/transcode/transcode.go` (`Manager.Get`)

The initial `SessionsStart` response is correctly filtered with `sessionsMessageFor(c.user())`, but every later playback update calls `broadcast(a.sessionsMessage())`, where `sessionsMessage()` contains the global session table. Thus a normal user can subscribe, then trigger any own playback update and receive every other user's `UserId`, device ID, `PlaySessionId`, now-playing item and play state. fileciteturn7file2L217-L230 fileciteturn7file2L282-L332 fileciteturn2file2L86-L102

The transcode ownership check also explicitly treats every non-`u<uid>-` session ID as authorized for every user. `hlsMaster` accepts such client-supplied IDs and also generates a non-user-bound `t<edition>-<millis>` fallback. Concrete failure: victim V starts edition E1 with `PlaySessionId=legacy`; attacker A requests E2 with the same `PlaySessionId=legacy`; `ownsPlaySession` returns true for A, and `Manager.Get` kills V's existing session on the edition mismatch before replacing it. `sessionStopped` has the same globally-authorized non-`u` path to `TC.Close`. fileciteturn2file3L113-L124 fileciteturn3file2L75-L94 fileciteturn12file1L38-L55 fileciteturn12file0L8-L27

The socket-user command fix is also not bound to the exact destination socket: `deviceOwnedBy` and `sendTo` independently scan the connection map by client-controlled `DeviceId`. With two sockets using the same device ID, authorization can resolve the attacker's socket while delivery resolves the victim's socket; disconnecting the attacker duplicate between the two calls makes that interleaving deterministic. Missing-`DeviceId` WebSockets make collisions easier because `handleSocket` authenticates `user` separately but calls `clientInfo(r)` without putting that user into request context, so its fallback is `dev-0`. fileciteturn2file4L157-L175 fileciteturn3file1L40-L56 fileciteturn7file2L233-L264 fileciteturn10file1L39-L55 fileciteturn11file0L13-L61

Session updates need per-recipient filtering, every server/client HLS session ID that can reach `Manager.Get`/`Close` needs owner binding, and command authorization must resolve-and-deliver to the same authenticated socket in one hub operation (or key sockets by user plus device).

### 4. [MEDIUM] Principal-aware limiter keys were added only to Subsonic; the other password faces retain the same bucket-reset bypass

**File:** `internal/server/server.go` (`Server.Handler`); `internal/api/core/core.go` (`API.login`); `internal/api/abs/abs.go` (`API.Login`); `internal/api/jellyfin/jellyfin.go` (`API.authenticate`); `internal/api/opds/opds.go` (`API.auth`); `internal/auth/limiter.go` (`Limiter.Success`)

One `Limiter` instance is shared by all five faces. Subsonic now uses `IP|normalized username`, but core, ABS, Jellyfin and OPDS still call `Allow`, `Failure` and `Success` with only the bare client IP; `Success` deletes that whole key. fileciteturn7file3L362-L403 fileciteturn3file4L136-L142 fileciteturn9file0L7-L27 fileciteturn9file1L34-L54 fileciteturn4file2L61-L81 fileciteturn4file3L88-L108 fileciteturn4file4L118-L122

Concrete bypass: from one IP, make four bad password attempts against victim V on Jellyfin (or core/ABS/OPDS), then perform one successful login as attacker A on any bare-IP face. `Success(ip)` erases V's four failures. Repeating `4 guesses -> own success` prevents the five-failure lockout indefinitely. The shared limiter even permits the reset to cross protocol faces. Principal-aware keys need to be used by every credential path, optionally alongside a separate coarse IP bucket.

### 5. [HIGH] The CBR `unar` path still materializes unbounded output before enforcing the 20 MiB cap

**File:** `internal/scan/books.go` (`cbrExtract`, `cbrList`)

The `unrar` branch is fixed: it reads max+1, kills the child on oversize, and waits. The `unar` branch, however, runs `unar ... -o <tmp>` to completion before it stats the extracted file. The size check is therefore only a post-extraction read guard, not an extraction resource cap. A small compressed CBR whose first image expands to tens or hundreds of gigabytes can fill the temp filesystem before the code ever observes `fi.Size() > cbrMaxCoverBytes`; scan cancellation also cannot stop this `exec.Command(...).Run()` because no context is attached. fileciteturn7file0L18-L77

The archive-listing side is likewise unbounded: `cbrList` calls `cmd.Output()` and buffers the extractor's complete listing before `cbrPageNames` can apply the 2,000-page limit. An archive with a very large entry table can therefore consume memory proportional to the full listing. fileciteturn5file1L47-L67

Preflight/reject the selected entry by trustworthy uncompressed-size metadata or extract under a real file-size/quota boundary and scan context; stream and cap the listing rather than using `Output()`.

### 6. [MEDIUM] `updatePlaylist` validates first but still commits a failed request partially

**File:** `internal/api/subsonic/subsonic.go` (`API.updatePlaylist`); `internal/store/playlists.go` (`DB.RemovePlaylistItem`, `DB.AddPlaylistItem`, `DB.RenamePlaylist`)

The handler now resolves the removal snapshot and all added song IDs before mutation, but it still applies each removal/addition and the rename through separate store transactions. fileciteturn5file2L77-L100 fileciteturn6file3L87-L115

Concrete single-request failure: playlist `[A,B]`, request `songIndexToRemove=0&songIndexToRemove=0`. Both values are valid against the pre-request snapshot and resolve to `[A,A]`. The first `RemovePlaylistItem` transaction commits deletion of A; the second finds no A, returns `ErrNotFound`, and the handler emits an internal-error response. The request failed after permanently changing the playlist. fileciteturn6file4L126-L140

The validated delta needs one store transaction covering removals, additions, rename and position compaction. Deduplicating removal IDs would close this trigger but would not provide failure/concurrency atomicity for the complete request.

### 7. [LOW] OPDS pagination still performs raw overflowing page arithmetic

**File:** `internal/api/opds/opds.go` (`pageParam`, `libraryFeed`, `allFeed`, `inProgressFeed`, `newestFeed`, `searchFeed`, `emitAcquisition`)

`pageParam` accepts any positive `int`, and the feed handlers pass `pageParam(r) * pageLimit` directly as the SQL offset; `emitAcquisition` separately computes `(page+1)*pageLimit`. fileciteturn6file1L37-L50 fileciteturn6file2L67-L78 fileciteturn5file4L143-L160

On a 64-bit build, `page=9223372036854775807` makes `page*50` wrap to `-50`, so SQLite treats the offset as the beginning of the result set rather than the requested page. `page+1` also wraps, producing a nonsensical negative next-page link that `pageParam` subsequently maps back to page 0. This is the same overflow class already fixed in ABS/Jellyfin, missed in OPDS. Clamp before multiplication or use an overflow-safe page-window/offset helper.
