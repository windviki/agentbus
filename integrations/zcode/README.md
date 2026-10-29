# ZCode integration

ZCode supports the same hook events as Claude Code (`SessionStart`,
`UserPromptSubmit`, `PostToolUse`, `Stop` — but **no** `SessionEnd`), with two
ZCode specifics: hooks live under `hooks.events.*` in
`~/.zcode/cli/config.json` and **must** be enabled with `hooks.enabled: true`.

## 1. Install the binary + skill

```bash
cd <agentbus repo>
./scripts/install.sh
```

The skill lands in `~/.agents/skills/agentbus/` (shared across agents) and,
when present, `~/.zcode/skills/agentbus/`.

## 2. Wire the hooks

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

Notes:

- ZCode's `Stop` hook may request continuation up to three times per stop,
  which conveniently bounds any wake-up loop.
- `${CLAUDE_SESSION_ID}` is injected as an environment variable in hooks —
  `agentbus hook` picks it up automatically.
- Liveness: ZCode's node process is detected via `/proc` ancestry; exit ⇒
  session reaped without any explicit logout.
