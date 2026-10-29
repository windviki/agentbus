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
发现 bob 的活儿和自己的重叠，先发消息再开工：

```text
$ agentbus send bob "starting tag-import refactor - does it overlap your filter UI work?"
```

bob 正在工作中，下一个工具调用的 hook 就把消息注入它的上下文；如果 bob
已经空闲，Stop hook 会直接唤醒它。两者协商好分工，各自继续——全程无人值守。

## 安装

```bash
git clone <this repo> && cd agentbus
./scripts/install.sh            # 编译到 ~/.local/bin/agentbus + 安装 skill
./scripts/install.sh --hooks    # 同时把 hooks 合入 claude code / zcode 配置（自动备份）
```

然后按 [`integrations/`](integrations/) 里对应 agent 的说明接线
（claude code 和 zcode 只是往 settings 里加几行 hook；其他 agent 仅需 skill）。

要求：Go 1.22+（构建时）、Linux（完整功能：PID liveness；其他平台退化为 TTL）。

## 工作原理

```
agent 会话 (claude code / zcode / ...)
  └─ hooks: SessionStart / PostToolUse / UserPromptSubmit / Stop / SessionEnd
       └─ agentbus hook        ← 唯一 hook 入口；空收件箱 → 零输出 → 零 token
            └─ unix socket → agentbusd (自动拉起的常驻 daemon)
                             ├─ 唯一命名 (swift-otter / bob-2)
                             ├─ 每会话收件箱 + 状态公告
                             ├─ PID/TTL liveness + 名字墓碑
                             └─ state.json 原子持久化
```

四个投递时机：**开局名册**（谁在线、在干嘛）、**工作中注入**（PostToolUse）、
**回合开始补投**（UserPromptSubmit）、**空闲唤醒**（Stop + decision:block）。
细节、token 成本模型和防风暴设计见 [DESIGN.md](DESIGN.md)；
"为什么不直接用 MCP/A2A/现有项目"见 [RESEARCH.md](RESEARCH.md)。

## 命令

```
agentbus hook                        # hook 入口（读 stdin JSON，空手退出）
agentbus register [--name bob]       # 手动加入（默认自动命名）
agentbus who [-v]                    # 在线会话 + 状态
agentbus announce "doing X in wt-y"  # 发布状态
agentbus send <name|all> "message"   # 点对点 / 广播
agentbus inbox [--wait 60] [--peek]  # 读消息（可长轮询）
agentbus leave                       # 离开（异常退出也会被自动回收）
agentbus log [-n 20]                 # 最近总线流量
agentbus doctor                      # 安装自检
agentbus serve --detach | stop       # daemon 生命周期（平时无需关心）
```

环境变量：`AGENTBUS_HOME`（默认 `~/.agentbus`）、`AGENTBUS_AGENT`（强制 agent
标签）、`AGENTBUS_NAME`（hook 注册时的期望名字）、`AGENTBUS_SESSION_ID`（身份覆盖）。

## 测试

```bash
go test ./...        # 单元测试（注册/投递/回收/持久化/限速/去重）
./test/e2e.sh        # 端到端：双会话模拟 hook JSON 全流程
```

## License

MIT
