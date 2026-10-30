# DSH（DeepSeek Harness）集成

DSH 是一个基于 Cordis 的 harness：插件是 Node 包，可以挂接 agent 生命周期
事件并注册工具。内置插件
[`integrations/dsh/plugin`](../../../integrations/dsh/plugin) 是同一个 daemon 的
瘦客户端 —— 它不重新实现总线，而是调用 `agentbus hook` / CLI，把结果映射到
DSH 的原生事件上。跨 CLI 协调依然成立，因为 daemon 位于任何单一 harness 之外。

## 映射关系

| DSH 事件 | agentbus 等价物 | 效果 |
|---|---|---|
| `agent/created` | `agentbus hook` SessionStart | 注册会话；以用户消息（`agent.inject`）注入会话契约 + **按工作区分组的名册**（同一工作区 / 同一仓库） |
| `agent/pre-step` | UserPromptSubmit | 在下一个步骤边界投递排队消息 |
| `tools/post-execute` | PostToolUse | 在同一步骤内、工具调用之后立即投递消息 |
| `agent/turn-stopping` | Stop | 若有 **urgent** 消息或直接发给本会话的 request 待处理，就对会话执行 `agent.steer`，让回合继续；chat/notice/广播 request 继续排队 |
| `agent/disposed` | `leave` | 将会话从总线上移除 |

注册的原生工具：`agentbus_who`、`agentbus_announce`、`agentbus_send`、
`agentbus_ask`、`agentbus_reply`、`agentbus_inbox`、`agentbus_primer`。

每次 spawn 都有超时上界和防护：总线/daemon 的问题绝不会从插件回调中抛出
（未捕获的异常会拖垮整个 Host）。

## 安装

插件需要：
- `agentbus` 在 `PATH` 上，或设置 `AGENTBUS_BIN=/abs/path/to/agentbus`；
- 与你想触达的对端使用相同的 `AGENTBUS_HOME`（默认 `~/.agentbus`）。

### A. 自动化（推荐）

```bash
npm install -g agentbus-cli
agentbus setup --agents dsh
```

源码 checkout 请使用 `./scripts/install.sh --agents dsh`。setup 会把内嵌插件
解压到 `~/.agentbus/integrations/dsh-plugin`，并在 `dsh` 位于 `PATH` 上时
运行 `dsh plugin --profile web add <that path>`。该包声明了自己的
bundle patch，因此 DSH 会自行插入 loader 行；之后重启 host（或依赖配置
HMR）。如果 `dsh` 不在 `PATH` 上，setup 会打印需要手动运行的准确命令。

### B. 本地 checkout（已验证路径）

编写一个 patch overlay 并传给 DSH 启动器：

```yaml
# /tmp/agentbus.patch.yml
- insert:
    - id: agentbus
      name: /abs/path/to/agentbus/integrations/dsh/plugin/lib/index.js
```

```bash
dsh headless --patch /tmp/agentbus.patch.yml --dump-config | grep -A1 agentbus
dsh headless --patch /tmp/agentbus.patch.yml "Call agentbus_who and show its output"
```

对于 web profile，把同一个 `insert` 行追加到
`~/.dsh/profiles/web/cordis.patch.yml` 并重启 host（或依赖配置 HMR）。
loader 接受绝对路径 / file URL 作为 `insert.name`。

### C. 以包的形式安装

```bash
dsh plugin --profile web add /abs/path/to/agentbus/integrations/dsh/plugin
```

然后添加 loader 行（或让 bundle patch 来完成）：

```yaml
# ~/.dsh/profiles/web/cordis.patch.yml
- insert:
    - id: agentbus
      name: dsh-agentbus
```

该包声明了 `dsh.engines.dsh` 与 `dsh.bundle.patch`；它有意暂时没有 config
schema —— 通过环境变量配置。

## 验证

1. `dsh headless --patch … --dump-config` 会显示 `agentbus` 行。
2. 调用 `agentbus_who` 的 prompt 会返回在线会话表（证明工具 + daemon
   连通）。
3. 第二个会话发送一个 `request`；插件会注入它，模型可以用 `agentbus_reply`
   回答（见互操作测试与 [adoption.md](../adoption.md) §6 中的实测结果）。
4. `agentbus log` 会显示该 reply，`kind=reply`，且 `replyTo` 指向原 request。

## 限制

- DSH 插件只能看到**它自己的 Host 进程**。另一个终端、另一个
  `dsh headless`、Claude Code 或 ZCode 都是独立进程 —— 它们在 daemon 上
  会合，这也是插件是客户端而非总线本身的原因。
- 唤醒路径：`agent/turn-stopping` 覆盖回合关闭之前到达的消息；空闲轮询器
  覆盖会话已经停在 prompt 之后才到达的消息。当 agent 处于 `idle` 时，插件
  每 5s 调用一次 `agentbus hook Stop`（`AGENTBUS_DSH_POLL_MS`，默认 5000，
  0 表示关闭）；当 Stop 策略因 urgent 消息或定向 request 返回
  `decision:block` 时，调用 `agent.steer()` —— idle 驱动会开启一个回合，
  于是 request 会被回答，而不是等人类输入。消息在 steer 之前已 ack，因此
  不会重复投递。
- 纯 hook 的 harness（ZCode、Claude Code）没有等价的宿主唤醒 API：在它们
  已经 stop 之后发送的 request 仍要等到下一次 prompt 或 tool 调用。看板会
  把这些显示为 pending request。
- headless 运行需要模型凭据（`DEEPSEEK_API_KEY` 或 web 凭据存储）；插件
  本身不需要凭据。
- 如果 host 没有可用的 sandbox 后端，headless bash 会失败并提示
  `sandbox mode workspace-write ... no sandbox backend is usable`。用
  `DSH_PERMISSION_MODE=danger-full-access` 运行测试 host（或配置一个
  sandbox）；这是 harness 的环境设置，不是 agentbus 的。
- 插件是 ESM，并以构建后的 JS 加载；本地 checkout 无需构建步骤。
