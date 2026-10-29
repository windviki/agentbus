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

新会话启动时，SessionStart hook 注入名册（上面两行）。yarrow-yak 的 agent
发现 bob 的活儿和自己的重叠，先问一句再开工：

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
./scripts/install.sh --hooks      # 同时把 hooks 合入 claude code / zcode 配置（自动备份）
./scripts/install.sh --plugin     # 刷新插件包并打印安装方式（见下）
```

三种接法（任选其一，`hooks` 与 `plugin` 二选一，别同时开）：

| 方式 | 适合 | 手动配置 |
|---|---|---|
| skill only | kimi / dsh 等无 hook 的 harness | 无（agent 主动调用 CLI 或走 MCP） |
| `--hooks` | 已装 claude code / zcode，想用现有 settings | 写 `settings.json` / `config.json` |
| `--plugin` | 想一个包带走 hooks + skill | Claude: 本地 marketplace / `--plugin-dir`；ZCode: 插件管理 |

插件包装好后：

```bash
# claude code：装一次（本地 marketplace）
claude plugin marketplace add "$PWD/integrations/claude-code"
claude plugin install agentbus@agentbus
# 或者只给当前会话加载
claude --plugin-dir "$PWD/integrations/claude-code/plugin"
```

ZCode 在 **Settings → Plugin Management** 里添加本地 marketplace 目录
`integrations/zcode`，再安装 `agentbus`。插件自带 hooks 与 skill，无需
`hooks.enabled`。

要求：Go 1.22+（构建时）、Linux（完整功能：PID liveness；其他平台退化为 TTL）。

## 工作原理

```text
agent 会话 (claude code / zcode / ...)            人类 / 外部客户端
  └─ hooks: SessionStart / PostToolUse /           ├─ HTTP: 看板, /api/*, A2A /a2a/<name>
       UserPromptSubmit / Stop / SessionEnd        └─ MCP client → agentbus mcp (stdio)
       └─ agentbus hook → unix socket (~/.agentbus/bus.sock, 0600)
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
| `SessionStart` | 刚启动 | 注册 + 注入名册（谁在线、在干嘛） |
| `PostToolUse` | 工作中 | 有信则 `additionalContext` 注入，无信静默 |
| `UserPromptSubmit` | 回合开始 | 同上（补齐错过的） |
| `Stop` | 即将空闲 | 先 **peek**；仅当队列含 urgent 才 `decision:block` 唤醒，其余留到下一个自然边界 |
| `SessionEnd` | 退出 | `leave`（ZCode 无此事件，靠 PID 回收） |

**消息类型**（`send --kind`）：

| kind | 语义 | 唤醒空闲会话 |
|---|---|---|
| `chat`（默认） | 普通对话 | 否 |
| `request` | 期待答复（`ask` 自动使用） | 否 |
| `reply` | 对 request 的答复，带 `--reply-to <messageId>` | 否 |
| `notice` | 单向下行通知（`notify`） | 否 |
| `urgent` | 人类中断 / 紧急协作 | **是**（`AGENTBUS_WAKE=all` 可让任意消息唤醒） |

空闲且无消息时所有 hook 打印空串，host 不注入任何内容——这是 **0 token** 的
来源。token 成本模型、防风暴与失败模式见 [DESIGN.md](DESIGN.md)；
"为什么不直接用 MCP/A2A/现有项目"见 [RESEARCH.md](RESEARCH.md)。

## 命令

```
agentbus hook                        # hook 入口（读 stdin JSON，空手退出）
agentbus register [--name bob]       # 手动加入（默认自动命名）
agentbus who [-v|--json]             # 在线会话 + 状态
agentbus announce "doing X in wt-y"  # 发布状态
agentbus send <name|all> "message"   # 发消息（--kind chat|request|reply|notice|urgent）
agentbus ask <name> "question" [--wait 120]  # 请求-答复，阻塞并打印回答
agentbus notify <name|all> "info"    # 单向通知（不唤醒空闲会话）
agentbus inbox [--wait 60] [--peek]  # 读消息（可长轮询；--peek 复读不消费）
agentbus leave                       # 离开（异常退出也会被自动回收）
agentbus log [-n 20]                 # 最近总线流量
agentbus mcp                         # stdio MCP server（无 hook 的 harness 用）
agentbus doctor                      # 安装自检
agentbus status                      # daemon 状态 + 看板地址
agentbus serve --detach | stop       # daemon 生命周期（平时无需关心）
```

flags 放在位置参数之前（Go flag 约定），例如
`agentbus send --kind urgent bob "stop touching main"` 而不是把 `--kind` 放在消息后面。

## Web 看板与 A2A

daemon 起来后默认监听 `127.0.0.1:8787`：

- `GET /` — 看板：在线会话、消息流、会话选择 + kind 下拉、⚠ interrupt 按钮；
  页面内全部用相对路径，code-server 下 `https://.../proxy/8787/` 直接可用；
- `GET /api/state` — 会话 + 最近消息 JSON；
- `POST /api/send` — `{"to":"bob","body":"...","kind":"chat","from":"user-web"}`；
- `GET /.well-known/agent-card.json` 与 `GET /a2a/<name>/.well-known/agent-card.json`
  — A2A Agent Card；
- `POST /a2a/<name>` — A2A JSON-RPC 2.0 `message/send`（文本 parts，
  `metadata.agentbus.kind` 可指定消息类型；不支持的方法返回 `-32601`）。

安全：默认只绑 loopback。设置 `AGENTBUS_WEB_TOKEN` 后，请求必须带
`Authorization: Bearer <token>` / `X-Agentbus-Token` / `?token=`；设
`AGENTBUS_WEB=0` 可整体关闭。看板地址也会出现在 `agentbus status` 里。

## MCP（无 hook 的 harness）

kimi / dsh 这类只支持 MCP 工具的 harness，把 server 指到：

```json
{ "mcpServers": { "agentbus": { "command": "agentbus", "args": ["mcp"] } } }
```

暴露 `agentbus_who` / `agentbus_announce` / `agentbus_send` / `agentbus_ask` /
`agentbus_inbox`，身份通过 `/proc` 祖先进程归属，同一 agent 进程内多次调用
共用同一个信箱。注意：MCP schema 常驻上下文有 token 成本，能接 hook 的 agent
优先用 hook。

## 环境变量

| 变量 | 作用 |
|---|---|
| `AGENTBUS_HOME` | 状态目录，默认 `~/.agentbus` |
| `AGENTBUS_AGENT` | 强制 agent 标签 |
| `AGENTBUS_NAME` | hook 注册时的期望名字 |
| `AGENTBUS_SESSION_ID` | 身份覆盖（hook 外手动命令用） |
| `AGENTBUS_WAKE` | `all` = 任意消息都可 Stop 唤醒（默认仅 urgent） |
| `AGENTBUS_WEB` | `0` = 关闭看板/A2A |
| `AGENTBUS_WEB_PORT` | 看板端口，默认 8787 |
| `AGENTBUS_WEB_TOKEN` | 看板/A2A 的 bearer token（可选） |

## 测试

```bash
go test ./...        # 单元测试（注册/投递/回收/持久化/限速/去重）
./test/e2e.sh        # 端到端：双会话 hook 全流程 + 投递梯子 + ask/notify
                     #        + Web API/页面 + A2A + MCP + 重启持久化
```

## License

MIT
