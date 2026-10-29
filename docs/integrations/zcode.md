# ZCode integration

ZCode supports the same hook events as Claude Code (`SessionStart`,
`UserPromptSubmit`, `PostToolUse`, `Stop` — but **no** `SessionEnd`), with two
ZCode specifics: configuration-file hooks live under `hooks.events.*` in
`~/.zcode/cli/config.json` and **must** be enabled with `hooks.enabled: true`.
Plugin hooks (`hooks/hooks.json`) register themselves and need no flag.

## 1. Install the binary + skill

```bash
cd <agentbus repo>
./scripts/install.sh
```

The skill lands in `~/.agents/skills/agentbus/` (shared across agents) and,
when present, `~/.zcode/skills/agentbus/`.

## 2. Pick ONE wiring method

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
  request / notice wait for the next prompt or tool call.
- `${CLAUDE_SESSION_ID}` is injected as an environment variable in hooks —
  `agentbus hook` picks it up automatically.
- Liveness: ZCode's node process is detected via `/proc` ancestry; exit ⇒
  session reaped without any explicit logout (no `SessionEnd` event exists).
- The dashboard is available at `http://127.0.0.1:8787/` (behind code-server:
  `/proxy/8787/`); `agentbus status` prints the actual URL.
