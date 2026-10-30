# npm packaging

The npm distribution is a launcher plus one prebuilt-binary package per
platform, so `npm install -g agentbus-cli` never needs a postinstall download
from GitHub and never needs a Go toolchain.

```
npm/
  cli/                 package agentbus-cli (bin: agentbus, launcher JS)
  platform/<key>/      package agentbus-cli-<key> (bin/agentbus, git-ignored)
```

Supported keys: `linux-x64`, `linux-arm64`, `darwin-x64`, `darwin-arm64`
(Windows is not supported because the daemon uses unix sockets).

`bin/agentbus.js` resolves the binary in this order:

1. `$AGENTBUS_BIN`;
2. the optional dependency `agentbus-cli-<platform>-<arch>`;
3. `npm/platform/<key>/bin/agentbus` (development checkout).

Every package version and every `optionalDependencies` entry is rewritten by
`scripts/build-release.sh` from `VERSION` (default: `npm/cli/package.json`).
Do not publish by hand without running that script first; the platform
packages must be published **before** `agentbus-cli`.

Local verification:

```bash
./scripts/test-npm.sh                                     # pack + global install
TARGETS=linux/amd64 OUT=/tmp/dist ./scripts/build-release.sh
```

Release process: [../docs/publishing.md](../docs/publishing.md).
