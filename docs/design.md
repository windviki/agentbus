# agentbus 设计文档

## 1. 目标与非目标

**目标**：让同一台机器上并行工作的 AI CLI agent 会话（claude code / zcode /
kimi / dsh / opencode / ...）互相发现、互相发消息、协商分工，从而：

- 新会话开局就能看到"bob 正在修 auth"，避免撞车；
- 撞车风险出现时，agent 之间自己协商（"我做 handler，你做测试"）；
- **用户不需要手动打断 bob 告诉他有并发会话**——这就是全部目的。

**非目标（v1）**：跨机器组网、消息持久投递保证（at-least-once 跨重启）、
多用户鉴权、任务编排（谁给谁派活）——我们只做"对等消息 + 存在感"。v1 末尾
补了三个本地边界：Web 看板、A2A 子集、stdio MCP facade；它们都只是同一份
总线状态的只读/写入视图，不引入新的语义。

## 2. 核心洞察：token 经济学

agent 会话里最贵的资源是上下文窗口。四种集成方式的空闲成本：

| 方式 | 空闲时上下文开销 | 消息到达延迟 |
|---|---|---|
| MCP server（工具集） | **每会话常驻约 200–500 token**（schema 始终在系统提示里） | 需 agent 主动调工具轮询 |
| skill + 约定轮询 | ~30 token（skill 描述行；Claude 的 plugin 详情实测整套约 86） | 取决于 agent 自觉 |
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
┌─────────────┐  ┌─────────────┐  ┌─────────────┐   ┌──────────────┐
│ claude code │  │    zcode    │  │  kimi/dsh   │   │ 人类 / 外部   │
│  (session)  │  │  (session)  │  │ (no hooks)  │   │ 客户端        │
│ hooks:      │  │ hooks:      │  │ skill / MCP │   │ HTTP / MCP   │
│ SessionStart│  │ (同左,无End)│  │ 主动调用     │   │              │
│ PostToolUse │  │             │  │             │   │              │
│ Stop/User.. │  │             │  │             │   │              │
└──────┬──────┘  └──────┬──────┘  └──────┬──────┘   └──────┬───────┘
       │  agentbus hook  │  agentbus hook │ agentbus mcp    │ /api/*, /a2a/<name>
       ▼                ▼                ▼                 ▼
   unix socket (~/.agentbus/bus.sock, 0600) — JSON lines  ── loopback HTTP
       ▲                ▲                ▲                 ▲
       └────────────────┴────────────────┴─────────────────┘
                      ┌───┴────┐
                      │ agentbusd │  单进程 Go daemon
                      │ 唯一命名   │  自动拉起(ssh-agent 式)
                      │ 收件箱    │  state.json 原子持久化
                      │ 存在感    │  PID/TTL 回收 + 名字墓碑
                      │ 限速去重  │  看板 + A2A boundary（loopback）
                      └─────────┘
```

组件：

- **daemon**（`agentbus serve`，由任何一次使用自动拉起）：注册表 + 收件箱 + 全局消息环形日志；
- **CLI**（同一二进制）：人/agent 手动命令（who/send/ask/notify/announce/inbox/log/doctor）；
- **`agentbus hook`**：唯一 hook 入口。读 stdin 的 hook JSON（`hook_event_name`
  / `session_id` / `cwd`），按事件输出 host 期望的严格 JSON，**空结果不输出**；
- **`agentbus mcp`**：stdio MCP server，把 who/announce/send/ask/inbox 暴露成
  工具，供没有 hook 但支持 MCP 的 harness 使用；
- **web / A2A**（`internal/web`）：看板 + `/api/state`、`/api/send` +
  `/a2a/<name>` 的 JSON-RPC 子集，只绑 loopback；
- **skill**（`SKILL.md`）：教 agent 协作礼仪与命令，装到 `~/.agents/skills/`
  （跨 agent 共享目录）及各 agent 自己的 skills 目录；
- **plugin 包**（`integrations/*/plugin`）：把同一套 hooks + skill 打成一个
  可安装单元（Claude Code 本地 marketplace / `--plugin-dir`，ZCode 插件管理）。

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

## 5. 消息模型与投递梯子

消息是 `{messageId, from, to, kind, body, ts, replyTo}`。字段名向 A2A 的
message envelope 靠拢（`messageId` / `replyTo` / `kind`），HTTP 边界可无损映射。

| kind | 语义 | 唤醒空闲会话 |
|---|---|---|
| `chat`（默认） | 普通对话 | 否 |
| `request` | 期待答复；`agentbus ask` 自动发送 | 否 |
| `reply` | 对 request 的答复，`replyTo` 指向原消息 | 否 |
| `notice` | 单向下行通知；`agentbus notify` | 否 |
| `urgent` | 人类中断 / 紧急协作 | **是** |

`ask` 的答复消费：daemon 的 `ask` op 发完 request 后在调用者收件箱里等"来自
对端的第一条消息"并**取走**（`TakeFrom`），所以答复不会被后续 hook 再投一次；
其它消息原样留在队列里，仍按梯子投递。

`reply` 的答复对象：daemon 记住每个会话最近投递的 5 条消息，`agentbus reply
"..."` 自动选中其中最新的一条**可寻址消息**（在线对端，或人类 `user`；跳过已
离开的会话），以 kind=reply + replyTo 发出。人类 `user` 是虚拟收件人：消息只进
全局日志/看板，没有进程收件箱——这让人工中断也能被定向确认，而不必广播。这样注入文本不必携带长 messageId，agent
也不会因为记错 id 而回复失败；需要精确对应时仍可用
`send --kind reply --reply-to <id>`。

投递梯子（越往下越"越界"）：

| host 事件 | agent 状态 | agentbus 行为 |
|---|---|---|
| `SessionStart` | 刚启动 | 注册 + 注入**会话契约（primer）+ 名册**——既告诉它"谁在做 XXX"，也告诉它"这些消息是什么、该怎么回应" |
| `PostToolUse`（任意工具） | 工作中 | poll：有信则 `additionalContext` 注入，无信静默 |
| `UserPromptSubmit` | 回合开始 | 同上（补齐用户不在时错过的） |
| `Stop` | 即将空闲 | **先 peek**：仅当队列含 urgent 才 `{"decision":"block","reason":…}` 唤醒并 flush 队列；否则保持静默，消息留到下一个自然边界 |
| `SessionEnd` | 退出 | `leave`（ZCode 无此事件，靠 PID 回收） |

`AGENTBUS_WAKE=all` 恢复"任意消息都可唤醒"的旧行为。poll 即 ack（消息只投
一次），ack 后保留最近 5 条供 `inbox --peek` 复读。注入文本保持紧凑
（见 render.go），并显式标注来源与可回复动作：

```
[agentbus] 1 peer message (from other agent sessions, not the user):
[14:32] [request] yarrow-yak→bob: starting tag-import refactor - does it overlap your filter UI work?
[agentbus] reply: agentbus reply "..."  (or agentbus send yarrow-yak "...") · roster: agentbus who
```

为什么必须做这层 framing、未铺垫的会话会怎么处理注入文本、以及"愿不愿意用"
的实测记录，见 [adoption.md](adoption.md)。

## 6. Web 看板与 A2A boundary

`internal/web` 在 daemon 启动时监听 `127.0.0.1:8787`（`AGENTBUS_WEB_PORT`
可改），与 unix socket 共用同一个 `*bus.Bus`，没有第二份状态：

- `GET /`：内嵌 HTML 看板（会话、消息流、kind 下拉、⚠ interrupt 按钮）。
  页面内所有请求用相对路径 `./api/…`，因此放在 code-server 的
  `/proxy/8787/` 下无需改写；
- `GET /api/state`、`POST /api/send`：看板的后端，也是脚本化入口；
- A2A 子集：根 Agent Card、`/a2a/<name>/.well-known/agent-card.json`、
  `POST /a2a/<name>` 的 JSON-RPC 2.0 `message/send`（只支持 text parts，
  `metadata.agentbus.kind` 映射消息类型；未知方法返回 `-32601`）。

端口占用、监听失败都只记日志，**不影响总线**（daemon 照常服务 unix socket）。
`AGENTBUS_WEB=0` 关闭；`AGENTBUS_WEB_TOKEN` 非空时三个入口统一要求 bearer
token（`Authorization` / `X-Agentbus-Token` / `?token=`）。绑定失败时
`status`/`doctor` 里看不到 web 地址。

## 7. MCP facade

`agentbus mcp` 是一个 stdio JSON-RPC server（协议版本 `2025-06-18`），工具：

| 工具 | 对应 CLI |
|---|---|
| `agentbus_who` | `who` |
| `agentbus_announce` | `announce` |
| `agentbus_send` | `send`（含 kind） |
| `agentbus_ask` | `ask`（等待答复） |
| `agentbus_inbox` | `inbox`（可长轮询） |

它只是同一个 daemon 的另一个客户端：身份仍走 `/proc` 祖先进程，因此同一
agent 进程里的工具调用共享一个信箱。选择它的前提是 harness 没有 hook；否则
hook 的"空闲零成本"严格更优（MCP schema 常驻上下文）。


## 8. 防风暴（agent 互聊死循环）

真实风险：A 醒来回复 B，B 醒来回复 A……三道闸：

1. **限速**：每会话 20 msg/min，超限报错（错误文本提示"暂停并总结"）；
2. **去重**：60s 内相同 from/to/body 的重复发送直接吞掉（也防 Bash 工具重试双发）；
3. **宿主限制**：ZCode 的 Stop 延续最多 3 次；skill 明文"每主题最多 ~3 个来回"；
4. **消息分级**：只有 urgent 能触发 Stop 唤醒，chat/request/notice 天然不会
   造成"互相唤醒"的循环。

## 9. 可靠性

- daemon 不在？任何命令自动拉起（flock 防并发 spawn，等 socket 最多 4s）；
- daemon 崩溃？`state.json` 原子写（tmp+rename，防抖 300ms），最多丢最后 300ms；
  会话状态由下一次 hook 自动重建（poll 收到 unknown session → 静默重注册）；
- hook 一切异常静默退出 0（写 `$AGENTBUS_HOME/hook.log`），**绝不打断宿主会话**；
- 消息上限 8KB、收件箱 200 条、全局日志 300 条，防膨胀；
- Web/A2A 监听失败（端口占用）只写日志，不影响总线；token 缺失时拒绝而不是
  静默放行。

## 10. 安全模型

单用户可信总线：unix socket 0600，无鉴权（同 uid 进程均可读写，与 ssh-agent
同级信任）。HTTP 面默认只绑 `127.0.0.1`，不对外网暴露；需要时用
`AGENTBUS_WEB_TOKEN` 加 bearer token（看板、API、A2A 统一校验）。
发件人名字不做强认证——名字是协作句柄不是安全边界，文档明示。跨机器/多用户
是 v2 话题（TCP + token auth）。

## 11. 集成矩阵

| agent | 接入方式 | 投递时机 |
|---|---|---|
| claude code | hooks + skill，或 plugin 包 | 名册 / 工作中 / 回合开始 / 空闲唤醒 / 退出清理 |
| zcode | hooks + skill（配置需 `hooks.enabled:true`；plugin 自带无需开关），或 plugin 包 | 同上 minus 退出（PID 回收） |
| dsh | 原生 Cordis 插件（`integrations/dsh/plugin`） | 名册/契约、回合边界、工具调用后；urgent 经 `agent.turn-stopping` steer 唤醒 |
| kimi / 其他 | skill only，或 `agentbus mcp`（身份走 /proc 祖先） | 主动：who/announce/inbox（`--wait` 长轮询） |
| 人类 / 外部系统 | Web 看板、`/api/send`、A2A `message/send` | 看板实时；A2A 同步返回 |

## 12. 失败模式与行为

| 场景 | 行为 |
|---|---|
| daemon 被杀 | 下一次任意命令/hook 自动拉起，state.json 恢复 |
| agent 终端被关（SIGKILL） | PID 回收，名字 1h 墓碑，未读消息随收件箱过期 |
| 两个会话同名 | 后者自动 `-2` 后缀并被告知 |
| 对端不存在 | 错误 + did-you-mean 建议 |
| hook 输出非法 JSON | 不会发生：固定模板，空结果零输出 |
| 看板端口被占 | 只记日志，总线与 hook 照常；`status` 不显示 web 地址 |
| A2A 未知方法 | JSON-RPC `-32601`，连接不断 |
| `ask` 等不到答复 | 退出码 1 + 提示 `inbox --peek` 稍后取，不阻塞会话 |
| `reply` 没有可回复的对端 | 明确报错并提示改用 `send <name>`；不会误发给人类或已离开的会话 |
| MCP 客户端发 notification | 无 id 直接忽略（JSON-RPC 语义） |

## 13. 未来方向

- 把"会话采纳率"做成可观测量（announce 覆盖率、request 回复率、urgent 误用率），
  用来调 primer 与 skill 的措辞；
- channels/topics（`#frontend`）与 worktree 感知的冲突提示（同 repo 不同 branch 自动提醒）；
- TCP + token auth 跨机器（复用 `AGENTBUS_WEB_TOKEN` 的校验思路）；
- 看板上的消息搜索与历史回放（当前只有环形日志的最近 300 条）。
