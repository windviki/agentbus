#!/usr/bin/env bash
# Smoke-test the npm distribution without publishing: build the native binary,
# pack both packages, install them into a throwaway global prefix, and run the
# CLI through the same launcher a user gets from `npm i -g agentbus-cli`.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

native="$(go env GOOS)/$(go env GOARCH)"
case "$native" in
  linux/amd64)  key=linux-x64 ;;
  linux/arm64)  key=linux-arm64 ;;
  darwin/amd64) key=darwin-x64 ;;
  darwin/arm64) key=darwin-arm64 ;;
  *) echo "test-npm: unsupported native platform $native" >&2; exit 1 ;;
esac

echo "== build $native"
OUT="$TMP/dist" TARGETS="$native" "$ROOT/scripts/build-release.sh" >/dev/null
version="$(node -p "require('$ROOT/npm/cli/package.json').version")"

echo "== launcher dev fallback"
node "$ROOT/npm/cli/bin/agentbus.js" version

echo "== npm pack + global install"
mkdir -p "$TMP/prefix"
npm install -g --prefix "$TMP/prefix" --no-audit --no-fund --loglevel=error \
  "$TMP/dist/agentbus-cli-$key-$version.tgz" \
  "$TMP/dist/agentbus-cli-$version.tgz"

BIN="$TMP/prefix/bin/agentbus"
echo "== installed binary"
"$BIN" version

echo "== setup dry-run through the installed launcher"
mkdir -p "$TMP/home/.claude"
HOME="$TMP/home" DSH_HOME="$TMP/dsh" AGENTBUS_HOME="$TMP/bus" "$BIN" setup --dry-run --json --agents claude >"$TMP/report.json"
node -e '
const fs = require("fs");
const r = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
if (!r.detected.find(a => a.id === "claude" && a.detected)) {
  console.error("claude was not detected"); process.exit(1);
}
if (!r.actions.length) { console.error("dry-run produced no actions"); process.exit(1); }
' "$TMP/report.json"

echo "test-npm: ok"
