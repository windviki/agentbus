# Claude Code integration

Claude Code hooks give agentbus three delivery moments: at session start
(roster injection), mid-turn (`PostToolUse` context injection), and when the
agent goes idle (`Stop` continuation = wake-up). `SessionEnd` cleans up.

## 1. Install the binary + skill

```bash
cd <agentbus repo>
./scripts/install.sh          # builds to ~/.local/bin/agentbus, installs the skill
```

## 2. Wire the hooks

Merge this into `~/.claude/settings.json` (user scope) or
`<repo>/.claude/settings.json` (workspace scope):

```json
{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "agentbus hook", "timeout": 15 } ] }
    ],
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "command": "agentbus hook", "timeout": 5 } ] }
    ],
    "PostToolUse": [
      { "hooks": [ { "type": "command", "command": "agentbus hook", "timeout": 5 } ] }
    ],
    "Stop": [
      { "hooks": [ { "type": "command", "command": "agentbus hook", "timeout": 5 } ] }
    ],
    "SessionEnd": [
      { "hooks": [ { "type": "command", "command": "agentbus hook", "timeout": 5 } ] }
    ]
  }
}
```

Or run `./scripts/install.sh --hooks` to merge automatically (backs up the
original file first).

## 3. Optional: preferred name

Set `AGENTBUS_NAME` in the settings `"env"` block if you want this project's
sessions to register with a specific name; otherwise a unique name
(`swift-otter`) is generated.

## How it behaves

- Idle session, no messages: hooks print nothing — **zero token cost**.
- New message mid-turn: injected as additional context on the next tool call.
- New message while idle: the `Stop` hook requests continuation, so Claude
  wakes up, reads and answers, then stops again.
- Session identity comes from Claude Code's `session_id`; liveness from the
  CLI process pid (crash or exit ⇒ session reaped automatically).
