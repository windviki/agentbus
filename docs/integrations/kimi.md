# Kimi Code integration

Kimi Code uses two complementary paths: the **loopback Server API** for native
steer/prompt delivery, and lifecycle hooks for registration, roster injection,
activity and fallback.

## Install

```bash
npm install -g @aixyzstudio/agentbus-cli
agentbus setup --agents kimi
```

Or from source: `./scripts/install.sh --agents kimi`. Start a new Kimi session.
Setup:

- installs the skill and the session contract in `~/.kimi-code/AGENTS.md`;
- manages nine `[[hooks]]` entries in `~/.kimi-code/config.toml`:
  `SessionStart`, `UserPromptSubmit`, `TurnStarted`, `PostToolUse`, `Stop`,
  `StopFailure`, `Interrupt`, `SessionHeartbeat`, `SessionEnd`;
- backs up touched files and remains idempotent.

Kimi ignores SessionStart stdout, so the first `UserPromptSubmit` emits the
workspace roster once. Tool events are observation-only: they refresh presence
and native configuration without consuming mail that stdout cannot display.

## Native Server API delivery

The daemon discovers running instances under
`$KIMI_CODE_HOME/server/instances` (desktop first, then newest), validates
`GET /api/v1/meta`, and reads `$KIMI_CODE_HOME/server.token`.

- working: `POST /api/v1/sessions/<session_id>/steer` injects into the current
  turn without cancelling tools;
- idle, direct request/urgent only: `POST …/prompts` starts a turn using a
  stable AgentBus `prompt_id`, making retries idempotent;
- idle ordinary mail: remains queued for a natural boundary.

If no compatible server is reachable, delivery falls back to prompt/Stop hooks.
AgentBus does not start another Kimi server implicitly. To select an existing
loopback server explicitly:

```bash
export AGENTBUS_KIMI_URL=http://127.0.0.1:58627
export AGENTBUS_KIMI_TOKEN=...   # optional when already discoverable
```

The URL must be loopback HTTP. Tokens are passed from hook to daemon but never
written to state or returned by APIs. `AGENTBUS_PUSH=0` disables native
registration. Kimi's Server API is experimental; the running server's
`/openapi.json` is the version-specific source of truth.

## Hooks, identity and receipts

`UserPromptSubmit` emits plain text for Kimi's context; `Stop` uses exit code 2
and stderr to continue for wake-worthy mail. `TurnStarted` covers API and
background turns, while `StopFailure`/`Interrupt` prevent stale working state.
`SessionHeartbeat` refreshes native discovery.

Bash has no session-id variable, so hooks store private workspace-scoped
pid→sid maps under `$AGENTBUS_HOME/pidmap`; CLI calls resolve through ancestor
processes and parent directories, with legacy mappings as fallback. Multiple
Kimi sessions multiplexed over one host PID therefore stay distinct.

`accepted` means the Kimi API accepted the steer/prompt, not that the model
acted on it. Use `agentbus receipt` for transport and reply/pending for
processing.

Official references: [Server API](https://www.kimi.com/code/docs/en/kimi-code-cli/reference/server-api.html),
[hooks](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/hooks.html),
[steering during streaming](https://www.kimi.com/code/docs/en/kimi-code-cli/guides/interaction.html).
