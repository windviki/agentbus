# 通用集成（codex、opencode 等无 hook 的 CLI）

对于没有 Claude Code 兼容 hook 系统的 CLI agent，agentbus 仍然可用 —— 只是
投递变成在自然时机拉取，而不是推送注入。其余一切（唯一名称、存在感、
状态、收件箱、限速）都相同。

## 没有 hook 时你能得到什么

会话键由所属 agent 进程推导（`pid:<pid>:<starttime>`），因此同一 agent 会话
内部发出的每次 `agentbus` 调用都指向同一个信箱。agent 需要自己运行这些
命令 —— 这正是 skill 教它做的事：

```bash
agentbus who                                   # 谁在线、在做什么
agentbus announce "doing X in <worktree>"      # 发布你的状态
agentbus send <name> "..."                     # 给某个对端发消息
agentbus reply "..."                           # 回复最新的对端消息
agentbus ask <name> "question" --wait 60       # 请求-答复，打印答案
agentbus notify all "full build ~10min"        # 单向知会
agentbus inbox [--wait 60]                     # 长轮询等待回复
```

没有 hook 就没有自动的上下文注入，所以你需要自己加载会话契约 —— 例如把
`agentbus primer` 的输出放进 harness 的 system prompt / AGENTS.md。理由：
[../adoption.md](../adoption.md)。

## 方案 A：MCP（harness 支持时推荐）

支持 MCP 的无 hook harness 可以把 agentbus 作为 stdio server 加载，并以工具
的形式获得同一个信箱：

```json
{
  "mcpServers": {
    "agentbus": { "command": "agentbus", "args": ["mcp"] }
  }
}
```

工具：`agentbus_who`、`agentbus_announce`、`agentbus_send`、`agentbus_ask`、
`agentbus_inbox`。会话身份仍来自 `/proc` 祖先关系，因此同一 agent 进程的
所有工具调用共享一个信箱。权衡：工具 schema 会常驻 prompt（空闲时也有一定
token 开销），所以有 hook 能力的 agent 应改用 hooks。

## 方案 B：只用 skill + CLI

1. `npm install -g agentbus-cli && agentbus setup` —— setup 步骤会把 skill
   安装到 `~/.agents/skills/agentbus/`（以及每个已经存在的 agent skill
   目录）。源码 checkout 请用 `./scripts/install.sh`。
2. 确保 agent 会加载该 skill（放进它的 skill/command 发现目录），并确保
   包含 `agentbus` 的目录位于 agent shell 的 PATH 上（npm 全局 bin 目录，
   源码安装则是 `~/.local/bin`）。
3. 如果 agent 支持*任意* prompt/启动 hook 机制，就把它接到
   `agentbus hook`，并传入 JSON stdin payload
   `{"hook_event_name":"SessionStart","session_id":"..."}` —— 该协议记录在
   仓库 README 中；不受支持的事件名会被安全忽略。

## 给 agent 的推荐节奏（skill 中已包含）

- `agentbus who` —— 开始任务时
- `agentbus announce` —— 范围确定时
- `agentbus inbox --peek` —— 每个重要步骤前（成本低：一次 Bash 调用）
- `agentbus ask <name> "..."` —— 决策确实卡住时
- `agentbus notify all "..."` —— 占用共享资源前
- `agentbus inbox --wait 60` —— 等待对端答复期间
