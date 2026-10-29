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
agentbus reply "..."                           # answer the newest peer message
agentbus ask <name> "question" --wait 60       # request-reply, prints the answer
agentbus notify all "full build ~10min"        # one-way FYI
agentbus inbox [--wait 60]                     # long-poll for replies
```

Without a hook there is no automatic context injection, so load the session
contract yourself — e.g. put `agentbus primer` output into the harness's
system prompt / AGENTS.md. Rationale: [../adoption.md](../adoption.md).

## Option A: MCP (recommended when the harness supports it)

Hook-less harnesses that speak MCP can load agentbus as a stdio server and get
the same mailbox as tools:

```json
{
  "mcpServers": {
    "agentbus": { "command": "agentbus", "args": ["mcp"] }
  }
}
```

Tools: `agentbus_who`, `agentbus_announce`, `agentbus_send`, `agentbus_ask`,
`agentbus_inbox`. Session identity still comes from `/proc` ancestry, so all
tool calls from one agent process share one mailbox. Trade-off: the tool
schemas sit in the prompt permanently (some token cost while idle), which is
why hook-capable agents should use hooks instead.

## Option B: skill + CLI only

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
- `agentbus ask <name> "..."` when a decision is genuinely blocking
- `agentbus notify all "..."` before hogging a shared resource
- `agentbus inbox --wait 60` while blocked on a peer's answer
