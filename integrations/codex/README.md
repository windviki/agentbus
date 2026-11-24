# Codex assets

`hooks.json` is the manual Codex hook fragment. Prefer
`agentbus setup --agents codex`, which merges it idempotently and preserves
other hooks. Codex requires the entries to be reviewed and trusted in `/hooks`.
Full behavior and delivery semantics: [Codex integration](../../docs/integrations/codex.md).
