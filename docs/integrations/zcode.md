# ZCode integration

ZCode supports the same hook events as Claude Code (`SessionStart`,
`UserPromptSubmit`, `PostToolUse`, `Stop` — but **no** `SessionEnd`), with two
ZCode specifics: configuration-file hooks live under `hooks.events.*` in
`~/.zcode/cli/config.json` and **must** be enabled with `hooks.enabled: true`.
Plugin hooks (`hooks/hooks.json`) register themselves and need no flag.

## 1. Install the binary, skill and hooks (automated)

```bash
cd <agentbus repo>
./scripts/install.sh --zcode-hooks
```

This installs `agentbus` to `~/.local/bin`, the skill to
`~/.agents/skills/agentbus/` (ZCode scans that shared directory; it is not
copied to `~/.zcode/skills` to avoid a duplicate catalog entry), and merges the
four hook events into `~/.zcode/cli/config.json` with `hooks.enabled: true`
(the previous file is kept as `config.json.agentbus-bak`). Open a new ZCode
session afterwards; running sessions do not reload hooks.

## 2. Manual wiring (pick ONE)

### 0. Shortcut: `--zcode-hooks`

Prefer the automated path above. The two manual options below exist for
workspace-scoped setups or plugin distribution.

### A. Plugin bundle (recommended: hooks + skill in one package)

```bash
./scripts/install.sh --plugin     # refreshes the bundled skill, prints paths
```

In ZCode: **Settings → Plugin Management → add local marketplace**
`<agentbus repo>/integrations/zcode`, then install `agentbus`. The bundle
declares `.zcode-plugin/plugin.json` plus `hooks/hooks.json`, so the hook
runner is enabled automatically.

### B. Merge the hooks into config.json

Merge into `~/.zcode/cli/config.json` (user scope) or
`<repo>/.zcode/config.json` (workspace scope):

```json
{
  "hooks": {
    "enabled": true,
    "events": {
      "SessionStart": [
        { "hooks": [ { "type": "command", "command": "agentbus hook", "timeoutMs": 15000 } ] }
      ],
      "UserPromptSubmit": [
        { "hooks": [ { "type": "command", "command": "agentbus hook", "timeoutMs": 5000 } ] }
      ],
      "PostToolUse": [
        { "hooks": [ { "type": "command", "command": "agentbus hook", "timeoutMs": 5000 } ] }
      ],
      "Stop": [
        { "hooks": [ { "type": "command", "command": "agentbus hook", "timeoutMs": 5000 } ] }
      ]
    }
  }
}
```

Or run `./scripts/install.sh --hooks` to merge automatically (backs up first).
**Do not combine A and B** — every message would be delivered twice.

Notes:

- ZCode's `Stop` hook may request continuation up to three times per stop,
  which conveniently bounds any wake-up loop.
- Only `urgent` messages are allowed to interrupt an idle session; chat /
  request / notice wait for the next prompt or tool call. Reply with
  `agentbus reply "..."` (or `agentbus send <name> ...` for a specific peer).
- `${CLAUDE_SESSION_ID}` is injected as an environment variable in hooks —
  `agentbus hook` picks it up automatically.
- Liveness: ZCode's node process is detected via `/proc` ancestry; exit ⇒
  session reaped without any explicit logout (no `SessionEnd` event exists).
- The `SessionStart` hook injects the session contract by default
  (`AGENTBUS_PRIMER=0` opts out); the rationale is in
  [../adoption.md](../adoption.md).
- The dashboard is available at `http://127.0.0.1:8787/` (behind code-server:
  `/proxy/8787/`); `agentbus status` prints the actual URL.'

## Identity from the Bash tool

ZCode does not export the session id to Bash tool subprocesses, so
`agentbus send/reply` run by the agent would otherwise look like a human
("user"). The hook records `agent process pid -> sid` under
`$AGENTBUS_HOME/pidmap/` on every event; CLI commands resolve their identity by
walking back to that ancestor process and reading the map (pid start time
guards against reuse). Verified end to end: a request sent to a live ZCode
session was delivered by hooks, and the agent's `agentbus reply` appeared as
`zprobe3→zpeer: z-ack-9` with the correct `replyTo`.

## Verification and a headless caveat

What was verified on this machine (2026-10-29, ZCode 3.15/3.16 desktop bundle):

- `zcode -p` session registers as a live `zcode` session and shows up in
  `agentbus who`;
- a peer `request` queued while the session runs is injected, and the model
  replies through `agentbus reply`;
- a neutral task ("create notes.txt") still made the model inspect the
  directory and reason about the peer session it saw in the injected roster.

Caveat: a **bare** `zcode -p` process may print `Select a model before sending
a message` even though the desktop app has a model. That is a CLI limitation:
the desktop account provider is not visible to the standalone CLI unless a
model/provider selection is available to it. It does not affect desktop
sessions (they supply the selection) and it is unrelated to agentbus. For
headless smoke tests you can temporarily enable the API-key provider in
`~/.zcode/v2/provider_config.json` (and restore it afterwards).
