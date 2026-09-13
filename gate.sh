#!/bin/bash
# Gate runner: boots a fresh libteca against a chosen directory and prints
# the three gates with exactly what to look for. Run, judge, kill.
#
#   ./gate.sh ~/path/to/real/books        # G1: scan your real library
#   ./gate.sh                             # demo library (synthetic)
#
# The binary is built into /tmp; the data dir is a throwaway in /tmp; your
# library is only READ (scan), never written to.

set -euo pipefail
cd "$(dirname "$0")"

LIB_PATH="${1:-./data/demo}"
PORT=8096
DATA=$(mktemp -d /tmp/libteca-gate.XXXXXX)

echo "building..."
go build -o "$DATA/libteca" ./cmd/libteca

echo "booting on :$PORT (data: $DATA, library: $LIB_PATH)"
"$DATA/libteca" --data "$DATA" --port $PORT --init-admin gate:gate12345 &
PID=$!
sleep 2

TOKEN=$(curl -s -X POST localhost:$PORT/api/core/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"gate","password":"gate12345"}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')

LIB_ID=$(curl -s -X POST localhost:$PORT/api/core/libraries \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"Gate Library\",\"type\":\"audiobooks\",\"path\":\"$LIB_PATH\"}" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')

echo "scanning..."
curl -s -X POST localhost:$PORT/api/core/libraries/$LIB_ID/scan \
  -H "Authorization: Bearer $TOKEN" > /dev/null

echo "waiting for scan to settle..."
sleep 5

COUNT=$(curl -s "localhost:$PORT/api/core/libraries/$LIB_ID/works?limit=500" \
  -H "Authorization: Bearer $TOKEN" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')

cat <<EOF

═══════════════════════════════════════════════════════════════════
 FOUND $COUNT WORKS from $LIB_PATH

 G1 (scanner quality): open http://localhost:$PORT in a browser,
 log in as gate/gate12345, and browse the library.

   PASS: works are findable, titles are right, covers where expected,
         nothing obviously missing or duplicated
   FAIL: note what's wrong — that IS the fix list (providers, parsing,
         whatever the actual gap turns out to be)
   KILL: you would never use this over Jellyfin → close this script,
         run kill $PID, and say so

 G2 (web player): click a work, play it. Chapter seek? Resume after
   reload? That's the whole gate.

 G3 (phone): point the official ABS app at http://<this-machine>:$PORT
   (same credentials). Connects, browses, plays, syncs both ways?

 When done: kill $PID   (the data dir is /tmp garbage; nothing touched
 your library files)
═══════════════════════════════════════════════════════════════════

EOF

echo "server pid: $PID  (kill it when done)"
echo "data dir:   $DATA"
echo "library:    $LIB_PATH (read-only)"
