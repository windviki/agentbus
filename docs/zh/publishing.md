# 发布 agentbus

一次发布是一个不可变版本，发到三个地方：

1. **npm** — `agentbus-cli` 以及四个携带预编译二进制的平台包。
2. **GitHub Releases** — 各平台的 `.tar.gz` 归档 + `SHA256SUMS`，面向不使用
   npm 的用户。
3. **git tag** — `v<version>`，CI 以它为构建基准。

`scripts/build-release.sh` 从同一份 checkout 产出所有产物。release workflow
运行的是同一个脚本，因此本地预演与 CI 不会分叉。

## 版本

- Semver。1.0 之前的 minor 版本升级可能改动 CLI flag；在 `CHANGELOG.md`
  中说明。
- 同一个版本号出现在三处：`internal/server/server.go`（`var Version`）、
  `npm/cli/package.json`，以及各平台包 manifest。
  `build-release.sh` 会按 `VERSION` 重写所有 npm manifest，并用
  `-ldflags -X` 注入 Go 版本，因此 Go 里的默认值只是开发时的兜底。
- Tag 格式：`v0.2.0`（workflow 会去掉前导 `v`）。

## 一次性配置

- 对 `agentbus-cli` 与 `agentbus-cli-{linux,darwin}-{x64,arm64}` 有发布权限的
  npm 账号。npm 上的裸名 `agentbus` 已被一个无关项目占用，所以 CLI 包名带
  `-cli` 后缀；安装后的命令仍叫 `agentbus`。
- 仓库 secret `NPM_TOKEN`（automation token，具备 publish 权限）。
- GitHub 仓库 `windviki/agentbus`（`package.json`、`README.md` 与 `go.mod`
  里的 URL 都假定它；仓库迁移时三处都要更新）。
- 本地 `gh` 可选；CI 使用内置的 `GITHUB_TOKEN`。

## 本地发布预演

```bash
# 1. 工作区干净，所有测试通过
go test -race ./...
./test/setup.sh
./test/e2e.sh
./scripts/test-npm.sh

# 2. 构建全部产物（版本默认取自 npm/cli/package.json）
VERSION=0.2.0 ./scripts/build-release.sh
ls -lh dist/
#   agentbus-cli-0.2.0.tgz
#   agentbus-cli-linux-x64-0.2.0.tgz ...（4 个平台）
#   agentbus-0.2.0-linux-amd64.tar.gz ...（4 个归档）
#   SHA256SUMS

# 3. 完全按用户的方式安装打包产物
tmp=$(mktemp -d)
npm install -g --prefix "$tmp" dist/agentbus-cli-linux-x64-0.2.0.tgz dist/agentbus-cli-0.2.0.tgz
"$tmp/bin/agentbus" version
```

不构建全部四个目标时的快速迭代：

```bash
TARGETS="linux/amd64" OUT=/tmp/agentbus-dist ./scripts/build-release.sh
```

## 发布

**先发布平台包**，再发布 CLI 包。npm 会在安装时解析 CLI 的
`optionalDependencies`，若先发布 CLI，那么几分钟内 `npm install` 会装不到
二进制。

先检查 `npm config get registry`：公司/镜像 registry（例如
`registry.npmmirror.com`）可以安装但不能发布。要么登录你实际发布的目标
registry，要么给下面每条 publish 命令加上
`--registry https://registry.npmjs.org`。

```bash
npm login
for pkg in dist/agentbus-cli-*-<version>.tgz; do
  npm publish "$pkg" --access public
done
npm publish "dist/agentbus-cli-<version>.tgz" --access public
```

然后创建 GitHub Release：

```bash
gh release create "v0.2.0" \
  dist/agentbus-*.tar.gz dist/SHA256SUMS \
  --title "agentbus v0.2.0" --generate-notes
```

## 自动发布

`.github/workflows/release.yml` 在推送 `v*` tag 时运行：

1. `go test`、`test/setup.sh`、`test-npm.sh`；
2. 用 tag 版本运行 `scripts/build-release.sh`；
3. 上传产物到 GitHub Release；
4. 发布平台包，再发布 `agentbus-cli`；已存在的包版本会跳过（因此失败的运行
   可以重跑）。

```bash
git tag v0.2.0
git push origin v0.2.0
```

workflow 需要 `NPM_TOKEN` secret。它还用到 `id-token: write`，但仅用于仓库
权限，不用于 npm provenance；若想要 provenance 证明，在 `npm publish`
命令上加 `--provenance`（要求包在公开仓库中构建）。

## 发布之后

- `npm view agentbus-cli version` 和 `npm dist-tag ls agentbus-cli`。
- 在干净的机器/容器里安装：
  `npm i -g agentbus-cli && agentbus version && agentbus setup --dry-run`。
- 校验 GitHub 归档：`sha256sum -c SHA256SUMS`，解包其中一个，运行
  `./agentbus version`。
- 打 tag 前，把 `CHANGELOG.md` 的 `## [Unreleased]` 段改成已发布的版本并
  提交（tag 指向该提交）。
- 如果某个发布有问题，发一个 patch 版本。不要 unpublish：npm 版本不可变。
  必要时 deprecate 有问题的版本：
  `npm deprecate agentbus-cli@0.2.0 "broken binary; use 0.2.1"`。

## 检查清单

- [ ] `CHANGELOG.md` 在新版本下包含发布说明
- [ ] `go.mod` / README 的要求与 CI 使用的 Go 版本一致
- [ ] `npm/cli/LICENSE` 与仓库 `LICENSE` 完全一致
- [ ] `go test -race ./...`、`./test/setup.sh`、`./test/e2e.sh` 通过
- [ ] `./scripts/test-npm.sh` 通过
- [ ] 本地已验证 `./scripts/build-release.sh` 的产物
- [ ] tag 已推送、workflow 全绿、GitHub Release 含 4 个归档 + checksums
- [ ] npm 显示新的 `latest`
