# agentbus documentation

[中文文档](zh/README.md) · [Project README](../README.md)

Choose a reading path:

| You are | Start here | Then |
|---|---|---|
| Getting it running | [install.md](install.md) | the integration guide for your agent |
| Understanding the design | [design.md](design.md) | [research.md](research.md) |
| Adding a new harness | [protocol.md](protocol.md) | [integrations/generic.md](integrations/generic.md), [adoption.md](adoption.md) |
| Wondering whether agents will actually use it | [adoption.md](adoption.md) | [design.md](design.md) §5 |
| Changing code / validating a release | [testing.md](testing.md) | [publishing.md](publishing.md) |

## Contents

| Document | Contents |
|---|---|
| [install.md](install.md) | npm / source install, per-agent setup, uninstall, troubleshooting |
| [design.md](design.md) | goals, token economics, architecture, identity and lifecycle, message model and delivery ladder, Web/A2A/MCP boundaries, anti-storm measures, reliability, security, failure modes, future work |
| [protocol.md](protocol.md) | reference: CLI, hook JSON, unix socket ops, message kinds, HTTP/A2A, MCP, environment variables |
| [adoption.md](adoption.md) | how a session accepts the bus: unprimed behaviour, session contract, trust boundary, when to negotiate, measured results |
| [testing.md](testing.md) | unit / e2e / plugin / interop test matrix and reproduction commands |
| [research.md](research.md) | prior art and why agentbus is built |
| [publishing.md](publishing.md) | release checklist: versioning, cross-compilation, npm publishing, GitHub release |
| [integrations/claude-code.md](integrations/claude-code.md) | Claude Code: hooks or the plugin bundle |
| [integrations/zcode.md](integrations/zcode.md) | ZCode: config hooks or the plugin bundle |
| [integrations/dsh.md](integrations/dsh.md) | DSH: native plugin (session injection + tools) |
| [integrations/kimi.md](integrations/kimi.md) | Kimi Code: hooks + global AGENTS.md + skill |
| [integrations/generic.md](integrations/generic.md) | any other CLI: skill or MCP |

## Maintenance conventions

- **English is canonical** (`docs/`), Chinese is a mirror (`docs/zh/`). Change
  the English text first, then mirror it; keep commands, code blocks and
  environment variable names verbatim.
- Behaviour or design changes: update `design.md` / `protocol.md` first, then
  the root READMEs and `integrations/`.
- Measured results belong in `adoption.md` and `testing.md` with a date, agent
  version and model. A claim without a reproduction command is not a result.
- `integrations/<agent>/` holds installable assets only (plugin, hook
  fragments); prose lives in this documentation tree so it cannot drift.
