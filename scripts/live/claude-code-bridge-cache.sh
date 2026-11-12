#!/usr/bin/env bash
# Live check of prompt caching through the Claude Code bridge (D-045). Not run
# in CI: it needs a deployed Harbor Pilot with a claude_code subscription
# account, and it spends that subscription's allowance.
#
# It drives a short scripted session with the real Claude Code client, in
# print mode, through the gateway:
#   1. two quick turns (a parked process continues: "live_process");
#   2. a pause past the bridge's idle timeout, then a turn (the shelved CLI
#      session is resumed: "shelved_session");
#   3. a turn with a tool call (the client reads a file);
#   4. /compact.
# Then it prints, per request, the cache read/write tokens and which CLI
# session served it, from the activity API. Expect cache reads to stay high
# after step 2; a large cache write there means the prefix was rebuilt.
#
#   HP_URL=https://gateway.example.com \
#   HP_GATEWAY_TOKEN=hpk_...        # a gateway credential routed to the bridge
#   HP_ADMIN_TOKEN=hpa_...          # an API token with activity.read
#   IDLE_WAIT=150                   # seconds; above bridges.claude_code.idle_timeout
#   scripts/live/claude-code-bridge-cache.sh
#
# Tokens are read from the environment, never from arguments. Needs claude,
# curl and jq.
set -euo pipefail

: "${HP_URL:?set HP_URL}" "${HP_GATEWAY_TOKEN:?set HP_GATEWAY_TOKEN}" "${HP_ADMIN_TOKEN:?set HP_ADMIN_TOKEN}"
IDLE_WAIT=${IDLE_WAIT:-150}
for bin in claude curl jq; do command -v "$bin" >/dev/null || { echo "missing $bin" >&2; exit 1; }; done

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"
printf 'The answer is 42.\n' > notes.txt
start=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sid=$(cat /proc/sys/kernel/random/uuid 2>/dev/null || uuidgen)

export ANTHROPIC_BASE_URL="$HP_URL" ANTHROPIC_AUTH_TOKEN="$HP_GATEWAY_TOKEN"
ask() { claude -p --output-format text "$@" >/dev/null; }

echo "turn 1 (new session)";            ask --session-id "$sid" "Reply with one word: ready."
echo "turn 2 (quick follow-up)";        ask --resume "$sid" "Reply with one word: again."
echo "pause ${IDLE_WAIT}s (past the bridge idle timeout)"; sleep "$IDLE_WAIT"
echo "turn 3 (after the pause)";        ask --resume "$sid" "Reply with one word: back."
echo "turn 4 (tool call)";              ask --resume "$sid" --allowedTools Read "Read notes.txt and tell me the answer."
echo "turn 5 (/compact)";               ask --resume "$sid" "/compact"

sleep 3
api() { curl -fsS -H "Authorization: Bearer $HP_ADMIN_TOKEN" "$HP_URL/api/v1$1"; }
printf '\n%-30s %-6s %-26s %10s %10s %10s  %s\n' request status model input cache_rd cache_wr session
api "/activity/requests?limit=100&from=$start" | jq -r '.items | reverse | .[].id' | while read -r id; do
  api "/activity/requests/$id" | jq -r '[.id, .status, .final_model,
      (.tokens.input // "-"), (.tokens.cache_read // "-"), (.tokens.cache_write // "-"),
      ([.ledger[].provenance.bridge_session? // empty] | first // "-")] | @tsv' |
    awk -F'\t' '{ printf "%-30s %-6s %-26s %10s %10s %10s  %s\n", $1, $2, $3, $4, $5, $6, $7 }'
done
