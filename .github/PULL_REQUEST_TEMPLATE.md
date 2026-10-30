## Summary

<!-- What changed and why. Link the issue: "Fixes #123". -->

## Test plan

<!-- Commands you ran. At minimum: go test ./... -->

- [ ] `go test ./...`
- [ ] `./test/setup.sh` (installer changes)
- [ ] `./test/e2e.sh` (delivery, web, A2A, MCP changes)
- [ ] `./scripts/test-npm.sh` (packaging changes)

## Checklist

- [ ] Behaviour/design docs updated (`docs/design.md`, `docs/protocol.md`)
- [ ] English docs updated first, Chinese mirror (`docs/zh/`) synced
- [ ] `CHANGELOG.md` updated under `Unreleased` for user-visible changes
- [ ] No secrets or machine-specific paths committed
