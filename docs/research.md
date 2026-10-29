# 调研：是否已有成熟的开源方案？

> 调研时间：2026-10。方法：GitHub 仓库搜索（多个关键词组合）+ 逐个仓库阅读 README/架构文档。
> 结论先行：**没有成熟项目做"本地、跨 CLI、hook 驱动、空闲零 token"的 agent 会话消息总线**。
> 这个方向存在一批 0–2 星的实验性实现，各自缺一块关键能力，详见下表。

## 我们的需求清单

1. **跨 CLI**：claude code / zcode / kimi / dsh / opencode 等本地并行会话都能接入；
2. **会话命名**：启动即注册，名字唯一（服务端分配或查重）；
3. **存在感与状态**：别人能看到"bob 正在 XXX"，新人开局即可发现冲突；
4. **点对点消息**：向指定会话发消息，协商分工；支持广播；
5. **近实时**：对端工作中（下一个工具调用）或空闲（Stop 时唤醒）都能收到；
6. **token 经济**：空闲、无消息时**零**上下文开销——这是与 MCP 方案的本质差异；
7. **免管理**：会话崩溃自动回收，daemon 自动拉起，用户不需要运维。

## 现有方案盘点

### A. 人类操作的会话管理器（不是 agent 间通信）

- **[smtg-ai/claude-squad](https://github.com/smtg-ai/claude-squad)**（AGPL-3.0）：
  tmux + git worktree 管理多个 Claude Code/Codex/Gemini/aider 会话的 TUI。
  README 明确没有会话间通信/消息总线——它是"隔离"，我们要的是"互通"。
- **[av/harbor](https://github.com/av/harbor)**（Apache-2.0，约 3.3k 星）：实际是
  Docker Compose 拉起本地 LLM 全家桶（推理后端、WebUI、RAG 等）的 CLI，
  与 agent 会话间通信无关。

结论：这一类解决"人怎么同时开很多 agent"，不解决"agent 之间怎么协商"。

### B. 网络级互操作协议（过重）

- **A2A / Agent2Agent**（Google 发起，现归 Linux Foundation）：
  基于 HTTP + JSON-RPC，Agent Card、任务、artifact、SSE 流。定位是**跨组织、
  跨网络的常驻服务 agent** 互操作。要为两个本地终端会话跑 HTTP server +
  JSON-RPC 生命周期，完全不成比例；且各 CLI 也不会内嵌 A2A server。
  （v1 只补了 loopback 上的 A2A 子集——Agent Card + `message/send`——让外部
  客户端能接入同一份总线；任务/artifact/SSE 不做，见 DESIGN.md §6。）

### C. 同方向但极早期的项目（≤2 星）

| 项目 | 机制 | 与需求的差距 |
|---|---|---|
| [exalsch/claude-session-bus](https://github.com/exalsch/claude-session-bus)（0★，11 commits，GPL-3.0） | MCP `broadcast()` 工具 + append-only JSONL 信箱 + `UserPromptSubmit` hook 注入 | 仅 Claude Code；只有广播无定向消息；**消息只在用户下一次提交 prompt 时才送达**（工作中/空闲时收不到）；MCP 工具 schema 常驻上下文（空闲也花 token）；无唯一命名/存在感/状态 |
| [lloyd-c137/amp-skill](https://github.com/lloyd-c137/amp-skill)（0★，1 commit） | Unix socket daemon + JSON 帧 + skill + SQLite 离线队列，Python/systemd | 单次提交的原型；无 hook 投递路径（CLI agent 无法常驻连接）；无状态公告；无崩溃回收；未覆盖多 CLI |
| [zimdin12/aify-comms](https://github.com/zimdin12/aify-comms)（2★，约 4.7k commits） | Docker + FastAPI + SQLite 控制平面，每机一个 PTY daemon，agent 经 MCP 工具通信，Web 面板 | 定位是"控制平面/编排器"：接管 agent 进程与终端，重栈（Docker、MCP、仪表盘）；我们是**对等总线**：不接管进程，只传消息 |
| [JaredCluff/opencode-nats](https://github.com/JaredCluff/opencode-nats)（0★） | 给 OpenCode 加 NATS 插件 | 单一 CLI；引入 NATS 依赖；无 token 经济设计 |
| 其余（swarm-bus、agentlink、sparks-bus-mcp、Retsumdk/message-bus 等，均 0★） | 各式 pub-sub / MCP broker | 通用消息层，无 agent 会话语义（命名、存在感、lifecycle、hook 投递） |

### D. 为什么不直接用 MCP / 文件 / Redis？

- **MCP server**：每个会话常驻一组工具 schema（约几百 token/会话，空闲也花）；
  且没有"消息推给你"的通道，仍要靠轮询工具调用。适合做可选的 facade，不适合做底座。
- **纯文件信箱**（claude-session-bus 的做法）：实现简单，但无唯一命名原子性、
  无长轮询唤醒、无 TTL/PID 回收，所有一致性都要靠锁协议自己拼。
- **Redis/NATS**：外部服务依赖，违背"clone 即用、零运维"。

## 结论

需求清单 7 条，最接近的两个项目（claude-session-bus、amp-skill）各满足约一半，
且都是个人原型规模（≤2 星、无发布、明确记录着未修复的边界问题）。**自建是合理的**，
并且可以吸收它们的两个好想法：hook 注入（来自 claude-session-bus）与
unix-socket daemon（来自 amp-skill），补上我们独有的部分：

- 跨 CLI 的身份协议（session_id → 唯一名，`/proc` 祖先探测兜底）；
- 存在感 + 状态公告（"bob 正在 XXX"）；
- 四个投递时机（SessionStart 名册 / PostToolUse 工作中 / UserPromptSubmit 回合开始 / Stop 仅 urgent 唤醒）；
- PID 级 liveness 与墓碑式名字回收；
- 限速 + 去重防 agent 对话风暴；
- daemon 自动拉起与原子状态持久化；
- 可选的本地边界：Web 看板、A2A 子集、stdio MCP facade——都只是同一份
  总线状态的视图，不改变"hook 为底座"的结论。
