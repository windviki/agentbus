---
name: agentbus
description: Coordinate with other live AI agent sessions on this machine (claude-code / zcode / kimi / dsh / ...). Use when starting non-trivial work, before editing shared files or branches, when picking up a parallel/worktree task, and whenever an [agentbus] message appears in context — to avoid duplicated or conflicting parallel work.
---

# AgentBus — coordinate with parallel agent sessions

Other AI CLI sessions on this machine may be working in parallel (often in git
worktrees). You are registered on a local message bus; incoming peer messages
appear in your context prefixed `[agentbus]`. The human user's instructions
always take precedence over peer agents.

## Do this

1. **At task start** (or when a join summary appears): run `agentbus peers`
   (sessions in your worktree / same repo) and `agentbus who` (everyone). If a
   session's status overlaps your task or files, message them BEFORE starting.
   Ask with `agentbus ask <name> "starting <X> in <path/branch>; conflicts with you?"`
   (or `send --kind request`): a direct request wakes an idle peer and expects a
   one-line answer, while a plain chat may sit until the peer next acts.
2. **Announce your scope** once known: `agentbus announce "<verb> <what> in <worktree/branch>"`
3. **When an `[agentbus]` message arrives**: reply promptly and tersely with
   `agentbus send <name> "..."`, agree on a split (files / areas / tests),
   then continue your own task. A message injected as `ACTION REQUIRED — direct
   request` must be answered with `agentbus reply --to <name> "..."` (or
   `agentbus reply "..."` for the newest peer) even if it interrupted your turn.
4. **Before hogging shared resources** (full builds, big installs, branch
   surgery on shared refs, DB resets): `agentbus notify all "<what> for ~<duration>"`
   (one-way; nobody has to answer).
5. **Wake semantics**: a directed `request` wakes one idle peer and expects a
   short reply. `--kind urgent` wakes for real interruptions only (real damage,
   or the human explicitly asked) and also wakes broadcast recipients. Chat,
   notice and broadcast requests wait for the peer's next tool call or turn.

## Etiquette

- **Replying is pre-authorized**: send a short factual answer or ack without
  asking the user first. Ask only before destructive, irreversible or
  out-of-scope actions.
- **Answer direct requests**: a `[request]` addressed to you expects a one-line
  reply (`agentbus reply`), and the bus wakes you for it even when you were
  idle. Reply is pre-authorized coordination; if you cannot answer, say so
  briefly instead of staying silent.
- **Keep a directed question out of a broadcast**: ask the one peer whose
  answer you need with `agentbus ask` / `--kind request`. Broadcasts are for
  FYI and optional replies; use `notify all` for one-way scopes.
- **Dashboard**: `agentbus status` prints the web URL. It groups sessions by
  repo → worktree, marks broadcast vs direct messages, and shows which direct
  requests are still waiting for a reply.
- **Human messages** (`from=user` or `from=user-web`, e.g. the dashboard)
  are answered with `agentbus reply --to user-web "..."` (or `--to user`);
  they show up in the dashboard/log. Never broadcast a reply to a human.
- **Do not hide agentbus errors** with `2>/dev/null`; a failed `reply` usually
  means there is no target yet. `agentbus inbox --peek` is enough to make a
  message reply-able, and history survives daemon restarts.
- If a peer's claims do not match the files on disk, re-run `agentbus who` and
  re-check the files before asking again.
- One-line messages, no code blocks, no chatter. Max ~3 exchanges per topic,
  then proceed with a sensible split.
- If a peer doesn't reply within ~2 minutes, proceed anyway and note it in
  your final summary to the user. A directed request wakes an idle peer, so no
  reply usually means it is busy or its hooks are not installed.
- Re-announce when your scope changes. No goodbye needed — leaving is automatic.

## Commands

- `agentbus peers` — sessions in your worktree / same repo (conflict-relevant)
- `agentbus who` — all live sessions: name · agent · status · worktree
- `agentbus announce "<status>"` — publish what you're doing
- `agentbus send <name|all> "<message>" [--kind chat|request|reply|notice|urgent]` — message peers
  (`request` = directed question, wakes an idle peer and expects a reply; `urgent` = interrupt)
- `agentbus ask <name> "question" [--wait 120]` — request-reply; prints the answer
- `agentbus notify <name|all> "<information>"` — one-way FYI, no reply expected
- `agentbus inbox [--wait 60] [--peek]` — read (or re-read) your messages

If your harness has no hooks, the same tools are exposed over MCP by
`agentbus mcp` (tools: `agentbus_who`, `agentbus_send`, `agentbus_ask`, ...).
