#!/usr/bin/env bash
# agentbus installer: builds the binary, installs the cross-agent skill, and
# optionally wires hooks (all agents or one agent) or prints plugin paths.
#
# Usage:
#   ./scripts/install.sh                 # binary + skill
#   ./scripts/install.sh --hooks         # wire hooks for claude code + zcode + kimi
#   ./scripts/install.sh --zcode-hooks   # wire hooks for zcode only
#   ./scripts/install.sh --claude-hooks  # wire hooks for claude code only
#   ./scripts/install.sh --kimi-hooks    # wire hooks for kimi only
#   ./scripts/install.sh --plugin        # refresh + show installable plugin bundles
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN_DEST="${AGENTBUS_BIN:-$HOME/.local/bin}"
CLAUDE_HOOKS=0
ZCODE_HOOKS=0
KIMI_HOOKS=0
PLUGIN=0
for arg in "$@"; do
  case "$arg" in
    --hooks)        CLAUDE_HOOKS=1; ZCODE_HOOKS=1; KIMI_HOOKS=1 ;;
    --claude-hooks) CLAUDE_HOOKS=1 ;;
    --zcode-hooks)  ZCODE_HOOKS=1 ;;
    --kimi-hooks)   KIMI_HOOKS=1 ;;
    --plugin)       PLUGIN=1 ;;
    -h|--help)
      sed -n '5,10p' "$0" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *)
      echo "unknown option: $arg (expected --hooks, --claude-hooks, --zcode-hooks, --kimi-hooks or --plugin)" >&2
      exit 2 ;;
  esac
done
if { [ "$CLAUDE_HOOKS" = "1" ] || [ "$ZCODE_HOOKS" = "1" ] || [ "$KIMI_HOOKS" = "1" ]; } && [ "$PLUGIN" = "1" ]; then
  echo "hooks and --plugin wire the same events; pick one to avoid double delivery" >&2
  exit 2
fi

echo "== building agentbus"
mkdir -p "$BIN_DEST"
(cd "$ROOT" && go build -o "$BIN_DEST/agentbus" .)
echo "   installed: $BIN_DEST/agentbus ($("$BIN_DEST/agentbus" version))"

echo "== installing skill"
SKILL_SRC="$ROOT/skills/agentbus"
install_skill() {
  local dir="$1" created="${2:-}"
  if [ -d "$(dirname "$dir")" ] || [ "$created" = "always" ]; then
    mkdir -p "$dir"
    cp "$SKILL_SRC/SKILL.md" "$dir/SKILL.md"
    echo "   skill: $dir"
  fi
}
install_skill "$HOME/.agents/skills/agentbus" always      # shared across agents
install_skill "$HOME/.claude/skills/agentbus"             # claude code, if present
# zcode scans the shared ~/.agents/skills directory too; installing a second
# copy in ~/.zcode/skills would show the same skill twice in its catalog.
if [ ! -f "$HOME/.agents/skills/agentbus/SKILL.md" ]; then
  install_skill "$HOME/.zcode/skills/agentbus"
fi
install_skill "$HOME/.kimi/skills/agentbus"               # legacy kimi, if present
install_skill "$HOME/.kimi-code/skills/agentbus"          # kimi code, if present

if [ "$PLUGIN" = "1" ]; then
  echo "== plugin bundles (hooks + skill in one package)"
  # Keep the bundled skill copies identical to the canonical one.
  for d in "$ROOT/integrations/claude-code/plugin" "$ROOT/integrations/zcode/plugin"; do
    mkdir -p "$d/skills/agentbus"
    cp "$ROOT/skills/agentbus/SKILL.md" "$d/skills/agentbus/SKILL.md"
  done
  cat <<EOF
   claude code: add the local marketplace once, then install the plugin:
                  claude plugin marketplace add "$ROOT/integrations/claude-code"
                  claude plugin install agentbus@agentbus
                or load it for one session only:
                  claude --plugin-dir "$ROOT/integrations/claude-code/plugin"
   zcode:       Settings → Plugin Management → add the local marketplace
                "$ROOT/integrations/zcode", then install "agentbus"
                (plugin hooks need no hooks.enabled flag; they register themselves)
                or simply use:  ./scripts/install.sh --zcode-hooks
   dsh:         native Cordis plugin; add a loader row or install the package:
                  dsh plugin --profile web add "$ROOT/integrations/dsh/plugin"
                then follow docs/integrations/dsh.md for the insert row.
   The bundles carry the same hooks as --hooks plus the skill — use one or the other.
EOF
fi

if [ "$CLAUDE_HOOKS" = "1" ] || [ "$ZCODE_HOOKS" = "1" ] || [ "$KIMI_HOOKS" = "1" ]; then
  echo "== wiring hooks"
  command -v python3 >/dev/null || { echo "   !! python3 required; skipping hooks (see docs/integrations/)"; exit 1; }
  ROOT="$ROOT" DO_CLAUDE="$CLAUDE_HOOKS" DO_ZCODE="$ZCODE_HOOKS" python3 - <<'PY'
import json, os, shutil

def load_json(path):
    if not os.path.exists(path):
        return {}
    shutil.copyfile(path, path + ".agentbus-bak")
    try:
        with open(path) as f:
            return json.load(f)
    except json.JSONDecodeError:
        print(f"   !! {path} is not valid JSON; skipped (backup at .agentbus-bak)")
        return None

def save_json(path, cfg):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        json.dump(cfg, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print(f"   hooks: {path} (backup at .agentbus-bak)")

cmd = lambda t: {"type": "command", "command": "agentbus hook", **t}

if os.environ.get("DO_CLAUDE") == "1":
    path = os.path.expanduser("~/.claude/settings.json")
    cfg = load_json(path)
    if cfg is not None:
        hooks = {
            "SessionStart":     [{"hooks": [cmd({"timeout": 15})]}],
            "UserPromptSubmit": [{"hooks": [cmd({"timeout": 5})]}],
            "PostToolUse":      [{"hooks": [cmd({"timeout": 5})]}],
            "Stop":             [{"hooks": [cmd({"timeout": 5})]}],
            "SessionEnd":       [{"hooks": [cmd({"timeout": 5})]}],
        }
        for event, entries in hooks.items():
            kept = [e for e in cfg.get("hooks", {}).get(event, [])
                    if "agentbus" not in json.dumps(e)]
            kept += entries
            cfg.setdefault("hooks", {})[event] = kept
        save_json(path, cfg)

if os.environ.get("DO_ZCODE") == "1":
    path = os.path.expanduser("~/.zcode/cli/config.json")
    cfg = load_json(path)
    if cfg is not None:
        # ZCode configuration hooks: hooks.enabled + hooks.events.<Event>.
        hooks = {
            "SessionStart":     [{"hooks": [cmd({"timeoutMs": 15000})]}],
            "UserPromptSubmit": [{"hooks": [cmd({"timeoutMs": 5000})]}],
            "PostToolUse":      [{"hooks": [cmd({"timeoutMs": 5000})]}],
            "Stop":             [{"hooks": [cmd({"timeoutMs": 5000})]}],
        }
        h = cfg.setdefault("hooks", {})
        h["enabled"] = True
        events = h.setdefault("events", {})
        for event, entries in hooks.items():
            kept = [e for e in events.get(event, [])
                    if "agentbus" not in json.dumps(e)]
            events[event] = kept + entries
        save_json(path, cfg)

if os.environ.get("DO_KIMI") == "1":
    # Kimi Code: TOML [[hooks]] array; stdout is appended to context and Stop
    # blocks with exit code 2, so `agentbus hook` switches output modes from
    # the payload's client_type.
    home = os.path.expanduser("~/.kimi-code")
    path = os.path.join(home, "config.toml")
    if os.path.exists(path):
        text = open(path).read()
        marker = "# agentbus hooks"
        if marker not in text:
            shutil.copyfile(path, path + ".agentbus-bak")
            binary = os.path.join(os.environ.get("BIN_DEST", os.path.expanduser("~/.local/bin")), "agentbus")
            block = f"""
# agentbus hooks (managed by agentbus; remove this block to uninstall)
[[hooks]]
event = "SessionStart"
command = "{binary} hook"
timeout = 15

[[hooks]]
event = "UserPromptSubmit"
command = "{binary} hook"
timeout = 5

[[hooks]]
event = "PostToolUse"
command = "{binary} hook"
timeout = 5

[[hooks]]
event = "Stop"
command = "{binary} hook"
timeout = 5

[[hooks]]
event = "SessionEnd"
command = "{binary} hook"
timeout = 5
"""
            with open(path, "a") as f:
                f.write(block)
            print(f"   hooks: {path} (backup at .agentbus-bak)")
        else:
            print(f"   hooks: {path} (already configured)")
        # Global instructions: Kimi loads ~/.kimi-code/AGENTS.md every session.
        agents = os.path.join(home, "AGENTS.md")
        primer = os.popen(binary + " primer").read()
        block = "<!-- agentbus:begin -->\n" + primer + "<!-- agentbus:end -->\n"
        existing = open(agents).read() if os.path.exists(agents) else ""
        if "<!-- agentbus:begin -->" not in existing:
            if os.path.exists(agents):
                shutil.copyfile(agents, agents + ".agentbus-bak")
            with open(agents, "a") as f:
                f.write("\n" + block)
            print(f"   instructions: {agents}")
        else:
            print(f"   instructions: {agents} (already configured)")
PY
fi

echo "== doctor"
"$BIN_DEST/agentbus" doctor || true

case ":$PATH:" in
  *":$BIN_DEST:"*) ;;
  *) echo "NOTE: $BIN_DEST is not on PATH — hooks need to run 'agentbus'; add it to your shell profile." ;;
esac
echo "done."
