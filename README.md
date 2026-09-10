# libteca

One media server, every client. Movies, TV, music, audiobooks, podcasts, books,
and comics from a single Go binary with an embedded SQLite store — serving four
wire-protocol faces so the client apps you already use connect unchanged. The
product is the compatibility layer: instead of building a fifth client
ecosystem, libteca inherits the existing ones.

Status: BUILDING (private). The spine, first-party web UI, Audiobookshelf face,
and Jellyfin face v1 (including HLS transcode) are built; OPDS and Subsonic
subsets are mounted. Live-client verification against a recorded traffic corpus
is the next owed step — every client claim below says exactly how far it has
actually been tested.

## The four faces

| Face | Protocol surface | Inherited clients | Status |
|---|---|---|---|
| Audiobookshelf | ABS API: login token, libraries/items, progress sync, cover + file streaming, socket.io | Official ABS iOS/Android apps | Built; corpus verification pending |
| Jellyfin | Jellyfin 10.10.x API: auth, /Items hierarchy, images, PlaybackInfo + DeviceProfile engine, HLS transcode, /Sessions + websocket | Jellyfin Media Player, Android TV, Swiftfin, Infuse, Findroid | Built v1; corpus verification pending |
| OPDS | OPDS 1.2 navigation/acquisition feeds, Basic auth, OpenSearch, OPDS-PSE paged CBZ | KOReader, KyBook, Chunky, Moon+, Panel | Built; untested against clients |
| Subsonic | Subsonic subset: ping, authenticate, artists/indexes, album lists, stream, search, scrobble | Symfonium, Feishin, play:Sub | Subset built; untested against clients |

Podcast serving (subscribe, auto-download, OPML import/export) is built on the
spine and surfaces in the ABS-compatible apps.

## Quickstart

Requires Go 1.26+ and Node/npm for the embedded web UI; ffmpeg + ffprobe on
PATH for scan-time probing and transcoding.

```sh
make build                                  # builds web UI into the binary
./libteca --data ./data --init-admin admin:secret   # creates admin, then serves
```

Open http://localhost:8096, log in, add a library pointing at your media, and
scan (from the web UI, or `./libteca --data ./data --scan` as a one-shot).
Then point clients at it:

| Client | Face | How to connect |
|---|---|---|
| Audiobookshelf app (iOS/Android) | ABS | Server URL `http://<host>:8096`, username + password |
| Jellyfin Media Player | Jellyfin | Add server `http://<host>:8096`, username + password |
| KOReader | OPDS | OPDS catalog `http://<host>:8096/opds`, Basic auth |
| Symfonium | Subsonic | Subsonic server `http://<host>:8096/rest` |

From a release tarball instead of source: unpack, verify against SHA256SUMS,
run the binary with the same flags.

## Client compatibility matrix

Honesty method (PLAN §7.1): a cell only says "works" after recorded
client traffic replays in CI and a live client test passes. The corpus capture
run is pending, so nothing says "works" yet.

| Client | Face | Status |
|---|---|---|
| Audiobookshelf official app (iOS/Android) | ABS | Expected works — contract read from client source; corpus replay pending |
| Jellyfin Media Player (desktop) | Jellyfin | Untested — corpus capture pending |
| Jellyfin Android TV / Fire TV | Jellyfin | Untested |
| Swiftfin (Apple TV) | Jellyfin | Untested |
| Infuse | Jellyfin | Untested |
| Findroid | Jellyfin | Untested |
| KOReader | OPDS | Untested |
| KyBook / Moon+ / Panel | OPDS | Untested |
| Chunky | OPDS (incl. OPDS-PSE) | Untested |
| Symfonium | Subsonic | Untested |
| Feishin | Subsonic | Untested |
| play:Sub | Subsonic | Untested |

The web UI (library browse, players for audio/video, EPUB/CBZ readers, admin)
is the fallback for anything a face does not cover yet.

## Benchmarks

Real numbers from `bench/results/` on this machine (darwin/arm64, 10-core,
go1.26.6, 2026-09-09). 10k-file synthetic audio library, 1080p H.264 source.
Reproduce with `make seed` then `make bench`; JSON lands in `bench/results/`.

| Benchmark | Result | Target | Pass |
|---|---|---|---|
| Idle RSS | 23.2 MB | < 100 MB | yes |
| Cold scan, 10k files | 217 s (46 files/s) | < 600 s | yes |
| Warm rescan, no changes | 0.17 s, 0 re-probes | near-instant | yes |
| Transcode first-segment p95 | 0.91 s | < 2.0 s | yes |
| Transcode full-prebuffer p95 | 1.67 s | informational | yes |
| Concurrent transcodes (mean time-to-first-frame, 1/2/4/8 sessions) | 0.91 / 1.14 / 1.86 / 3.95 s | — | — |

Direct play is disk-bound: no server CPU beyond file reads.

## Deploy

- Bare, systemd (recommended): hardened unit + 6-line install + first-run
  admin + hardware-transcode notes in [deploy/README.md](deploy/README.md).
- Teploy: app template at [deploy/teploy/teploy.yml](deploy/teploy/teploy.yml)
  (single service, port 8096, `/healthcheck` health, `data` volume; read its
  header for current limitations).

Release artifacts: `make release` cross-compiles linux/amd64, linux/arm64,
darwin/amd64, darwin/arm64 into `dist/release/` as tarballs/zips with
SHA256SUMS. The binary is CGo-free; only ffmpeg is a runtime dependency.

## Security posture

Local-first, zero telemetry. Per-device tokens with revocation (web admin);
admin and user surfaces separated. No public-internet exposure in any default —
LAN or Tailscale is the intended deployment. All file serving resolves through
the scanned file table's stored paths; client-supplied paths never reach the
filesystem (path traversal is the classic media-server kill vector). See
PLAN.md §11.
