# Releasing agentbus

The release is one immutable version published to three places:

1. **npm** — `agentbus-cli` plus four platform packages that carry the
   prebuilt binaries.
2. **GitHub Releases** — per-platform `.tar.gz` archives + `SHA256SUMS` for
   people who do not use npm.
3. **The git tag** — `v<version>`, which is what CI builds from.

`scripts/build-release.sh` produces every artifact from one checkout. The
release workflow runs the same script, so a local rehearsal and CI cannot
diverge.

## Versioning

- Semver. Pre-1.0 minor bumps may change CLI flags; document it in
  `CHANGELOG.md`.
- One version string appears in three places: `internal/server/server.go`
  (`var Version`), `npm/cli/package.json`, and the platform package manifests.
  `build-release.sh` rewrites all npm manifests from `VERSION`, and injects the
  Go version with `-ldflags -X`, so the Go default is only a development
  fallback.
- Tag format: `v0.2.0` (the workflow strips the leading `v`).

## One-time setup

- npm account with publish rights for `agentbus-cli` and
  `agentbus-cli-{linux,darwin}-{x64,arm64}`. The unscoped name `agentbus` is
  already taken by an unrelated project, which is why the CLI package carries
  the `-cli` suffix; the installed binary is still `agentbus`.
- Repository secret `NPM_TOKEN` (automation token, publish permission).
- GitHub repository `windviki/agentbus` (the URLs in `package.json`,
  `README.md` and `go.mod` assume it; update all three if the repo moves).
- `gh` is optional locally; CI uses the built-in `GITHUB_TOKEN`.

## Local release rehearsal

```bash
# 1. clean tree, all tests green
go test -race ./...
./test/setup.sh
./test/e2e.sh
./scripts/test-npm.sh

# 2. build all artifacts (version from npm/cli/package.json by default)
VERSION=0.2.0 ./scripts/build-release.sh
ls -lh dist/
#   agentbus-cli-0.2.0.tgz
#   agentbus-cli-linux-x64-0.2.0.tgz ... (4 platforms)
#   agentbus-0.2.0-linux-amd64.tar.gz ... (4 archives)
#   SHA256SUMS

# 3. install the packed artifacts exactly like a user
tmp=$(mktemp -d)
npm install -g --prefix "$tmp" dist/agentbus-cli-linux-x64-0.2.0.tgz dist/agentbus-cli-0.2.0.tgz
"$tmp/bin/agentbus" version
```

Fast iteration without all four targets:

```bash
TARGETS="linux/amd64" OUT=/tmp/agentbus-dist ./scripts/build-release.sh
```

## Publish

Publish **platform packages first**, then the CLI package. npm resolves the
CLI's `optionalDependencies` during install, so a CLI published first would
`npm install` without a binary for a few minutes.

Check `npm config get registry` first: a corporate/mirror registry (for example
`registry.npmmirror.com`) accepts installs but not publishes. Either log in to
the registry you actually publish to, or pass
`--registry https://registry.npmjs.org` to every publish command below.

```bash
npm login
for pkg in dist/agentbus-cli-*-<version>.tgz; do
  npm publish "$pkg" --access public
done
npm publish "dist/agentbus-cli-<version>.tgz" --access public
```

Then create the GitHub release:

```bash
gh release create "v0.2.0" \
  dist/agentbus-*.tar.gz dist/SHA256SUMS \
  --title "agentbus v0.2.0" --generate-notes
```

## Automated release

`.github/workflows/release.yml` runs on a pushed `v*` tag:

1. `go test`, `test/setup.sh`, `test-npm.sh`;
2. `scripts/build-release.sh` with the tag version;
3. uploads assets to the GitHub release;
4. publishes platform packages, then `agentbus-cli`, skipping any package
   version that already exists (so a failed run can be re-run).

```bash
git tag v0.2.0
git push origin v0.2.0
```

The workflow needs the `NPM_TOKEN` secret. It also uses `id-token: write` only
for repository permissions, not for npm provenance; add
`--provenance` to the `npm publish` commands if you want provenance
attestations (requires the package to be built in a public repository).

## After publishing

- `npm view agentbus-cli version` and `npm dist-tag ls agentbus-cli`.
- Install from a clean machine/container:
  `npm i -g agentbus-cli && agentbus version && agentbus setup --dry-run`.
- Verify the GitHub archives: `sha256sum -c SHA256SUMS`, unpack one, run
  `./agentbus version`.
- Move the `## [Unreleased]` section of `CHANGELOG.md` to the released version
  and commit it before tagging (the tag points at that commit).
- If a release is broken, publish a patch version. Do not unpublish: npm
  versions are immutable. If necessary, deprecate the broken version:
  `npm deprecate agentbus-cli@0.2.0 "broken binary; use 0.2.1"`.

## Checklist

- [ ] `CHANGELOG.md` has the release notes under the new version
- [ ] `go.mod` / README requirements match the Go version used in CI
- [ ] `npm/cli/LICENSE` is identical to the repository `LICENSE`
- [ ] `go test -race ./...`, `./test/setup.sh`, `./test/e2e.sh` pass
- [ ] `./scripts/test-npm.sh` passes
- [ ] `./scripts/build-release.sh` output verified locally
- [ ] tag pushed, workflow green, GitHub release has 4 archives + checksums
- [ ] npm shows the new `latest`
