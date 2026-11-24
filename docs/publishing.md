# Releasing agentbus

One immutable version is published to three places:

1. **npm** — `@aixyzstudio/agentbus-cli` plus four scoped platform packages
   containing prebuilt binaries.
2. **GitHub Releases** — per-platform `.tar.gz` archives + `SHA256SUMS`.
3. **The git tag** — `v<version>`, from which CI builds.

`scripts/build-release.sh` produces every artifact from one checkout; the
release workflow runs the same script.

## Versioning

- Use semver and document changes in `CHANGELOG.md`.
- `npm/cli/package.json` and all platform manifests carry the same version.
  `build-release.sh` rewrites them from `VERSION` and injects it with Go
  `-ldflags`; `internal/server.Version` is only a development fallback.
- Tags look like `v0.2.0`.

## One-time setup

- An npm token with publish permission for the **`@aixyzstudio` scope**, valid
  for `@aixyzstudio/agentbus-cli` and
  `@aixyzstudio/agentbus-cli-{linux,darwin}-{x64,arm64}`. A token scoped only to
  another organization or to `@windviki` will fail with 403. Check with
  `npm token list --json --registry=https://registry.npmjs.org/`.
- Repository secret `NPM_TOKEN` with those same rights. The installed command
  remains `agentbus`; the bare `agentbus` package is unrelated.
- GitHub repository `windviki/agentbus`; update repository URLs if it moves.

## Local rehearsal

```bash
go test -race ./...
./test/setup.sh
./test/e2e.sh
./scripts/test-npm.sh

VERSION=0.2.0 ./scripts/build-release.sh
ls -lh dist/
#   aixyzstudio-agentbus-cli-0.2.0.tgz
#   aixyzstudio-agentbus-cli-linux-x64-0.2.0.tgz ... (4 platforms)
#   agentbus-0.2.0-linux-amd64.tar.gz ... (4 archives)
#   SHA256SUMS

tmp=$(mktemp -d)
npm install -g --prefix "$tmp" \
  dist/aixyzstudio-agentbus-cli-linux-x64-0.2.0.tgz \
  dist/aixyzstudio-agentbus-cli-0.2.0.tgz
"$tmp/bin/agentbus" version
```

For one target only:

```bash
TARGETS="linux/amd64" OUT=/tmp/agentbus-dist ./scripts/build-release.sh
```

## Publish

Publish **platform packages first**, then the CLI. npm resolves the CLI's
scoped `optionalDependencies` during installation.

A mirror such as `registry.npmmirror.com` may work for installs but not for this
publish credential; pass `--registry https://registry.npmjs.org/` explicitly.

```bash
for name in agentbus-cli-linux-x64 agentbus-cli-linux-arm64 \
            agentbus-cli-darwin-x64 agentbus-cli-darwin-arm64; do
  npm publish "dist/aixyzstudio-$name-0.2.0.tgz" \
    --access public --registry=https://registry.npmjs.org/
done
npm publish "dist/aixyzstudio-agentbus-cli-0.2.0.tgz" \
  --access public --registry=https://registry.npmjs.org/
```

Then create the GitHub release if desired:

```bash
gh release create "v0.2.0" \
  dist/agentbus-*.tar.gz dist/SHA256SUMS \
  --title "agentbus v0.2.0" --generate-notes
```

## Automated release

`.github/workflows/release.yml` runs on a pushed `v*` tag: tests, artifact
build, GitHub release upload, then scoped platform packages followed by
`@aixyzstudio/agentbus-cli`. Existing package versions are skipped so a failed
run can be retried.

```bash
git tag v0.2.0
git push origin v0.2.0
```

The workflow needs the scoped `NPM_TOKEN` described above. `id-token: write` is
for repository permissions, not npm provenance; add `--provenance` separately
if provenance attestations are wanted.

## After publishing

```bash
npm view @aixyzstudio/agentbus-cli version --registry=https://registry.npmjs.org/
npm dist-tag ls @aixyzstudio/agentbus-cli --registry=https://registry.npmjs.org/
npm install -g @aixyzstudio/agentbus-cli
agentbus version
agentbus setup --dry-run
```

Also verify `sha256sum -c SHA256SUMS` and one unpacked binary. Publish a patch
for a broken release; do not unpublish. Deprecate if needed:

```bash
npm deprecate @aixyzstudio/agentbus-cli@0.2.0 "broken binary; use 0.2.1"
```

## Checklist

- [ ] `CHANGELOG.md` contains the new version
- [ ] npm token has `@aixyzstudio` publish rights
- [ ] `go test -race ./...`, `./test/setup.sh`, `./test/e2e.sh` pass
- [ ] `./scripts/test-npm.sh` passes
- [ ] `./scripts/build-release.sh` artifacts and checksums verified
- [ ] tag pushed, workflow green, GitHub release has four archives + checksums
- [ ] npm shows the new `latest` and a clean install runs `agentbus version`
