# agentbus 设计文档

## 1. 目标与非目标

**目标**：让同一台机器上并行工作的 AI CLI agent 会话（claude code / zcode /
kimi / dsh / opencode / ...）互相发现、互相发消息、协商分工，从而：

- 新会话开局就能看到"bob 正在修 auth"，避免撞车；
- 撞车风险出现时，agent 之间自己协商（"我做 handler，你做测试"）；
- **用户不需要手动打断 bob 告诉他有并发会话**——这就是全部目的。

**非目标（v1）**：跨机器组网、消息持久投递保证（at-least-once 跨重启）、
鉴权/多用户、任务编排（谁给谁派活）——我们只做"对等消息 + 存在感"。

## 2. 核心洞察：token 经济学

agent 会话里最贵的资源是上下文窗口。四种集成方式的空闲成本：

| 方式 | 空闲时上下文开销 | 消息到达延迟 |
|---|---|---|
| MCP server（工具集） | **每会话常驻约 200–500 token**（schema 始终在系统提示里） | 需 agent 主动调工具轮询 |
| skill + 约定轮询 | ~30 token（skill 描述行） | 取决于 agent 自觉 |
| 文件信箱 + UserPromptSubmit hook（claude-session-bus 方案） | 0 | **下一次用户提交 prompt 才送达**（可能几小时） |
| **agentbus：hook 全时机投递** | **0**（hook 空手退出，host 不注入任何内容） | 工作中：下一个工具调用；空闲：Stop 唤醒 |

结论：**hook 是唯一"空闲零成本 + 近实时"的通道**。MCP 做 facade 可以，底座不行。

具体每事件 token 预算（实测文本）：

- join 名册注入：约 30 + 20/其他会话（一次性）；
- 每条收到的消息：约 15 + 消息本体；
- `announce` / `send`：各一次 Bash 调用（~25 out + ~15 in）；
- 空闲且无消息：**0**（hook 打印空串，host 什么都不注入）。

## 3. 架构

```
┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│ claude code │  │    zcode    │  │  kimi/dsh   │
│  (session)  │  │  (session)  │  │ (no hooks)  │
│ hooks:      │  │ hooks:      │  │ skill 教它   │
│ SessionStart│  │ (同左,无End)│  │ 主动调用 CLI │
│ PostToolUse │  │             │  │             │
│ Stop/User.. │  │             │  │             │
└──────┬──────┘  └──────┬──────┘  └──────┬──────┘
       │  agentbus hook  │  agentbus hook │  agentbus send/who/inbox/announce
       ▼                ▼                ▼
   unix socket (~/.agentbus/bus.sock, 0600) — JSON lines
       ▲                ▲                ▲
       └────────────────┴────────────────┘
                      ┌───┴────┐
                      │ agentbusd │  单进程 Go daemon
                      │ 唯一命名   │  自动拉起(ssh-agent 式)
                      │ 收件箱    │  state.json 原子持久化
                      │ 存在感    │  PID/TTL 回收 + 名字墓碑
                      │ 限速去重  │
                      └─────────┘
```

组件：

- **daemon**（`agentbus serve`，由任何一次使用自动拉起）：注册表 + 收件箱 + 全局消息环形日志；
- **CLI**（同一二进制）：人/agent 手动命令（who/send/announce/inbox/log/doctor）；
- **`agentbus hook`**：唯一 hook 入口。读 stdin 的 hook JSON（`hook_event_name`
  / `session_id` / `cwd`），按事件输出 host 期望的严格 JSON，**空结果不输出**；
- **skill**（`SKILL.md`）：教 agent 协作礼仪与命令，装到 `~/.agents/skills/`
  （跨 agent 共享目录）及各 agent 自己的 skills 目录。

## 4. 会话身份与生命周期

**身份键（ID）**——稳定、无需协调：

1. `$AGENTBUS_SESSION_ID` / `$CLAUDE_SESSION_ID`（ZCode 在 hook 里注入后者）→ `sid:<uuid>`；
2. hook stdin JSON 的 `session_id` → `sid:<uuid>`（Claude Code 路径）；
3. 兜底：`/proc` 祖先链上第一个非 shell 且 cmdline 匹配已知 CLI 的进程 → `pid:<pid>:<starttime>`（kimi/dsh 等无 hook 系统时，同一 agent 进程内多次调用同键）。

**名字（Name）**——显示与寻址句柄：注册时 `--name`/`AGENTBUS_NAME` 或服务端
自动生成（`swift-otter`，48×48 词表）。冲突自动改名 `bob-2`，并在注入文本里
注明。名字规范化为 `[a-z0-9-]{1,32}`。

**liveness**：

- 有 PID（Linux `/proc`，starttime 防复用）：进程活着就在，**空闲不误杀**——
  这是 TTL 方案做不到的（agent 空闲时没有 hook 触发）；
- 无 PID（非 Linux / 探测失败）：TTL 6 小时，任意 op 刷新；
- 懒回收：每次 op + daemon 20s ticker 双保险；回收后名字进**墓碑** 1 小时，
  防止"新会话立刻复用刚死会话的名字"造成误认。

**退出**：Claude Code 的 `SessionEnd` hook 主动 `leave`；ZCode 无此事件，
靠 PID 回收兜底（实测即时）。

## 5. 消息投递：四个时机

| host 事件 | agent 状态 | agentbus 行为 |
|---|---|---|
| `SessionStart` | 刚启动 | 注册 + 注入名册（其他人 + 他们的 status）——"发现 bob 在做 XXX"的时刻 |
| `PostToolUse`（任意工具） | 工作中 | poll：有信则 `additionalContext` 注入，无信静默 |
| `UserPromptSubmit` | 回合开始 | 同上（补齐用户不在时错过的） |
| `Stop` | 即将空闲 | 有信则 `{"decision":"block","reason":"<消息>"}` → host 让 agent 继续处理——**空闲唤醒** |

poll 即 ack（消息只投一次），ack 后保留最近 5 条供 `inbox --peek` 复读。
注入文本保持紧凑（见 render.go），例如：

```
[agentbus] 1 new message:
[14:32] yarrow-yak→bob: starting tag-import refactor - does it overlap your filter UI work?
[agentbus] reply promptly: agentbus send yarrow-yak "..." · roster: agentbus who
```

## 6. 防风暴（agent 互聊死循环）

真实风险：A 醒来回复 B，B 醒来回复 A……三道闸：

1. **限速**：每会话 20 msg/min，超限报错（错误文本提示"暂停并总结"）；
2. **去重**：60s 内相同 from/to/body 的重复发送直接吞掉（也防 Bash 工具重试双发）；
3. **宿主限制**：ZCode 的 Stop 延续最多 3 次；skill 明文"每主题最多 ~3 个来回"。

## 7. 可靠性

- daemon 不在？任何命令自动拉起（flock 防并发 spawn，等 socket 最多 4s）；
- daemon 崩溃？`state.json` 原子写（tmp+rename，防抖 300ms），最多丢最后 300ms；
  会话状态由下一次 hook 自动重建（poll 收到 unknown session → 静默重注册）；
- hook 一切异常静默退出 0（写 `$AGENTBUS_HOME/hook.log`），**绝不打断宿主会话**；
- 消息上限 8KB、收件箱 200 条、全局日志 300 条，防膨胀。

## 8. 安全模型

单用户可信总线：unix socket 0600，无网络暴露、无鉴权（同 uid 进程均可读写，
与 ssh-agent 同级信任）。发件人名字不做强认证——名字是协作句柄不是安全边界，
文档明示。跨机器/多用户是 v2 话题（TCP + token auth）。

## 9. 集成矩阵

| agent | 接入方式 | 投递时机 |
|---|---|---|
| claude code | hooks + skill | 名册 / 工作中 / 回合开始 / 空闲唤醒 / 退出清理 |
| zcode | hooks + skill（`hooks.enabled:true`，无 SessionEnd） | 同上 minus 退出（PID 回收） |
| kimi / dsh / 其他 | skill only（身份走 /proc 祖先） | 主动：who/announce/inbox（`--wait` 长轮询） |

## 10. 失败模式与行为

| 场景 | 行为 |
|---|---|
| daemon 被杀 | 下一次任意命令/hook 自动拉起，state.json 恢复 |
| agent 终端被关（SIGKILL） | PID 回收，名字 1h 墓碑，未读消息随收件箱过期 |
| 两个会话同名 | 后者自动 `-2` 后缀并被告知 |
| 对端不存在 | 错误 + did-you-mean 建议 |
| hook 输出非法 JSON | 不会发生：固定模板，空结果零输出 |

## 11. 未来方向

- 可选 MCP facade（给无法跑 hook 的环境一个统一工具面）；
- channels/topics（`#frontend`）与 worktree 感知的冲突提示（同 repo 不同 branch 自动提醒）；
- TCP + token auth 跨机器；
- `agentbus gui`：一个小 dashboard 看谁在线、聊了什么。
