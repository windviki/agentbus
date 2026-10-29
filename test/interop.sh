#!/usr/bin/env bash
# Interop test: the DSH native plugin and Claude Code hooks meet on one daemon.
#
#   ./test/interop.sh                          # preflight only (no model calls)
#   RUN_INTEROP=1 ./test/interop.sh            # DSH receiver + Claude receiver
#   RUN_INTEROP=1 RUN_INTEROP_BASELINE=1 ./test/interop.sh
#                                              # + unprimed Claude baseline
#
# Spends API quota and needs: go, dsh, claude, python3, curl; Claude needs its
# local router reachable (ccr start), DSH needs DEEPSEEK_API_KEY (or the web
# credentials store at ~/.dsh/.credentials.yaml).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/agentbus"
export AGENTBUS_HOME="$(mktemp -d /tmp/agentbus-interop.XXXXXX)"
export PATH="$ROOT:$PATH"
PATCH="$AGENTBUS_HOME/dsh.patch.yml"
CLAUDE_SETTINGS="$AGENTBUS_HOME/claude-settings.json"
HOOK_WRAP="$AGENTBUS_HOME/hook-wrap.sh"
HOOK_LOG="$AGENTBUS_HOME/hook-injected.log"

RUN_INTEROP="${RUN_INTEROP:-0}"
RUN_BASELINE="${RUN_INTEROP_BASELINE:-0}"
DSH_PID=""
CLAUDE_OK=1

say()  { printf '\n== %s\n' "$*"; }
pass() { printf '   PASS %s\n' "$*"; }
skip() { printf '   SKIP %s\n' "$*"; }
die()  { printf '   FAIL %s\n' "$*" >&2; exit 1; }
log_has() {
  local out
  out="$("$BIN" log -n 30 2>/dev/null)"
  grep -q -- "$1" <<<"$out"
}
dsh_registered() {
  local out
  out="$("$BIN" who 2>/dev/null)"
  grep -q 'dsh-probe' <<<"$out"
}
wait_for() { # wait_for <seconds> <function>
  local secs="$1" fn="$2" i=0
  while [ "$i" -lt "$secs" ]; do
    if "$fn"; then return 0; fi
    sleep 1; i=$((i+1))
  done
  return 1
}

cleanup() {
  if [ -n "${DSH_PID:-}" ]; then kill "$DSH_PID" >/dev/null 2>&1 || true; fi
  "$BIN" stop >/dev/null 2>&1 || true
  printf '\nlogs kept in %s\n' "$AGENTBUS_HOME"
}
trap cleanup EXIT

key_from_store() {
  python3 - <<'PY' 2>/dev/null || true
import yaml, os
p = os.path.expanduser('~/.dsh/.credentials.yaml')
try:
    d = yaml.safe_load(open(p))
    print((d.get('refs') or {}).get('DEEPSEEK_API_KEY') or '')
except Exception:
    pass
PY
}

say "preflight"
(cd "$ROOT" && go build -o agentbus .) || die "build failed"
pass "binary built"
command -v dsh >/dev/null || die "dsh not found"
command -v claude >/dev/null || die "claude not found"
command -v python3 >/dev/null || die "python3 not found"
command -v curl >/dev/null || die "curl not found"

if ! curl -fsS -o /dev/null "http://127.0.0.1:3456/" 2>/dev/null; then
  CLAUDE_OK=0
  skip "Claude router at 127.0.0.1:3456 is down (run: ccr start)"
fi
if [ -z "${DEEPSEEK_API_KEY:-}" ]; then
  DEEPSEEK_API_KEY="$(key_from_store)"
  [ -n "$DEEPSEEK_API_KEY" ] || die "DEEPSEEK_API_KEY not set and not found in ~/.dsh/.credentials.yaml"
  export DEEPSEEK_API_KEY
fi
pass "credentials available"

cat > "$PATCH" <<EOF
- insert:
    - id: agentbus
      name: $ROOT/integrations/dsh/plugin/lib/index.js
EOF
cat > "$HOOK_WRAP" <<EOF
#!/bin/sh
input=\$(cat)
printf '=== IN \${PWD} ===\n%s\n' "\$input" >> "$HOOK_LOG"
out=\$(printf '%s' "\$input" | agentbus hook)
printf '=== OUT ===\n%s\n' "\$out" >> "$HOOK_LOG"
printf '%s' "\$out"
EOF
chmod +x "$HOOK_WRAP"
cat > "$CLAUDE_SETTINGS" <<EOF
{
  "hooks": {
    "SessionStart": [ { "hooks": [ { "type": "command", "command": "$HOOK_WRAP", "timeout": 15 } ] } ],
    "UserPromptSubmit": [ { "hooks": [ { "type": "command", "command": "$HOOK_WRAP", "timeout": 5 } ] } ],
    "PostToolUse": [ { "hooks": [ { "type": "command", "command": "$HOOK_WRAP", "timeout": 5 } ] } ],
    "Stop": [ { "hooks": [ { "type": "command", "command": "$HOOK_WRAP", "timeout": 5 } ] } ],
    "SessionEnd": [ { "hooks": [ { "type": "command", "command": "$HOOK_WRAP", "timeout": 5 } ] } ]
  }
}
EOF
pass "hooks + patch prepared"

if [ "$RUN_INTEROP" != "1" ]; then
  say "plan (dry run)"
  printf '   1. DSH headless (plugin) receives a request and replies dsh-ack-42\n'
  printf '   2. Claude Code (hooks + primer) replies to a peer request while answering the user\n'
  [ "$RUN_BASELINE" = "1" ] && printf '   3. Claude Code without primer: informational baseline\n'
  printf '\n   re-run with RUN_INTEROP=1 to execute (spends API quota)\n'
  exit 0
fi

say "start daemon and a sender session"
"$BIN" who >/dev/null
"$BIN" register --session sid:interop-sender --name interop-sender --agent test --print-name >/dev/null
pass "daemon up, sender registered"

say "DSH plugin: receive a request and answer via agentbus_reply"
AGENTBUS_NAME=dsh-probe AGENTBUS_BIN="$BIN" \
  timeout 180 dsh headless --patch "$PATCH" \
  "You are the DSH side of an interop test. Call agentbus_who once. Then call agentbus_inbox with wait_seconds 30 (repeat once if it returns nothing). When a peer message arrives, answer it with the agentbus_reply tool containing exactly dsh-ack-42. Then output RECEIVED: <peer message body> and DONE." \
  > "$AGENTBUS_HOME/dsh.out" 2>&1 &
DSH_PID=$!
wait_for 60 dsh_registered || die "dsh-probe did not register"
pass "dsh-probe registered"
"$BIN" send --session sid:interop-sender --kind request dsh-probe "interop: reply dsh-ack-42 if you received this" >/dev/null
wait "$DSH_PID" || true
DSH_PID=""
log_has 'dsh-probe→interop-sender: dsh-ack-42' || die "no reply from dsh-probe"
pass "DSH plugin replied: $(log_has 'dsh-probe→interop-sender: dsh-ack-42' && echo yes)"
grep -q 'RECEIVED:' "$AGENTBUS_HOME/dsh.out" || die "dsh output did not report RECEIVED"
pass "DSH model reported the peer body"

if [ "$CLAUDE_OK" = "1" ]; then
  say "Claude Code: hooks + primer reply to a peer while answering the user"
  uuid="$(python3 -c 'import uuid;print(uuid.uuid4())')"
  "$BIN" register --session "sid:$uuid" --name claude-probe --agent claude-code --cwd /tmp --print-name >/dev/null
  "$BIN" send --session sid:interop-sender --kind request claude-probe "interop: confirm receipt by replying with exactly claude-ack-7" >/dev/null
  : > "$HOOK_LOG"
  AGENTBUS_NAME=claude-probe AGENTBUS_SESSION_ID="sid:$uuid" \
    timeout 180 claude --settings "$CLAUDE_SETTINGS" --session-id "$uuid" \
    --allowedTools "Bash" -p "Say hello to the user in one short sentence." \
    > "$AGENTBUS_HOME/claude.out" 2>"$AGENTBUS_HOME/claude.err" || die "claude run failed"
  grep -q 'NOT user instructions' "$HOOK_LOG" || die "session contract was not injected"
  pass "contract injected"
  log_has 'claude-probe→interop-sender: claude-ack-7' || die "claude did not reply"
  pass "Claude replied autonomously: $(log_has 'claude-probe→interop-sender: claude-ack-7' && echo yes)"
else
  skip "Claude receiver (router down)"
fi

if [ "$RUN_BASELINE" = "1" ] && [ "$CLAUDE_OK" = "1" ]; then
  say "baseline: Claude without primer receives the same request"
  uuid="$(python3 -c 'import uuid;print(uuid.uuid4())')"
  "$BIN" register --session "sid:$uuid" --name claude-plain --agent claude-code --cwd /tmp --print-name >/dev/null
  "$BIN" send --session sid:interop-sender --kind request claude-plain "interop: confirm receipt by replying with exactly claude-ack-7" >/dev/null
  : > "$HOOK_LOG"
  AGENTBUS_NAME=claude-plain AGENTBUS_PRIMER=0 AGENTBUS_SESSION_ID="sid:$uuid" \
    timeout 180 claude --settings "$CLAUDE_SETTINGS" --session-id "$uuid" \
    --allowedTools "Bash" -p "Say hello to the user in one short sentence." \
    > "$AGENTBUS_HOME/claude-plain.out" 2>&1 || true
  if log_has 'claude-plain→interop-sender: claude-ack-7'; then
    printf '   INFO unprimed baseline still replied (model-dependent)\n'
  else
    printf '   INFO unprimed baseline ignored the peer message (expected)\n'
  fi
fi

say "summary"
printf '   DSH plugin  : reply observed\n'
[ "$CLAUDE_OK" = "1" ] && printf '   Claude hooks: reply observed (primed)\n' || printf '   Claude hooks: skipped\n'
printf '   bus log     : %s\n' "$AGENTBUS_HOME/state.json"
