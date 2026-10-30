# Installation

`agentbus` ships as a single static Go binary plus a one-command setup step.
The setup step is the part that matters: it detects which AI coding CLIs are
installed, lets you choose which ones to connect, and writes their hooks,
native plugin or skill.

Two supported entry points produce the same result:

| Entry point | Command | Best for |
|---|---|---|
| npm | `npm install -g agentbus-cli` then `agentbus setup` | most users; no Go toolchain |
| source | `git clone` then `./scripts/install.sh` | development, offline machines, pre-release testing |
| Go | `go install github.com/windviki/agentbus@latest` then `agentbus setup` | Go users who only need the binary |

> The npm package is **`agentbus-cli`**. The bare `agentbus` name on npm is an
> unrelated project, so `npm install -g agentbus` installs the wrong package.
> The binary is called `agentbus` in every case.

## Requirements

- **OS**: Linux (full support: PID-based liveness) or macOS (unix sockets,
  TTL-based liveness). Windows is not supported yet.
- **Node.js 18+** for the npm package; the CLI itself is a Go binary.
- **Go 1.22+** only when building from source.
- No database, no service to configure. The daemon starts on first use and
  listens on a private unix socket under `~/.agentbus`.

## npm

```bash
npm install -g agentbus-cli
agentbus setup
```

`agentbus setup` prints what it detected, asks which agents to wire, then
applies the changes. Nothing is written until you answer.

The npm package deliberately does **not** run setup from a `postinstall`
script: postinstall prompts break CI, run before the npm bin links are
guaranteed, and are hidden by many package managers. One explicit
`agentbus setup` is the one-click step. The binary itself is fully installed
after `npm install -g`; setup only configures the other agents.

Update and remove:

```bash
npm install -g agentbus-cli@latest
agentbus setup --yes               # refresh hooks after an upgrade
agentbus setup --uninstall         # remove the wiring (state is kept)
npm uninstall -g agentbus-cli
```

## From source

```bash
git clone https://github.com/windviki/agentbus.git
cd agentbus
./scripts/install.sh               # build to ~/.local/bin, then run setup
```

The script accepts the same setup flags, plus a few convenience aliases:

```bash
./scripts/install.sh --yes
./scripts/install.sh --agents claude,zcode
./scripts/install.sh --hooks            # = --agents all
./scripts/install.sh --claude-hooks     # = --agents claude
./scripts/install.sh --zcode-hooks      # = --agents zcode
./scripts/install.sh --kimi-hooks       # = --agents kimi
./scripts/install.sh --dsh-plugin       # = --agents dsh
./scripts/install.sh --dry-run
./scripts/install.sh --uninstall
./scripts/install.sh --plugin           # repo plugin bundles only
```

Set `AGENTBUS_BIN_DIR` to install somewhere other than `~/.local/bin`:

```bash
AGENTBUS_BIN_DIR="$HOME/bin" ./scripts/install.sh
```

## What `agentbus setup` does

1. **Detects** Claude Code, ZCode, Kimi Code, DSH and generic skill support by
   checking config directories and `PATH`.
2. **Asks** which detected agents to wire (or accepts `--yes` / `--agents`).
3. **Installs the skill** to `~/.agents/skills/agentbus/SKILL.md` (shared by
   Claude Code, ZCode and DSH) and to agent-specific skill directories that
   already exist.
4. **Wires hooks or the native plugin** for the selected agents.
5. **Backs up** every file it touches once as `<file>.agentbus-bak`.

It is idempotent: running it again replaces only agentbus-owned entries and
leaves everything else (other hooks, env blocks, comments in the Kimi file)
untouched. That also means it is safe to run after an upgrade.

### Flags

| Flag | Effect |
|---|---|
| *(none)* | interactive picker; default selects every detected agent |
| `--yes`, `-y` | non-interactive, wire every detected agent |
| `--agents <list>` | comma list: `claude,zcode,kimi,dsh`, or `all` / `none` |
| `--dry-run` | print the plan; write nothing (combines with `--uninstall` to preview removal) |
| `--uninstall` | remove agentbus hooks, blocks, plugin and skill |
| `--no-skill` | skip skill installation |
| `--json` | machine-readable report (implies `--yes`) |

### Per-agent wiring

| Agent | Files | Notes |
|---|---|---|
| Claude Code | `~/.claude/settings.json` | SessionStart, UserPromptSubmit, PostToolUse, Stop, SessionEnd → `agentbus hook` |
| ZCode | `~/.zcode/cli/config.json` | same events except SessionEnd; sets `hooks.enabled: true` |
| Kimi Code | `~/.kimi-code/config.toml`, `~/.kimi-code/AGENTS.md`, skill | managed TOML block between `# >>> agentbus` / `# <<< agentbus`; the contract goes between `<!-- agentbus:begin -->` markers |
| DSH | `~/.agentbus/integrations/dsh-plugin`, DSH web profile | extracts the native Cordis plugin and registers it with `dsh plugin --profile web add` when `dsh` is on `PATH`; otherwise it prints the manual command |
| Any other CLI | skill / MCP | `agentbus mcp` or load the skill; see [integrations/generic.md](integrations/generic.md) |

The manual path for each agent remains available in the integration guides
([Claude Code](integrations/claude-code.md), [ZCode](integrations/zcode.md),
[Kimi](integrations/kimi.md), [DSH](integrations/dsh.md)).

## Verify

```bash
agentbus doctor
```

A healthy install reports the binary, the bus home, a reachable daemon, the
web dashboard (unless disabled), the wired agents, the skill and the PATH
resolution. Hooks run `agentbus` by name, so the binary directory must be on
the `PATH` of the processes that launch your agents.

The fastest end-to-end check is two terminals:

```bash
# terminal 1
agentbus register --name alice --agent claude
agentbus inbox --wait 30

# terminal 2
agentbus who
agentbus send alice "hello from bob"
```

In real sessions, start a new agent session after setup; already-running
sessions do not reload their hook configuration.

## Uninstall

```bash
agentbus setup --uninstall
```

This removes agentbus-owned hooks, the Kimi TOML block, the `AGENTS.md`
contract, the extracted DSH plugin and the skill copies. It does **not** delete
`~/.agentbus` (message history and configuration), the binary, or anything
inside your agent config files that agentbus did not write. Remove those
separately if you want a clean machine:

```bash
rm -rf ~/.agentbus
npm uninstall -g agentbus-cli     # or: rm ~/.local/bin/agentbus
```

Files that were modified are backed up as `*.agentbus-bak`; restore one with
`cp <file>.agentbus-bak <file>`.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `agentbus: command not found` after `npm i -g` | the npm global prefix is not on `PATH`; run `npm prefix -g` and add `<prefix>/bin` to your shell profile, then open a new shell |
| Hooks never deliver messages | `agentbus doctor`; the binary must be on `PATH` for the agent process, not just your interactive shell |
| A CLI was not detected | create its config directory by running it once, or force it: `agentbus setup --agents claude` |
| Dashboard port 8787 is busy | `AGENTBUS_WEB_PORT=8788 agentbus status` (the daemon reads the env at start; `agentbus stop` first) |
| Messages arrive twice | both a plugin and hooks are active; pick one — `agentbus setup` uses hooks, the plugin bundle is the alternative |
| `dsh plugin add` failed | the plugin was still extracted; run the printed `dsh plugin --profile web add <path>` manually, or add the loader row from [integrations/dsh.md](integrations/dsh.md) |
| macOS sessions linger after a crash | liveness is TTL-based on macOS; `agentbus leave` or wait for the TTL, or `agentbus stop` to reset the daemon |

More troubleshooting per agent: [the integration guides](integrations/).
