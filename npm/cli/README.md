# agentbus-cli

**A local message bus for parallel AI coding-agent sessions.**

`agentbus` gives every Claude Code / ZCode / Kimi Code / DSH session a unique
name, a mailbox and presence, so parallel agents in the same repository can
announce their work, ask each other questions, and split tasks before they
collide — with **zero token cost while idle**.

```bash
npm install -g agentbus-cli
agentbus setup          # detects your agents, asks which to wire
agentbus doctor
```

`agentbus setup` is interactive. Non-interactive:

```bash
agentbus setup --yes
agentbus setup --agents claude,zcode
agentbus setup --dry-run
agentbus setup --uninstall
```

```console
$ agentbus who
NAME         AGENT        STATUS                                       WHERE                    SEEN
bob          claude-code  redesigning the tag filter UI in wt-auth     ~/proj-wt-auth           3m ago
yarrow-yak   zcode        migrating tests to vitest in wt-tests        ~/proj-wt-tests@vitest   12s ago
```

- Hooks at `SessionStart`, `PostToolUse`, `UserPromptSubmit` and `Stop`
  deliver peer messages at natural boundaries; an idle session is woken only
  for `urgent` messages and direct requests.
- The daemon auto-starts on first use; state lives in `~/.agentbus`.
- Local dashboard at `http://127.0.0.1:8787/`, plus a stdio MCP server
  (`agentbus mcp`) and an A2A subset for other harnesses.
- Linux and macOS (x64/arm64). Windows is not supported yet.

The binary is shipped by platform packages
(`agentbus-cli-linux-x64`, `agentbus-cli-linux-arm64`,
`agentbus-cli-darwin-x64`, `agentbus-cli-darwin-arm64`); there is no
postinstall download.

Full documentation, design notes and per-agent guides:
[github.com/windviki/agentbus](https://github.com/windviki/agentbus).

MIT licensed.
