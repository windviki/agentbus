# DSH integration assets

`plugin/` is a Cordis plugin package (`dsh-agentbus`) that bridges a DSH Host
to the local agent bus: it injects the session contract and peer messages,
uses a per-agent long-poll watcher to steer active turns and wake idle sessions
for urgent messages/direct requests, and registers native tools
(`agentbus_who` / `send` / `ask` / `reply` / `announce` / `inbox` / `receipt` / `primer`).

Install and verification guide:
[docs/integrations/dsh.md](../../docs/integrations/dsh.md).
