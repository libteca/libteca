# Golden-traffic capture harness

Records real client traffic through mitmproxy, sanitizes it into replayable
fixtures under `testcorpus/<face>/`, and replays them in Go tests against the
emulated faces (SPEC.md section 6, DECISIONS 4).

## Capture

On a machine the phone can reach (default port 8080):

    LIBTECA_RECORD_FACE=abs LIBTECA_RECORD_HOST=<abs-server-host> mitmdump -s tools/record/record.py

- `LIBTECA_RECORD_FACE` - `abs` or `jellyfin` (default `abs`)
- `LIBTECA_RECORD_HOST` - only record requests whose host contains this
  string; strongly recommended, filters unrelated phone traffic
- `LIBTECA_RECORD_DIR` - output dir (default `testcorpus/<face>/raw/`)

Phone setup: wifi proxy to this machine:8080. For HTTPS install the mitmproxy
CA cert on the device (http://mitm.it while proxied). Point the official ABS
Android/iOS app at the real Audiobookshelf server as usual - the proxy sits in
the middle. For Jellyfin, point JMP (desktop) or the Android TV client at a
real Jellyfin server the same way.

Flows to capture (abs): login, libraries, items paging, item detail, play
open, sync ticks, close, progress round-trip (get + patch + get).
For jellyfin: AuthenticateByName, Views, Items, PlaybackInfo, Sessions
(Playing/Progress/Stopped), NextUp.

## Sanitize

    make corpus    # or: python3 tools/record/sanitize.py [abs|jellyfin...]

Writes `testcorpus/<face>/NNNN_<flow>.json`. The NNNN prefix is the capture
sequence; replay walks fixtures in that order so a play-open response can seed
the session id used by later sync/close fixtures. Sanitizing: redacts tokens
and passwords, strips hostnames and URLs, maps user ids to stable `user_N`
placeholders, rewrites uuids to `{uuid}`, and replaces dynamic path segments
with placeholders.

## Placeholder convention

The sanitizer rewrites ids in paths, query values, and body values. The
replay harness substitutes them with seeded entities before matching:

| placeholder | substituted with |
|---|---|
| `{id}`, `{itemId}` | seeded edition id (abs: numeric; jellyfin: `e<n>`) |
| `{libId}` | seeded library id (abs: numeric; jellyfin: `lib<n>`) |
| `{userId}` | seeded admin (abs: `u-<n>`; jellyfin: `<n>`) |
| `{fileId}` | seeded file id (abs: numeric; jellyfin: `f<n>`) |
| `{seriesId}` | seeded work id (jellyfin: `w<n>`) |
| `{sessionId}` | harvested from the preceding play/playbackinfo response |
| `{password}` | seeded admin password |

Wildcards - `{uuid}`, `{token}`, `[REDACTED]`, `user_N` - match any value.
Volatile keys are ignored by the subset matcher: `*_at`/`*At` suffixes,
`ServerId`, `ServerName`, `sessionId`/`SessionId`/`PlaySessionId`,
`userToken`/`AccessToken`/`token`, `serverTime`/`lastUpdate`/`startTime`,
`Path`, `TranscodingUrl`, `Etag`, `DateCreated`. A fixture with placeholders
that cannot be resolved is skipped with a message.

## Replay

`go test ./internal/api/abs/ ./internal/api/jellyfin/` - also part of
`make test`. Each test boots the real stack (temp data dir, store with
embedded migrations, seeded admin + token via the real auth paths, one
audiobook, the face's routes on an httptest.Server mirroring
internal/server/server.go) and replays every fixture: exact status match,
content-type match for JSON fixtures, JSON subset match. Binary (base64)
fixtures assert status only. Login request bodies are rewritten to the seeded
admin credentials; redacted auth headers and `api_key` query values are
re-injected with the seeded token.
