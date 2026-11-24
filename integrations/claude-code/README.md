# Claude Code integration assets

This directory carries the installable plugin bundle:

- `.claude-plugin/plugin.json` — plugin manifest
- `hooks/hooks.json` — SessionStart / UserPromptSubmit / PreToolUse /
  PostToolUse / PostToolUseFailure / Stop / StopFailure / SessionEnd, all calling
  `agentbus hook`; the daemon additionally uses Claude's native inbox when available
- `skills/agentbus/SKILL.md` — bundled copy of the skill

A local marketplace entry lives one level up (`../.claude-plugin/marketplace.json`).

Wiring guide, verification steps and the manual (non-plugin) option:
[docs/integrations/claude-code.md](../../docs/integrations/claude-code.md).
