# Research: does a mature open-source solution exist?

> Researched 2026-10. Method: GitHub repository searches (several keyword combinations) + reading each repository's README/architecture docs.
> Conclusion up front: **no mature project builds a "local, cross-CLI, hook-driven, zero idle-token" agent session message bus**.
> The space holds a cluster of experimental 0–2 star implementations, each missing one key capability; see the table below.

## Our requirements

1. **Cross-CLI**: local parallel sessions from claude code / zcode / kimi / dsh / opencode etc. can all connect;
2. **Session naming**: registered at startup, names unique (assigned by the server or collision-checked);
3. **Presence and status**: others can see "bob is working on XXX", and a newcomer spots conflicts as soon as it starts;
4. **Point-to-point messages**: send a message to a named session and negotiate the split of work; broadcast supported;
5. **Near real-time**: delivery works whether the peer is working (its next tool call) or idle (woken at Stop);
6. **Token economics**: **zero** context overhead when idle with no messages — the essential difference from MCP-based designs;
7. **Zero administration**: crashed sessions are reclaimed automatically, the daemon restarts itself, and the user does no operations work.

## Survey of existing solutions

### A. Human-operated session managers (not agent-to-agent communication)

- **[smtg-ai/claude-squad](https://github.com/smtg-ai/claude-squad)** (AGPL-3.0):
  a TUI that manages multiple Claude Code/Codex/Gemini/aider sessions with
  tmux + git worktree. Its README states explicitly that there is no
  inter-session communication/message bus — it is "isolation"; we want
  "interconnection".
- **[av/harbor](https://github.com/av/harbor)** (Apache-2.0, ~3.3k stars): actually
  a CLI that brings up a full local LLM stack (inference backends, WebUI, RAG,
  etc.) with Docker Compose, unrelated to inter-session agent communication.

Conclusion: this class solves "how a human runs many agents at once", not
"how agents negotiate with each other".

### B. Network-level interoperability protocols (too heavy)

- **A2A / Agent2Agent** (initiated by Google, now under the Linux Foundation):
  HTTP + JSON-RPC based, with Agent Card, tasks, artifacts, SSE streams. Its
  positioning is interoperability between **cross-organization, cross-network
  resident service agents**. Running an HTTP server + JSON-RPC lifecycle for
  two local terminal sessions is completely disproportionate; and no CLI
  embeds an A2A server anyway.
  (v1 adds only the A2A subset over loopback — Agent Card + `message/send` —
  so external clients can attach to the same bus; tasks/artifacts/SSE are not
  done, see [design.md](design.md) §6.)

### C. Same direction, very early projects (≤2 stars)

| Project | Mechanism | Gap to requirements |
|---|---|---|
| [exalsch/claude-session-bus](https://github.com/exalsch/claude-session-bus) (0★, 11 commits, GPL-3.0) | MCP `broadcast()` tool + append-only JSONL mailbox + `UserPromptSubmit` hook injection | Claude Code only; broadcast but no directed messages; **messages arrive only on the user's next prompt submission** (so neither during work nor when idle); the MCP tool schema permanently occupies context (costs tokens even when idle); no unique naming/presence/status |
| [lloyd-c137/amp-skill](https://github.com/lloyd-c137/amp-skill) (0★, 1 commit) | Unix socket daemon + JSON frames + skill + SQLite offline queue, Python/systemd | a one-commit prototype; no hook delivery path (a CLI agent cannot hold a persistent connection); no status announcements; no crash reclamation; no multi-CLI coverage |
| [zimdin12/aify-comms](https://github.com/zimdin12/aify-comms) (2★, ~4.7k commits) | Docker + FastAPI + SQLite control plane, one PTY daemon per machine, agents talk over MCP tools, web panel | its positioning is "control plane/orchestrator": it takes over agent processes and terminals, heavy stack (Docker, MCP, dashboard); we are a **peer bus**: we do not take over processes, we only pass messages |
| [JaredCluff/opencode-nats](https://github.com/JaredCluff/opencode-nats) (0★) | adds a NATS plugin to OpenCode | a single CLI; introduces a NATS dependency; no token-economics design |
| The rest (swarm-bus, agentlink, sparks-bus-mcp, Retsumdk/message-bus etc., all 0★) | assorted pub-sub / MCP brokers | a generic messaging layer with no agent session semantics (naming, presence, lifecycle, hook delivery) |

### D. Why not just use MCP / files / Redis?

- **MCP server**: every session permanently carries a set of tool schemas
  (~a few hundred tokens per session, spent even when idle); and there is no
  channel that "pushes a message to you", so it still depends on polling tool
  calls. Fits as an optional facade, not as a foundation.
- **Pure file mailbox** (what claude-session-bus does): simple to implement,
  but no atomicity for unique naming, no long-poll wake, no TTL/PID
  reclamation; every consistency property has to be assembled by hand from a
  lock protocol.
- **Redis/NATS**: an external service dependency, against "clone and use,
  zero operations".

## Conclusion

Of the 7 requirements, the two closest projects (claude-session-bus, amp-skill)
each satisfy roughly half, and both are personal-prototype scale (≤2 stars, no
releases, explicitly documented unfixed edge cases). **Building our own is
justified**, and we can absorb two good ideas from them: hook injection (from
claude-session-bus) and a unix-socket daemon (from amp-skill), while adding the
parts unique to us:

- a cross-CLI identity protocol (session_id → unique name, `/proc` ancestor
  probing as the fallback);
- presence + status announcements ("bob is working on XXX");
- four delivery moments (SessionStart roster / PostToolUse during work /
  UserPromptSubmit turn start / Stop wake for urgent and directed request);
- PID-level liveness and tombstone-style name reclamation;
- rate limiting + deduplication against agent conversation storms;
- daemon auto-start and atomic state persistence;
- optional local boundaries: web dashboard, A2A subset, stdio MCP facade — all
  just views over the same bus state, not changing the "hooks as the
  foundation" conclusion.
