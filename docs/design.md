# agentbus design

## 1. Goals and non-goals

**Goals**: let AI coding-agent sessions on one machine (Claude Code / ZCode /
Kimi / DSH / Codex / ...) discover each other, exchange information while work
is in flight, and adjust their current plans before they collide. The user
should not have to copy messages between terminals or interrupt a peer merely
to say that another session exists.

Delivery has three distinct milestones:

1. **queued** — the daemon accepted the message for a recipient;
2. **accepted** — the target Harness accepted it through a hook or native API;
3. **processed** — the agent replied or visibly adjusted its work. AgentBus
   never labels transport acceptance as model processing.

**Non-goals**: cross-machine networking, multi-user authentication, durable
exactly-once delivery, and task orchestration. Local delivery is best-effort /
at-least-once with short claims and hook fallback; a task claim board is a
separate layer.

## 2. Token and attention economics

| Approach | Idle context cost | Delivery timing |
|---|---|---|
| MCP polling tools | schemas remain in context | only when the agent polls |
| Skill + polling convention | small, but discipline-dependent | next agent-chosen call |
| Prompt-only mailbox | zero while empty | next user prompt, possibly hours |
| Lifecycle hooks | zero while empty | next prompt/tool/stop boundary |
| **Native push / async delivery** | zero while empty | active turn: next safe model boundary; idle request/urgent: new turn where supported |

Hooks remain the common fallback. Native channels improve latency without
adding an always-resident MCP schema: Claude's per-session inbox socket, Kimi's
loopback Server API, DSH's `agent.steer`, and Codex's async hooks. No channel
cancels an in-progress tool call. Ordinary chat/notice/reply does not wake idle
sessions; a direct request or urgent message may do so.

Per-message context is deliberately small: envelope + body + the sender's
cwd/branch/status. Empty hooks emit nothing.

## 3. Architecture

```
┌─────────────┐ ┌─────────────┐ ┌─────────────┐ ┌──────────────┐
│ Claude Code │ │ Kimi / DSH  │ │ Codex/ZCode │ │ human/client │
│ hooks+inbox │ │ API / steer │ │ async/hooks │ │ HTTP/MCP/A2A │
└──────┬──────┘ └──────┬──────┘ └──────┬──────┘ └──────┬───────┘
       └───────────────┴───────────────┴───────────────┘
                         │ unix JSON lines (0600)
                  ┌──────┴────────┐
                  │ agentbusd     │ registry + inbox + receipts
                  │ claim/ack     │ atomic state.json + ring log
                  │ native push   │ retry + ordinary-hook fallback
                  └───────────────┘
```

- **daemon**: sessions, names, presence, inboxes, claims, receipts, recent log;
- **CLI**: `who/send/ask/reply/receipt/inbox/log/doctor` and lifecycle entry
  points;
- **setup**: idempotent wiring for Claude, ZCode, Kimi, DSH and Codex, plus the
  shared skill;
- **native delivery**: a short claim hides a batch from other consumers while
  the daemon pushes it. Success marks it accepted; failure releases it for the
  ordinary hook ladder;
- **web / A2A / MCP**: alternate views of the same bus, not separate message
  stores;
- **plugin bundles**: package hooks + skill for Claude Code and ZCode.

## 4. Session identity and lifecycle

Session IDs come from `$AGENTBUS_SESSION_ID`, Claude's session environment, or
hook `session_id`, normalized to `sid:<id>`. Hooks write a private pid→sid map
so Bash subprocesses without a session variable resolve the same mailbox. Maps
are scoped by workspace with parent-directory lookup and legacy fallback, so a
multiplexed host PID does not collapse sessions into the last writer. The final
fallback is `pid:<pid>:<starttime>` from `/proc` ancestry.

Names are normalized to `[a-z0-9-]{1,32}`; collisions receive `-2`, etc. Linux
uses PID + start-time liveness; other targets use a six-hour TTL. Departed names
are tombstoned for an hour.

Presence is separate from activity. `working` means inside a turn; `idle` means
alive at a prompt. Prompt/tool/turn-start events assert working, Stop/Interrupt/
StopFailure assert idle. A working assertion older than two hours is considered
abandoned. `who`, `peers` and the join roster show working sessions by default;
`--all` includes idle, addressable sessions.

## 5. Message model and delivery

A message is `{messageId, from, to, kind, body, ts, replyTo, recipients,
context}`. `context` captures the sender's cwd, repo, branch and current status
at send time so the recipient can load only the relevant files and adjust its
existing plan instead of restarting.

| kind | Meaning | May wake an idle session |
|---|---|---|
| `chat` | ordinary peer message | no |
| `request` | expects an answer | direct: yes; broadcast: no |
| `reply` | answer correlated by `replyTo` | no |
| `notice` | one-way FYI | no |
| `urgent` | real interruption | yes |

`ask --wait N` correlates a reply by sender + `replyTo`; an empty `replyTo` is
accepted only for backward compatibility. Notices and chat from the same peer
are never consumed as the answer. `--wait 0` returns immediately and leaves the
reply to normal asynchronous delivery.

`agentbus reply` chooses the newest addressable message from accepted history,
the unacked inbox, or recent persisted history. Human `user` / `user-web` are
virtual recipients backed by the dashboard/log rather than a process inbox.

### Delivery ladder

| Path | Active turn | Already idle |
|---|---|---|
| Claude native inbox | between tool calls, ordered | native new turn for request/urgent |
| Kimi Server API | `POST …/steer` | idempotent `POST …/prompts` for request/urgent |
| DSH watcher | long-poll + `agent.steer` | same for request/urgent |
| Codex async hook | result enters the next model request | Stop invalidates the watcher; no idle wake |
| Ordinary hooks | prompt, Pre/PostToolUse, failures | Stop continuation for request/urgent |

Kimi tool events are observation-only: they refresh presence and native config
but never consume mail that stdout cannot display. `TurnStarted` covers native
API turns and background continuations; `SessionHeartbeat` refreshes discovery.

Every accepted batch is retained in the five-message re-read area. Native
acceptance and hook injection both create per-recipient receipts, visible via
`agentbus receipt` and `/api/state`.

## 6. Claims and failure recovery

A native push uses this sequence:

1. atomically claim the currently visible batch for up to 30 seconds;
2. push with a six-second outer timeout;
3. on success, remove it from the inbox and record `accepted` + driver;
4. on failure, remove the claim and record `queued` + a bounded error. A retry
   backoff lets a normal hook deliver it meanwhile.

Other polls cannot see claimed messages. A daemon restart drops claims and
turns persisted `pushing` receipts back to `queued`, so a crash can cause a
repeat but not a silent acknowledgement. This is at-least-once, not exactly-
once. Native tokens are wire-only: they are passed from a hook to the daemon,
never written to `state.json` or returned by session APIs. After a restart a
Claude hook must refresh the token before native push resumes; queued mail is
safe in the meantime.

## 7. Web dashboard and A2A

The loopback dashboard groups sessions by repo → worktree and shows activity,
native drivers, broadcast/direct traffic and pending replies. It also displays
queued/pushing/accepted receipts. “Accepted, not replied” deliberately replaces
the old misleading “read, not replied”. Unread pending messages may be deleted;
accepted history is immutable.

- `GET /api/state`, `POST /api/send`, `POST /api/delete`;
- A2A Agent Cards and JSON-RPC `message/send` at `/a2a/<name>`;
- all endpoints use the same bus and optional bearer-token guard;
- a bind failure only disables the web surface, never the unix bus.

## 8. MCP facade

`agentbus mcp` exposes who/announce/send/ask/inbox/receipt over stdio JSON-RPC.
It is for Harnesses without hooks; native push or lifecycle hooks are preferred
when available because MCP tool schemas stay resident in context.

## 9. Anti-storm controls

- 20 messages/minute per sender;
- identical from/to/body suppressed for 60 seconds;
- idle wake limited to urgent and direct requests by default;
- replies and broadcasts do not create automatic wake loops;
- ZCode limits Stop continuations, and the skill recommends at most three
  exchanges per topic;
- ordered native steering does not cancel existing tool calls.

## 10. Reliability

- first use auto-spawns the daemon under a file lock;
- state snapshots use tmp+rename with a 300ms debounce;
- unknown sessions silently re-register on the next hook;
- hook failures are logged and fail open without breaking the host;
- message body 8KB, inbox 200, accepted re-read area 5, recent log 300;
- native push timeouts, claims and fallback are bounded;
- `agentbus doctor`, receipts and the dashboard expose degraded delivery.

## 11. Security model

AgentBus is a single-user trusted bus. The unix socket is 0600; any process with
the same uid is trusted at approximately ssh-agent level. HTTP binds loopback
by default and can require `AGENTBUS_WEB_TOKEN`. Claude/Kimi native credentials
stay on loopback, are never included in logs/state/session APIs, and custom
Kimi URLs must be loopback HTTP.

Peer messages are coordination notes, not user authorization. They cannot
approve permissions, rewrite Harness configuration, or justify destructive /
out-of-scope work. Sender names are collaboration handles, not cryptographic
identities.

## 12. Integration matrix

| Agent | Integration | Active / idle delivery |
|---|---|---|
| Claude Code | native inbox + hooks + skill, or plugin hooks | native between tool calls / request wake; hooks fallback |
| Kimi Code | Server API + lifecycle hooks + AGENTS.md + skill | steer / idempotent prompt; hooks fallback |
| DSH | native Cordis plugin + watcher | long-poll steer / request wake |
| Codex | lifecycle hooks + async watcher | next model request / no wake after Stop |
| ZCode | config hooks or plugin bundle | next safe hook / no wake after Stop |
| Human / external | dashboard, HTTP, A2A, MCP | immediate bus write; delivery depends on target |

## 13. Failure modes

| Scenario | Behaviour |
|---|---|
| native API unavailable | queued receipt with error, bounded retry, hook fallback |
| daemon killed | restart restores state; claims reset to queued |
| Harness closed | PID/TTL reaping and name tombstone |
| duplicate integration | claims/ack prevent native-vs-hook duplicates; avoid installing two hook bundles |
| `ask` times out | exit 1 and continue; late reply stays available |
| `ask --wait 0` | success once queued; answer is asynchronous |
| web port busy | web only is disabled |

## 14. Future directions

- processing acknowledgements richer than reply/pending state;
- adoption and delivery-latency metrics;
- channels/topics and stronger worktree conflict hints;
- cross-machine transport with real authentication;
- searchable history beyond the 300-message ring.
