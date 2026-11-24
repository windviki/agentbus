# ZCode integration

ZCode uses lifecycle hooks: `SessionStart`, `UserPromptSubmit`, `PreToolUse`,
`PostToolUse`, `PostToolUseFailure`, and `Stop`. It has no `SessionEnd`; PID
liveness handles exit. Config hooks live under `hooks.events.*` and require
`hooks.enabled: true`; plugin hooks register themselves.

## Install

```bash
npm install -g @aixyzstudio/agentbus-cli
agentbus setup --agents zcode
```

Or from source: `./scripts/install.sh --agents zcode`. Setup installs the skill
to `~/.agents/skills/agentbus/` (no duplicate under `~/.zcode/skills`), merges
all six events into `~/.zcode/cli/config.json`, enables hooks, and backs up the
original. Open a new ZCode session afterwards.

### Plugin alternative (choose one)

```bash
./scripts/install.sh --plugin
```

In ZCode open **Settings → Plugin Management → add local marketplace** and
choose `<agentbus repo>/integrations/zcode`, then install `agentbus`. Do not
combine the plugin with config-file hooks. The manual fragment is
[hooks.json](../../integrations/zcode/plugin/hooks/hooks.json).

## Delivery and identity

- Prompt and Pre/PostToolUse events deliver queued mail as additional context;
  failures use the same path. Empty events emit nothing.
- `Stop` can continue up to three times for urgent/direct requests. Chat,
  notice, reply and broadcast requests do not wake idle sessions.
- If ZCode has already stopped, no hook fires and there is no native wake API;
  mail waits for the next prompt/tool event. Claude/Kimi/DSH have native paths,
  and Codex has an active-turn async watcher, but fully idle behavior differs
  by Harness.
- `${CLAUDE_SESSION_ID}` is available in hooks. Bash does not receive a session
  id, so AgentBus resolves the hook-recorded pid→sid map through `/proc`
  ancestry. No manual `--session` is needed.
- `SessionStart` injects the session contract (`AGENTBUS_PRIMER=0` opts out).
- The dashboard is at `http://127.0.0.1:8787/` by default;
  `agentbus status` prints the actual URL.

## Headless verification and troubleshooting

The historical live verification (2026-10-29, ZCode 3.15/3.16) confirmed
registration, request injection and a correctly correlated `agentbus reply`.
A bare `zcode -p` may report `Select a model before sending a message` when the
standalone CLI cannot see the desktop provider; that is unrelated to AgentBus.

ZCode may schedule several tools from one assistant message with limited
concurrency. If an `agentbus send` Bash call is pending behind a long Agent or
SendMessage part, the OS process has not started and nothing has reached the
bus. Wait for the running tool or send in a separate turn; this is Harness
scheduling, not a daemon stall.

Official reference: [ZCode hooks](https://zcode.z.ai/en/docs/hooks).
