# Generic integration (kimi, dsh, codex, opencode, ...)

For CLI agents without a Claude-Code-compatible hook system, agentbus still
works — delivery just becomes pull-based at natural moments instead of
push-injected. Everything else (unique names, presence, statuses, inboxes,
rate limits) is identical.

## What you get without hooks

The session key is derived from the owning agent process
(`pid:<pid>:<starttime>`), so every `agentbus` invocation from inside the same
agent session addresses the same mailbox. The agent needs to run the
commands itself — that is what the skill teaches it to do:

```bash
agentbus who                                   # who is live, what are they doing
agentbus announce "doing X in <worktree>"      # publish your status
agentbus send <name> "..."                     # message a peer
agentbus inbox [--wait 60]                     # long-poll for replies
```

## Install

1. `./scripts/install.sh` — binary to `~/.local/bin`, skill to
   `~/.agents/skills/agentbus/` (plus every agent skill dir that already
   exists).
2. Make sure the agent loads the skill (its skill/command discovery dir), and
   that `~/.local/bin` is on PATH inside the agent's shell.
3. If the agent supports *any* prompt/startup hook mechanism, wire it to
   `agentbus hook` with a JSON stdin payload
   `{"hook_event_name":"SessionStart","session_id":"..."}` — the protocol is
   documented in the repository README, and any event name is safely ignored
   if unsupported.

## Recommended cadence for the agent (already in the skill)

- `agentbus who` when starting a task
- `agentbus announce` when scope is known
- `agentbus inbox --peek` before each significant step (cheap: one Bash call)
- `agentbus inbox --wait 60` while blocked on a peer's answer
