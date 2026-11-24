# Claude Code 集成

AgentBus 在可用时优先使用 Claude Code 的**每会话原生 inbox socket**（Linux/
macOS 上 v2.1.224+）。活动 turn 中消息在工具调用之间进入；空闲时，允许的定向
request/urgent 可开启新 turn。生命周期 hooks 是兼容降级路径，不需要研究预览的
Channels 功能。

## 安装

```bash
npm install -g agentbus-cli
agentbus setup --agents claude
```

源码使用 `./scripts/install.sh --agents claude`。之后新开 Claude 会话。setup 安装
共享 skill 和 8 个托管 hook：`SessionStart`、`UserPromptSubmit`、`PreToolUse`、
`PostToolUse`、`PostToolUseFailure`、`Stop`、`StopFailure`、`SessionEnd`。

### 插件备选（二选一）

```bash
./scripts/install.sh --plugin
claude plugin marketplace add "$PWD/integrations/claude-code"
claude plugin install agentbus@agentbus
# 或仅在单个会话加载：
claude --plugin-dir "$PWD/integrations/claude-code/plugin"
```

不要同时启用插件与配置文件 hooks，它们会消费同一信箱。可用
`claude plugin validate integrations/claude-code/plugin` 校验。手动合并到
`~/.claude/settings.json` 的片段见
[hooks.json](../../../integrations/claude-code/plugin/hooks/hooks.json)。

headless 首个 prompt 前需要铺垫时：

```bash
claude --append-system-prompt "$(agentbus primer)" -p "your task"
```

## 原生投递

Claude 在 `SessionStart` 前导出本会话的 `CLAUDE_CODE_MESSAGING_SOCKET` 与
`CLAUDE_CODE_MESSAGING_TOKEN`。hook 通过本地 wire 协议把端点传给 daemon，后者：

1. 原子占用待投批次，避免 hook 同时消费；
2. 写入 auth frame 与有序（`priority=next`）user frame，并带预期 `session_id`；
3. socket 写成功后记录 `accepted`；失败则释放批次给 hooks。

活动 turn 中消息在工具调用之间到达，不取消工具。空闲时普通 chat/notice/reply 与
广播 request 保持排队；定向 request 与 urgent 可按 `AGENTBUS_WAKE` 开启 turn。

Claude 的 `crossSessionInbound` hold/refuse 规则仍然有效。socket 接收不代表 Claude
已展示或处理消息。用 `agentbus receipt <message-id>` 看传输状态，用 reply/pending
判断处理状态。

token 不持久化，也不经 session API 暴露。daemon 重启后由下一次 Claude hook 刷新；
此前消息走普通 hook 梯子。`AGENTBUS_PUSH=0` 可禁用原生注册。

## Hook 降级与身份

- prompt 与 Pre/PostToolUse 以 additional context 注入待投消息。
- `Stop` 先 peek，只对可唤醒消息续跑。如果 Claude 已停止且原生推送不可用，只能等
  下一次 prompt。
- `StopFailure` 标记 idle，不强制续跑。
- `SessionEnd` 主动 leave；崩溃由 PID 存活回收。
- `CLAUDE_CODE_SESSION_ID` 让 Bash 中的 `agentbus send/reply` 与 hooks 解析到同一
  会话。

官方能力参考：[跨会话消息](https://code.claude.com/docs/en/cross-session-messaging)、
[hooks](https://code.claude.com/docs/en/hooks)。
