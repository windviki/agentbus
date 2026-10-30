# 安装

`agentbus` 以单个静态 Go 二进制加一步 setup 分发。关键在 setup 这一步：它检测本机
安装了哪些 AI 编码 CLI，让你选择要接入哪些，然后写入各自的 hooks、原生插件或
skill。

三个受支持的入口，结果相同：

| 入口 | 命令 | 适合 |
|---|---|---|
| npm | `npm install -g agentbus-cli`，然后 `agentbus setup` | 大多数用户；无需 Go 工具链 |
| 源码 | `git clone`，然后 `./scripts/install.sh` | 开发、离线机器、预发布测试 |
| Go | `go install github.com/windviki/agentbus@latest`，然后 `agentbus setup` | 只需要二进制的 Go 用户 |

> npm 包名是 **`agentbus-cli`**。npm 上裸的 `agentbus` 名字属于一个无关项目，
> 所以 `npm install -g agentbus` 会装错包。二进制在任何情况下都叫 `agentbus`。

## 环境要求

- **操作系统**：Linux（完整支持：基于 PID 的 liveness）或 macOS（unix socket，
  基于 TTL 的 liveness）。暂不支持 Windows。
- **Node.js 18+** 用于 npm 包；CLI 本身是 Go 二进制。
- **Go 1.22+** 仅在从源码构建时需要。
- 无需数据库，也无需配置服务。daemon 首次使用时启动，监听 `~/.agentbus` 下的
  私有 unix socket。

## npm

```bash
npm install -g agentbus-cli
agentbus setup
```

`agentbus setup` 打印检测到的结果，询问要给哪些 agent 接线，然后应用改动。在你
回答之前不会写入任何内容。

npm 包刻意**不**在 `postinstall` 里自动跑 setup：postinstall 里的交互会破坏
CI、可能早于 npm bin 链接就绪，而且很多包管理器会隐藏它的输出。显式执行一次
`agentbus setup` 就是那个"一键"步骤；`npm install -g` 之后二进制已经装好，
setup 只负责接线其他 agent。

更新与移除：

```bash
npm install -g agentbus-cli@latest
agentbus setup --yes               # refresh hooks after an upgrade
agentbus setup --uninstall         # remove the wiring (state is kept)
npm uninstall -g agentbus-cli
```

## 从源码安装

```bash
git clone https://github.com/windviki/agentbus.git
cd agentbus
./scripts/install.sh               # build to ~/.local/bin, then run setup
```

脚本接受同样的 setup 参数，另加几个便捷别名：

```bash
./scripts/install.sh --yes
./scripts/install.sh --agents claude,zcode
./scripts/install.sh --hooks            # = --agents all
./scripts/install.sh --claude-hooks     # = --agents claude
./scripts/install.sh --zcode-hooks      # = --agents zcode
./scripts/install.sh --kimi-hooks       # = --agents kimi
./scripts/install.sh --dsh-plugin       # = --agents dsh
./scripts/install.sh --dry-run
./scripts/install.sh --uninstall
./scripts/install.sh --plugin           # repo plugin bundles only
```

设置 `AGENTBUS_BIN_DIR` 可以安装到 `~/.local/bin` 之外的位置：

```bash
AGENTBUS_BIN_DIR="$HOME/bin" ./scripts/install.sh
```

## `agentbus setup` 做了什么

1. **检测** Claude Code、ZCode、Kimi Code、DSH 以及通用 skill 支持：检查配置目录
   与 `PATH`。
2. **询问**要给哪些检测到的 agent 接线（或接受 `--yes` / `--agents`）。
3. **安装 skill** 到 `~/.agents/skills/agentbus/SKILL.md`（Claude Code、ZCode 与
   DSH 共用），以及已存在的各 agent 专属 skill 目录。
4. 为选中的 agent **接线 hooks 或原生插件**。
5. 把它碰过的每个文件**备份**一次，命名为 `<file>.agentbus-bak`。

它是幂等的：再跑一次只替换 agentbus 自己写入的条目，其他内容（别的 hooks、env
块、Kimi 文件里的注释）原样保留。这也意味着升级之后可以安全重跑。

### 参数

| 参数 | 作用 |
|---|---|
| *（无）* | 交互式选择；默认选中所有检测到的 agent |
| `--yes`、`-y` | 非交互，为所有检测到的 agent 接线 |
| `--agents <list>` | 逗号列表：`claude,zcode,kimi,dsh`，或 `all` / `none` |
| `--dry-run` | 打印计划；不写入任何内容（可与 `--uninstall` 组合预览移除） |
| `--uninstall` | 移除 agentbus 的 hooks、配置块、插件与 skill |
| `--no-skill` | 跳过 skill 安装 |
| `--json` | 机器可读报告（隐含 `--yes`） |

### 各 agent 的接线

| Agent | 文件 | 说明 |
|---|---|---|
| Claude Code | `~/.claude/settings.json` | SessionStart、UserPromptSubmit、PostToolUse、Stop、SessionEnd → `agentbus hook` |
| ZCode | `~/.zcode/cli/config.json` | 除 SessionEnd 外同样的事件；设置 `hooks.enabled: true` |
| Kimi Code | `~/.kimi-code/config.toml`、`~/.kimi-code/AGENTS.md`、skill | 托管 TOML 块位于 `# >>> agentbus` / `# <<< agentbus` 之间；会话契约位于 `<!-- agentbus:begin -->` 标记之间 |
| DSH | `~/.agentbus/integrations/dsh-plugin`、DSH web profile | 解出原生 Cordis 插件，并在 `dsh` 位于 `PATH` 上时用 `dsh plugin --profile web add` 注册；否则打印手动命令 |
| 其他任意 CLI | skill / MCP | `agentbus mcp` 或加载 skill；见 [integrations/generic.md](integrations/generic.md) |

每个 agent 的手动路径仍保留在集成指南中（[Claude Code](integrations/claude-code.md)、
[ZCode](integrations/zcode.md)、[Kimi](integrations/kimi.md)、
[DSH](integrations/dsh.md)）。

## 验证

```bash
agentbus doctor
```

健康的安装会报告二进制、总线 home、可达的 daemon、web 看板（除非禁用）、已接线的
agent、skill 以及 PATH 解析结果。hooks 按名字调用 `agentbus`，所以二进制目录必须
在启动 agent 的那些进程的 `PATH` 上。

最快的端到端检查是用两个终端：

```bash
# terminal 1
agentbus register --name alice --agent claude
agentbus inbox --wait 30

# terminal 2
agentbus who
agentbus send alice "hello from bob"
```

在真实会话中，setup 之后请新开一个 agent 会话；已经在跑的会话不会重新加载它们的
hook 配置。

## 卸载

```bash
agentbus setup --uninstall
```

这会移除 agentbus 自己写入的 hooks、Kimi TOML 块、`AGENTS.md` 契约、解出的 DSH
插件以及各份 skill 拷贝。它**不会**删除 `~/.agentbus`（消息历史与配置）、二进制，
也不会删除你 agent 配置文件里任何不是 agentbus 写入的内容。想要一台干净的机器，
请另行删除这些：

```bash
rm -rf ~/.agentbus
npm uninstall -g agentbus-cli     # or: rm ~/.local/bin/agentbus
```

被修改过的文件会备份为 `*.agentbus-bak`；用
`cp <file>.agentbus-bak <file>` 恢复。

## 排障

| 症状 | 修复 |
|---|---|
| `npm i -g` 之后 `agentbus: command not found` | npm 全局 prefix 不在 `PATH` 上；运行 `npm prefix -g`，把 `<prefix>/bin` 加入 shell profile，然后新开一个 shell |
| hooks 从不投递消息 | `agentbus doctor`；二进制必须在 agent 进程的 `PATH` 上，而不只是你的交互 shell |
| 某个 CLI 未被检测到 | 先运行它一次以创建其配置目录，或强制指定：`agentbus setup --agents claude` |
| 看板端口 8787 被占用 | `AGENTBUS_WEB_PORT=8788 agentbus status`（daemon 在启动时读取环境变量；先执行 `agentbus stop`） |
| 消息收到两次 | 插件与 hooks 同时生效；二选一——`agentbus setup` 用 hooks，插件包是另一种方案 |
| `dsh plugin add` 失败 | 插件已经解出；手动运行打印出的 `dsh plugin --profile web add <path>`，或按 [integrations/dsh.md](integrations/dsh.md) 添加 loader 行 |
| macOS 崩溃后会话残留 | macOS 上 liveness 基于 TTL；`agentbus leave` 或等待 TTL 过期，或 `agentbus stop` 重置 daemon |

各 agent 的更多排障见 [集成指南](integrations/)。
