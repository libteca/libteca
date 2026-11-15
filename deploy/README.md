# Deploying libteca

Two paths, both first-class: bare (binary + systemd) and Teploy. Requirements
either way: `ffmpeg` and `ffprobe` on PATH (transcoding and scan-time probing
shell out to them), and a data directory. The server binds port 8096 by
default, which is unprivileged, so it never needs root capabilities.

## Bare: systemd (recommended for a single box)

The shipped unit (`deploy/systemd/libteca.service`) is hardened:
`DynamicUser=yes` (a transient UID per start, no system user to create or
rotate passwords for), `StateDirectory=libteca` (persists `/var/lib/libteca`
across those changing UIDs), `ProtectSystem=strict` + `ReadWritePaths` limited
to the data dir, `PrivateTmp`, `NoNewPrivileges`, empty ambient capabilities.

Install:

```sh
sudo cp libteca /usr/local/bin/
sudo cp deploy/systemd/libteca.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now libteca
curl http://localhost:8096/healthcheck   # -> {"success":true}
```

No `useradd` needed — `DynamicUser=yes` means there is no fixed user. If you
want one (fixed UID for backups, NFS idmapping), build it instead:

```sh
sudo useradd --system --home-dir /var/lib/libteca libteca
# then in the unit: comment out DynamicUser= and StateDirectory=,
# add User=libteca and Group=libteca (ReadWritePaths stays)
```

First run — create the admin (the unit's exact sandbox, so file ownership
stays consistent):

```sh
sudo systemd-run --pipe --unit=libteca-init \
  -p DynamicUser=yes -p StateDirectory=libteca \
  /usr/local/bin/libteca --data /var/lib/libteca --init-admin admin:changeme
```

Then open http://<host>:8096, log in as that admin, and add a library pointing
at your media — under this unit the whole filesystem stays readable
(`ProtectSystem=strict`, `ProtectHome=read-only`); only writes are confined
to `/var/lib/libteca`. Scan from the web UI.

Updates: replace `/usr/local/bin/libteca` (from a release tarball or `make
build`) and `sudo systemctl restart libteca`.

### Hardware transcode

`LIBTECA_HWACCEL=auto` picks VAAPI/QSV/NVENC/VideoToolbox when available and
falls back to software otherwise. The unit deliberately does not use
`PrivateDevices`, so `/dev/dri` passthrough works on hosts that have it; check
with `ls /dev/dri` and that the render node is readable (`video`/`render`
group membership matters only for the static-user variant; `DynamicUser`
units get device access via the device cgroup defaults).

## Docker

Released images are published as `ghcr.io/libteca/libteca` for linux/amd64
and linux/arm64: a tag per version, `stable` for the latest full release and
`edge` for the latest tag of any kind.

```sh
docker run -d --name libteca --restart unless-stopped -p 8096:8096 \
  -v libteca-data:/data -v /path/to/media:/media:ro \
  -e LIBTECA_INIT_ADMIN='admin:replace-with-a-unique-long-password' \
  ghcr.io/libteca/libteca:stable
```

`LIBTECA_INIT_ADMIN` creates that administrator only when the server has no
users at all. Once any user exists it is ignored, so it is safe to leave set
across restarts and after the password is changed in the web UI. The
`--init-admin` flag is stricter: it fails when the user exists with a
different password.

To build the image yourself, `make docker` (or `docker build -t libteca:dev .`)
builds a multi-stage image: node:22-alpine builds web/, the dist is copied into
internal/server/webdist so the Go embed picks it up at compile time, and the
final alpine stage carries the static binary plus ffmpeg/ffprobe
(`apk add --no-cache ffmpeg`, which ships both).

```sh
docker run -d --name libteca -p 8096:8096 -v libteca-data:/data libteca:dev
```

All state lives under `/data` (named volume above; a bind mount works too).
First run creates the admin by appending the flag to the entrypoint:

```sh
docker run -d --name libteca -p 8096:8096 -v libteca-data:/data \
  libteca:dev --init-admin admin:changeme
```

The container runs as the unprivileged uid/gid 10001 (`libteca`); only `/data`
must be writable, so media library mounts can be read-only. Upgrading a
bind-mounted data directory from an older root-mode image needs a one-time
ownership migration on the host — the server never chowns host paths itself:

```sh
chown -R 10001:10001 /path/to/data
```

Named volumes initialize from the image and need no migration.

ffmpeg note: transcoding in-container is software-only by default — the
teploy format has no device mapping yet, so `/dev/dri` passthrough is
pending there. Plain `docker run --device /dev/dri ...` works; with
`LIBTECA_HWACCEL=auto` the server picks up VAAPI/QSV when the driver
stack is present in the image and falls back to software otherwise.

## Teploy

`deploy/teploy/teploy.yml` is the app template for `teploy deploy libteca`
(single service, port 8096, `/healthcheck` health, `data` volume, the released
image). Set the first administrator with
`teploy secret set LIBTECA_INIT_ADMIN 'admin:<password>'` before the first
deploy. The teploy format has no device mapping, so in-container hwaccel
degrades to software; use the bare systemd path for hardware transcoding.

## Network posture

No public exposure by default, ever. The intended deployment is LAN or
Tailscale: reach the box at `http://<tailscale-ip>:8096` and leave it off the
public internet. If you front it with a proxy anyway, put auth in front too —
the client protocols carry their own tokens, but the attack surface you add
is yours. See docs/PLAN.md section 11 (security posture).
