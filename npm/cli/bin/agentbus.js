#!/usr/bin/env node
'use strict';

// Resolve the platform-specific agentbus binary and hand over argv unchanged.
// The binary is shipped by a per-platform package so `npm i -g agentbus-cli`
// needs no network access to GitHub and no postinstall script. The Go binary
// is the real CLI; this file only locates it.

const path = require('path');
const fs = require('fs');
const { spawnSync } = require('child_process');

const platform = process.platform;
const arch = process.arch;

if (platform === 'win32') {
  console.error(
    'agentbus: Windows is not supported yet (it needs unix sockets for the local bus).\n' +
    'Use WSL2 or macOS/Linux. See https://github.com/windviki/agentbus#platforms'
  );
  process.exit(1);
}

const key = `${platform}-${arch}`;
const exe = 'agentbus';

function candidates() {
  const list = [];
  if (process.env.AGENTBUS_BIN) list.push(process.env.AGENTBUS_BIN);
  try {
    const pkg = require.resolve(`agentbus-cli-${key}/package.json`);
    list.push(path.join(path.dirname(pkg), 'bin', exe));
  } catch (_) {
    // not installed as an optional dependency (local checkout or npm quirk)
  }
  // Development checkout: npm/platform/<key>/bin/agentbus
  list.push(path.join(__dirname, '..', '..', 'platform', key, 'bin', exe));
  return list;
}

const tried = [];
for (const bin of candidates()) {
  tried.push(bin);
  let st;
  try {
    st = fs.statSync(bin);
  } catch (_) {
    continue;
  }
  if (!st.isFile()) continue;
  const result = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
  if (result.error) {
    console.error(`agentbus: failed to run ${bin}: ${result.error.message}`);
    process.exit(1);
  }
  if (result.signal) {
    process.kill(process.pid, result.signal);
    process.exit(1);
  }
  process.exit(result.status === null ? 1 : result.status);
}

console.error(
  `agentbus: no binary for ${key}.\n` +
  `This package ships ${['linux-x64', 'linux-arm64', 'darwin-x64', 'darwin-arm64'].join(', ')} on npm.\n` +
  `Tried:\n  ${tried.join('\n  ')}\n` +
  `Reinstall with: npm install -g agentbus-cli\n` +
  `Source checkout: ./scripts/install.sh\n` +
  `Releases: https://github.com/windviki/agentbus/releases`
);
process.exit(1);
