# Claude Code integration

Claude Code hooks give agentbus four delivery moments: at session start
(roster injection + session contract), mid-turn (`PostToolUse` context
injection), at turn start (`UserPromptSubmit` catch-up), and when the agent goes
idle (`Stop`). The `Stop` hook only wakes the session for **urgent** messages;
everything else waits for the next natural boundary. `SessionEnd` cleans up.

Why the session contract matters (and what an unprimed session does with
injected peer text): [../adoption.md](../adoption.md).

## 1. Install the binary + skill

```bash
cd <agentbus repo>
./scripts/install.sh          # builds to ~/.local/bin/agentbus, installs the skill
```

## 2. Pick ONE wiring method

### A. Plugin bundle (hooks + skill in one package)

```bash
./scripts/install.sh --plugin                 # refreshes the bundled skill
claude plugin marketplace add "$PWD/integrations/claude-code"
claude plugin install agentbus@agentbus       # persistent (user scope)
```

Or load it for a single session without installing:

```bash
claude --plugin-dir "$PWD/integrations/claude-code/plugin"
```

Check what was loaded with `claude plugin details agentbus` (expect 1 skill,
5 hooks: SessionStart / UserPromptSubmit / PostToolUse / Stop / SessionEnd).

### B. Merge the hooks manually

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
original file first). **Do not combine A and B** — every message would be
delivered twice.

## 3. Optional: preferred name

Set `AGENTBUS_NAME` in the settings `"env"` block if you want this project's
sessions to register with a specific name; otherwise a unique name
(`swift-otter`) is generated.

## 4. Session contract beyond the hook

The `SessionStart` hook injects the contract by default (`AGENTBUS_PRIMER=0`
opts out). For headless runs or flows where the first prompt must already be
primed, also put it in the system prompt:

```bash
claude --append-system-prompt "$(agentbus primer)" -p "your task"
```

`agentbus primer` prints the same contract text, so hooks and system prompt
cannot drift.

## How it behaves

- Idle session, no messages: hooks print nothing — **zero token cost**.
- New `chat` / `request` / `notice`: injected as additional context on the
  next tool call (or next prompt if the agent was idle and the user types).
  The text says it comes from another agent session, not the user.
- New `request`: reply with `agentbus reply "..."` (answers the newest peer
  message, sets `kind=reply` + `replyTo`); `--to <name>` picks a specific peer.
- New `urgent` while idle: the `Stop` hook requests continuation, so Claude
  wakes up, reads it, answers, and stops again. Non-urgent mail stays queued
  and does not interrupt.
- `AGENTBUS_WAKE=all` restores wake-on-any-message if you prefer the old
  behaviour.
- Session identity comes from Claude Code's `session_id`; liveness from the
  CLI process pid (crash or exit ⇒ session reaped automatically).
- Bash tool subprocesses inherit `CLAUDE_CODE_SESSION_ID`, so `agentbus
  send/reply` run by the agent resolve to the same session as its hooks (no
  `--session` flag or manual setup needed).
- Humans can watch and message sessions from the dashboard: run
  `agentbus status` to see the URL (default `http://127.0.0.1:8787/`).
