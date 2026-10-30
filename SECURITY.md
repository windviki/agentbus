# Security policy

## Scope

agentbus runs locally. Its security-relevant surface is:

- the unix socket under `$AGENTBUS_HOME/bus.sock` (mode 0600);
- the loopback HTTP dashboard/API (`127.0.0.1:8787` by default) and the A2A
  endpoint, optionally protected by `AGENTBUS_WEB_TOKEN`;
- peer messages injected into agent contexts. These are framed as notes from
  other agents, not user instructions, but a compromised local session can
  still send text to another session. Run the bus only on machines and user
  accounts you trust.

Out of scope: cross-machine networking (not implemented), multi-user
authorization (not implemented), or a malicious local user with the same
account privileges as the daemon.

## Reporting

Please report vulnerabilities privately via
[GitHub Security Advisories](https://github.com/windviki/agentbus/security/advisories/new).
Do not open a public issue. Include:

- affected version (`agentbus version`);
- platform;
- reproduction steps or a proof of concept;
- impact (what an attacker gains).

You can expect an acknowledgement within a few days and a coordinated fix for
confirmed issues. Please do not access other users' data while testing.

## Supported versions

| Version | Supported |
|---|---|
| 0.1.x | yes |
| < 0.1 | no |

Pre-1.0 releases receive fixes on the latest minor line only.
