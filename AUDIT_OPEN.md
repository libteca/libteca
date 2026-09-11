# Open audit items

Register libteca-01 through libteca-14 (local scout, 2026-09-11) is fully fixed - see `audit libteca-NN` commits. Remaining items are coverage gaps, not known defects:

- **Unaudited areas** (scout skipped deliberately): `web/` TS UI, importer bodies (abs/kavita internals), trickplay/cbz internals, discovery/works/queries store internals (grep-level only), `tools/record` + test corpus (founder-gated - do not automate).
- **Noted during fixes, unfixed, out of register scope:** `internal/egress/proxy_test.go` gofmt-dirty (pre-existing); `Reap`-style untrack-before-remove patterns elsewhere in the codebase were not swept.
- **Founder-owned gates remain:** corpus capture runs (`make record`), G1-G3, launch decisions.
