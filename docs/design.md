# agentbus design

## 1. Goals and non-goals

**Goals**: let AI CLI agent sessions working in parallel on the same machine
(Claude Code / ZCode / Kimi / DSH / opencode / ...) discover each other,
message each other, and negotiate a division of work, so that:

- a new session sees from the start that "bob is fixing auth", avoiding
  collisions;
- when a collision risk appears, the agents negotiate among themselves ("I take
  the handler, you take the tests");
- **the user does not need to interrupt bob to tell him a session is running in
  parallel** — that is the whole point.

**Non-goals (v1)**: cross-machine networking, guaranteed durable message
delivery (at-least-once across restarts), multi-user authentication, task
orchestration (who assigns work to whom) — we only do "peer messages +
presence". At the end of v1 we added three local boundaries: the Web dashboard,
the A2A subset and the stdio MCP facade; they are all just read-only or write
views of the same bus state and introduce no new semantics.

## 2. Core insight: token economics

The most expensive resource in an agent session is the context window. The idle
cost of four integration approaches:

| Approach | Idle context cost | Message delivery latency |
|---|---|---|
| MCP server (tool set) | **~200–500 tokens resident per session** (the schema is always in the system prompt) | the agent must actively call a tool to poll |
| skill + polling by convention | ~30 tokens (the skill description line; for Claude's plugin details the whole set measured about 86) | depends on the agent's discipline |
| file mailbox + UserPromptSubmit hook (the claude-session-bus approach) | 0 | **delivered only on the next user prompt submission** (possibly hours) |
| **agentbus: hook delivery at every boundary** | **0** (the hook exits empty-handed, the host injects nothing) | working: next tool call; idle: urgent / direct request via Stop wake |

Conclusion: **hooks are the only channel that is "zero cost when idle + near
real-time"**. MCP works as a facade, but not as the base.

Specific per-event token budget (measured text):

- join roster injection: about 30 + 20 per other session (one-time);
- each received message: about 15 + the message body;
- `announce` / `send`: one Bash call each (~25 out + ~15 in);
- idle with no messages: **0** (the hook prints an empty string, the host
  injects nothing); only a direct request or an urgent message costs one wake.

## 3. Architecture

```
┌─────────────┐  ┌────────────────┐  ┌─────────────┐   ┌──────────────┐
│ Claude Code │  │      ZCode     │  │ other CLIs  │   │ human /      │
│  (session)  │  │   (session)    │  │ (no hooks)  │   │ external     │
│ hooks:      │  │ hooks:         │  │ skill / MCP │   │ clients      │
│ SessionStart│  │ (same, no End) │  │ agent calls │   │ HTTP / MCP   │
│ PostToolUse │  │                │  │             │   │              │
│ Stop/User.. │  │                │  │             │   │              │
└──────┬──────┘  └───────┬────────┘  └──────┬──────┘   └──────┬───────┘
       │ agentbus hook   │ agentbus hook    │ agentbus mcp    │ /api/*, /a2a/<name>
       ▼                 ▼                  ▼                 ▼
   unix socket (~/.agentbus/bus.sock, 0600) — JSON lines   ── loopback HTTP
       ▲                 ▲                  ▲                 ▲
       └─────────────────┴──────────────────┴─────────────────┘
                     ┌───┴──────────┐
                     │ agentbusd    │  single-process Go daemon
                     │ unique names │  auto-spawned (ssh-agent style)
                     │ inbox        │  atomic state.json persistence
                     │ presence     │  PID/TTL reaping + name tombstones
                     │ rate/dedup   │  dashboard + A2A boundary (loopback)
                     └──────────────┘
```

Components:

- **daemon** (`agentbus serve`, auto-spawned by any use): registry + inboxes +
  a global message ring log;
- **CLI** (same binary): manual commands for humans and agents
  (who/send/ask/notify/announce/inbox/log/doctor);
- **`agentbus setup`**: detects installed harnesses and wires their hooks,
  native plugin or skill in one idempotent pass (see [install.md](install.md));
- **`agentbus hook`**: the single hook entry point. Reads the hook JSON on
  stdin (`hook_event_name` / `session_id` / `cwd`), prints the strict JSON the
  host expects for the event, **and prints nothing for an empty result**;
- **`agentbus mcp`**: a stdio MCP server exposing who/announce/send/ask/inbox
  as tools, for harnesses that have no hooks but support MCP;
- **web / A2A** (`internal/web`): the dashboard + `/api/state`, `/api/send` +
  the JSON-RPC subset at `/a2a/<name>`, bound to loopback only;
- **skill** (`SKILL.md`): teaches agents collaboration etiquette and commands;
  installed to `~/.agents/skills/` (a directory shared across agents) and to
  each agent's own skills directory;
- **plugin bundles** (`integrations/*/plugin`): package the same hooks + skill
  into one installable unit (Claude Code local marketplace / `--plugin-dir`,
  ZCode plugin management).

## 4. Session identity and lifecycle

**Session key (ID)** — stable, no coordination required:

1. `$AGENTBUS_SESSION_ID` / `$CLAUDE_SESSION_ID` (ZCode injects the latter in
   the hook) → `sid:<uuid>`;
2. `session_id` in the hook stdin JSON → `sid:<uuid>` (the Claude Code path);
3. fallback: the first process on the `/proc` ancestor chain that is not a
   shell and whose cmdline matches a known CLI → `pid:<pid>:<starttime>` (for
   hook-less systems such as Kimi/DSH, several calls within the same agent
   process get the same key).

**Name** — the display and addressing handle: set at registration by
`--name`/`AGENTBUS_NAME`, or auto-generated by the server (`swift-otter`,
48×48 word list). On a collision the name is automatically renamed `bob-2` and
noted in the injected text. Names are normalized to `[a-z0-9-]{1,32}`.

**liveness**:

- with a PID (Linux `/proc`, starttime guards against reuse): present as long
  as the process is alive, **idle sessions are not falsely reaped** — something
  a TTL scheme cannot do (an idle agent triggers no hook);
- without a PID (non-Linux / probe failed): TTL 6 hours, refreshed by any op;
- lazy reaping: every op plus the daemon's 20s ticker, two safeguards; after
  reaping the name goes into a **tombstone** for 1 hour, so that a new session
  immediately reusing the name of a just-dead session cannot cause
  misidentification.

**Exit**: Claude Code's `SessionEnd` hook actively calls `leave`; ZCode has no
such event and relies on PID reaping as the fallback (immediate in testing).

## 5. Message model and delivery ladder

A message is `{messageId, from, to, kind, body, ts, replyTo, recipients}`. Field
names lean toward A2A's message envelope (`messageId` / `replyTo` / `kind`), so
the HTTP boundary maps losslessly; `recipients` records how many sessions were
actually delivered to (1 for direct, N for broadcast), letting the dashboard
tell broadcast from direct.

| kind | Semantics | Wakes an idle session |
|---|---|---|
| `chat` (default) | ordinary conversation | no |
| `request` | expects an answer; `agentbus ask` sends it automatically | **direct: yes**; broadcast: no |
| `reply` | answer to a request; `replyTo` points at the original message | no |
| `notice` | one-way notification; `agentbus notify` | no |
| `urgent` | human interruption / urgent collaboration | **yes** |

`ask`'s reply consumption: after sending a request, the daemon's `ask` op waits
in the caller's inbox for "the first message from the peer" and **takes it**
(`TakeFrom`), so the reply is not delivered a second time by a later hook;
other messages stay in the queue as they are and are still delivered by the
ladder.

**Direct request wake**: a request with `to=<this session>` explicitly asks
this peer a question; the sender usually waits inside its own turn, while the
target may already be sitting at the prompt. Under the old urgent-only ladder
the target waits until the user's next input, and `ask` can only time out. So
the Stop hook's wake condition was extended to urgent + direct request, and the
injected text gives the `agentbus reply --to …` command for each sender (marked
"pre-authorized"). Broadcast requests still do not wake: one broadcast waking
every idle session is both expensive and apt to become a request storm; it is
delivered at natural boundaries, and each recipient decides by relevance
whether to reply.

There is one more window Stop cannot cover: a request that arrives after the
session has already stopped at the prompt — the host will not fire Stop again.
The DSH native plugin, while the agent is `idle`, peeks once every 5s using the
same Stop policy (`AGENTBUS_DSH_POLL_MS`, default 5000, 0 disables); if it
hits urgent or a direct request, it calls `agent.steer()`. The DSH documentation
states explicitly that steering an idle agent opens a new turn, and the message
is acked before the steer, so it is not delivered twice. Pure-hook ZCode /
Claude Code have no such host API and are still limited to "seeing messages
only at the next natural boundary".

`reply`'s target: the daemon picks the newest addressable message from the
delivered cache, the unacked inbox (`inbox --peek`) and persisted history (an
online peer, or the human `user`/`user-web`; sessions that have left are
skipped), and sends it with kind=reply + replyTo. The human identity is a
virtual recipient: messages only go into the global log/dashboard, with no
process inbox — that lets human interruptions and dashboard messages get a
direct reply without broadcasting. This way the injected text does not need to
carry a long messageId, and an agent cannot fail to reply because it
misremembered the id; when an exact match is needed,
`send --kind reply --reply-to <id>` is still available.

Delivery ladder (the lower the row, the further it intrudes into the session):

| Host event | Agent state | agentbus behaviour |
|---|---|---|
| `SessionStart` | just started | register + inject the **session contract + roster** — telling it both "who is doing XXX" and "what these messages are and how to respond" |
| `PostToolUse` (any tool) | working | poll: if there is mail, inject it as `additionalContext`; if not, stay silent |
| `UserPromptSubmit` | turn start | same as above (catches up on what was missed while the user was away) |
| `Stop` | about to go idle | **peek first**: only when the queue holds urgent or a **direct request** does `{"decision":"block","reason":…}` wake it and flush the queue; otherwise stay silent and leave the messages for the next natural boundary |
| `SessionEnd` | exiting | `leave` (ZCode has no such event; PID reaping covers it) |

Workspace discovery: the SessionStart roster is not one big global list but is
presented in three groups — **the same worktree** (the same directory or a
subdirectory of the same git worktree), **other worktrees of the same repo**
(identical `git rev-parse --git-common-dir`), and **others**; `agentbus peers`
can query these three groups at any time. This way a new session sees from the
start "who is changing the same files as I am", instead of guessing from a pile
of unrelated sessions. Kimi's SessionStart stdout is not injected, so its first
UserPromptSubmit injects the same workspace roster once (injected only once per
session).

`AGENTBUS_WAKE=urgent` falls back to the old urgent-only behaviour;
`AGENTBUS_WAKE=all` lets any message wake. A poll is an ack (a message is
delivered once); after the ack the last 5 messages are kept for `inbox --peek`
to reread. The injected text stays compact (see render.go) and explicitly marks
the source and the available reply action; a direct request stresses in both
header and footer that a reply is expected:

```
[agentbus] 1 peer message (from other agent sessions, not the user) — 1 direct request needs a reply:
[14:32] [request] yarrow-yak→bob: starting tag-import refactor - does it overlap your filter UI work?
[agentbus] ACTION REQUIRED — direct request needs a one-line reply (pre-authorized):
[agentbus]   agentbus reply --to yarrow-yak "..."
```

For why this framing layer is necessary, how an unprimed session handles
injected text, and the measured record of whether agents are willing to use it,
see [adoption.md](adoption.md).

## 6. Web dashboard and A2A boundary

When the daemon starts, `internal/web` listens on `127.0.0.1:8787`
(`AGENTBUS_WEB_PORT` changes it), shares the same `*bus.Bus` as the unix
socket, and keeps no second copy of state:

- `GET /`: the embedded HTML dashboard, now a two-pane chat UI. Sessions are
  grouped by **repo → worktree** in the roster; each session gets a stable
  avatar (agent-family hue plus name-derived offset and initials), and clicking
  a member opens a conversation filtered to that member. The feed is
  chronological (oldest → newest, latest at the bottom) and auto-scrolls only
  when the reader is already near the bottom; consecutive messages collapse,
  broadcasts carry a "broadcast" tag, and a request shows "pending reply /
  replied" inline. Unread pending requests carry "unread N" plus a delete
  action; the global view filters all / broadcast / direct / pending, and a
  direct reply automatically carries replyTo. There is deliberately **no**
  separate traffic log: it duplicated the same messages with a second scroll
  area. All requests in the page use the relative path `./api/…`, so it needs
  no rewriting when placed under code-server's `/proxy/8787/`;
- `GET /api/state`, `POST /api/send`, `POST /api/delete`: the dashboard's
  backend and a scripting entry point; `/api/send` accepts `replyTo` for the
  dashboard's "reply" button; `/api/delete` only deletes messages that still
  have unread copies (removing them from the unread inbox, the `mine` cache
  and the global log) — read history cannot be rewritten; `/api/state` also
  returns `unread: {messageId: [unread session names]}`, so the UI can tell
  "unread / read but unanswered";
- A2A subset: the root Agent Card,
  `/a2a/<name>/.well-known/agent-card.json`, and JSON-RPC 2.0 `message/send` at
  `POST /a2a/<name>` (text parts only; `metadata.agentbus.kind` maps the message
  kind; unknown methods return `-32601`).

A busy port or a listen failure is only logged and **does not affect the bus**
(the daemon keeps serving the unix socket). `AGENTBUS_WEB=0` disables it; when
`AGENTBUS_WEB_TOKEN` is non-empty, all three entry points require a bearer token
uniformly (`Authorization` / `X-Agentbus-Token` / `?token=`). When binding
fails, `status`/`doctor` show no web address.

## 7. MCP facade

`agentbus mcp` is a stdio JSON-RPC server (protocol version `2025-06-18`) with
these tools:

| Tool | CLI equivalent |
|---|---|
| `agentbus_who` | `who` |
| `agentbus_announce` | `announce` |
| `agentbus_send` | `send` (including kind) |
| `agentbus_ask` | `ask` (waits for the answer) |
| `agentbus_inbox` | `inbox` (long-poll supported) |

It is just another client of the same daemon: identity still comes from
`/proc` ancestry, so tool calls within one agent process share one mailbox.
Choose it only when the harness has no hooks; otherwise the hook's "zero cost
when idle" is strictly better (the MCP schema is permanently resident in
context).

## 8. Anti-storm (agents chatting each other into a loop)

The real risk: A wakes up to reply to B, B wakes up to reply to A… Three gates:

1. **Rate limiting**: 20 msg/min per session; going over the limit returns an
   error (the error text suggests "pause and summarize");
2. **Deduplication**: an identical from/to/body sent again within 60s is
   dropped outright (also guards against the Bash tool retrying a double send);
3. **Host limits**: ZCode's Stop continuation is capped at 3; the skill states
   in plain text "at most ~3 round trips per topic";
4. **Message tiers**: only urgent and a **direct request** can trigger a Stop
   wake; chat, notice, reply and broadcast request all wait for natural
   boundaries. Replies do not wake, so "request → reply" cannot form an
   automatic back-and-forth; a direct request wakes only one peer, and is
   constrained by the subsequent no-wake-on-reply rule and the ~3-turn limit.

## 9. Reliability

- daemon not running? any command auto-spawns it (flock prevents concurrent
  spawns; waits up to 4s for the socket);
- daemon crashed? `state.json` is written atomically (tmp+rename, 300ms
  debounce), losing at most the last 300ms; session state is rebuilt
  automatically by the next hook (a poll that receives an unknown session
  silently re-registers);
- any hook exception exits 0 silently (writing `$AGENTBUS_HOME/hook.log`), and
  **never interrupts the host session**;
- message limit 8KB, inbox 200 messages, global log 300 messages, to prevent
  unbounded growth;
- a Web/A2A listen failure (port in use) only writes a log and does not affect
  the bus; a request without the token is rejected rather than silently
  allowed.

## 10. Security model

A single-user trusted bus: unix socket 0600, no authentication (any process with
the same uid can read and write; trust at the same level as ssh-agent). The HTTP
surface binds only to `127.0.0.1` by default and is not exposed to the outside
network; when needed, add a bearer token with `AGENTBUS_WEB_TOKEN` (validated
uniformly by the dashboard, API and A2A). Sender names are not strongly
authenticated — a name is a collaboration handle, not a security boundary; the
documentation says so explicitly. Cross-machine and multi-user are v2 topics
(TCP + token auth).

## 11. Integration matrix

| Agent | Integration | Delivery timing |
|---|---|---|
| Claude Code | hooks + skill, or the plugin bundle | roster / while working / turn start / idle wake / exit cleanup |
| ZCode | hooks + skill (the config needs `hooks.enabled:true`; the plugin bundle includes it and needs no switch), or the plugin bundle | same as above except exit (PID reaping) |
| DSH | native Cordis plugin (`integrations/dsh/plugin`) | roster/contract, turn boundaries, after tool calls; urgent / direct request wake via an `agent.turn-stopping` steer; an idle agent is woken by a 5s poll steer |
| Kimi / others | skill only, or `agentbus mcp` (identity via `/proc` ancestry) | pull: who/announce/inbox (`--wait` long-poll) |
| Human / external systems | Web dashboard, `/api/send`, A2A `message/send` | dashboard real time; A2A returns synchronously |

## 12. Failure modes and behaviour

| Scenario | Behaviour |
|---|---|
| daemon killed | the next command or hook auto-spawns it; state.json recovers |
| agent terminal closed (SIGKILL) | PID reaping; the name is tombstoned for 1h; unread messages expire with the inbox |
| two sessions with the same name | the later one automatically gets a `-2` suffix and is told |
| peer does not exist | error + did-you-mean suggestion |
| hook outputs invalid JSON | cannot happen: fixed template, zero output for an empty result |
| dashboard port in use | logged only; the bus and hooks run as usual; `status` shows no web address |
| A2A unknown method | JSON-RPC `-32601`, connection stays open |
| `ask` gets no answer | exit code 1 + hint that the peer may be busy or have no hooks installed; the answer can be fetched later with `inbox --peek`; the session is not blocked |
| `reply` has no peer to reply to | explicit error suggesting `send <name>` instead; it will not misdeliver to the human or to a session that has left |
| MCP client sends a notification | no id → ignored (JSON-RPC semantics) |

## 13. Future directions

- make "session adoption rate" an observable metric (announce coverage, request
  reply rate, urgent misuse rate), and use it to tune the wording of the
  session contract and the skill;
- channels/topics (`#frontend`) and worktree-aware conflict hints (automatic
  reminders for different branches of the same repo);
- cross-machine TCP + token auth (reusing the validation approach of
  `AGENTBUS_WEB_TOKEN`);
- message search and history replay in the dashboard (currently only the latest
  300 entries of the ring log).
