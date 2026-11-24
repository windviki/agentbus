# 发布 agentbus

一次不可变版本发布到三个地方：

1. **npm** — `@aixyzstudio/agentbus-cli` 与四个携带预编译二进制的 scoped
   平台包。
2. **GitHub Releases** — 各平台 `.tar.gz` 归档 + `SHA256SUMS`。
3. **git tag** — `v<version>`，CI 以此为构建基准。

`scripts/build-release.sh` 从同一份 checkout 产出所有产物，release workflow
运行同一脚本。

## 版本

- 使用 semver，并在 `CHANGELOG.md` 记录变更。
- `npm/cli/package.json` 与所有平台 manifest 版本一致。`build-release.sh`
  按 `VERSION` 重写它们并通过 Go `-ldflags` 注入；`internal/server.Version`
  只是开发兜底。
- tag 形如 `v0.2.0`。

## 一次性配置

- 具有 **`@aixyzstudio` scope** 发布权限的 npm token，覆盖
  `@aixyzstudio/agentbus-cli` 与
  `@aixyzstudio/agentbus-cli-{linux,darwin}-{x64,arm64}`。仅允许其他组织或
  `@windviki` 的 token 会返回 403。可用
  `npm token list --json --registry=https://registry.npmjs.org/` 检查。
- 仓库 secret `NPM_TOKEN` 需要同样权限。安装后的命令仍为 `agentbus`；裸名
  `agentbus` 是无关项目。
- GitHub 仓库 `windviki/agentbus`；迁移时同步更新仓库 URL。

## 本地发布预演

```bash
go test -race ./...
./test/setup.sh
./test/e2e.sh
./scripts/test-npm.sh

VERSION=0.2.0 ./scripts/build-release.sh
ls -lh dist/
#   aixyzstudio-agentbus-cli-0.2.0.tgz
#   aixyzstudio-agentbus-cli-linux-x64-0.2.0.tgz ...（4 个平台）
#   agentbus-0.2.0-linux-amd64.tar.gz ...（4 个归档）
#   SHA256SUMS

tmp=$(mktemp -d)
npm install -g --prefix "$tmp" \
  dist/aixyzstudio-agentbus-cli-linux-x64-0.2.0.tgz \
  dist/aixyzstudio-agentbus-cli-0.2.0.tgz
"$tmp/bin/agentbus" version
```

仅构建一个目标：

```bash
TARGETS="linux/amd64" OUT=/tmp/agentbus-dist ./scripts/build-release.sh
```

## 发布

**先发布平台包，再发布 CLI**。npm 安装时会解析 CLI 的 scoped
`optionalDependencies`。

镜像 registry（例如 `registry.npmmirror.com`）可以安装，但不一定接受当前凭证
发布；请显式指定 `--registry https://registry.npmjs.org/`。

```bash
for name in agentbus-cli-linux-x64 agentbus-cli-linux-arm64 \
            agentbus-cli-darwin-x64 agentbus-cli-darwin-arm64; do
  npm publish "dist/aixyzstudio-$name-0.2.0.tgz" \
    --access public --registry=https://registry.npmjs.org/
done
npm publish "dist/aixyzstudio-agentbus-cli-0.2.0.tgz" \
  --access public --registry=https://registry.npmjs.org/
```

需要时再创建 GitHub Release：

```bash
gh release create "v0.2.0" \
  dist/agentbus-*.tar.gz dist/SHA256SUMS \
  --title "agentbus v0.2.0" --generate-notes
```

## 自动发布

`.github/workflows/release.yml` 在推送 `v*` tag 时运行：测试、构建产物、上传
GitHub Release，再依次发布 scoped 平台包与 `@aixyzstudio/agentbus-cli`。已存在
的版本会跳过，因此失败后可重跑。

```bash
git tag v0.2.0
git push origin v0.2.0
```

workflow 需要上述 scoped `NPM_TOKEN`。`id-token: write` 用于仓库权限，不等于
npm provenance；需要 provenance 时另加 `--provenance`。

## 发布之后

```bash
npm view @aixyzstudio/agentbus-cli version --registry=https://registry.npmjs.org/
npm dist-tag ls @aixyzstudio/agentbus-cli --registry=https://registry.npmjs.org/
npm install -g @aixyzstudio/agentbus-cli
agentbus version
agentbus setup --dry-run
```

同时运行 `sha256sum -c SHA256SUMS`，并解包一个二进制验证。发布有问题时发 patch，
不要 unpublish；必要时 deprecate：

```bash
npm deprecate @aixyzstudio/agentbus-cli@0.2.0 "broken binary; use 0.2.1"
```

## 检查清单

- [ ] `CHANGELOG.md` 包含新版本
- [ ] npm token 具有 `@aixyzstudio` 发布权限
- [ ] `go test -race ./...`、`./test/setup.sh`、`./test/e2e.sh` 通过
- [ ] `./scripts/test-npm.sh` 通过
- [ ] `./scripts/build-release.sh` 产物与校验和已验证
- [ ] tag 已推送、workflow 全绿、GitHub Release 含 4 个归档 + checksums
- [ ] npm 显示新的 `latest`，干净安装可运行 `agentbus version`
