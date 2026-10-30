#!/usr/bin/env bash
# agentbus installer for a source checkout.
#
# It builds the binary, then delegates every integration decision to
# `agentbus setup`, which detects the AI CLIs on this machine, lets you pick
# which ones to wire, and writes their hooks / plugin / skill. The npm package
# runs the same `agentbus setup` code — this script exists for `git clone`
# installs and offline machines.
#
# Usage:
#   ./scripts/install.sh                 # build + interactive agent picker
#   ./scripts/install.sh --yes           # build + wire every detected agent
#   ./scripts/install.sh --agents claude,zcode
#   ./scripts/install.sh --hooks         # alias for --agents all
#   ./scripts/install.sh --zcode-hooks   # alias for --agents zcode
#   ./scripts/install.sh --kimi-hooks    # alias for --agents kimi
#   ./scripts/install.sh --dsh-plugin    # alias for --agents dsh
#   ./scripts/install.sh --dry-run       # show the plan, write nothing
#   ./scripts/install.sh --uninstall     # remove the wiring
#   ./scripts/install.sh --plugin        # refresh repo plugin bundles, print paths
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN_DEST="${AGENTBUS_BIN_DIR:-$HOME/.local/bin}"
PLUGIN=0
JSON=0
SETUP_ARGS=()
log() { if [ "$JSON" = "1" ]; then echo "$@" >&2; else echo "$@"; fi; }

die() { echo "install: $*" >&2; exit 1; }

while [ "$#" -gt 0 ]; do
  arg="$1"
  shift
  case "$arg" in
    --hooks)         SETUP_ARGS+=(--agents all) ;;
    --claude-hooks)  SETUP_ARGS+=(--agents claude) ;;
    --zcode-hooks)   SETUP_ARGS+=(--agents zcode) ;;
    --kimi-hooks)    SETUP_ARGS+=(--agents kimi) ;;
    --dsh-plugin)    SETUP_ARGS+=(--agents dsh) ;;
    --plugin)        PLUGIN=1 ;;
    --yes|-y)        SETUP_ARGS+=(--yes) ;;
    --dry-run)       SETUP_ARGS+=(--dry-run) ;;
    --uninstall)     SETUP_ARGS+=(--uninstall) ;;
    --no-skill)      SETUP_ARGS+=(--no-skill) ;;
    --json)          JSON=1; SETUP_ARGS+=(--json) ;;
    --agents)
      [ "$#" -gt 0 ] || die "--agents needs a value (claude,zcode,kimi,dsh,all)"
      SETUP_ARGS+=(--agents "$1"); shift ;;
    --agents=*)      SETUP_ARGS+=(--agents "${arg#--agents=}") ;;
    -h|--help)
      sed -n '/^# Usage:/,/^set -euo pipefail/p' "$0" | grep '^#' | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *)
      die "unknown option: $arg (see --help)" ;;
  esac
done

if [ "$PLUGIN" = "1" ] && [ "${#SETUP_ARGS[@]}" -gt 0 ]; then
  die "--plugin prints installable bundles; it cannot be combined with hook setup flags"
fi

if [ "$PLUGIN" = "1" ]; then
  echo "== plugin bundles (hooks + skill in one package)"
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
   dsh:         `agentbus setup --agents dsh` installs and registers the native
                plugin (recommended); see docs/integrations/dsh.md for the manual row.
   The bundles carry the same hooks as `setup` plus the skill — use one or the other.
EOF
  exit 0
fi

[ -f "$ROOT/go.mod" ] || die "run this from an agentbus source checkout"
command -v go >/dev/null || die "Go 1.22+ is required to build from source; install Go, or use 'npm i -g agentbus-cli'"

log "== building agentbus"
mkdir -p "$BIN_DEST"
(cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -o "$BIN_DEST/agentbus" .)
log "   installed: $BIN_DEST/agentbus ($("$BIN_DEST/agentbus" version))"

echo
"$BIN_DEST/agentbus" setup "${SETUP_ARGS[@]}"

case ":$PATH:" in
  *":$BIN_DEST:"*) ;;
  *) log "NOTE: $BIN_DEST is not on PATH — hooks invoke 'agentbus' by name, so add it to your shell profile." ;;
esac
