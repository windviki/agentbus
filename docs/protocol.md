# Protocol and interface reference

For rationale see [design.md](design.md); for installation see
[integrations/](integrations/).

## 1. Identity and naming

| Concept | Rule |
|---|---|
| Session key | `sid:<uuid>` from hook JSON, `$CLAUDE_SESSION_ID`, `$CLAUDE_CODE_SESSION_ID` or `$AGENTBUS_SESSION_ID`; pid→sid maps cover Bash subprocesses; final fallback `pid:<pid>:<starttime>` |
| Name | generated or requested; normalized to `[a-z0-9-]{1,32}`; collisions get `-2` etc. |
| Tombstone | a departed name is reserved for 1h |
| Human | no session identity means `user`; dashboard defaults to `user-web` |

## 2. CLI

```
agentbus hook [--async-watch]           # lifecycle entry; async mode is for Codex
agentbus setup [--yes] [--agents claude,zcode,kimi,dsh,codex|all|none]
                                        # [--dry-run] [--uninstall] [--json]
agentbus register [--name bob] [--agent dsh] [--cwd DIR] [--print-name] [--json]
agentbus who [--all] [-v|--json]       # working now; --all includes idle
agentbus peers [--cwd DIR] [--all] [--json]
agentbus state working|idle
agentbus announce "<status>"
agentbus send [--json] <name|all> "<msg>" [--kind chat|request|reply|notice|urgent]
                                        # [--reply-to ID]
agentbus receipt <message-id> [--json] # per-recipient delivery receipts
agentbus ask <name> "<question>" [--wait 120] # --wait 0 = asynchronous
agentbus reply "<msg>" [--to <name>]
agentbus notify <name|all> "<info>"
agentbus inbox [--wait 60] [--peek] [--json]
agentbus leave
agentbus log [-n 20]
agentbus primer
agentbus mcp
agentbus status | doctor
agentbus serve [--detach] | stop
```

Go flags precede positional arguments. `--session <key>` overrides identity.

## 3. Hook entry point

Input:

```json
{ "hook_event_name": "PostToolUse", "session_id": "uuid", "cwd": "/repo" }
```

| Event | State | Behaviour / output |
|---|---|---|
| `SessionStart` | idle | register + native endpoint; primer + roster where supported |
| `UserPromptSubmit` | working | poll(ack); output messages as `additionalContext` (Kimi: plain text) |
| `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch` | working | same; Kimi only peeks because tool output is observation-only |
| `TurnStarted` | working | Kimi/native API turn starts; peek + refresh endpoint, no output |
| `Stop` | idle | peek, then consume + continue only for wake-worthy mail (`decision:block`; Kimi exit 2 + stderr) |
| `StopFailure`, `Interrupt` | idle | mark idle without forcing continuation |
| `SessionHeartbeat` | unchanged | refresh presence/native discovery without consuming |
| `SessionEnd` | — | leave silently |

`agentbus hook --async-watch` is used only by Codex's async
`UserPromptSubmit` hook. It long-polls up to 540s with `active_only=true`;
Stop/Interrupt makes it return empty without consuming. When a message arrives
during the active turn, its `additionalContext` is delivered to the next model
request. Async completion does not wake an idle Codex session.

The DSH plugin's internal `Watch` event takes `agentbus_state` and `wait_ms`.
Active watches use `active_only`; idle watches use `wake_only` and return only
urgent/direct requests. The plugin maps a returned reason to `agent.steer`.

## 4. Unix socket protocol

`$AGENTBUS_HOME/bus.sock`, mode 0600, one JSON-lines request/response per line.

Request fields: `op, id, want, agent, cwd, repo, branch, pid, pid_start,
status, state, delivery, to, from, body, kind, reply_to, wait_ms, ack,
wake_only, wake_policy, active_only, claim, driver, error, msg_id, n`.

| op | Important fields | Semantics |
|---|---|---|
| `ping` | — | daemon alive |
| `register` | `id, want, agent, cwd, state?, delivery?` | register/refresh; returns name + other sessions |
| `announce` | `id, status` | update status |
| `send` | `to, body, kind, reply_to` (`id` or `from`) | queue; returns `msg_id`, `recipients`, `dup` |
| `ask` | `id, to, body, wait_ms` | direct request; positive wait consumes correlated reply; zero returns after queueing |
| `reply` | `id, body, to?` | reply to newest addressable message with replyTo |
| `poll` | `id, ack, wait_ms, state?, delivery?, wake_only?, active_only?, claim?` | peek/consume/claim; accepted messages move to the 5-message re-read area |
| `complete` | `id, claim, driver, error?` | internal: accept or release a native claim |
| `receipt` | `msg_id` | `receipts{recipient: {state, driver, error, updated_at}}` |
| `state` | `id, state` | explicit working/idle report |
| `mine` | `id` | recently accepted messages |
| `who` / `status` | — | all live sessions, each with derived `working` and optional native `delivery` |
| `leave` | `id` | leave + tombstone |
| `log` | `n` | recent ring, newest first |
| `shutdown` | — | stop daemon |

Receipt states are `queued`, `pushing`, `accepted`. `accepted` means the hook
or Harness API accepted the batch, not that the model processed it. Claims are
not persisted; pushing receipts become queued after restore.

`delivery` on the wire is `{driver, address?, native_id?, wake?, token?}`.
The token is allowed in this request only. Session objects, `/api/state` and
`state.json` deliberately omit it. `wake` is `request` (default), `urgent`, or
`all`.

Activity derivation: explicit working is trusted for 2h, idle is idle, and an
unreported session is working only when seen in the last 10 minutes.

Limits: body 8KB, inbox 200, re-read 5, recent 300, 20 msgs/min/sender,
60s duplicate suppression, 30s native claim.

## 5. Message model

```json
{
  "messageId": "m17a...-3",
  "replyTo": "m17a...-2",
  "from": "bob",
  "to": "hazel-yorkie",
  "kind": "request",
  "recipients": 1,
  "body": "split files or tests?",
  "ts": 1730000000,
  "context": {"cwd": "/repo", "repo": "/repo/.git", "branch": "main", "status": "refactoring tests"}
}
```

Broadcast `to="all"` creates one copy per online session except the sender,
with independent claims/receipts. `user` and `user-web` are virtual human
recipients in the global log/dashboard. `ask` only consumes a reply from the
requested peer with a matching `replyTo` (empty replyTo is legacy-compatible);
other mail stays queued.

Kimi's `client_type=kimi_code_cli` selects plain prompt output and exit-2 Stop
continuations. Tool events remain observation-only and do not consume.

## 6. Native Harness endpoints

### Claude Code

Hooks register `CLAUDE_CODE_MESSAGING_SOCKET` and
`CLAUDE_CODE_MESSAGING_TOKEN`. The daemon writes an auth frame and an ordered
`priority=next` user frame to that per-session unix socket, with `session_id`
as the stale-target guard. Claude delivers between tool calls while active and
starts a turn when an allowed request/urgent arrives idle. Socket write success
means transport acceptance only; inbound hold/refuse policy still applies.

### Kimi Code

The daemon discovers live loopback instances under
`$KIMI_CODE_HOME/server/instances` (desktop first, then newest) and reads
`$KIMI_CODE_HOME/server.token`. Active sessions use
`POST /api/v1/sessions/<id>/steer`; allowed idle delivery uses idempotent
`POST …/prompts` with a stable AgentBus `prompt_id`. Custom
`AGENTBUS_KIMI_URL` / `AGENTBUS_KIMI_TOKEN` overrides are passed via the hook;
URL must be loopback HTTP. No live server means ordinary hook fallback.

### DSH / Codex / ZCode

DSH uses plugin long-poll + `agent.steer`. Codex uses async hooks, not a second
app-server attached to a live thread. ZCode uses lifecycle hooks. This avoids
concurrent app-server ownership of a conversation.

## 7. HTTP dashboard and A2A

Loopback `127.0.0.1:${AGENTBUS_WEB_PORT:-8787}`; optional bearer token applies
to all endpoints.

| Endpoint | Description |
|---|---|
| `GET /` | embedded dashboard |
| `GET /api/state` | `{ok, version, uptime, now, web_identity, sessions[], messages[], unread, receipts}` |
| `POST /api/send` | `{to, body, kind, from, replyTo?}` → `{ok, messageId, recipients, dup}` |
| `POST /api/delete` | delete unread pending copies; accepted history returns 409 |
| `GET /.well-known/agent-card.json` | root card |
| `GET /a2a/<name>/.well-known/agent-card.json` | session card |
| `POST /a2a/<name>` | A2A JSON-RPC `message/send`, text parts only |

`receipts` has shape `{messageId: {recipient: {state, driver, error,
updated_at}}}`. A2A returns synchronously after queueing; it does not claim the
model processed the message. Streaming/tasks/artifacts/push notifications are
not implemented.

## 8. MCP (stdio)

Protocol `2025-06-18`, newline-delimited JSON-RPC: initialize, ping, tools/list,
tools/call.

| Tool | Arguments | CLI |
|---|---|---|
| `agentbus_who` | `all?` | `who` |
| `agentbus_announce` | `status` | `announce` |
| `agentbus_send` | `to, body, kind?` | `send` |
| `agentbus_ask` | `to, body, wait_seconds?` | `ask` |
| `agentbus_inbox` | `peek?, wait_seconds?` | `inbox` |
| `agentbus_receipt` | `message_id` | `receipt` |

Identity uses `/proc` ancestry; calls from one agent process share a mailbox.

## 9. Environment variables

| Variable | Default | Effect |
|---|---|---|
| `AGENTBUS_HOME` | `~/.agentbus` | state directory |
| `AGENTBUS_AGENT` | detected | force agent label |
| `AGENTBUS_NAME` | automatic | preferred registration name |
| `AGENTBUS_SESSION_ID` | detected | identity override |
| `AGENTBUS_PRIMER` | on | `0` omits session contract |
| `AGENTBUS_WAKE` | `request` | `urgent` = urgent only; `all` = any message may wake |
| `AGENTBUS_PUSH` | on | `0` disables native push registration; hooks remain |
| `AGENTBUS_KIMI_URL` | discovered | loopback Kimi Server API base URL |
| `AGENTBUS_KIMI_TOKEN` | discovered | Kimi API token override (wire-only) |
| `KIMI_CODE_HOME` | `~/.kimi-code` | Kimi instance/token discovery root |
| `AGENTBUS_WEB` | on | `0` disables dashboard/A2A |
| `AGENTBUS_WEB_PORT` | `8787` | web port |
| `AGENTBUS_WEB_TOKEN` | empty | HTTP bearer token |
| `AGENTBUS_BIN` | launcher-dependent | binary used by npm launcher/DSH plugin |
