# DSH integration assets

`plugin/` is a Cordis plugin package (`dsh-agentbus`) that bridges a DSH Host
to the local agent bus: it injects the session contract and peer messages, wakes
the session for urgent messages and direct requests (including a 5s idle poller,
because a Stop hook cannot fire after the agent has stopped), and registers native tools
(`agentbus_who` / `send` / `ask` / `reply` / `announce` / `inbox` / `primer`).

Install and verification guide:
[docs/integrations/dsh.md](../../docs/integrations/dsh.md).
