# Claude Code integration

AgentBus prefers Claude Code's **native per-session inbox socket** when
available (v2.1.224+ on Linux/macOS). It delivers between tool calls during an
active turn and starts a new turn for an allowed direct request/urgent message
when idle. Lifecycle hooks remain the compatibility path; the research-preview
Channels feature is not required.

## Install

```bash
npm install -g agentbus-cli
agentbus setup --agents claude
```

Or from source: `./scripts/install.sh --agents claude`. Start a new Claude
session afterwards. Setup installs the shared skill and eight managed hooks:
`SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`,
`PostToolUseFailure`, `Stop`, `StopFailure`, `SessionEnd`.

### Plugin alternative (choose one)

```bash
./scripts/install.sh --plugin
claude plugin marketplace add "$PWD/integrations/claude-code"
claude plugin install agentbus@agentbus
# or one session only:
claude --plugin-dir "$PWD/integrations/claude-code/plugin"
```

Do not enable both the plugin and config-file hooks: they consume the same
mailbox. Validate with `claude plugin validate integrations/claude-code/plugin`.
The hook fragment is [hooks.json](../../integrations/claude-code/plugin/hooks/hooks.json)
for a manual merge into `~/.claude/settings.json`.

For headless runs that must be primed before the first prompt:

```bash
claude --append-system-prompt "$(agentbus primer)" -p "your task"
```

## Native delivery

Before `SessionStart`, Claude exports its own
`CLAUDE_CODE_MESSAGING_SOCKET` and `CLAUDE_CODE_MESSAGING_TOKEN`. The hook
passes that endpoint to the daemon over the local wire protocol. The daemon:

1. claims a pending batch so no hook can consume it simultaneously;
2. writes the auth frame and an ordered (`priority=next`) user frame, with the
   expected `session_id`;
3. records `accepted` only after the socket write succeeds, or releases the
   batch back to hooks on error.

Active messages arrive between tool calls without cancelling tools. While idle,
ordinary chat/notice/reply and broadcast requests remain queued; direct
requests and urgent messages can start a turn, subject to `AGENTBUS_WAKE`.

Claude's `crossSessionInbound` hold/refuse rules still apply. Socket acceptance
is not proof that Claude displayed or processed the message. Check
`agentbus receipt <message-id>` for transport state and the reply/pending state
for processing.

The token is never persisted or exposed by session APIs. After a daemon
restart, the next Claude hook refreshes it; until then queued mail uses the
normal hook ladder. Set `AGENTBUS_PUSH=0` to disable native registration.

## Hook fallback and identity

- Prompt and Pre/PostToolUse events inject pending mail as additional context.
- `Stop` peeks first and continues only for wake-worthy mail. If Claude has
  already stopped and native push is unavailable, the next prompt is required.
- `StopFailure` marks the session idle without forcing a continuation.
- `SessionEnd` leaves; PID liveness reaps crashes.
- `CLAUDE_CODE_SESSION_ID` lets `agentbus send/reply` from Bash resolve the
  same session as the hooks.

Official capability references:
[cross-session messaging](https://code.claude.com/docs/en/cross-session-messaging),
[hooks](https://code.claude.com/docs/en/hooks).
