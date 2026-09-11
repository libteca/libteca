# AGENTS.md

Working on libteca? Read SPEC.md first (architecture + contracts), then
DECISIONS.md (every non-obvious call, newest at the bottom), then PLAN.md
status. House rules:

- No comments in code. No emojis anywhere. Match the quiet dark design.
- Go: neutron-go router, sqlc-style queries in internal/store, goose
  embedded migrations. Web: Preact + neutron/vite, inline style objects
  from web/src/styles.ts, hash router.
- Build: make build (web into webdist, embedded at compile time), then
  go build ./cmd/libteca. Demo: ./libteca --data data/demo --port 8096.
- Tests must stay green: go test ./... and web tsc+build.
- Do not commit data/, credentials, or session notes. This is a public
  repo - nothing private goes in, including in history.
