# Codex integration

Current Codex releases provide lifecycle hooks and background (`async`) command
hooks. AgentBus uses these directly; it does not attach a second app-server to
a conversation already owned by the CLI.

## Install and trust

```bash
npm install -g @aixyzstudio/agentbus-cli
agentbus setup --agents codex
```

Setup merges managed entries into `$CODEX_HOME/hooks.json`
(`~/.codex/hooks.json` by default), installs the skill when `~/.codex` exists,
and backs up before rewriting.

Codex deliberately requires review of changed non-managed hooks. Open `/hooks`
in Codex, inspect the AgentBus entries, and trust them before they can run.
Changing the hook definition requires another review. Start a new Codex session
after setup and trust.

## Delivery behavior

- `SessionStart`, prompt, Pre/PostToolUse and `Stop` provide the ordinary
  context-delivery ladder; `Interrupt` and `SessionEnd` clean up state.
- A second, async `UserPromptSubmit` handler runs
  `agentbus hook --async-watch` for up to 540 seconds. While the turn is active,
  a new message completes the hook and Codex offers its `additionalContext` to
  the next model request, without waiting for another tool call.
- `Stop` or `Interrupt` marks the session idle and invalidates the outstanding
  watcher, so it cannot consume mail in the background and falsely mark it
  accepted. Async completion alone does not wake an idle Codex session.
- Direct request/urgent can continue through the synchronous `Stop` hook. A
  message arriving after Codex has already stopped waits for the next user
  turn. The dashboard shows it queued.

The manual hook fragment is [hooks.json](../../integrations/codex/hooks.json).
Use it instead of setup only if you also manage Codex hook trust yourself.

`ask --wait 0` is useful when you want a request without blocking the current
Codex task; the reply arrives asynchronously. Receipts distinguish bus queueing
and Harness acceptance from an actual answer.

Official references: [Codex hooks](https://developers.openai.com/codex/hooks),
[app-server and turn steering](https://developers.openai.com/codex/app-server).
