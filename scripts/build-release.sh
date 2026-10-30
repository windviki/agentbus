#!/usr/bin/env bash
# Build every release artifact from one source tree:
#   * npm platform packages (prebuilt binaries, one package per platform)
#   * the agentbus-cli npm package
#   * GitHub Release tarballs + SHA256SUMS
#
# Usage:
#   ./scripts/build-release.sh                 # all four platforms, version from npm/cli
#   VERSION=0.2.0 ./scripts/build-release.sh
#   TARGETS="linux/amd64" ./scripts/build-release.sh    # fast local check
#   OUT=/tmp/agentbus-dist ./scripts/build-release.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${OUT:-$ROOT/dist}"
VERSION="${VERSION:-$(node -p "require('$ROOT/npm/cli/package.json').version")}"
case "$OUT" in
  ""|"/"|"$HOME"|"$ROOT") echo "build-release: refusing unsafe OUT=$OUT" >&2; exit 1 ;;
esac
case "$VERSION" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "build-release: VERSION must look like 1.2.3 (got '$VERSION')" >&2; exit 1 ;;
esac
TARGETS="${TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64}"
LDVERSION="github.com/windviki/agentbus/internal/server.Version"

command -v go >/dev/null || { echo "build-release: Go is required" >&2; exit 1; }
command -v node >/dev/null || { echo "build-release: Node.js is required" >&2; exit 1; }

key_for() {
  case "$1/$2" in
    linux/amd64)  echo linux-x64 ;;
    linux/arm64)  echo linux-arm64 ;;
    darwin/amd64) echo darwin-x64 ;;
    darwin/arm64) echo darwin-arm64 ;;
    *) echo "unsupported target $1/$2" >&2; return 1 ;;
  esac
}

echo "== agentbus $VERSION"
rm -rf "$OUT"
mkdir -p "$OUT"

# Keep every package.json at the release version before packing.
VERSION="$VERSION" node - <<'NODE'
const fs = require('fs');
const path = require('path');
const version = process.env.VERSION;
const root = process.cwd();
const files = ['npm/cli/package.json', ...fs.readdirSync('npm/platform').map(d => `npm/platform/${d}/package.json`)];
for (const f of files) {
  const p = path.join(root, f);
  const pkg = JSON.parse(fs.readFileSync(p, 'utf8'));
  pkg.version = version;
  if (pkg.optionalDependencies) {
    for (const k of Object.keys(pkg.optionalDependencies)) pkg.optionalDependencies[k] = version;
  }
  fs.writeFileSync(p, JSON.stringify(pkg, null, 2) + '\n');
}
NODE

for target in $TARGETS; do
  os="${target%/*}"; arch="${target#*/}"
  key="$(key_for "$os" "$arch")"
  mkdir -p "$ROOT/npm/platform/$key/bin"
  echo "   go build $os/$arch -> npm/platform/$key/bin/agentbus"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X $LDVERSION=$VERSION" \
    -o "npm/platform/$key/bin/agentbus" .)
  chmod +x "$ROOT/npm/platform/$key/bin/agentbus"
done

echo "== npm tarballs"
for key in $(for t in $TARGETS; do os="${t%/*}"; arch="${t#*/}"; key_for "$os" "$arch"; done); do
  (cd "$ROOT/npm/platform/$key" && npm pack --pack-destination "$OUT" --silent >/dev/null)
done
(cd "$ROOT/npm/cli" && npm pack --pack-destination "$OUT" --silent >/dev/null)

echo "== release tarballs"
for target in $TARGETS; do
  os="${target%/*}"; arch="${target#*/}"
  key="$(key_for "$os" "$arch")"
  stage="$(mktemp -d)"
  mkdir -p "$stage/agentbus-$VERSION-$os-$arch"
  cp "$ROOT/npm/platform/$key/bin/agentbus" "$stage/agentbus-$VERSION-$os-$arch/agentbus"
  cp "$ROOT/LICENSE" "$stage/agentbus-$VERSION-$os-$arch/LICENSE"
  cp "$ROOT/README.md" "$stage/agentbus-$VERSION-$os-$arch/README.md"
  tar -C "$stage" -czf "$OUT/agentbus-$VERSION-$os-$arch.tar.gz" "agentbus-$VERSION-$os-$arch"
  rm -rf "$stage"
done

echo "== checksums"
(cd "$OUT" && sha256sum ./*.tgz ./*.tar.gz > SHA256SUMS)

echo "== artifacts in $OUT"
ls -lh "$OUT"
