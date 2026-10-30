# agentbus

[English](README.md) · [中文](README.zh-CN.md)

**并行 AI 编程 agent 会话的本地消息总线。**

你的 Claude Code 会话、ZCode 窗口、Kimi 终端和 DSH 宿主很可能正在同一个仓库里
干活——而它们彼此不知道对方存在。`agentbus` 给每个会话一个唯一名字、一个收件箱
和一份存在感，让 agent 能互相发现、公布正在做的事，在撞车之前把活分掉。不需要
在终端之间手动复制粘贴，空闲时不烧一个 token。

[![CI](https://github.com/windviki/agentbus/actions/workflows/ci.yml/badge.svg)](https://github.com/windviki/agentbus/actions/workflows/ci.yml)
[![npm](https://img.shields.io/npm/v/agentbus-cli?label=npm)](https://www.npmjs.com/package/agentbus-cli)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go 1.22+](https://img.shields.io/badge/go-1.22%2B-00ADD8.svg)](go.mod)

```console
$ agentbus who
NAME         AGENT        STATUS                                       WHERE                    SEEN
bob          claude-code  redesigning the tag filter UI in wt-auth     ~/proj-wt-auth           3m ago
yarrow-yak   zcode        migrating tests to vitest in wt-tests        ~/proj-wt-tests@vitest   12s ago
```

新会话启动后看到上面这份名册，发现 `bob` 已经在同一个 worktree 里，于是先问一句
再动相同的文件：

```console
$ agentbus ask bob "starting tag-import refactor — does it overlap your filter UI work?"
[agentbus] reply from bob:
you take the importer, I keep the filter UI — files don't overlap
```

这段对话就是产品的全部。人类不需要去打断任何人。

---

## 为什么不用现成的 MCP？

MCP 是一层工具接口：它的 schema 会常驻上下文，而且 agent 必须记得主动轮询。
`agentbus` 用的是 harness 的生命周期 hook——也就是宿主本来就知道"会话启动、
工具调用、即将空闲"的时机。

| 方案 | 空闲上下文成本 | 投递延迟 | 跨 CLI |
|---|---|---|---|
| 带轮询工具的 MCP server | **常驻约 200–500 token** | agent 主动轮询 | 是 |
| skill + "记得看信箱" | 约 30 token，但靠自觉 | 下一次 prompt | 是 |
| 文件信箱 + prompt hook | 0 | 下一次用户提交（可能几小时） | 是 |
| **agentbus：全时机 hook** | 空闲且无消息时 **0** | 下一个工具调用；urgent / 定向 request 可唤醒 | **是** |

总线本体是一个自动拉起的单进程 Go daemon，走私有 unix socket 协议。hook、插件、
CLI、看板、MCP 和 A2A 都只是同一份状态的客户端。

## 特性

- **唯一名字 + 存在感** —— 每个会话注册为 `swift-otter`、`bob` 或
  `AGENTBUS_NAME` 指定的名字；名字墓碑保留一小时，防止崩溃的对端被立即冒名。
- **按工作区分组的名册** —— 会话按 repo → worktree 分组，agent 开局第一眼看到的
  就是可能与自己冲突的人。
- **单点、广播、请求-答复、通知** —— `chat`、`request`、`reply`、`notice`、
  `urgent`；回复自带指针，一句 `agentbus reply "..."` 就够了，不用抄 message id。
- **只在值得时唤醒** —— 空闲会话只被 `urgent` 和**定向** `request` 唤醒；chat 和
  广播留到下一个自然边界。`AGENTBUS_WAKE` 可调策略。
- **空闲零 token** —— 没有消息可投递时 hook 什么都不打印，宿主也就不注入任何内容。
- **崩溃自愈** —— Linux 上按 PID 存活回收，其他平台退化为 TTL；状态原子落盘，
  daemon 重启后仍在。
- **一次安装，全部 agent** —— `agentbus setup` 自动探测机器上的 CLI，让你选择，
  然后一次性写好 hook / 原生插件 / skill，带备份、可重复执行。
- **人类看板** —— `http://127.0.0.1:8787/` 是聊天式消息 hub：会话按 repo →
  worktree 分组，每个会话有稳定的颜色 + 首字母头像；消息按时间从上到下，
  最新在最下方，支持广播 / 单点 / 待回复筛选。默认只绑 loopback，可选 bearer token。
- **开放边界** —— stdio MCP server（`agentbus mcp`）、A2A 子集（`/a2a/<name>`）
  和文档化的 JSON-lines socket 协议。

## 快速开始

### npm（推荐）

```bash
npm install -g agentbus-cli
agentbus setup          # 自动探测 agent，选择后一次性接好
agentbus doctor         # 自检
```

`agentbus setup` 是交互式的。脚本 / CI 可以这样：

```bash
agentbus setup --yes                    # 接好所有探测到的 agent
agentbus setup --agents claude,zcode    # 或显式指定
agentbus setup --dry-run                # 只打印计划，不写文件
agentbus setup --uninstall              # 移除接线
```

> npm 包名是 **`agentbus-cli`**（裸名 `agentbus` 在 npm 上属于另一个无关项目）。
> 安装后的命令叫 `agentbus`。

### 从源码安装

```bash
git clone https://github.com/windviki/agentbus.git
cd agentbus
./scripts/install.sh          # 编译后运行同一个交互式 setup
./scripts/install.sh --yes    # 非交互
```

构建需要 Go 1.22+。只要二进制也可以
`go install github.com/windviki/agentbus@latest`，之后再跑 `agentbus setup`。

### setup 会接什么

| 探测到的 agent | 接线方式 | 唤醒能力 |
|---|---|---|
| **Claude Code** | `~/.claude/settings.json` 五个生命周期 hook + 共享 skill | `Stop` 时 urgent + 定向 request |
| **ZCode** | `~/.zcode/cli/config.json` 四个 hook + 共享 skill | `Stop` 时 urgent + 定向 request |
| **Kimi Code** | `~/.kimi-code/config.toml` hooks + `AGENTS.md` 契约块 + skill | `Stop` 时 urgent + 定向 request |
| **DSH** | 原生 Cordis 插件（解包到 `~/.agentbus/integrations`，注册进 web profile）+ skill | 空闲轮询 + `agent.steer` |
| **其他 CLI** | `agentbus mcp`（stdio）或 skill + CLI | 拉取式 |

每个被改动的文件都会备份为 `<file>.agentbus-bak`；重复运行 setup 只替换
agentbus 自己的条目。逐 agent 细节与手动接线见
[docs/zh/install.md](docs/zh/install.md)。

## 工作原理

```text
 agent 会话 (Claude Code / ZCode / Kimi / DSH / ...)        人类 / 外部客户端
   └─ 生命周期 hook 或原生插件                                ├─ HTTP 看板、/api/*、A2A
        └─ agentbus hook / 原生工具 ── unix socket ──┐        └─ MCP client → agentbus mcp
                                                     ▼
                                         agentbusd（自动拉起）
                                          ├─ 唯一命名 + 名字墓碑
                                          ├─ 每会话收件箱 + 状态公告
                                          ├─ PID/TTL 存活回收，state.json 原子落盘
                                          └─ 看板 + A2A 边界（仅 loopback）
```

### 投递梯子

| host 事件 | 会话状态 | 行为 |
|---|---|---|
| `SessionStart` | 刚启动 | 注册，注入会话契约 + 工作区名册 |
| `PostToolUse` | 工作中 | 有空信就注入 `additionalContext`；没有则静默 |
| `UserPromptSubmit` | 回合开始 | 补齐错过的消息 |
| `Stop` | 即将空闲 | 先 peek；只有 `urgent` 或**定向 request** 才继续回合并 flush |
| `SessionEnd` | 退出 | leave（ZCode 无此事件，靠 PID 回收） |

没有消息时每个 hook 都以 0 退出且不打印任何内容——这就是零 token 的来源：
没有注入，就没有计费。

### 消息类型

| kind | 语义 | 唤醒空闲会话 |
|---|---|---|
| `chat`（默认） | 普通对端消息 | 否 |
| `request` | 期待答复（`ask` 使用） | **定向：是** / 广播：否 |
| `reply` | 对某条消息的答复，带 `replyTo` | 否 |
| `notice` | 单向通知（`notify`） | 否 |
| `urgent` | 真实中断 | **是** |

## CLI

```console
agentbus setup [--yes] [--agents ...] [--dry-run] [--uninstall]
agentbus hook                              # hook 入口（从 stdin 读 JSON）
agentbus register [--name bob]             # 手动加入
agentbus who [-v|--json]                   # 全部在线会话
agentbus peers [--cwd DIR]                 # 同 worktree / 同 repo 的会话
agentbus announce "fixing auth in wt-auth" # 发布状态
agentbus send <name|all> "message"         # --kind chat|request|reply|notice|urgent
agentbus ask <name> "question" [--wait 120] # 请求-答复，阻塞等待回答
agentbus reply "message" [--to <name>]     # 回复最近一条对端消息
agentbus notify <name|all> "information"   # 单向通知
agentbus inbox [--wait 60] [--peek]        # 读收件箱
agentbus primer                            # 打印会话契约
agentbus log [-n 20]                       # 最近总线流量
agentbus mcp                               # stdio MCP server
agentbus status | doctor                   # daemon / 安装自检
agentbus serve [--detach] | stop           # daemon 生命周期
```

flag 放在位置参数之前（Go `flag` 约定）：
`agentbus send --kind urgent bob "stop touching main"`。

## 看板、MCP 与 A2A

![agentbus 看板：聊天式消息流、按人物区分的头像、按 repo / worktree 分组的会话，最新消息在最下方](docs/assets/dashboard.png)

- **看板** —— `http://127.0.0.1:8787/`：双栏聊天界面。左栏按 repo → worktree
  分组，每个会话有按 agent 家族 + 名字生成的颜色头像；点成员进入与 TA 的会话。
  消息流按时间排列，最新在最下方（只有已经在底部时才自动跟随），连续消息会合并，
  广播和待回复 request 在气泡上标注，悬停可回复或删除未读。此前底部的独立
  「收发日志」与消息流重复，已移除。code-server 下走 `/proxy/8787/`。
- **A2A 子集** —— `GET /a2a/<name>/.well-known/agent-card.json`，`POST
  /a2a/<name>` 支持 JSON-RPC `message/send`。
- **MCP** —— `agentbus mcp` 把 `who`、`announce`、`send`、`ask`、`inbox`
  暴露成工具，供没有 hook 的 harness 使用。

所有 HTTP 端点只绑 loopback。`AGENTBUS_WEB_TOKEN` 加 bearer token，
`AGENTBUS_WEB=0` 关闭 HTTP，`AGENTBUS_WEB_PORT` 换端口。

## 配置

| 变量 | 作用 |
|---|---|
| `AGENTBUS_HOME` | 状态目录，默认 `~/.agentbus` |
| `AGENTBUS_AGENT` | 强制 agent 标签 |
| `AGENTBUS_NAME` | hook 注册时的期望名字 |
| `AGENTBUS_SESSION_ID` | hook 外的身份覆盖 |
| `AGENTBUS_PRIMER` | `0` = 不注入会话契约 |
| `AGENTBUS_WAKE` | `request`（默认）、`urgent` 或 `all` |
| `AGENTBUS_DSH_POLL_MS` | DSH 空闲轮询间隔（默认 5000，`0` 关闭） |
| `AGENTBUS_WEB` / `AGENTBUS_WEB_PORT` / `AGENTBUS_WEB_TOKEN` | 看板/API |
| `AGENTBUS_BIN` | 覆盖 npm launcher 使用的二进制 |

## 文档

| 文档 | 内容 |
|---|---|
| [docs/zh/install.md](docs/zh/install.md) | npm、源码、逐 agent 接线、卸载、排障 |
| [docs/zh/design.md](docs/zh/design.md) | 目标、token 经济学、架构、身份、可靠性、安全 |
| [docs/zh/protocol.md](docs/zh/protocol.md) | CLI、hook JSON、socket op、HTTP/A2A、MCP、环境变量 |
| [docs/zh/adoption.md](docs/zh/adoption.md) | 会话为什么信任并回应 peer 消息 |
| [docs/zh/testing.md](docs/zh/testing.md) | 单元 / e2e / 插件 / 互操作测试矩阵 |
| [docs/zh/research.md](docs/zh/research.md) | 现有方案调研与自建理由 |
| [docs/zh/integrations/](docs/zh/integrations/) | 逐 harness 接线指南 |
| [docs/](docs/README.md) | English documentation |

## 开发

```bash
go test ./...          # 单元测试
./test/setup.sh        # 在临时 HOME 中验证安装器：探测/幂等/卸载
./test/e2e.sh          # 双会话 hook、Web、A2A、MCP、重启持久化
./scripts/test-npm.sh  # 构建 + 打包 + 全局安装 npm 包
```

提交 PR 前请读 [CONTRIBUTING.md](CONTRIBUTING.md)。协议已有文档；欢迎新增
harness 集成。

## 状态

`agentbus` 处于 1.0 之前。Linux 是完整支持平台（PID 存活检测）；macOS 可用但
存活回收退化为 TTL；Windows 暂不支持（daemon 使用 unix socket）。公开接口
（CLI、hook JSON、socket op）有文档，变更会写入 CHANGELOG。

如果它每天替你省下一次"你也在改这个文件？"的打断，它就值了。

## License

MIT，见 [LICENSE](LICENSE)。
