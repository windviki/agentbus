#!/usr/bin/env bash
# End-to-end test: two simulated agent sessions (fake hook stdin payloads),
# full collaboration flow + web dashboard + A2A boundary + MCP server.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/agentbus"
export AGENTBUS_HOME="$(mktemp -d /tmp/agentbus-e2e.XXXXXX)"
export AGENTBUS_WEB_PORT="${AGENTBUS_WEB_PORT:-18787}"
trap '$BIN stop >/dev/null 2>&1 || true' EXIT

say()  { printf '\n== %s\n' "$*"; }
pass() { printf '   PASS %s\n' "$*"; }

hook() { # hook <event> <session-id> [cwd]
  local ev="$1" sid="$2" cwd="${3:-/tmp}"
  printf '{"hook_event_name":"%s","session_id":"%s","cwd":"%s"}' "$ev" "$sid" "$cwd" \
    | AGENTBUS_SESSION_ID="$sid" "$BIN" hook
}

say "build"
(cd "$ROOT" && go build -o agentbus .)
pass "binary built"

say "daemon auto-starts on first use (web on :$AGENTBUS_WEB_PORT)"
"$BIN" who >/dev/null
out="$("$BIN" status)"
grep -q 'daemon' <<<"$out"
grep -q "web: http://127.0.0.1:$AGENTBUS_WEB_PORT" <<<"$out"
pass "status ok, web advertised"

say "SessionStart hook registers session A (bob)"
out="$(AGENTBUS_NAME=bob hook SessionStart e2e-a /tmp/agentbus-e2e/proj-wt-auth)"
echo "$out" | grep -q '"SessionStart"'
nameA="$("$BIN" register --session sid:e2e-a --print-name)"
[ "$nameA" = "bob" ]
pass "registered as $nameA"

say "session B joins; roster in its SessionStart output includes bob's announcement"
"$BIN" announce --session sid:e2e-a "fixing auth flow in worktree wt-auth" >/dev/null
out="$(hook SessionStart e2e-b /tmp/agentbus-e2e/proj-wt-tests)"
echo "$out" | grep -q 'fixing auth flow in worktree wt-auth'
nameB="$("$BIN" register --session sid:e2e-b --print-name)"
pass "B registered as $nameB and saw A's status"

say "who shows both with status"
out="$("$BIN" who)"
grep -q 'bob' <<<"$out"
grep -q 'fixing auth flow' <<<"$out"
pass "roster correct"

say "B messages A; PostToolUse hook delivers it as additionalContext"
"$BIN" send --session sid:e2e-b "$nameA" "are you touching src/auth? I am refactoring its tests" >/dev/null
out="$(hook PostToolUse e2e-b)"   # B's own hook: nothing for B (sent by B)
[ -z "$out" ]
out="$(hook PostToolUse e2e-a)"
echo "$out" | grep -q '"PostToolUse"'
echo "$out" | grep -q 'src/auth'
pass "delivered via PostToolUse context"

say "inbox now empty for A (acked)"
out="$(hook Stop e2e-a)"
[ -z "$out" ]
pass "no double delivery"

say "delivery ladder: chat does NOT wake an idle session, urgent does"
"$BIN" send --session sid:e2e-b "$nameA" "casual chat while you idle" >/dev/null
out="$(hook Stop e2e-a)"
[ -z "$out" ]                    # chat never blocks Stop
"$BIN" send --session sid:e2e-b --kind urgent "$nameA" "stop touching main!" >/dev/null
out="$(hook Stop e2e-a)"
echo "$out" | grep -q '"block"'
echo "$out" | grep -q 'stop touching main'
grep -q '\[urgent\]' <<<"$out"
# both the queued chat and the urgent message arrive in the wake payload
echo "$out" | grep -q 'casual chat while you idle'
pass "chat rides natural boundary; urgent wakes and flushes queue"

say "request-reply: B asks A, A replies, ask prints the answer"
( sleep 1
  "$BIN" send --session sid:e2e-a --kind reply "$nameB" "you take files, I take tests" >/dev/null
) &
out="$("$BIN" ask --session sid:e2e-b --wait 10 "$nameA" "split files or tests?")"
wait
echo "$out" | grep -q 'reply from bob'
echo "$out" | grep -q 'you take files, I take tests'
pass "ask roundtrip works (reply consumed, not double-delivered)"
out="$(hook UserPromptSubmit e2e-b)"
[ -z "$out" ]
pass "no double delivery of the reply"

say "notify: one-way notice delivered at natural boundary"
"$BIN" notify --session sid:e2e-a "$nameB" "running full build in main wt ~10min" >/dev/null
out="$(hook UserPromptSubmit e2e-b)"
echo "$out" | grep -q 'full build in main wt'
echo "$out" | grep -q '\[notice\]'
echo "$out" | grep -q 'no reply needed'
pass "notice delivered with no-reply semantics"

say "unknown recipient gives a helpful error"
if "$BIN" send --session sid:e2e-a nobod-y "hi" 2>/dev/null; then
  echo "FATAL: send to unknown session should fail"; exit 1
fi
pass "unknown recipient rejected"

say "web dashboard API"
state="$(curl -fsS "http://127.0.0.1:$AGENTBUS_WEB_PORT/api/state")"
grep -q '"bob"' <<<"$state"
grep -q 'full build in main wt' <<<"$state"
resp="$(curl -fsS -X POST "http://127.0.0.1:$AGENTBUS_WEB_PORT/api/send" \
  -d '{"to":"bob","body":"web human says: wrap up auth today","kind":"chat","from":"user-web"}')"
grep -q '"ok":true' <<<"$resp"
out="$(hook UserPromptSubmit e2e-a)"
echo "$out" | grep -q 'web human says'
pass "GET /api/state + POST /api/send work end-to-end"

say "web page is served and proxy-safe (relative api paths)"
page="$(curl -fsS "http://127.0.0.1:$AGENTBUS_WEB_PORT/")"
echo "$page" | grep -q 'agentbus'
echo "$page" | grep -q '"\./api/"'
pass "index.html embedded, relative URLs"

say "A2A boundary: agent card + message/send"
card="$(curl -fsS "http://127.0.0.1:$AGENTBUS_WEB_PORT/a2a/bob/.well-known/agent-card.json")"
grep -q '"name": *"bob"' <<<"$card"
resp="$(curl -fsS -X POST "http://127.0.0.1:$AGENTBUS_WEB_PORT/a2a/bob" \
  -d '{"jsonrpc":"2.0","id":7,"method":"message/send","params":{"message":{"messageId":"ext-1","kind":"message","role":"user","parts":[{"kind":"text","text":"external A2A client pinging bob"}],"metadata":{"agentbus":{"from":"ext-client","kind":"chat"}}}}}')"
grep -q '"jsonrpc":"2.0"' <<<"$resp"
out="$(hook UserPromptSubmit e2e-a)"
echo "$out" | grep -q 'external A2A client pinging bob'
pass "A2A message/send delivered to bob"

say "A2A: unsupported method returns JSON-RPC error"
resp="$(curl -fsS -X POST "http://127.0.0.1:$AGENTBUS_WEB_PORT/a2a/bob" \
  -d '{"jsonrpc":"2.0","id":8,"method":"message/stream","params":{}}')"
grep -q -- '-32601' <<<"$resp"
pass "A2A subset enforced"

say "MCP server: initialize, tools/list, tools/call"
out="$(printf '%s\n%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"agentbus_who","arguments":{}}}' \
  | "$BIN" mcp)"
echo "$out" | grep -q 'agentbus_ask'
echo "$out" | grep -q 'bob'
pass "stdio MCP server exposes tools and answers via the bus"

say "log shows traffic"
log="$("$BIN" log -n 30)"
grep -q 'external A2A client' <<<"$log"
pass "log ok"

say "leave removes session; name tombstoned briefly"
"$BIN" leave --session sid:e2e-a >/dev/null
who="$("$BIN" who)"
if grep -q '^bob' <<<"$who"; then
  echo "FATAL: bob still listed"; exit 1
fi
dup="$("$BIN" register --session sid:e2e-c --name bob --print-name)"
[ "$dup" = "bob-2" ]
pass "bob left; immediate reuse renamed to $dup"

say "pid-based reap: dead pid vanishes from roster"
deadpid=999999999
"$BIN" register --session sid:e2e-d --name ghost --pid "$deadpid" --print-name >/dev/null
"$BIN" who >/dev/null   # any op triggers lazy reap
who="$("$BIN" who)"
if grep -q 'ghost' <<<"$who"; then
  echo "FATAL: dead-pid session still listed"; exit 1
fi
pass "dead pid reaped"

say "persistence across daemon restart"
"$BIN" stop >/dev/null
if "$BIN" status >/dev/null 2>&1; then echo "FATAL: daemon still up"; exit 1; fi
"$BIN" who >/dev/null   # auto-starts again
out="$("$BIN" who)"
grep -q "$nameB" <<<"$out"
state="$(curl -fsS "http://127.0.0.1:$AGENTBUS_WEB_PORT/api/state")"
grep -q "$nameB" <<<"$state"
pass "session B survived restart (bus + web)"

say "human without a session sends as 'user'"
"$BIN" send --from user "$nameB" "human checking in" >/dev/null
out="$(hook UserPromptSubmit e2e-b)"
echo "$out" | grep -q 'human checking in'
pass "human message delivered"

"$BIN" stop >/dev/null
printf '\nALL E2E TESTS PASSED\n'
