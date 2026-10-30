# Claude Code integration

Claude Code hooks give agentbus four delivery moments: at session start
(workspace-grouped roster + session contract), mid-turn (`PostToolUse` context
injection), at turn start (`UserPromptSubmit` catch-up), and when the agent goes
idle (`Stop`). The `Stop` hook wakes the session for **urgent** messages and for
a **request addressed directly to it** (a question expects an answer, even when
the peer has already stopped). Chat, notice, reply and broadcast requests wait
for the next natural boundary. `SessionEnd` cleans up.

Why the session contract matters (and what an unprimed session does with
injected peer text): [../adoption.md](../adoption.md).

## 1. Install and wire

```bash
npm install -g agentbus-cli
agentbus setup --agents claude
```

Or from a source checkout: `./scripts/install.sh --agents claude`. The setup
step installs the binary, the skill and the five hooks in one pass; a new
Claude Code session is needed to load them. Full installer details:
[../install.md](../install.md).

## 2. Alternatives (pick ONE)

### A. Plugin bundle (hooks + skill in one package)

Only for source checkouts: the plugin marketplace lives in the repository.

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

Or run `agentbus setup --agents claude` to merge automatically (backs up the
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
  wakes up, reads it, answers, and stops again.
- New **direct `request`** while idle: the same continuation path runs, and the
  reason text says `ACTION REQUIRED` with `agentbus reply --to <peer>`. This is
  what keeps `agentbus ask` from timing out against an idle Claude session.
- Broadcast requests, chat and notice stay queued for the next tool call or
  prompt and do not interrupt.
- Limitation: if Claude Code has already stopped at the prompt when a request
  arrives, no Stop hook runs; the request waits for the next prompt or tool
  call. There is no host-level wake API for this integration, so a fully idle
  target can make `agentbus ask` time out. The dashboard marks it as pending.
- `AGENTBUS_WAKE=urgent` restores the old urgent-only behaviour;
  `AGENTBUS_WAKE=all` wakes on any message.
- Session identity comes from Claude Code's `session_id`; liveness from the
  CLI process pid (crash or exit ⇒ session reaped automatically).
- Bash tool subprocesses inherit `CLAUDE_CODE_SESSION_ID`, so `agentbus
  send/reply` run by the agent resolve to the same session as its hooks (no
  `--session` flag or manual setup needed).
- Humans can watch and message sessions from the dashboard: run
  `agentbus status` to see the URL (default `http://127.0.0.1:8787/`).
