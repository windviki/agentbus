# Changelog

All notable user-visible changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[semantic versioning](https://semver.org/).

## [Unreleased]

## [0.2.0] - 2026-11-24

### Added

- Harness-native real-time delivery: Claude Code inbox sockets, Kimi Server
  API `steer` / idempotent `prompts`, DSH active-turn steering, and Codex
  active-turn async hooks. Ordinary lifecycle hooks remain the fallback.
- Atomic native-delivery claims with timeout, retry and hook fallback, plus
  per-recipient `queued` / `pushing` / `accepted` receipts in `agentbus
  receipt`, MCP, the dashboard and `/api/state`. Accepted is transport state,
  not a claim that the model processed the message.
- Sender work context (`cwd`, `repo`, `branch`, `status`) on messages and
  session guidance to adjust the existing plan without restarting the task.
- Codex setup through `$CODEX_HOME/hooks.json`, including an async
  `UserPromptSubmit` watcher, with `/hooks` trust guidance and manual assets.
- `ask --wait 0` for non-blocking requests, `send --json`, and the DSH
  `agentbus_receipt` / MCP `agentbus_receipt` tools.
- Native transport end-to-end tests for Claude, Kimi and Codex, and DSH
  active-turn push coverage.
- Session activity state: hooks report `working` at turn start / tool calls and
  `idle` at Stop, so the bus can tell "doing something now" from "finished but
  still alive". A stale `working` assertion (older than 2h) is treated as an
  abandoned turn.
- `agentbus who --all` and `agentbus peers --all` list idle sessions;
  `agentbus state working|idle` lets hook-less integrations report the state;
  the MCP `agentbus_who` tool takes `all`; the dashboard marks sessions
  `进行中` / `空闲` and sorts working sessions first.

### Changed

- npm distribution uses the `@aixyzstudio` scope for `agentbus-cli` and all
  platform packages, matching the release credentials; the installed command
  remains `agentbus`.
- DSH replaces the 5-second idle poller with a 30-second long-poll watcher;
  active watchers stop at turn end and idle watchers only return wake-worthy
  mail. `AGENTBUS_DSH_POLL_MS` is removed.
- Claude, ZCode and Kimi configurations cover additional lifecycle events;
  Kimi tool events remain observation-only while Server API push handles
  mid-turn delivery.
- `ask` correlates replies by sender and `replyTo` instead of consuming the
  first message from that peer. Legacy empty `replyTo` replies still work.
- `agentbus who`, `agentbus peers`, the SessionStart join summary and
  `agentbus status` list only sessions currently working by default. Finished
  sessions used to clutter every new session's roster, especially under DSH and
  Kimi, where one long-lived host PID keeps every past session "alive". Idle
  sessions remain registered, addressable and wakeable by a directed request.
- Protocol: `register` and `poll` accept an optional `state`, a new `state` op
  reports it explicitly, and session objects carry `state`, `state_at` and the
  derived `working` flag. `who` still returns every live session.

### Fixed

- Native endpoint tokens cross the hook→daemon wire protocol but are never
  serialized into session state or public APIs.
- The plugin-install help heredoc no longer executes backtick examples as
  shell commands.

## [0.1.0] - 2026-10-30

First public release.

### Added

- Local daemon with unique session names, name tombstones, per-session
  inboxes, presence, direct and broadcast messages, request/reply, notices and
  an urgent wake-up kind. The daemon auto-starts on first use, persists state
  atomically, and reaps dead sessions by PID (Linux) or TTL.
- Hook-driven delivery for Claude Code, ZCode and Kimi Code; a native Cordis
  plugin for DSH with idle steering; a stdio MCP server and a skill for other
  CLIs.
- Workspace-aware roster grouped by repository and worktree, a loopback-only
  chat-style dashboard (per-session color/initials avatars, chronological feed
  with the newest message at the bottom, broadcast/direct/pending filters,
  inline pending-request actions), an A2A subset and a documented JSON-lines
  socket protocol. A separate traffic log is deliberately omitted from the
  dashboard because it would duplicate the same messages in a second scroll
  area.
- `agentbus setup`: detects installed agents, lets the user choose which ones
  to wire, and configures hooks / the native DSH plugin / the skill in one
  idempotent pass. Supports `--yes`, `--agents`, `--dry-run`, `--uninstall`,
  `--json` and installs backups as `<file>.agentbus-bak`.
- npm distribution as `agentbus-cli` with prebuilt binaries for Linux and
  macOS (x64/arm64) through per-platform packages; no postinstall script.
- `scripts/build-release.sh` builds the npm packages, GitHub Release archives
  and `SHA256SUMS` from one tag; a tag-driven release workflow publishes them.
- Operator surface: `agentbus doctor`, `agentbus status`, `agentbus log`,
  `agentbus primer`, session contract injection and `AGENTBUS_*` configuration.
- Test suites: unit tests, `test/setup.sh` (installer), `test/e2e.sh`
  (delivery/web/A2A/MCP/restart), `test/dsh-idle-wake.sh` (mock agent) and
  `test/interop.sh` (real dsh ↔ claude, run manually).

### Changed

- `scripts/install.sh` now builds the binary and delegates all wiring to
  `agentbus setup`; `--hooks`, `--claude-hooks`, `--zcode-hooks`,
  `--kimi-hooks`, `--dsh-plugin` and `--plugin` keep working.
- `agentbus doctor` reports every supported agent and checks PATH resolution.
- Go module path is `github.com/windviki/agentbus`, so `go install` works.
- Documentation is English-canonical with a complete Chinese mirror, and adds
  an installation guide, release guide, contribution guide, security policy,
  changelog and issue/PR templates.
