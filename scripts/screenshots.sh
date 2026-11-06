#!/usr/bin/env bash
# Regenerates the documentation screenshots (site/public/screenshots) from a
# fresh demo instance. The demo's synthetic traffic runs on the real clock so
# latencies look real; relative times reflect the moment of capture.
#
#   scripts/screenshots.sh            # every shot
#   scripts/screenshots.sh home plan  # only these shots
#
# The demo's data is synthetic: no real providers, credentials or prompts.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT=${SCREENSHOT_PORT:-18190}
OUT=site/public/screenshots
DATA=$(mktemp -d)
trap 'kill "${PID:-}" 2>/dev/null || true; rm -rf "$DATA"' EXIT

make build >/dev/null
./bin/harbor-pilot serve --demo --listen "127.0.0.1:$PORT" --data-dir "$DATA" >"$DATA/server.log" 2>&1 &
PID=$!
for _ in $(seq 1 60); do
  curl -fsS "http://127.0.0.1:$PORT/readyz" >/dev/null 2>&1 && break
  sleep 0.5
done

(cd web && node scripts/screenshots.mjs "http://127.0.0.1:$PORT" "../$OUT" "$@")
echo "screenshots written to $OUT"
