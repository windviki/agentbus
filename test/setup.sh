#!/usr/bin/env bash
# Exercise `agentbus setup` against a throwaway HOME: detection, idempotency,
# backups, JSON report, doctor and uninstall. No real agent config is touched.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap '"$TMP/bin/agentbus" stop >/dev/null 2>&1 || true; rm -rf "$TMP"' EXIT
HOME_DIR="$TMP/home"
BUS="$TMP/bus"
BIN="$TMP/bin/agentbus"
mkdir -p "$HOME_DIR/.claude" "$HOME_DIR/.zcode/cli" "$HOME_DIR/.kimi-code" "$TMP/bin"

fail() { echo "setup-test: FAIL: $*" >&2; echo "--- tree ---" >&2; find "$HOME_DIR" -type f -maxdepth 6 >&2; exit 1; }

echo "== build"
(cd "$ROOT" && go build -o "$BIN" .)
export PATH="$TMP/bin:/usr/bin:/bin"
export HOME="$HOME_DIR"
export AGENTBUS_HOME="$BUS"
export AGENTBUS_WEB=0
export DSH_HOME="$TMP/dsh"   # never touch the real ~/.dsh when uninstalling

cat >"$HOME_DIR/.claude/settings.json" <<'EOF'
{
  "env": {"KEEP": "1"},
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "echo custom"}]}
    ]
  }
}
EOF
echo '{}' >"$HOME_DIR/.zcode/cli/config.json"
cat >"$HOME_DIR/.kimi-code/config.toml" <<'EOF'
model = "k2"

# agentbus hooks (managed by agentbus; remove this block to uninstall)
[[hooks]]
event = "Stop"
command = "/old/agentbus hook"
timeout = 5
EOF

echo "== dry-run writes nothing"
"$BIN" setup --dry-run --json --agents claude,zcode,kimi >"$TMP/dry.json"
[ ! -e "$HOME_DIR/.agents/skills/agentbus/SKILL.md" ] || fail "dry-run installed the skill"
"$BIN" setup --dry-run --json --agents claude,zcode,kimi >"$TMP/dry.json"
python3 - "$TMP/dry.json" <<'PY'
import json,sys
r=json.load(open(sys.argv[1]))
assert r["dryRun"] is True
assert {a["id"] for a in r["detected"]} == {"claude","zcode","kimi","dsh"}, r["detected"]
assert r["actions"], r
PY

echo "== install"
"$BIN" setup --yes --agents claude,zcode,kimi >"$TMP/install.log"

python3 - "$HOME_DIR" <<'PY'
import json, os, sys
home = sys.argv[1]
claude = json.load(open(os.path.join(home, ".claude/settings.json")))
hooks = claude["hooks"]
events = ["SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd"]
for ev in events:
    cmds = [h["command"] for e in hooks.get(ev, []) for h in e.get("hooks", [])]
    assert cmds.count("agentbus hook") == 1, (ev, cmds)
assert any(h["command"] == "echo custom" for e in hooks["Stop"] for h in e.get("hooks", [])), hooks["Stop"]
assert claude["env"]["KEEP"] == "1"

z = json.load(open(os.path.join(home, ".zcode/cli/config.json")))
assert z["hooks"]["enabled"] is True
for ev in ["SessionStart", "UserPromptSubmit", "PostToolUse", "Stop"]:
    cmds = [h["command"] for e in z["hooks"]["events"][ev] for h in e.get("hooks", [])]
    assert cmds.count("agentbus hook") == 1, (ev, cmds)

toml = open(os.path.join(home, ".kimi-code/config.toml")).read()
assert toml.count("# >>> agentbus") == 1, toml
assert "/old/agentbus hook" not in toml, toml
assert 'model = "k2"' in toml
agents = open(os.path.join(home, ".kimi-code/AGENTS.md")).read()
assert "<!-- agentbus:begin -->" in agents and "AgentBus" in agents
for p in [".agents/skills/agentbus/SKILL.md", ".claude/skills/agentbus/SKILL.md",
          ".kimi-code/skills/agentbus/SKILL.md"]:
    assert os.path.getsize(os.path.join(home, p)) > 1000, p
print("   files look correct")
PY

echo "== idempotent"
before="$(cd "$HOME_DIR" && find . -type f ! -name '*.agentbus-bak' | sort | xargs sha256sum | sha256sum)"
"$BIN" setup --yes --agents claude,zcode,kimi >"$TMP/install2.log"
after="$(cd "$HOME_DIR" && find . -type f ! -name '*.agentbus-bak' | sort | xargs sha256sum | sha256sum)"
[ "$before" = "$after" ] || fail "second setup changed files"
grep -q 'agentbus setup' "$TMP/install2.log" || fail "second setup produced no report"

echo "== uninstall dry-run writes nothing"
"$BIN" setup --yes --uninstall --dry-run >/dev/null
grep -q 'agentbus hook' "$HOME_DIR/.claude/settings.json" || fail "dry-run uninstall removed hooks"

echo "== doctor"
"$BIN" doctor >"$TMP/doctor.log" || fail "doctor exited non-zero"
grep -q 'integration: Claude Code  wired' "$TMP/doctor.log" || fail "doctor did not report the claude wiring"
grep -q 'PATH: agentbus resolves' "$TMP/doctor.log" || fail "doctor did not resolve agentbus"

echo "== uninstall"
"$BIN" setup --yes --uninstall >"$TMP/uninstall.log"
python3 - "$HOME_DIR" <<'PY'
import json, os, sys
home = sys.argv[1]
claude = json.load(open(os.path.join(home, ".claude/settings.json")))
cmds = [h["command"] for e in claude["hooks"]["Stop"] for h in e.get("hooks", [])]
assert "agentbus hook" not in cmds, cmds
assert "echo custom" in cmds, cmds
assert claude["env"]["KEEP"] == "1"
z = json.load(open(os.path.join(home, ".zcode/cli/config.json")))
assert "agentbus hook" not in json.dumps(z)
toml = open(os.path.join(home, ".kimi-code/config.toml")).read()
assert "agentbus" not in toml, toml
assert 'model = "k2"' in toml, toml
assert not os.path.exists(os.path.join(home, ".kimi-code/AGENTS.md"))
assert not os.path.exists(os.path.join(home, ".agents/skills/agentbus"))
print("   wiring removed")
PY

echo "== unknown agent"
if "$BIN" setup --agents chatgpt >/dev/null 2>&1; then fail "unknown agent accepted"; fi

echo "setup-test: ok"
