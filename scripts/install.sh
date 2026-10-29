#!/usr/bin/env bash
# agentbus installer: builds the binary, installs the cross-agent skill, and
# optionally wires hooks (--hooks) or prints plugin-bundle paths (--plugin).
#
# Usage:
#   ./scripts/install.sh              # binary + skill
#   ./scripts/install.sh --hooks      # additionally wire hooks (backs up settings)
#   ./scripts/install.sh --plugin     # refresh + show the installable plugin bundles
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN_DEST="${AGENTBUS_BIN:-$HOME/.local/bin}"
HOOKS=0
PLUGIN=0
for arg in "$@"; do
  case "$arg" in
    --hooks)  HOOKS=1 ;;
    --plugin) PLUGIN=1 ;;
    -h|--help)
      sed -n '5,8p' "$0" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *)
      echo "unknown option: $arg (expected --hooks or --plugin)" >&2
      exit 2 ;;
  esac
done
if [ "$HOOKS" = "1" ] && [ "$PLUGIN" = "1" ]; then
  echo "--hooks and --plugin wire the same hooks; pick one to avoid double delivery" >&2
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
install_skill "$HOME/.zcode/skills/agentbus"              # zcode, if present
install_skill "$HOME/.kimi/skills/agentbus"               # kimi, if present

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
   dsh:         native Cordis plugin; add a loader row or install the package:
                  dsh plugin --profile web add "$ROOT/integrations/dsh/plugin"
                then follow docs/integrations/dsh.md for the insert row.
   The bundles carry the same hooks as --hooks plus the skill — use one or the other.
EOF
fi

if [ "$HOOKS" = "1" ]; then
  echo "== wiring hooks"
  command -v python3 >/dev/null || { echo "   !! python3 required for --hooks; skipping (see integrations/)"; HOOKS=0; }
fi
if [ "$HOOKS" = "1" ]; then
  ROOT="$ROOT" python3 - <<'PY'
import json, os, shutil, sys

root = os.environ["ROOT"]

def merge(path, hooks, backup=True):
    cfg = {}
    if os.path.exists(path):
        shutil.copyfile(path, path + ".agentbus-bak")
        try:
            with open(path) as f:
                cfg = json.load(f)
        except json.JSONDecodeError:
            print(f"   !! {path} is not valid JSON; skipped (backup at .agentbus-bak)")
            return
    # idempotent: drop previous agentbus hook entries, then add fresh ones
    for event, entries in hooks.items():
        kept = [e for e in cfg.get("hooks", {}).get(event, [])
                if "agentbus" not in json.dumps(e)]
        kept += entries
        cfg.setdefault("hooks", {})[event] = kept
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        json.dump(cfg, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print(f"   hooks: {path}" + (" (backup saved)" if os.path.exists(path + ".agentbus-bak") else ""))

cmd = lambda t: {"type": "command", "command": "agentbus hook", **t}

claude_hooks = {
    "SessionStart":    [{"hooks": [cmd({"timeout": 15})]}],
    "UserPromptSubmit":[{"hooks": [cmd({"timeout": 5})]}],
    "PostToolUse":     [{"hooks": [cmd({"timeout": 5})]}],
    "Stop":            [{"hooks": [cmd({"timeout": 5})]}],
    "SessionEnd":      [{"hooks": [cmd({"timeout": 5})]}],
}
merge(os.path.expanduser("~/.claude/settings.json"), claude_hooks)

# ZCode: hooks.events.* + hooks.enabled
zpath = os.path.expanduser("~/.zcode/cli/config.json")
cfg = {}
if os.path.exists(zpath):
    shutil.copyfile(zpath, zpath + ".agentbus-bak")
    with open(zpath) as f:
        cfg = json.load(f)
zcode_hooks = {
    "SessionStart":    [{"hooks": [cmd({"timeoutMs": 15000})]}],
    "UserPromptSubmit":[{"hooks": [cmd({"timeoutMs": 5000})]}],
    "PostToolUse":     [{"hooks": [cmd({"timeoutMs": 5000})]}],
    "Stop":            [{"hooks": [cmd({"timeoutMs": 5000})]}],
}
h = cfg.setdefault("hooks", {})
h["enabled"] = True
for event, entries in zcode_hooks.items():
    kept = [e for e in h.get("events", {}).get(event, [])
            if "agentbus" not in json.dumps(e)]
    kept += entries
    h.setdefault("events", {})[event] = kept
os.makedirs(os.path.dirname(zpath), exist_ok=True)
with open(zpath, "w") as f:
    json.dump(cfg, f, indent=2, ensure_ascii=False)
    f.write("\n")
print(f"   hooks: {zpath} (backup saved)" if os.path.exists(zpath + ".agentbus-bak") else f"   hooks: {zpath}")
PY
fi

echo "== doctor"
"$BIN_DEST/agentbus" doctor || true

case ":$PATH:" in
  *":$BIN_DEST:"*) ;;
  *) echo "NOTE: $BIN_DEST is not on PATH — hooks need to run 'agentbus'; add it to your shell profile." ;;
esac
echo "done."
