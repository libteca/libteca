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

## Teploy

`deploy/teploy/teploy.yml` is the app template for `teploy deploy libteca`
(single service, port 8096, `/healthcheck` health, `data` volume). Read its
header before using: it is the target shape — the container image is not
published yet (the `neutron-go` dependency is a relative `replace` until it
publishes), and the teploy format has no device mapping, so in-container
hwaccel degrades to software. Until then, deploy bare as above.

## Network posture

No public exposure by default, ever. The intended deployment is LAN or
Tailscale: reach the box at `http://<tailscale-ip>:8096` and leave it off the
public internet. If you front it with a proxy anyway, put auth in front too —
the client protocols carry their own tokens, but the attack surface you add
is yours. See PLAN.md section 11 (security posture).
