# libteca

One library, one UI. Movies, TV, music, audiobooks, podcasts, books, and comics
from a single Go binary with an embedded SQLite store. The product is the
first-party web UI. Four protocol faces (Jellyfin, Audiobookshelf, OPDS,
Subsonic) are optional — existing apps are a nicety, not the target.

Status: RELEASE-READY (public, MIT). All codeable v1 work closed through the
2026-09-11 release waves (audit + polish; DECISIONS 28): security hardening,
race/tx fixes, motion system, PWA, Docker image, provider keys in Admin.
The live gate is Gate W - daily use of the web UI on a real library. No
inherited-client cell below says "works" (Gate F pending).

## The four faces

| Face | Protocol surface | Inherited clients | Status |
|---|---|---|---|
| Audiobookshelf | ABS API: login token, libraries/items, progress sync, cover + file streaming. No socket.io (stretch, not built). | Official ABS iOS/Android apps | Built; corpus verification pending |
| Jellyfin | Jellyfin-shaped 10.10.x surface: auth, /Items hierarchy, images, PlaybackInfo (hardcoded direct-play vs HLS — not a DeviceProfile engine), HLS transcode, /Sessions + websocket | Jellyfin Media Player, Android TV, Swiftfin, Infuse, Findroid | Built v1; corpus verification pending |
| OPDS | OPDS 1.2 navigation/acquisition feeds, Basic auth, OpenSearch, OPDS-PSE paged CBZ | KOReader, KyBook, Chunky, Moon+, Panel | Built; untested against clients |
| Subsonic | Subsonic subset: ping, password/token auth on `/rest/*`, artists/indexes, album lists, stream, search, scrobble | Symfonium, Feishin, play:Sub | Subset built; untested against clients |

Podcast subscribe, auto-download, and OPML are on the spine and first-party
UI. Parallel ABS `/api/podcasts` routes exist; the official app lists podcasts
via `/api/libraries/{id}/items`, which is works-only — so the app will not
see them. Corpus-unverified either way.

## Quickstart

Source build needs Go 1.26+, Node/npm (embedded web UI), and ffmpeg + ffprobe
on PATH. The Go module `github.com/neutron-build/neutron/go` is a published
dependency (`v0.1.0`). Release tarballs need only ffmpeg + ffprobe.

```sh
make build                                  # web UI into the binary, then go build
./libteca --data ./data --init-admin admin:secret   # creates admin, then serves :8096
```

`--watch` is on by default (`--watch=false` or `LIBTECA_WATCH=false` disables;
`LIBTECA_SWEEP=<seconds>` sets the sweep, `0` disables). `--hwaccel` accepts
`auto|none|videotoolbox|vaapi|nvenc|qsv`; empty defers to `$LIBTECA_HWACCEL`,
then auto-detect. `--port` defaults to 8096.

Open http://localhost:8096, log in, add a library, and scan (web UI, or
`./libteca --data ./data --scan` as a one-shot). Then point clients at it:

| Client | Face | How to connect |
|---|---|---|
| Audiobookshelf app (iOS/Android) | ABS | Server URL `http://<host>:8096`, username + password |
| Jellyfin Media Player | Jellyfin | Add server `http://<host>:8096`, username + password |
| KOReader | OPDS | OPDS catalog `http://<host>:8096/opds`, Basic auth |
| Symfonium | Subsonic | Subsonic server `http://<host>:8096/rest` |

Those connection recipes are the intended URLs, not a claim the clients work.

From a release tarball: unpack, verify against SHA256SUMS, run the binary with
the same flags. No Node, no Neutron checkout.

## Client compatibility matrix

Honesty method (PLAN §7.1): a cell only says "works" after recorded
client traffic replays in CI and a live client test passes. The corpus capture
run is pending, so nothing says "works" yet.

| Client | Face | Status |
|---|---|---|
| Audiobookshelf official app (iOS/Android) | ABS | Expected — contract read from client source; corpus replay pending |
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

The web UI is the product (library, players, EPUB/CBZ/PDF readers, podcasts,
admin). Face cells above are optional compatibility, not a substitute for
that UI being good enough to replace Jellyfin / Kavita / ABS in the browser.

## Benchmarks

Real numbers from `bench/results/` on this machine (darwin/arm64, 10-core,
go1.26.6, 2026-09-09). 10k-file synthetic audio library, 1080p H.264 source.
`make bench` self-seeds anything missing; `make seed` is the 10k audio library
only. JSON lands in `bench/results/`.

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
  header for current limitations — no published image yet).

Release artifacts: `make release` cross-compiles linux/amd64, linux/arm64,
darwin/amd64, darwin/arm64 into `dist/release/` as tarballs/zips with
SHA256SUMS. The binary is CGo-free; runtime deps are ffmpeg and ffprobe.

## Security posture

Local-first, zero telemetry. Listens on `:<port>` (all interfaces); LAN or
Tailscale is the intended deployment — nothing in the defaults puts auth in
front of a public bind. Per-device tokens with revocation (web admin); admin
and user surfaces separated. All file serving resolves through the scanned
file table's stored paths; client-supplied paths never reach the filesystem
(path traversal is the classic media-server kill vector). See PLAN.md §11.
