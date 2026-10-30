# Claude Code 集成

Claude Code hooks 为 agentbus 提供四个投递时机：会话开始时（按工作区分组的
名册 + 会话契约）、回合进行中（`PostToolUse` 上下文注入）、回合开始时
（`UserPromptSubmit` 补投），以及 agent 进入空闲时（`Stop`）。`Stop` hook 会
为 **urgent** 消息、以及**直接发给它的 request** 唤醒会话（对方提问就期待回答，
哪怕对端已经 stop）。chat、notice、reply 和广播 request 则等待下一个自然边界。
`SessionEnd` 负责清理。

会话契约为什么重要（以及未做铺垫的会话会怎样处理注入的对端文本）：
[../adoption.md](../adoption.md)。

## 1. 安装与接线

```bash
npm install -g agentbus-cli
agentbus setup --agents claude
```

或从源码 checkout 安装：`./scripts/install.sh --agents claude`。setup 一步会
安装二进制、skill 和五个 hook；需要新开一个 Claude Code 会话才能加载它们。
安装器的完整说明：[../install.md](../install.md)。

## 2. 备选方案（任选其一）

### A. 插件包（hooks + skill 打包在一起）

仅适用于源码 checkout：插件市场位于仓库内。

```bash
./scripts/install.sh --plugin                 # 刷新内置 skill
claude plugin marketplace add "$PWD/integrations/claude-code"
claude plugin install agentbus@agentbus       # 持久生效（用户级）
```

或者不安装，只在单个会话里加载：

```bash
claude --plugin-dir "$PWD/integrations/claude-code/plugin"
```

用 `claude plugin details agentbus` 检查加载了什么（预期 1 个 skill、5 个 hook：
SessionStart / UserPromptSubmit / PostToolUse / Stop / SessionEnd）。

### B. 手动合并 hooks

将以下内容合并进 `~/.claude/settings.json`（用户级）或
`<repo>/.claude/settings.json`（工作区级）：

```json
{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "agentbus hook", "timeout": 15 } ] }
    ],
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "command": "agentbus hook", "timeout": 5 } ] }
    ],
    "PostToolUse": [
      { "hooks": [ { "type": "command", "command": "agentbus hook", "timeout": 5 } ] }
    ],
    "Stop": [
      { "hooks": [ { "type": "command", "command": "agentbus hook", "timeout": 5 } ] }
    ],
    "SessionEnd": [
      { "hooks": [ { "type": "command", "command": "agentbus hook", "timeout": 5 } ] }
    ]
  }
}
```

或者运行 `agentbus setup --agents claude` 自动合并（会先备份原文件）。
**不要同时使用 A 和 B** —— 每条消息都会投递两次。

## 3. 可选：首选名称

如果希望本项目的会话以指定名称注册，请在 settings 的 `"env"` 块中设置
`AGENTBUS_NAME`；否则会自动生成一个唯一名称（如 `swift-otter`）。

## 4. hook 之外的会话契约

`SessionStart` hook 默认会注入契约（`AGENTBUS_PRIMER=0` 可关闭）。对于
headless 运行，或首个 prompt 就必须已经完成铺垫的流程，还应把它放进
system prompt：

```bash
claude --append-system-prompt "$(agentbus primer)" -p "your task"
```

`agentbus primer` 打印的是同一份契约文本，因此 hook 和 system prompt 不会
出现漂移。

## 行为说明

- 空闲会话、无消息：hook 不输出任何内容 —— **零 token 开销**。
- 新的 `chat` / `request` / `notice`：在下一次 tool 调用时作为附加上下文注入
  （如果 agent 空闲且用户正在输入，则在下一个 prompt 时注入）。注入文本会
  注明它来自另一个 agent 会话，而不是用户。
- 新的 `request`：用 `agentbus reply "..."` 回复（回复最新的对端消息，设置
  `kind=reply` + `replyTo`）；`--to <name>` 指定某个对端。
- 空闲时收到新的 `urgent`：`Stop` hook 请求继续，于是 Claude 被唤醒、读取
  消息、作答，然后再次 stop。
- 空闲时收到新的**直接 `request`**：走同一条继续路径，reason 文本会写明
  `ACTION REQUIRED` 并给出 `agentbus reply --to <peer>`。这正是避免
  `agentbus ask` 在空闲的 Claude 会话上超时的机制。
- 广播 request、chat 和 notice 留在队列里，等下一次 tool 调用或 prompt，
  不会打断当前工作。
- 限制：如果 request 到达时 Claude Code 已经停在 prompt 上，就不会有 Stop
  hook 运行；该请求要等到下一次 prompt 或 tool 调用。此集成没有宿主级唤醒
  API，因此对完全空闲的目标，`agentbus ask` 可能超时。看板会把它标记为
  pending。
- `AGENTBUS_WAKE=urgent` 恢复旧的仅 urgent 唤醒行为；`AGENTBUS_WAKE=all`
  对任何消息都唤醒。
- 会话身份来自 Claude Code 的 `session_id`；存活状态来自 CLI 进程 pid
  （崩溃或退出 ⇒ 会话被自动回收）。
- Bash 工具子进程会继承 `CLAUDE_CODE_SESSION_ID`，因此 agent 运行的
  `agentbus send/reply` 会解析到与其 hook 相同的会话（无需 `--session`
  参数或手动设置）。
- 人类可以在看板上观察会话并给会话发消息：运行 `agentbus status` 查看 URL
  （默认 `http://127.0.0.1:8787/`）。
