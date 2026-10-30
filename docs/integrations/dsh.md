# DSH (DeepSeek Harness) integration

DSH is a Cordis-based harness: plugins are Node packages that can hook agent
lifecycle events and register tools. The bundled plugin
[`integrations/dsh/plugin`](../../integrations/dsh/plugin) is a thin client of the same daemon —
it does not reimplement the bus, it calls `agentbus hook` / the CLI and maps
the results onto DSH's native events. Cross-CLI coordination still works
because the daemon is outside any one harness.

## What it maps

| DSH event | agentbus equivalent | Effect |
|---|---|---|
| `agent/created` | `agentbus hook` SessionStart | registers the session; injects session contract + **workspace-grouped roster** (same worktree / same repo) as a user message (`agent.inject`) |
| `agent/pre-step` | UserPromptSubmit | delivers queued messages at the next step boundary |
| `tools/post-execute` | PostToolUse | delivers messages in the same step, right after a tool call |
| `agent/turn-stopping` | Stop | if an **urgent** message or a request addressed directly to this session is pending, `agent.steer`s the session so the turn continues; chat/notice/broadcast requests stay queued |
| `agent/disposed` | `leave` | removes the session from the bus |

Native tools registered: `agentbus_who`, `agentbus_announce`, `agentbus_send`,
`agentbus_ask`, `agentbus_reply`, `agentbus_inbox`, `agentbus_primer`.

Every spawn is time-bounded and guarded: a bus/daemon problem never throws out
of a plugin callback (an unhandled throw would take the whole Host down).

## Install

The plugin needs:
- `agentbus` on `PATH`, or `AGENTBUS_BIN=/abs/path/to/agentbus`;
- the same `AGENTBUS_HOME` as the peers you want to reach (default
  `~/.agentbus`).

### A. Local checkout (verified path)

Write a patch overlay and pass it to the DSH launcher:

```yaml
# /tmp/agentbus.patch.yml
- insert:
    - id: agentbus
      name: /abs/path/to/agentbus/integrations/dsh/plugin/lib/index.js
```

```bash
dsh headless --patch /tmp/agentbus.patch.yml --dump-config | grep -A1 agentbus
dsh headless --patch /tmp/agentbus.patch.yml "Call agentbus_who and show its output"
```

For the web profile, append the same `insert` row to
`~/.dsh/profiles/web/cordis.patch.yml` and restart the host (or rely on
config HMR). The loader accepts absolute paths / file URLs for `insert.name`.

### B. Install as a package

```bash
dsh plugin --profile web add /abs/path/to/agentbus/integrations/dsh/plugin
```

Then add the loader row (or let the bundle patch do it):

```yaml
# ~/.dsh/profiles/web/cordis.patch.yml
- insert:
    - id: agentbus
      name: dsh-agentbus
```

The package declares `dsh.engines.dsh` and `dsh.bundle.patch`; it intentionally
has no config schema yet — configure through environment variables.

## Verification

1. `dsh headless --patch … --dump-config` shows the `agentbus` row.
2. A prompt that calls `agentbus_who` returns the live-session table (proves
   tools + daemon connectivity).
3. A second session sends a `request`; the plugin injects it and the model can
   answer with `agentbus_reply` (see the interop test and the measured results
   in [adoption.md](../../docs/adoption.md) §6).
4. `agentbus log` shows the reply with `kind=reply` and a `replyTo` pointing at
   the request.

## Limits

- A DSH plugin only sees **its own Host process**. Another terminal, another
  `dsh headless`, Claude Code or ZCode are separate processes — they meet on
  the daemon, which is why the plugin is a client and not the bus itself.
- Wake paths: `agent/turn-stopping` covers a message that arrives before the
  turn closes; an idle poller covers a message that arrives after the session
  has already stopped at the prompt. While an agent is `idle`, the plugin calls
  `agentbus hook Stop` every 5s (`AGENTBUS_DSH_POLL_MS`, default 5000, 0
  disables) and, when the Stop policy returns `decision:block` for an urgent
  message or a direct request, calls `agent.steer()` — an idle driver starts a
  turn, so the request is answered instead of waiting for the human. Messages
  are acked before the steer, so there is no duplicate delivery.
- Hook-only harnesses (ZCode, Claude Code) have no equivalent host wake API:
  a request sent after they have already stopped still waits for the next
  prompt or tool call. The dashboard shows those as pending requests.
- Headless runs need model credentials (`DEEPSEEK_API_KEY` or the web
  credentials store); the plugin itself needs no credentials.
- If the host has no usable sandbox backend, headless bash fails with
  `sandbox mode workspace-write ... no sandbox backend is usable`. Run the
  test host with `DSH_PERMISSION_MODE=danger-full-access` (or configure a
  sandbox); this is a harness environment setting, not an agentbus one.
- The plugin is ESM and is loaded as built JS; a local checkout needs no build
  step.
