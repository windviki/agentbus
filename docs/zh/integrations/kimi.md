# Kimi Code 集成

Kimi Code 支持生命周期 hook，其 stdin payload 与 Claude Code / ZCode 的 hook
结构兼容（`hook_event_name`、`session_id`、`cwd`、`client_type`），但
**输出契约不同**：

- `UserPromptSubmit` 的纯 stdout 会追加到模型上下文（没有 JSON 信封）；
- `Stop` 通过以退出码 **2** 退出、并把原因写到 stderr 来阻塞/继续；
- `PostToolUse` 只做观察：stdout 会被忽略。

`agentbus hook` 会识别 `client_type: "kimi_code_cli"` 并切换到这种输出模式。
在 Kimi 模式下，PostToolUse 刻意不 poll/ack（否则会消费消息却不展示它）；
消息会在下一个 prompt、Stop 唤醒或显式 `agentbus inbox` 时投递。

## 安装（自动化）

```bash
npm install -g agentbus-cli
agentbus setup --agents kimi
```

源码 checkout 请使用 `./scripts/install.sh --agents kimi`。setup 步骤：

- 把 skill 复制到 `~/.kimi-code/skills/agentbus/`；
- 向 `~/.kimi-code/config.toml` 追加一个受管理的 `[[hooks]]` 块
  （SessionStart、UserPromptSubmit、PostToolUse、Stop、SessionEnd）
  （备份：`config.toml.agentbus-bak`；重跑 setup 会替换该块，而不是重复追加）；
- 在 **`~/.kimi-code/AGENTS.md`** 中两个 `<!-- agentbus:begin -->` 标记之间
  upsert 会话契约。Kimi 每个会话都会加载这个全局指令文件，新会话就是靠它
  学会使用总线的，无需用户提醒（Kimi 会忽略 SessionStart 的 stdout）。
- Kimi 忽略 SessionStart 的 stdout，因此首个 `UserPromptSubmit` 会恰好注入
  一次按工作区分组的名册（每会话一个标记，防止重复）。

之后请新开一个 Kimi 会话；运行中的会话不会重新加载配置。

## 身份

Kimi 的 Bash 工具没有会话 id 环境变量。hook 会把 `kimi process pid -> sid`
记录到 `$AGENTBUS_HOME/pidmap/` 下，从 Kimi 的 Bash 工具运行的
`agentbus send/reply` 会通过回溯到祖先进程来确定身份（用 pid 启动时间防止
复用）。无需额外参数。

## 已验证（2026-10-29）

安装期间 Kimi 的 7 天额度已耗尽：
`provider.api_error: 429 weekly usage limit`，因此无法完成一次真实的模型
会话。hook 契约是用真实 payload 对测试 daemon 验证的：

- 带 `client_type: kimi_code_cli` 的 `SessionStart` 注册了会话（agent 标签
  为 `kimi`）；
- `UserPromptSubmit` 打印了带边框的纯文本对端消息，并附带 `reply` 指引
  （Kimi 会把它追加到上下文）；
- 存在待处理的 `urgent`（或发给该会话的 request）时，`Stop` 以退出码 **2**
  退出并在 stderr 打印原因，Kimi 会将其视为“继续该回合”；
  `AGENTBUS_WAKE=urgent` 可恢复仅 urgent 唤醒。
- 和所有仅靠 hook 的 harness 一样，在 Kimi 已经停在 prompt 之后到达的
  request 要等到下一次 prompt 或 tool 调用；Kimi 没有宿主级唤醒 API。总线
  看板会显示这条待处理的 request。

额度恢复后，可以用 `kimi -p "..."` 做一次真实冒烟测试；无需其他配置。
