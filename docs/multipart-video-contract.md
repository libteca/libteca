# Multipart video contract preparation

Status: proposed API and pure executable model only. No handler, database schema, media element, HLS ticket, transcode process, thumbnail cache, or compatibility adapter is changed by these files. The endpoint names and status codes below are proposals, not shipped routes. This does not close the multipart video/HLS acceptance gate.

## Current implementation and scope

The shipped `GET /api/core/editions/{id}/playback` handler in `internal/api/core/hls.go` chooses `ed.Files[0]`. Its `PlaybackInfo` in `web/src/api.ts` contains `mode`, `fileId`, and optional `sessionId`, without generation or offset. The HLS handler opens the first edition file; edition thumbnails do likewise. The current first-party player uses client-side resume against the full existing resource, without adding a server-truncation `start` parameter. None of these semantics change here.

Playlist audio already resolves an ordered edition queue and cumulative positions in `web/src/players/editionAudio.tsx`; primary audio also has cumulative seek logic. These runtime implementations are not replaced or imported by this preparation. `web/src/contracts/mediaTimeline.ts` is an isolated, side-effect-free candidate shared resolver, imported only by its contract test. Audio and video integration remains a separate, reviewed change.

This work implements the safe preparation in `media-consistency-proposal.md` section 3. It relies on a future source-generation decision in `source-identity-proposal.md`; it does not invent a persistent content identity from titles, paths, modification timestamps, or ordered IDs alone.

## Timeline model

`createMediaTimeline(editionId, generation, files)` consumes the authoritative ordered membership list. It never sorts by numeric file ID, modifies input, consults a global cache, or fetches metadata. It copies and freezes its result. IDs must be positive JavaScript-safe integers, unique within the edition; the edition must contain at least one file. Missing/unavailable files must not be silently removed by an adapter, since that would change order and cumulative positions. The future server must return a changed generation or an explicit availability failure.

The generation is a nonblank opaque server-issued string. Exact equality is required. Its persistence, atomic publication, generation allocation and source evidence are not implemented here. A future implementation must change it whenever ordered membership, content identity, or authoritative duration changes. Equal file IDs alone do not establish equal content. A generation must never be reused for different content/order/duration state. A display-only title change need not change it. Generation mismatch always requires refetch; this resolver does not silently rebase a file through a content replacement.

Each entry has `fileId`, `durationSecs` and derived `startSecs`. The array order is the playback order. There is one derived `totalDurationSecs`. All positions are seconds, not milliseconds, percentages, or HLS media sequence numbers.

Duration policy proposed by this model:

- Positive finite durations up to `Number.MAX_SAFE_INTEGER` are valid
- `null` means explicitly unknown; it is never interpreted as zero
- Zero, negative, omitted, string, NaN and infinite durations are rejected
- Overflow and a positive span swallowed by floating-point precision are rejected
- `startSecs` is known through the beginning of the first unknown file; every subsequent start and the total become `null`
- An explicit file ID plus nonnegative finite offset is still resolvable after an unknown duration; its cumulative position remains `null`
- An explicit offset inside a file with unknown duration is provisionally accepted because an upper bound is unavailable; this is not evidence that the offset is seekable or within that file
- A cumulative seek can resolve a known prefix or exactly the start of the first unknown file. It cannot guess which file contains a later cumulative position

Legacy records sometimes use zero for missing duration. An eventual adapter must deliberately map that state to `null`, or reject it and request reprobe. It must not silently treat a genuine zero-length file as a playable span. This proposal intentionally does not alter today's audio zero-duration behavior.

`resolveFilePosition` and `resolveEditionPosition` both require a matching generation and return the full edition/generation/file/file-offset/cumulative tuple. Negative, nonfinite, overflowing and known out-of-range positions are rejected, not clamped. An absent selected file is rejected even when the edition itself exists. Selection functions assume their timeline came from `createMediaTimeline`; they are not a general-purpose parser for arbitrary timeline JSON.

Known intervals are half-open: at an interior cumulative boundary the next file owns offset zero. Exactly the total duration belongs to the final file at its end. An explicit file-local offset equal to a nonfinal file's duration preserves that file endpoint; it is not rewritten to the next file. This distinction lets the caller represent a final sample from the old file while resolving a seek to the next file. There is no epsilon window that silently moves a near-boundary seek. Clients should use the server's authoritative start values and the same seconds convention rather than rounded display values.

`resolveEndedFile` consumes an explicit, current file-ended event. A nonfinal file advances to the next membership entry at offset zero; it never emits completion. A final-file event emits completion only when the entire timeline has known durations, producing the exact final tuple. Unknown final or preceding duration returns `unknown_duration`, requiring metadata refresh and a new generation before a cumulative completion can be committed. Reaching the end through `resolveEditionPosition` alone does not mark playback finished. The caller must fence the event before invoking this helper; it does not independently prove that media ended.

## Proposed HTTP v1 fixtures

These proposed routes intentionally separate explicit multipart opt-in from the shipped legacy GET. No caller should send them until server support is actually implemented and advertised. A legacy response cannot satisfy the v1 response validator. Preserve the current single-file legacy route while implementing an additive or explicitly versioned route in a future patch.

### Timeline retrieval

Proposed: `GET /api/core/editions/7/timeline`, response `200`:

```json
{
  "contractVersion": 1,
  "editionId": 7,
  "generation": "content-order-17",
  "files": [
    { "fileId": 41, "durationSecs": 40, "startSecs": 0 },
    { "fileId": 9, "durationSecs": 60, "startSecs": 40 },
    { "fileId": 73, "durationSecs": 20, "startSecs": 100 }
  ],
  "totalDurationSecs": 120
}
```

This fixture is serialized in the tests. A server must obtain membership, durations and generation from one consistent snapshot, not independent reads that could straddle a scan. The future JSON parser must validate the version, membership and numeric fields and verify advertised derived offsets rather than blindly trusting them. There is no timeline-fetch handler or client parser in this preparation.

### Selected-file playback creation

Proposed: `POST /api/core/editions/7/playback-sessions`:

```json
{
  "contractVersion": 1,
  "requestId": "attempt-2",
  "generation": "content-order-17",
  "fileId": 9,
  "fileOffsetSecs": 25,
  "mode": "auto"
}
```

`mode` is `auto` or `hls`. `requestId` is a fresh opaque client attempt identifier for every selection, seek, retry or direct-to-HLS fallback, including selecting the same file again. It is an echo/fencing key, not an authentication credential, durable operation receipt, or idempotency guarantee. It must not be reused across login changes or player lifetimes. The edition comes from the route, not a caller-selected alternate owner in the body.

Successful direct response `201`:

```json
{
  "contractVersion": 1,
  "requestId": "attempt-2",
  "editionId": 7,
  "generation": "content-order-17",
  "fileId": 9,
  "fileOffsetSecs": 25,
  "editionPositionSecs": 65,
  "mediaTimeOrigin": "file-start",
  "mediaStartSecs": 0,
  "mode": "direct"
}
```

An HLS success has the same tuple, `mode: "hls"`, and a nonblank `sessionId`. A direct success has no HLS session ID. These fixtures deliberately contain no credentials or live URLs.

`proposePlaybackHTTP` models body validation, route-edition membership, generation, offset checks, mode selection and the above response. The negotiated mode and optional issued ticket are supplied by a test fixture; this function does not inspect codecs, allocate sessions, authenticate callers, create URLs, or perform HTTP. It does not test those runtime behaviors.

| Proposed status | Condition |
|---|---|
| 201 | Valid, matching selected-file request with available negotiated mode |
| 400 | Unsupported version, malformed/missing fields, invalid ID/offset or mode |
| 404 | Edition unavailable in the fixture, or file is not a member |
| 409 | Content/order/duration generation changed |
| 422 | Offset exceeds a known file duration, or cannot be represented safely |
| 503 | HLS requested/negotiated but no session is available |

Error bodies contain an `error` code. Fixtures include JSON serialization, but do not invoke the shipped Go router. Authorization and resource-opening checks remain mandatory in eventual handlers and are outside this pure contract and the excluded focused security review.

## Direct, HLS and replacement-session invariants

The proposed media resource always spans the full selected file, with `mediaTimeOrigin: "file-start"` and `mediaStartSecs: 0`. The selected resume offset is applied by the client: native direct/native HLS `currentTime` when seekable, or hls.js `startPosition`. The client derives edition position from the selected file's start plus media time. It must not add the saved offset again. This preserves the current full-resource/client-side-resume approach rather than reintroducing server truncation. It proposes a file-local resource when multipart support is added; it does not change the current first-file handler.

`proposeHLSFallback` changes only the request ID and mode. It preserves the supplied selected file, offset and generation. If direct playback has advanced, the controller must first capture and validate the latest file-local position and use that offset in the supplied request. Falling back from the original boot request would rewind to the old resume point. Both original-resume and advanced-playback fallback fixtures are included.

Before a new selection/seek attempt, the eventual controller must invalidate its old event/response identity, detach the old media/HLS object, and request stop for its own prior HLS session. A failed stop must not make an obsolete event current. A failed or cancelled creation must not reactivate the old response, silently choose the first file, or emit completion. This pure layer does not send DELETE requests, guarantee cleanup, or manage a player lifecycle.

`inspectProposedPlaybackResponse` accepts only a response for the current attempt and current timeline with the exact selected file and full positional tuple. It rejects legacy payloads, dropped offsets, stale generations, truncated resource origins, missing HLS tickets and forced-HLS responses that silently return direct mode. No active attempt means cancellation. A newer request ID supersedes older responses even for the same file and offset. The controller must also associate failures with the request closure that produced them; an error payload alone does not identify an attempt.

`isCurrentPlaybackEvent` compares edition, generation, file, request and HLS session against the accepted active response. Clear that active response immediately on replacement, logout, generation invalidation or cancellation; do not leave the previous object active while the next creation is pending. Only accepted-current events may invoke `resolveEndedFile`, update progress, switch subtitles, publish thumbnail state, or mutate the UI. An eventual implementation must dispose of an abandoned attempt's returned session using its own tracked ownership context without installing its media source. Server idle cleanup remains necessary for an ambiguous creation response.

Future HLS ticket/session records must bind the resolved physical file and generation as well as existing edition/owner identity. Validate the same binding when manifests and segments are requested; do not mint a ticket for one generation and resolve its input from the edition's newer first file. There is no ticket schema or server-side binding change here.

## Subtitles, chapters and thumbnail integration still required

- Cache/resource identities must include content generation and selected file, plus track/variant where applicable
- File-local subtitle cues are interpreted on that file's media clock; cumulative chapter positions retain their file identity and explicit file-local intersections
- Thumbnail time/grid indexes belong to a selected file, with its cumulative start carried separately. Do not treat the second file's zero as edition zero
- A generation change invalidates these resources together with playback; a metadata-only title update should not restart media
- The current subtitle and thumbnail endpoints have not been extended or covered by executable HTTP handler tests in this preparation

## Executable evidence and remaining gates

Run from the repository root:

```sh
./web/node_modules/.bin/vitest run web/tests/mediaTimelineContract.test.ts --config web/vitest.config.ts
./web/node_modules/.bin/tsc --project web/tsconfig.json --noEmit
```

The tests cover authoritative order, membership, validation, immutable construction, unknown-duration propagation, every known boundary before/at/after, explicit file ends, fractional offsets, round trips, exact final completion, generation changes, single-file compatibility within v1, malformed requests, serialized proposed HTTP envelopes, mixed direct/HLS responses, fallback preservation, stale/cancelled/failed replacement responses, and detached-session event identity.

These are synthetic pure-contract tests. Runtime integration still requires a reviewed generation/source schema, additive handlers with consistent snapshots, selected-file opening and ticket binding, a version-aware client adapter, lifecycle/cleanup and progress integration, subtitle/thumbnail contracts, compatibility adapter decisions, and representative multipart direct/native-HLS/hls.js playback in real browsers. No database migration, production repair, network/socket/browser execution, publication, deployment or full security verification is part of this change.
