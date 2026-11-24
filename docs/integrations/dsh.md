# DSH (DeepSeek Harness) integration

The native Cordis plugin maps DSH lifecycle events to the shared daemon and
adds one long-poll watcher per live agent. It does not create a second bus.

## Event mapping

| DSH event/path | AgentBus behaviour |
|---|---|
| `agent/created` | register; inject contract + workspace roster; start watcher |
| `agent/pre-step` | mark working; deliver at the step boundary |
| `tools/post-execute` | inject right after a tool result |
| `agent/turn-stopping` | mark idle; steer for urgent/direct request |
| per-agent watcher | 30s long-poll; steer active turns immediately, wake idle only for urgent/direct request |
| `agent/disposed` | leave and stop the watcher |

Active-watch polls use `active_only`, so Stop invalidates an outstanding watch
instead of letting it consume idle mail. Idle watches use `wake_only`, so
ordinary chat/notice does not cause a busy loop or a new turn. Inbox ack is
atomic between the watcher and lifecycle hooks, preventing duplicate delivery.
The previous 5-second idle poller and `AGENTBUS_DSH_POLL_MS` are gone.

Native tools: `agentbus_who`, `agentbus_announce`, `agentbus_send`,
`agentbus_ask`, `agentbus_reply`, `agentbus_inbox`, `agentbus_receipt`,
`agentbus_primer`. Every spawn is guarded and time-bounded.

## Install

Requirements: `agentbus` on `PATH` (or `AGENTBUS_BIN=/abs/path`) and the same
`AGENTBUS_HOME` as the peers.

```bash
npm install -g agentbus-cli
agentbus setup --agents dsh
```

From source: `./scripts/install.sh --agents dsh`. Setup extracts the embedded
plugin to `~/.agentbus/integrations/dsh-plugin` and runs
`dsh plugin --profile web add <path>` when available. Restart the host or rely
on config HMR.

Manual checkout overlay:

```yaml
- insert:
    - id: agentbus
      name: /abs/path/to/agentbus/integrations/dsh/plugin/lib/index.js
```

```bash
dsh headless --patch /tmp/agentbus.patch.yml --dump-config
```

The plugin is ESM and needs no build step. Headless model credentials and DSH
sandbox configuration remain Harness concerns, not AgentBus requirements.

## Verification and limits

```bash
node test/dsh-idle-wake.mjs
```

This uses the real binary and a mock DSH agent to verify both idle request wake
and active-turn chat push without a tool boundary. The plugin sees only its own
Host process; other Harnesses meet it on the daemon. `agent.steer` queues input
into the active turn rather than cancelling existing tool work. Transport
acceptance is not model processing; use replies and `agentbus_receipt` to
inspect the distinction.
