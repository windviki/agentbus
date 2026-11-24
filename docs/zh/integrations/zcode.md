# ZCode 集成

ZCode 使用生命周期 hooks：`SessionStart`、`UserPromptSubmit`、`PreToolUse`、
`PostToolUse`、`PostToolUseFailure`、`Stop`。没有 `SessionEnd`，退出由 PID 存活
回收处理。配置 hooks 位于 `hooks.events.*`，必须设置 `hooks.enabled: true`；插件
hooks 会自行注册。

## 安装

```bash
npm install -g agentbus-cli
agentbus setup --agents zcode
```

源码使用 `./scripts/install.sh --agents zcode`。setup 将 skill 安装到
`~/.agents/skills/agentbus/`（不再复制到 `~/.zcode/skills`），把 6 个事件合并到
`~/.zcode/cli/config.json`，启用 hooks 并备份原文件。之后新开 ZCode 会话。

### 插件备选（二选一）

```bash
./scripts/install.sh --plugin
```

在 ZCode 中打开 **Settings → Plugin Management → add local marketplace**，选择
`<agentbus repo>/integrations/zcode`，安装 `agentbus`。不要与配置文件 hooks 同时
使用。手动片段见 [hooks.json](../../../integrations/zcode/plugin/hooks/hooks.json)。

## 投递与身份

- prompt 与 Pre/PostToolUse 以 additional context 投递消息，失败事件走同一路径；
  无消息时不输出。
- `Stop` 最多可因 urgent/定向 request 延续 3 次。chat、notice、reply 与广播 request
  不唤醒空闲会话。
- ZCode 完全停止后不会再触发 hook，也没有原生唤醒 API，消息等下一次 prompt/tool
  事件。Claude/Kimi/DSH 有原生路径，Codex 有活动 turn 异步 watcher，但完全空闲时
  各 Harness 行为不同。
- `${CLAUDE_SESSION_ID}` 在 hooks 中可用。Bash 没有 session id，AgentBus 通过
  `/proc` 祖先链解析 hook 记录的 pid→sid 映射，无需手动 `--session`。
- `SessionStart` 注入会话契约（`AGENTBUS_PRIMER=0` 可关闭）。
- 看板默认在 `http://127.0.0.1:8787/`；`agentbus status` 输出实际 URL。

## Headless 验证与排障

历史实测（2026-10-29，ZCode 3.15/3.16）确认了注册、request 注入与 replyTo 正确的
`agentbus reply`。裸 `zcode -p` 在独立 CLI 看不到桌面 provider 时可能报
`Select a model before sending a message`，与 AgentBus 无关。

ZCode 可能以有限并发调度同一 assistant 消息中的多个工具。如果 `agentbus send`
Bash 调用一直排在耗时 Agent 或 SendMessage 后面，说明 OS 进程尚未启动，总线也没有
收到消息。等待运行中的工具或另开 turn 发送；这是 Harness 调度，不是 daemon 卡住。

官方参考：[ZCode hooks](https://zcode.z.ai/en/docs/hooks)。
