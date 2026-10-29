# ZCode integration assets

This directory carries the installable plugin bundle:

- `.zcode-plugin/plugin.json` — plugin manifest (`.zcode-plugin` spec)
- `hooks/hooks.json` — SessionStart / UserPromptSubmit / PostToolUse / Stop
  (ZCode has no SessionEnd), all calling `agentbus hook`
- `skills/agentbus/SKILL.md` — bundled copy of the skill

A local marketplace entry lives one level up (`../.claude-plugin/marketplace.json`).

Wiring guide (plugin and configuration-file options) and verification steps:
[docs/integrations/zcode.md](../../docs/integrations/zcode.md).
