# Testing and verification

## 1. Test layers

| Layer | Location | What it verifies | Dependencies |
|---|---|---|---|
| Unit | `internal/bus/*_test.go` | registration/naming/delivery/reclamation/persistence/rate limiting/deduplication/kind/ask/reply | Go |
| End-to-end | [test/e2e.sh](../test/e2e.sh) | real daemon + two-session hook JSON + Web + A2A + MCP + restart | Go, curl |
| DSH idle wake | [test/dsh-idle-wake.mjs](../test/dsh-idle-wake.mjs) | the plugin's Stop policy for idle agents + `agent.steer` (mock agent, no model quota spent) | Go, Node |
| Plugin structure | `claude plugin validate` / `plugin details` | whether the host accepts the plugin manifest and hooks | Claude Code |
| Installer | [test/setup.sh](../test/setup.sh) | detection, selection, idempotency, backups and uninstall in a throwaway `HOME` | Go, python3 |
| Interop | [test/interop.sh](../test/interop.sh) | whether real dsh ↔ claude sessions can exchange messages and react | dsh, claude, quota |

## 2. Unit tests

```bash
go test ./...            # all
go test ./internal/bus -v
go test -race ./...      # concurrency/race (optional, slower)
```

## 3. End-to-end (e2e)

```bash
./test/setup.sh           # installer: detection/idempotency/uninstall, temp HOME
./test/e2e.sh
./test/dsh-idle-wake.sh   # DSH plugin idle wake (mock agent, no quota spent)
```

The script starts its own daemon inside a `mktemp`ed `AGENTBUS_HOME` (dashboard
port defaults to 18787 and can be overridden with `AGENTBUS_WEB_PORT`), stops
the daemon on exit, and never touches the real `~/.agentbus`. Scenarios
covered:

| Section | Assertions |
|---|---|
| build | the binary builds |
| daemon | auto-started on first use; `status` advertises the web address |
| SessionStart | registration, name, roster includes peer status, session contract (primer) |
| Workspace | `peers` groups by worktree/repo; Kimi's first prompt automatically gets the roster injected |
| Delivery | PostToolUse injection; no re-delivery after ack |
| Delivery ladder | chat does not wake; a directed request wakes and flushes the queue; a broadcast request does not wake; `AGENTBUS_WAKE=urgent` falls back to urgent-only; urgent always wakes |
| ask | directed request round-trip; the answer is consumed and not re-delivered |
| reply | no need to remember the id; replies to the peer's most recent message (kind=reply + replyTo) |
| notify | one-way notification, no reply semantics |
| Errors | an unknown recipient reports an error and offers a suggestion |
| Web | `/api/state` (including unread), `/api/send` (including wake on kind=request), `/api/delete` deleting unread messages, the embedded page and relative paths, broadcast/unicast, workspace roster and the chat feed UI markers |
| A2A | Agent Card, `message/send` delivery, unknown method `-32601` |
| MCP | initialize / tools/list / tools/call over the real bus |
| Operations | log, leave + tombstone, PID reclamation, restart persistence, a human sending as `user` |

Things to watch when writing tests:

- Do not chain `cmd | grep -q` directly: `grep -q` exits early, the Go process
  receives SIGPIPE, and with `pipefail` this produces an occasional exit code
  141; capture into a variable first, then match with `<<<`.
- Use `--session sid:...` to construct deterministic identities; test daemons
  use a temporary HOME and a separate port.

## 4. Plugin bundle validation

```bash
claude plugin validate integrations/claude-code
claude plugin validate integrations/zcode
claude --plugin-dir "$PWD/integrations/claude-code/plugin" plugin details agentbus
```

Expect `plugin details` to show 1 skill, 5 hooks (SessionStart /
UserPromptSubmit / PostToolUse / Stop / SessionEnd), and `Status: ✔ loaded`.

ZCode has no scriptable CLI; its plugin manifest follows the
`.zcode-plugin/plugin.json` spec; on this machine only JSON/structural
validation and manual loading verification are possible.

## 5. Interop live test (dsh ↔ claude)

Script: [../test/interop.sh](../test/interop.sh).

```bash
./test/interop.sh                                # preflight, no quota spent
RUN_INTEROP=1 ./test/interop.sh                  # DSH receives + Claude receives
RUN_INTEROP=1 RUN_INTEROP_BASELINE=1 ./test/interop.sh   # plus a no-primer baseline
```

Prerequisites: `go`, `dsh`, `claude`, `python3`, `curl`; Claude's local route
must be reachable (`ccr start`, default `127.0.0.1:3456`); DSH model
credentials come from `DEEPSEEK_API_KEY` or `~/.dsh/.credentials.yaml` (the
script only reads them, never prints them).

Inside a temporary `AGENTBUS_HOME`, the script:

1. starts the daemon + an `interop-sender` session;
2. **DSH**: `dsh headless --patch` loads `integrations/dsh/plugin`, waits for it
   to register, then sends a `request`; asserts that the bus log shows
   `dsh-probe→…: dsh-ack-42` and the model output shows `RECEIVED`;
3. **Claude**: writes a `--settings` containing only hooks, pre-registers the
   session and pre-seeds a `request`, runs `claude -p "Say hello …"`, and
   asserts that the contract was injected and that
   `claude-probe→…: claude-ack-7` appears;
4. **Baseline** (optional): run the same scenario with `AGENTBUS_PRIMER=0`,
   recording only "reply/ignore"; this is not a failure condition;
5. at the end, stops the daemon, keeps the log directory and prints its path.

Most recent result (2026-10-29): DSH plugin and primed Claude all PASS; the
no-primer baseline ignored the peer message. See [adoption.md](adoption.md) §6
for the behaviour analysis.

### Long-running concurrent scenario (semi-automatic, verified manually)

Concurrent tasks across two agents are highly non-deterministic, so there is no
fixed assertion script; reproduce it with the steps below and write the results
into [adoption.md](adoption.md) §6.

1. Create a shared repository: `TASK.md` requires two halves, calc + words, and
   both must edit `toolkit/__init__.py` and `README.md`;
2. start the daemon; start the DSH words half first
   (`DSH_PERMISSION_MODE=danger-full-access`), wait for it to register; then
   start the Claude calc half (`--plugin-dir`), with a prompt that **does not
   mention agentbus**;
3. while they run, send each side one message with
   `agentbus send --from user --kind urgent <name> "reply 'ack-running' then continue"`;
4. afterwards run `python3 -m unittest discover -s tests` and check the
   `__init__.py`/README contents against `agentbus log`.

Most recent result (2026-10-29): both sides ran `who`/`announce` automatically;
DSH sent 2 requests and Claude sent 3 replies (including division-of-labour
negotiation and two urgent acks); 21 tests OK; no files overwritten. The run
uncovered and fixed three kinds of problems — Claude Bash identity, human
replies, and flag misuse (see adoption.md).

## 6. ZCode hooks live test (2026-10-29)

Install: `agentbus setup --agents zcode` (a source checkout can use
`./scripts/install.sh --zcode-hooks`). A real session was started in
`/tmp/ab-zrepo` with `zcode -p` (the desktop bundle's
`server-3.16/glm/zcode.cjs`):

- the SessionStart hook registered successfully; the `zcode` session appeared
  in `agentbus who`;
- while the session was running, a peer sent a `request`; after the hook
  injected it, the model ran `agentbus reply`, and the bus log showed
  `zprobe3→zpeer: z-ack-9` (with replyTo);
- identity fix: ZCode does not pass a session id to Bash, so the pid→sid mapping
  written by the hook resolves it (see the identity table in
  [protocol.md](protocol.md));
- with a neutral task (one that does not mention agentbus), the model read the
  injected roster and judged that a peer in the same directory would conflict,
  but did not proactively `announce`.

Note: a bare `zcode -p` may report `Select a model before sending a message`
(the desktop account's provider is not visible to the standalone CLI); this is
a CLI limitation. When testing, temporarily enable an API-key provider and
restore it afterwards; see [integrations/zcode.md](integrations/zcode.md).

## 7. Kimi hooks contract verification (2026-10-29)

The local Kimi 7-day quota was exhausted (429), so a real model session could
not be started; verification used real hook payloads:

```bash
printf '%s' '{"hook_event_name":"SessionStart","session_id":"kimi-sim-2",
  "cwd":"/tmp/ab-zrepo","client_type":"kimi_code_cli"}' | agentbus hook
# → registered as a kimi session
printf '%s' '{"hook_event_name":"UserPromptSubmit",...}' | agentbus hook
# → plain-text peer message (Kimi splices it into context)
printf '%s' '{"hook_event_name":"Stop",...}' | agentbus hook
# → exits 2 when there is an urgent or directed request; reason on stderr
```

Installation: see [integrations/kimi.md](integrations/kimi.md). After the quota
resets, one live smoke test with `kimi -p "..."` is enough.

## 8. Dashboard shows 0 sessions: troubleshooting

If `agentbus who` has sessions but the dashboard/API is empty:

```bash
curl -s http://127.0.0.1:8787/api/state | jq '.sessions|length'
# find the process holding 8787 and check its AGENTBUS_HOME
ss -ltnp | grep 8787   # or read /proc/net/tcp + /proc/<pid>/fd
```

Common cause: an early test daemon still holds 8787, so the daemon for
`~/.agentbus` failed to bind (the log shows
`web disabled: bind: address already in use`). Stop the test daemon holding
the port, then restart the default daemon.

## 9. Pre-commit checklist

```bash
gofmt -l .
go vet ./...
go test ./...
./test/e2e.sh
git diff --check
```
