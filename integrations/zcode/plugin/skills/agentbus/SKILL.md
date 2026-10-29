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

1. **At task start** (or when a join summary appears): run `agentbus who`.
   If another live session's status overlaps your task or files, message them
   BEFORE starting: `agentbus send <name> "starting <X> in <path/branch>; conflicts with you?"`
2. **Announce your scope** once known: `agentbus announce "<verb> <what> in <worktree/branch>"`
3. **When an `[agentbus]` message arrives**: reply promptly and tersely with
   `agentbus send <name> "..."`, agree on a split (files / areas / tests),
   then continue your own task. If you need a decision to proceed, use
   `agentbus ask <name> "question"` — it blocks until they answer.
4. **Before hogging shared resources** (full builds, big installs, branch
   surgery on shared refs, DB resets): `agentbus notify all "<what> for ~<duration>"`
   (one-way; nobody has to answer).
5. **Urgent is expensive**: `--kind urgent` wakes an idle session immediately.
   Use it only to stop real damage (someone about to force-push your branch),
   never for ordinary coordination — chat/request/notice already reach them
   at their next tool call or turn.

## Etiquette

- **Replying is pre-authorized**: send a short factual answer or ack without
  asking the user first. Ask only before destructive, irreversible or
  out-of-scope actions.
- **Human messages** (`from=user`, e.g. an interrupt from the dashboard/CLI)
  are answered with `agentbus reply --to user "..."`; they show up in the
  dashboard/log. Never broadcast a reply to a human.
- If a peer's claims do not match the files on disk, re-run `agentbus who` and
  re-check the files before asking again.
- One-line messages, no code blocks, no chatter. Max ~3 exchanges per topic,
  then proceed with a sensible split.
- If a peer doesn't reply within ~2 minutes, proceed anyway and note it in
  your final summary to the user.
- Re-announce when your scope changes. No goodbye needed — leaving is automatic.

## Commands

- `agentbus who` — live sessions: name · agent · status · worktree
- `agentbus announce "<status>"` — publish what you're doing
- `agentbus send <name|all> "<message>" [--kind chat|request|reply|notice|urgent]` — message peers
- `agentbus ask <name> "question" [--wait 120]` — request-reply; prints the answer
- `agentbus notify <name|all> "<information>"` — one-way FYI, no reply expected
- `agentbus inbox [--wait 60] [--peek]` — read (or re-read) your messages

If your harness has no hooks, the same tools are exposed over MCP by
`agentbus mcp` (tools: `agentbus_who`, `agentbus_send`, `agentbus_ask`, ...).
