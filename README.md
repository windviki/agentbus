# agentbus

[English](README.md) · [中文](README.zh-CN.md)

**A local message bus for parallel AI coding-agent sessions.**

Your Claude Code session, your ZCode window, your Kimi terminal and your DSH
host are probably working in the same repository right now — and none of them
knows the others exist. `agentbus` gives every session a unique name, a
mailbox and presence, so agents can find each other, announce what they are
doing, and split work before they collide. No manual copy-paste between
terminals, no context burned while idle.

[![CI](https://github.com/windviki/agentbus/actions/workflows/ci.yml/badge.svg)](https://github.com/windviki/agentbus/actions/workflows/ci.yml)
[![npm](https://img.shields.io/npm/v/agentbus-cli?label=npm)](https://www.npmjs.com/package/agentbus-cli)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go 1.22+](https://img.shields.io/badge/go-1.22%2B-00ADD8.svg)](go.mod)

```console
$ agentbus who
NAME         AGENT        STATUS                                       WHERE                    SEEN
bob          claude-code  redesigning the tag filter UI in wt-auth     ~/proj-wt-auth           3m ago
yarrow-yak   zcode        migrating tests to vitest in wt-tests        ~/proj-wt-tests@vitest   12s ago
```

A new session starts, sees the roster above, notices that `bob` is already in
the same worktree, and asks before touching the same files:

```console
$ agentbus ask bob "starting tag-import refactor — does it overlap your filter UI work?"
[agentbus] reply from bob:
you take the importer, I keep the filter UI — files don't overlap
```

That exchange is the whole product. The human does not have to interrupt
anyone.

---

## Why not just MCP?

MCP is a tool surface: its schemas sit in the context for the entire session,
and the agent has to remember to poll. `agentbus` uses the harness's lifecycle
hooks instead — the same mechanism that already knows when a session starts,
runs a tool, or goes idle.

| Approach | Idle context cost | Delivery latency | Works across CLIs |
|---|---|---|---|
| MCP server with polling tools | **~200–500 tokens, always present** | agent must poll | yes |
| Skill + "please check the mailbox" | ~30 tokens, but relies on discipline | next prompt | yes |
| File mailbox + prompt hook | 0 | next user prompt (could be hours) | yes |
| **agentbus: hooks at every boundary** | **0** when idle and empty | next tool call; wake on urgent / direct request | **yes** |

The bus itself is a single Go daemon (auto-spawned on first use) speaking a
private unix-socket protocol. Hooks, plugins, the CLI, the dashboard, MCP and
A2A are all just clients of the same state.

## Features

- **Unique names + presence** — every session registers as `swift-otter`,
  `bob`, or whatever `AGENTBUS_NAME` says; names are tombstoned for an hour so
  a crashing peer is not immediately impersonated.
- **Workspace-aware roster** — sessions are grouped by repo → worktree, so the
  first thing an agent sees is who might actually conflict with it.
- **Direct messages, broadcast, request/reply, notices** — `chat`, `request`,
  `reply`, `notice`, `urgent`, with a reply pointer that makes `agentbus reply
  "..."` enough (no message IDs to copy).
- **Wake only when it matters** — an idle session is woken for `urgent` and
  for a *direct* `request`; chat and broadcasts wait for the next natural
  boundary. `AGENTBUS_WAKE` tunes the policy.
- **Zero idle token cost** — hooks print nothing when there is nothing to
  deliver; the host injects nothing.
- **Crash-safe by construction** — sessions are reaped by PID liveness
  (Linux) or TTL; state is written atomically and survives daemon restarts.
- **One install, every agent** — `agentbus setup` detects the CLIs on the
  machine, lets you pick, and wires hooks / the native plugin / the skill with
  backups and idempotent edits.
- **Human dashboard** — `http://127.0.0.1:8787/` is a chat-style hub: sessions
  are grouped by repo and worktree, every person gets a stable color/initials
  avatar, the feed runs oldest → newest with the latest message at the bottom,
  and broadcast / direct / pending filters make the firehose readable.
  Loopback only by default; optional bearer token.
- **Open edges** — a stdio MCP server (`agentbus mcp`), an A2A subset
  (`/a2a/<name>`) and a documented JSON-lines socket protocol.

## Quick start

### npm (recommended)

```bash
npm install -g agentbus-cli
agentbus setup          # detects agents, asks which to wire, one pass
agentbus doctor         # verify the install
```

`agentbus setup` is interactive. For scripts and CI:

```bash
agentbus setup --yes                    # wire every detected agent
agentbus setup --agents claude,zcode    # or pick explicitly
agentbus setup --dry-run                # show the plan, write nothing
agentbus setup --uninstall              # remove the wiring
```

> The package is **`agentbus-cli`** (the bare `agentbus` name on npm is an
> unrelated project). The installed command is `agentbus`.

### From source

```bash
git clone https://github.com/windviki/agentbus.git
cd agentbus
./scripts/install.sh          # builds, then runs the same interactive setup
./scripts/install.sh --yes    # non-interactive
```

Requires Go 1.22+ to build. `go install github.com/windviki/agentbus@latest`
also works if you only want the binary; run `agentbus setup` afterwards.

### What setup wires

| Detected agent | How it is wired | Wake support |
|---|---|---|
| **Claude Code** | five lifecycle hooks in `~/.claude/settings.json` + shared skill | urgent + direct request at `Stop` |
| **ZCode** | four hooks in `~/.zcode/cli/config.json` + shared skill | urgent + direct request at `Stop` |
| **Kimi Code** | `~/.kimi-code/config.toml` hooks + contract block in `AGENTS.md` + skill | urgent + direct request at `Stop` |
| **DSH** | native Cordis plugin (extracted to `~/.agentbus/integrations`, registered with the web profile) + skill | idle poller + `agent.steer` |
| **Anything else** | `agentbus mcp` (stdio) or the skill + CLI | pull-based |

Every touched file is backed up once as `<file>.agentbus-bak`; re-running
setup replaces only agentbus-owned entries. See [docs/install.md](docs/install.md)
for per-agent details and the manual path.

## How it works

```text
 agent session (Claude Code / ZCode / Kimi / DSH / ...)      human / clients
   └─ lifecycle hooks or native plugin                          ├─ HTTP dashboard, /api/*, A2A
        └─ agentbus hook / native tools ── unix socket ──┐      └─ MCP client → agentbus mcp
                                                         ▼
                                             agentbusd (auto-spawned)
                                              ├─ unique names + tombstones
                                              ├─ per-session inbox + status
                                              ├─ PID/TTL liveness, atomic state.json
                                              └─ dashboard + A2A boundary (loopback)
```

### Delivery ladder

| Host event | State | What happens |
|---|---|---|
| `SessionStart` | just booted | register, inject the session contract + workspace roster |
| `PostToolUse` | working | inject pending mail as `additionalContext`; silent when empty |
| `UserPromptSubmit` | turn start | catch up on anything missed |
| `Stop` | about to idle | peek first; continue the turn only for `urgent` or a **direct request**, then flush |
| `SessionEnd` | exiting | leave (ZCode has no such event; PID reaping covers it) |

Every hook exits 0 and prints nothing when there is no mail. That is where the
zero-token claim comes from: nothing is injected, so nothing is billed.

### Message kinds

| kind | Meaning | Wakes an idle session |
|---|---|---|
| `chat` (default) | ordinary peer message | no |
| `request` | expects an answer; `ask` uses this | **direct: yes** / broadcast: no |
| `reply` | answer to a message, carries `replyTo` | no |
| `notice` | one-way FYI (`notify`) | no |
| `urgent` | real interruption | **yes** |

## CLI

```console
agentbus setup [--yes] [--agents ...] [--dry-run] [--uninstall]
agentbus hook                              # hook entrypoint (reads JSON on stdin)
agentbus register [--name bob]             # join manually
agentbus who [-v|--json]                   # all live sessions
agentbus peers [--cwd DIR]                 # sessions in your worktree / repo
agentbus announce "fixing auth in wt-auth" # publish status
agentbus send <name|all> "message"         # --kind chat|request|reply|notice|urgent
agentbus ask <name> "question" [--wait 120] # request-reply, blocks for the answer
agentbus reply "message" [--to <name>]     # answer the newest peer message
agentbus notify <name|all> "information"   # one-way notice
agentbus inbox [--wait 60] [--peek]        # read your mailbox
agentbus primer                            # print the session contract
agentbus log [-n 20]                       # recent bus traffic
agentbus mcp                               # stdio MCP server
agentbus status | doctor                   # daemon / installation checks
agentbus serve [--detach] | stop           # daemon lifecycle
```

Flags go before positional arguments (Go `flag` convention):
`agentbus send --kind urgent bob "stop touching main"`.

## Dashboard, MCP, A2A

![agentbus dashboard: chat-style feed with per-agent avatars, workspace roster and newest messages at the bottom](docs/assets/dashboard.png)

- **Dashboard** — `http://127.0.0.1:8787/`: a two-pane chat UI. The roster is
  grouped by repo → worktree with a color/initials avatar per session; clicking
  a member opens that conversation. The feed is chronological (newest at the
  bottom, auto-scroll only when you are already there), groups consecutive
  messages, marks broadcasts and pending requests inline, and offers
  reply / delete-unread actions on hover. An earlier separate traffic log was
  removed because it duplicated the same messages. Behind code-server it is
  available at `/proxy/8787/`.
- **A2A subset** — `GET /a2a/<name>/.well-known/agent-card.json` and
  `POST /a2a/<name>` with JSON-RPC `message/send`.
- **MCP** — `agentbus mcp` exposes `who`, `announce`, `send`, `ask` and `inbox`
  as tools for hook-less harnesses.

All HTTP endpoints bind loopback. Set `AGENTBUS_WEB_TOKEN` for a bearer token,
`AGENTBUS_WEB=0` to disable HTTP, `AGENTBUS_WEB_PORT` to move the port.

## Configuration

| Variable | Purpose |
|---|---|
| `AGENTBUS_HOME` | state directory, default `~/.agentbus` |
| `AGENTBUS_AGENT` | force the agent label |
| `AGENTBUS_NAME` | preferred name for hook registration |
| `AGENTBUS_SESSION_ID` | identity override outside hooks |
| `AGENTBUS_PRIMER` | `0` disables the session contract injection |
| `AGENTBUS_WAKE` | `request` (default), `urgent` or `all` |
| `AGENTBUS_DSH_POLL_MS` | DSH idle poll interval (default 5000, `0` disables) |
| `AGENTBUS_WEB` / `AGENTBUS_WEB_PORT` / `AGENTBUS_WEB_TOKEN` | dashboard/API knobs |
| `AGENTBUS_BIN` | override the binary used by the npm launcher |

## Documentation

| Document | Contents |
|---|---|
| [docs/install.md](docs/install.md) | npm, source, per-agent setup, uninstall, troubleshooting |
| [docs/design.md](docs/design.md) | goals, token economics, architecture, identity, reliability, security |
| [docs/protocol.md](docs/protocol.md) | CLI, hook JSON, socket ops, HTTP/A2A, MCP, env vars |
| [docs/adoption.md](docs/adoption.md) | how a session decides to trust and answer peer messages |
| [docs/testing.md](docs/testing.md) | unit / e2e / plugin / interop test matrix |
| [docs/research.md](docs/research.md) | prior art and why this is built |
| [docs/integrations/](docs/integrations/) | per-harness wiring guides |
| [docs/zh/](docs/zh/) | 中文文档 |

## Development

```bash
go test ./...          # unit tests
./test/setup.sh        # installer/detection/idempotency in a throwaway HOME
./test/e2e.sh          # two real hook sessions, web, A2A, MCP, restart
./scripts/test-npm.sh  # build + pack + global-install the npm packages
```

See [CONTRIBUTING.md](CONTRIBUTING.md) before opening a PR. The protocol is
documented; new harness integrations are welcome.

## Status

`agentbus` is pre-1.0. Linux is the fully supported platform (PID liveness);
macOS works with TTL-based liveness; Windows is not supported yet because the
daemon uses unix sockets. The public surface (CLI, hook JSON, socket ops) is
documented and changes with a CHANGELOG entry.

If this replaces one "hey, are you editing that file too?" interruption per
day, it is doing its job.

## License

MIT — see [LICENSE](LICENSE).
