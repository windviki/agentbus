# Codex 集成

当前 Codex 提供生命周期 hooks 与后台（`async`）command hooks。AgentBus 直接使用
这些能力，不会再向 CLI 已拥有的会话挂载第二个 app-server。

## 安装与信任

```bash
npm install -g agentbus-cli
agentbus setup --agents codex
```

setup 将托管条目合并到 `$CODEX_HOME/hooks.json`（默认 `~/.codex/hooks.json`），
在 `~/.codex` 存在时安装 skill，并在改写前备份。

Codex 刻意要求审核发生变化的用户 hooks。请在 Codex 中打开 `/hooks`，检查并信任
AgentBus 条目后它们才会运行。hook 定义再变化时需要重新审核。setup 和信任完成后
新开 Codex 会话。

## 投递行为

- `SessionStart`、prompt、Pre/PostToolUse 与 `Stop` 构成普通上下文投递梯子；
  `Interrupt` 与 `SessionEnd` 负责状态清理。
- 另一个异步 `UserPromptSubmit` handler 运行 `agentbus hook --async-watch`，最长
  540 秒。活动 turn 中收到消息会结束该 hook，Codex 把它的 `additionalContext`
  提供给下一次模型请求，无需等待另一次工具调用。
- `Stop` 或 `Interrupt` 标记 idle 并使未完成 watcher 失效，避免它在后台消费消息
  并错误标记 accepted。异步完成本身不会唤醒空闲 Codex。
- 定向 request/urgent 可通过同步 `Stop` 续跑。如果 Codex 已经完全停止，消息等到
  下一次用户 turn；看板显示 queued。

手动 hook 片段见 [hooks.json](../../../integrations/codex/hooks.json)。只有同时自行
管理 Codex hook 信任时才建议绕过 setup 使用该片段。

不希望阻塞当前 Codex 任务时可用 `ask --wait 0`，答复异步到达。回执用于区分总线
排队、Harness 接收与真正回答。

官方参考：[Codex hooks](https://developers.openai.com/codex/hooks)、
[app-server 与 turn steering](https://developers.openai.com/codex/app-server)。
