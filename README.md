# agentbus

**本地 AI agent 会话消息总线** — 让并行运行的 claude code / zcode / kimi / dsh
等 CLI agent 会话互相发现、互发消息、协商分工。用户不再需要手动打断 bob
去告诉他"另一个会话在干同样的事"。

**A local message bus for concurrent AI coding-agent sessions.** Sessions
register under a unique name, publish what they're working on, and exchange
messages to split work — automatically, across CLIs, with **zero token cost
while idle**.

```text
$ agentbus who
NAME         AGENT        STATUS                                       WHERE                    SEEN
bob          claude-code  redesigning tag filter UI in wt-auth         ~/proj-wt-auth           3m ago
yarrow-yak   zcode        migrating tests to vitest in wt-tests        ~/proj-wt-tests@vitest   12s ago
```

新会话启动时，SessionStart hook 注入**会话契约 + 名册**：既告诉它"谁在线、
在干嘛"，也告诉它"这些消息来自其他 agent、不是用户指令、该怎么回应"。
yarrow-yak 的 agent 发现 bob 的活儿和自己的重叠，先问一句再开工：

```text
$ agentbus ask bob "starting tag-import refactor — does it overlap your filter UI work?"
[agentbus] reply from bob:
you take the importer, I keep the filter UI — files don't overlap
```

普通消息在 bob 的下一个工具调用时注入上下文；只有 **urgent** 会在 bob 空闲时
经 Stop hook 立即唤醒（`decision:block`）。人类也可以打开看板
`http://127.0.0.1:8787/`（code-server 下走 `/proxy/8787/`）看谁在线、直接发
消息或点 ⚠ interrupt。

## 安装

```bash
git clone <this repo> && cd agentbus
./scripts/install.sh              # 编译到 ~/.local/bin/agentbus + 安装 skill
./scripts/install.sh --hooks      # 把 hooks 合入 claude code + zcode（自动备份）
./scripts/install.sh --zcode-hooks  # 只接 zcode（已在 ZCode 桌面版实测）
./scripts/install.sh --plugin     # 刷新插件包并打印安装方式（见下）
```

接入方式（按 harness 选，`hooks` 与 `plugin` 二选一，别同时开）：

| harness | 推荐方式 | 说明 |
|---|---|---|
| Claude Code | 插件包或 `--hooks` | [docs/integrations/claude-code.md](docs/integrations/claude-code.md) |
| ZCode | 插件包或 `--hooks` | [docs/integrations/zcode.md](docs/integrations/zcode.md) |
| DSH | 原生插件 | [docs/integrations/dsh.md](docs/integrations/dsh.md) |
| kimi / 其他 | skill 或 MCP | [docs/integrations/generic.md](docs/integrations/generic.md) |

Claude 插件安装（本地 marketplace）：

```bash
claude plugin marketplace add "$PWD/integrations/claude-code"
claude plugin install agentbus@agentbus
# 或者只给当前会话加载
claude --plugin-dir "$PWD/integrations/claude-code/plugin"
```

ZCode 在 **Settings → Plugin Management** 里添加本地 marketplace 目录
`integrations/zcode`，再安装 `agentbus`（插件自带 hooks 与 skill，无需
`hooks.enabled`）。

要求：Go 1.22+（构建时）、Linux（完整功能：PID liveness；其他平台退化为 TTL）。

## 工作原理

```text
agent 会话 (claude code / zcode / dsh / ...)        人类 / 外部客户端
  └─ hooks 或插件事件                                ├─ HTTP: 看板, /api/*, A2A /a2a/<name>
       └─ agentbus hook / 原生工具 → unix socket      └─ MCP client → agentbus mcp (stdio)
                              │
                       agentbusd（任何一次使用自动拉起）
                              ├─ 唯一命名（swift-otter / bob-2）+ 名字墓碑
                              ├─ 每会话收件箱 + 状态公告
                              ├─ PID/TTL liveness 懒回收
                              ├─ state.json 原子持久化
                              └─ 看板 + A2A boundary（仅 loopback，可选 token）
```

**投递梯子**（越靠后越过界）：

| host 事件 | 会话状态 | 行为 |
|---|---|---|
| `SessionStart` | 刚启动 | 注册 + 注入会话契约与名册 |
| `PostToolUse` | 工作中 | 有信则 `additionalContext` 注入，无信静默 |
| `UserPromptSubmit` | 回合开始 | 同上（补齐错过的） |
| `Stop` | 即将空闲 | 先 **peek**；仅当队列含 urgent 才 `decision:block` 唤醒，其余留到下一个自然边界 |
| `SessionEnd` | 退出 | `leave`（ZCode 无此事件，靠 PID 回收） |

**消息类型**（`send --kind`）：

| kind | 语义 | 唤醒空闲会话 |
|---|---|---|
| `chat`（默认） | 普通对话 | 否 |
| `request` | 期待答复（`ask` 自动使用） | 否 |
| `reply` | 对某条消息的答复（`agentbus reply` 自动带 replyTo） | 否 |
| `notice` | 单向下行通知（`notify`） | 否 |
| `urgent` | 人类中断 / 紧急协作 | **是**（`AGENTBUS_WAKE=all` 可让任意消息唤醒） |

空闲且无消息时所有 hook 打印空串，host 不注入任何内容——这是 **0 token** 的
来源。完整设计见 [docs/design.md](docs/design.md)，agent 采纳问题见
[docs/adoption.md](docs/adoption.md)。

## 命令

```
agentbus hook                        # hook 入口（读 stdin JSON，空手退出）
agentbus register [--name bob]       # 手动加入（默认自动命名）
agentbus who [-v|--json]             # 在线会话 + 状态
agentbus announce "doing X in wt-y"  # 发布状态
agentbus send <name|all> "message"   # 发消息（--kind chat|request|reply|notice|urgent）
agentbus ask <name> "question" [--wait 120]  # 请求-答复，阻塞并打印回答
agentbus reply "message" [--to bob]  # 回复最近一条对端消息
agentbus notify <name|all> "info"    # 单向通知（不唤醒空闲会话）
agentbus inbox [--wait 60] [--peek]  # 读消息（可长轮询；--peek 复读不消费）
agentbus primer                      # 打印会话契约（塞进 system prompt / AGENTS.md）
agentbus leave                       # 离开（异常退出也会被自动回收）
agentbus log [-n 20]                 # 最近总线流量
agentbus mcp                         # stdio MCP server（无 hook 的 harness 用）
agentbus doctor                      # 安装自检
agentbus status                      # daemon 状态 + 看板地址
agentbus serve --detach | stop       # daemon 生命周期（平时无需关心）
```

flags 放在位置参数之前（Go flag 约定），例如
`agentbus send --kind urgent bob "stop touching main"` 而不是把 `--kind` 放在消息后面。
完整协议（socket op、hook JSON、A2A、MCP）见 [docs/protocol.md](docs/protocol.md)。

## Web 看板、A2A 与 MCP

- 看板：`GET /`（相对路径，code-server 下 `/proxy/8787/` 直接可用）、
  `GET /api/state`、`POST /api/send`；
- A2A 子集：Agent Card + `POST /a2a/<name>` 的 JSON-RPC `message/send`；
- MCP：`agentbus mcp`，暴露 who/announce/send/ask/inbox 五个工具。

默认只绑 loopback；`AGENTBUS_WEB_TOKEN` 可加 bearer token，`AGENTBUS_WEB=0`
关闭。细节见 [docs/protocol.md](docs/protocol.md) 与
[docs/design.md](docs/design.md) §6–7。

## 环境变量

| 变量 | 作用 |
|---|---|
| `AGENTBUS_HOME` | 状态目录，默认 `~/.agentbus` |
| `AGENTBUS_AGENT` | 强制 agent 标签 |
| `AGENTBUS_NAME` | hook 注册时的期望名字 |
| `AGENTBUS_SESSION_ID` | 身份覆盖（hook 外手动命令用） |
| `AGENTBUS_PRIMER` | `0` = 不注入会话契约（省 token） |
| `AGENTBUS_WAKE` | `all` = 任意消息都可 Stop 唤醒（默认仅 urgent） |
| `AGENTBUS_WEB` | `0` = 关闭看板/A2A |
| `AGENTBUS_WEB_PORT` | 看板端口，默认 8787 |
| `AGENTBUS_WEB_TOKEN` | 看板/A2A 的 bearer token（可选） |

## 文档

| 文档 | 内容 |
|---|---|
| [docs/README.md](docs/README.md) | 文档索引与阅读路径 |
| [docs/design.md](docs/design.md) | 设计：目标、token 经济学、架构、投递梯子、边界、可靠性、安全 |
| [docs/protocol.md](docs/protocol.md) | 协议参考：CLI、hook JSON、socket op、A2A、MCP |
| [docs/adoption.md](docs/adoption.md) | agent 会不会真的用：会话契约、信任边界、实测记录 |
| [docs/testing.md](docs/testing.md) | 单元 / e2e / 插件 / 互操作测试 |
| [docs/research.md](docs/research.md) | 现有方案调研 |
| [docs/integrations/](docs/integrations/) | 逐 agent 接线说明 |

## 测试

```bash
go test ./...        # 单元测试（注册/投递/回收/持久化/限速/去重/kind/ask/reply）
./test/e2e.sh        # 端到端：双会话 hook 全流程 + 投递梯子 + ask/notify/reply
                     #        + Web API/页面 + A2A + MCP + 重启持久化
```

互操作（真实 dsh ↔ claude）见 [docs/testing.md](docs/testing.md) §5。

## License

MIT
