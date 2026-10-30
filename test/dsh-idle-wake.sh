#!/usr/bin/env bash
# Verify that the DSH plugin wakes an idle agent for a direct request.
# Requires go + node; no model/API quota is used (the agent is mocked).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
(cd "$ROOT" && go build -o agentbus .)
node "$ROOT/test/dsh-idle-wake.mjs"
