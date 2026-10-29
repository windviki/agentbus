#!/usr/bin/env bash
# End-to-end test: two simulated agent sessions (fake hook stdin payloads),
# full collaboration flow: join -> announce -> send -> hook delivery ->
# Stop wake -> reply -> broadcast -> leave -> reap -> persistence -> stop.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/agentbus"
export AGENTBUS_HOME="$(mktemp -d /tmp/agentbus-e2e.XXXXXX)"
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

say "daemon auto-starts on first use"
"$BIN" who >/dev/null
"$BIN" status | grep -q 'daemon'
pass "status ok"

say "SessionStart hook registers session A (bob)"
out="$(AGENTBUS_NAME=bob hook SessionStart e2e-a /tmp/agentbus-e2e/proj-wt-auth)"
echo "$out" | grep -q '"SessionStart"'
echo "$out" | grep -q 'joined the local agent bus'
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
"$BIN" who | grep -q 'bob'
"$BIN" who | grep -q 'fixing auth flow'
pass "roster correct"

say "B messages A; PostToolUse hook delivers it as additionalContext"
"$BIN" send --session sid:e2e-b "$nameA" "are you touching src/auth? I am refactoring its tests" >/dev/null
out="$(hook PostToolUse e2e-b)"   # B's own hook: nothing for B (sent by B)
[ -z "$out" ]
out="$(hook PostToolUse e2e-a)"
echo "$out" | grep -q '"PostToolUse"'
echo "$out" | grep -q 'src/auth'
pass "delivered via PostToolUse context"

say "inbox now empty for A (acked); Stop hook emits nothing"
out="$(hook Stop e2e-a)"
[ -z "$out" ]
pass "no double delivery"

say "new message while A idle: Stop hook wakes A with decision block"
"$BIN" send --session sid:e2e-b "$nameA" "ping - are we splitting files or tests?" >/dev/null
out="$(hook Stop e2e-a)"
echo "$out" | grep -q '"decision"'
echo "$out" | grep -q '"block"'
echo "$out" | grep -q 'splitting files or tests'
pass "Stop wake works"

say "A replies; broadcast from A reaches B and not A"
"$BIN" send --session sid:e2e-a "$nameB" "yes - I own src/auth handlers, you take tests" >/dev/null
"$BIN" send --session sid:e2e-a all "running full build in main worktree ~10min" >/dev/null
out="$(hook UserPromptSubmit e2e-b)"
echo "$out" | grep -q 'own src/auth handlers'
echo "$out" | grep -q 'full build in main'
out="$(hook UserPromptSubmit e2e-a)"
[ -z "$out" ]
pass "reply + broadcast routed correctly"

say "unknown recipient gives a helpful error"
if "$BIN" send --session sid:e2e-a nobod-y "hi" 2>/dev/null; then
  echo "FATAL: send to unknown session should fail"; exit 1
fi
pass "unknown recipient rejected"

say "log shows traffic"
"$BIN" log -n 10 | grep -q 'full build in main'
pass "log ok"

say "leave removes session; name tombstoned briefly"
"$BIN" leave --session sid:e2e-a >/dev/null
if "$BIN" who | grep -q '^bob'; then
  echo "FATAL: bob still listed"; exit 1
fi
dup="$("$BIN" register --session sid:e2e-c --name bob --print-name)"
[ "$dup" = "bob-2" ]
pass "bob left; immediate reuse renamed to $dup"

say "pid-based reap: dead pid vanishes from roster"
deadpid=999999999
"$BIN" register --session sid:e2e-d --name ghost --pid "$deadpid" --print-name >/dev/null
"$BIN" who >/dev/null   # any op triggers lazy reap
if "$BIN" who | grep -q 'ghost'; then
  echo "FATAL: dead-pid session still listed"; exit 1
fi
pass "dead pid reaped"

say "persistence across daemon restart"
"$BIN" stop >/dev/null
if "$BIN" status >/dev/null 2>&1; then echo "FATAL: daemon still up"; exit 1; fi
"$BIN" who >/dev/null   # auto-starts again
"$BIN" who | grep -q "$nameB"
pass "session B survived restart"

say "human without a session sends as 'user'"
"$BIN" send --from user "$nameB" "human checking in" >/dev/null
out="$(hook UserPromptSubmit e2e-b)"
echo "$out" | grep -q 'human checking in'
pass "human message delivered"

"$BIN" stop >/dev/null
printf '\nALL E2E TESTS PASSED\n'
