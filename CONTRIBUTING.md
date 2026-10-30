# Contributing to agentbus

Thanks for helping. This project coordinates other people's agent sessions, so
correctness around delivery, identity and wake-up policy matters more than
feature count. Small, tested changes get merged fastest.

## Development setup

```bash
git clone https://github.com/windviki/agentbus.git
cd agentbus
go test ./...          # unit tests
go build -o agentbus .
./agentbus setup --dry-run
```

Requirements: Go 1.22+, Node.js 18+ only for the npm packaging tests,
`curl` and `python3` for the e2e scripts.

## Before opening a PR

```bash
gofmt -w .
go vet ./...
go test -race ./...
./test/setup.sh        # if you touched setup/installer code
./test/e2e.sh          # if you touched delivery, web, A2A or MCP
./scripts/test-npm.sh  # if you touched npm packaging
```

CI runs the same commands on Linux.

## What to change where

| Change | Files |
|---|---|
| CLI command / flag | `main.go`, `setup.go` (installer), update `usage()` and `docs/protocol.md` |
| Hook behaviour or delivery policy | `internal/server/server.go`, `internal/bus`, `docs/design.md` §delivery ladder, tests |
| Message model or socket op | `internal/proto`, `internal/server`, `docs/protocol.md`, `internal/bus/*_test.go` |
| Web dashboard or HTTP API | `internal/web`, `docs/protocol.md` §HTTP |
| Harness wiring | `setup.go` + `docs/integrations/<agent>.md` + `test/setup.sh` assertion |
| npm packaging | `npm/`, `scripts/build-release.sh`, `scripts/test-npm.sh` |

## Documentation rules

- **English is canonical.** `README.md` and `docs/` are English; mirror the
  change in `README.zh-CN.md` and `docs/zh/`. Keep commands, code blocks and
  environment variable names identical across languages.
- Behaviour changes update `design.md` / `protocol.md` before or with the code.
- Claims about agent behaviour need a reproduction command, a date and a
  harness version. Put them in `adoption.md` / `testing.md`.
- User-visible changes get a `CHANGELOG.md` entry under `Unreleased`.

## Adding a harness integration

1. Read `docs/protocol.md` §3 (hook JSON) and decide which events the harness
   exposes.
2. Add detection + wiring to `setup.go` (`discoverAgents`, `installAgent`), or
   document the generic MCP/skill path if the harness has no hooks.
3. Add an assertion to `test/setup.sh` for the generated config.
4. Write `docs/integrations/<harness>.md` (and the Chinese mirror) covering
   install, what is wired, wake semantics and limitations.
5. Do not add a second delivery mechanism for the same events — duplicate
   injection is worse than no injection.

## Commit style

Conventional-ish prefixes, imperative mood:

```text
feat: add Kimi hooks installer
fix: keep reply target when the newest message is a notice
docs: explain the Stop-wake policy
test: cover uninstall idempotency
```

Reference the issue in the body. Keep unrelated refactors out of behaviour PRs.

## Reporting security issues

Do not open a public issue. Use
[GitHub Security Advisories](https://github.com/windviki/agentbus/security/advisories/new)
and see [SECURITY.md](SECURITY.md).

By contributing you agree that your contribution is licensed under the MIT
license of this repository.
