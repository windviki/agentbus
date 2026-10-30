# Kimi Code integration

Kimi Code supports lifecycle hooks whose stdin payload is compatible with the
Claude Code / ZCode hook shape (`hook_event_name`, `session_id`, `cwd`,
`client_type`), but its **output contract differs**:

- plain stdout from `UserPromptSubmit` is appended to the model context
  (no JSON envelope);
- `Stop` blocks/continues by exiting **2** with the reason on stderr;
- `PostToolUse` is observation-only: stdout is ignored.

`agentbus hook` detects `client_type: "kimi_code_cli"` and switches to that
output mode. PostToolUse deliberately does not poll/ack in Kimi mode (it would
consume a message without showing it); mail is delivered at the next prompt,
the Stop wake, or an explicit `agentbus inbox`.

## Install (automated)

```bash
npm install -g agentbus-cli
agentbus setup --agents kimi
```

From a source checkout use `./scripts/install.sh --agents kimi`. The setup step:
- copies the skill to `~/.kimi-code/skills/agentbus/`;
- appends a managed `[[hooks]]` block (SessionStart, UserPromptSubmit,
  PostToolUse, Stop, SessionEnd) to `~/.kimi-code/config.toml`
  (backup: `config.toml.agentbus-bak`; re-running setup replaces the block
  instead of duplicating it);
- upserts the session contract in **`~/.kimi-code/AGENTS.md`** between
  `<!-- agentbus:begin -->` markers. Kimi loads this global instruction file
  every session, which is how new sessions learn the bus without a user
  reminder (SessionStart stdout is ignored by Kimi).
- Kimi ignores SessionStart stdout, so the first `UserPromptSubmit` emits the
  workspace-grouped roster exactly once (a per-session marker prevents repeats).

Start a new Kimi session afterwards; running sessions do not reload config.

## Identity

Kimi's Bash tool has no session-id environment variable. The hook records
`kimi process pid -> sid` under `$AGENTBUS_HOME/pidmap/`, and `agentbus
send/reply` run from Kimi's Bash tool resolves the identity by walking back to
the ancestor process (pid start time guards reuse). No extra flags needed.

## Verified (2026-10-29)

Kimi's 7-day quota was exhausted (`provider.api_error: 429 weekly usage
limit`) during installation, so a live model session could not be completed.
The hook contract was verified with real payloads against a test daemon:

- `SessionStart` with `client_type: kimi_code_cli` registered the session
  (`kimi` agent label);
- `UserPromptSubmit` printed the plain, framed peer message with `reply`
  guidance (Kimi appends this to context);
- `Stop` with a pending `urgent` (or a request addressed to the session)
  exited **2** and printed the reason on stderr, which Kimi treats as "continue
  the turn"; `AGENTBUS_WAKE=urgent` restores urgent-only wake.
- As with every hook-only harness, a request that arrives after Kimi has
  already stopped at the prompt waits for the next prompt or tool call; Kimi has
  no host-level wake API. The bus dashboard shows the pending request.

Once the quota resets, `kimi -p "..."` can be used for a live smoke test; no
further configuration is needed.
