# DSH（DeepSeek Harness）集成

原生 Cordis 插件把 DSH 生命周期事件映射到共享 daemon，并为每个存活 agent 启动一个
长轮询 watcher，不另建总线。

## 事件映射

| DSH 事件/路径 | AgentBus 行为 |
|---|---|
| `agent/created` | 注册；注入契约 + 工作区名册；启动 watcher |
| `agent/pre-step` | 标记 working；在步骤边界投递 |
| `tools/post-execute` | 工具结果后立即注入 |
| `agent/turn-stopping` | 标记 idle；urgent/定向 request 时 steer |
| 每 agent watcher | 30 秒长轮询；活动 turn 立即 steer，空闲仅 urgent/定向 request 唤醒 |
| `agent/disposed` | leave 并停止 watcher |

活动 watcher 使用 `active_only`，Stop 会使未完成 watch 失效，避免它继续消费空闲
消息。空闲 watcher 使用 `wake_only`，普通 chat/notice 不会造成忙循环或新 turn。
watcher 与生命周期 hooks 的收件箱 ack 是原子的，避免重复投递。旧的 5 秒空闲轮询
及 `AGENTBUS_DSH_POLL_MS` 已移除。

原生工具：`agentbus_who`、`agentbus_announce`、`agentbus_send`、`agentbus_ask`、
`agentbus_reply`、`agentbus_inbox`、`agentbus_receipt`、`agentbus_primer`。
每次 spawn 都有防护与超时上界。

## 安装

要求：`agentbus` 在 `PATH`（或 `AGENTBUS_BIN=/abs/path`），并与对端使用同一个
`AGENTBUS_HOME`。

```bash
npm install -g agentbus-cli
agentbus setup --agents dsh
```

源码使用 `./scripts/install.sh --agents dsh`。setup 将内嵌插件解到
`~/.agentbus/integrations/dsh-plugin`，可用时执行
`dsh plugin --profile web add <path>`。之后重启 host 或依赖配置 HMR。

手动 checkout overlay：

```yaml
- insert:
    - id: agentbus
      name: /abs/path/to/agentbus/integrations/dsh/plugin/lib/index.js
```

```bash
dsh headless --patch /tmp/agentbus.patch.yml --dump-config
```

插件为 ESM，无需构建。headless 模型凭据与 DSH sandbox 配置属于 Harness 自身，
不是 AgentBus 的要求。

## 验证与限制

```bash
node test/dsh-idle-wake.mjs
```

该测试用真实二进制 + mock DSH agent，验证空闲 request 唤醒与活动中无工具边界的
chat 推送。插件只能看到自己的 Host 进程；其他 Harness 在 daemon 上汇合。
`agent.steer` 将输入排入活动 turn，不取消已有工具工作。传输接收不等于模型处理；
通过回复与 `agentbus_receipt` 区分两者。
