# agentbus 设计文档

## 1. 目标与非目标

**目标**：让同一台机器上的 AI 编程 agent 会话（Claude Code / ZCode / Kimi /
DSH / Codex / ...）互相发现，在工作进行中交换信息，并在撞车前调整当前计划。
用户不应再充当终端之间的消息路由器，也不应仅为告诉 peer“还有另一个会话”
而打断它。

投递分为三个不同里程碑：

1. **queued（已排队）**：daemon 已为收件人接收消息；
2. **accepted（已接收）**：目标 Harness 通过 hook 或原生 API 接受投递；
3. **processed（已处理）**：agent 回复了消息，或可见地调整了工作。AgentBus
   绝不把传输层接收等同于模型已处理。

**非目标**：跨机器组网、多用户鉴权、持久化 exactly-once、任务编排。本地投递
采用短占用 + hook 降级的 best-effort / at-least-once 语义；任务认领板属于另一层。

## 2. Token 与注意力成本

| 方式 | 空闲上下文成本 | 投递时机 |
|---|---|---|
| MCP 轮询工具 | schema 常驻上下文 | agent 主动轮询时 |
| skill + 轮询约定 | 很小，但依赖自觉 | 下一次 agent 选择的调用 |
| 仅 prompt 信箱 | 无消息时为 0 | 下一次用户 prompt，可能数小时 |
| 生命周期 hooks | 无消息时为 0 | 下一次 prompt/tool/stop 边界 |
| **原生推送 / 异步投递** | 无消息时为 0 | 工作中：下一个安全模型边界；空闲 request/urgent：支持时开新 turn |

hooks 是统一降级路径。原生通道利用 Claude 的每会话 inbox socket、Kimi 的
loopback Server API、DSH 的 `agent.steer` 与 Codex 异步 hooks 降低延迟，且不增加
常驻 MCP schema。任何通道都不会取消正在执行的工具。普通 chat/notice/reply 不
唤醒空闲会话；定向 request 或 urgent 才可以。

每条消息只附少量上下文：信封、正文、发送者 cwd/branch/status。空 hook 不输出。

## 3. 架构

```
┌─────────────┐ ┌─────────────┐ ┌─────────────┐ ┌──────────────┐
│ Claude Code │ │ Kimi / DSH  │ │ Codex/ZCode │ │ human/client │
│ hooks+inbox │ │ API / steer │ │ async/hooks │ │ HTTP/MCP/A2A │
└──────┬──────┘ └──────┬──────┘ └──────┬──────┘ └──────┬───────┘
       └───────────────┴───────────────┴───────────────┘
                         │ unix JSON lines (0600)
                  ┌──────┴────────┐
                  │ agentbusd     │ 注册表 + 收件箱 + 回执
                  │ claim/ack     │ state.json 原子落盘 + 环形日志
                  │ native push   │ 重试 + 普通 hook 降级
                  └───────────────┘
```

- **daemon**：会话、命名、存活、收件箱、占用、回执、最近日志；
- **CLI**：`who/send/ask/reply/receipt/inbox/log/doctor` 与生命周期入口；
- **setup**：为 Claude、ZCode、Kimi、DSH、Codex 幂等接线，并安装共享 skill；
- **原生投递**：短占用让一批消息对其他消费者暂时不可见。成功记为 accepted；
  失败释放占用，回到普通 hook 梯子；
- **web / A2A / MCP**：同一总线的不同视图，不另存消息；
- **plugin 包**：为 Claude Code 与 ZCode 打包 hooks + skill。

## 4. 会话身份与生命周期

会话 ID 来自 `$AGENTBUS_SESSION_ID`、Claude 会话环境变量或 hook `session_id`，
统一为 `sid:<id>`。hook 写入私有 pid→sid 映射，让没有会话环境变量的 Bash 子进程
也解析到同一信箱。映射按工作区隔离，支持父目录与旧格式回退，避免共享宿主 PID
的多个会话都变成最后写入者。最后兜底是 `/proc` 祖先链得到的
`pid:<pid>:<starttime>`。

名字规范化为 `[a-z0-9-]{1,32}`，冲突追加 `-2` 等。Linux 用 PID + 启动时间判断
存活，其他目标使用 6 小时 TTL。离开的名字保留 1 小时墓碑。

presence 与 activity 分离：`working` 表示在 turn 内，`idle` 表示仍在 prompt 等待。
prompt/tool/turn-start 上报 working，Stop/Interrupt/StopFailure 上报 idle。超过
2 小时的 working 视为已遗弃。`who`、`peers` 与开局名册默认只列 working；`--all`
显示仍可寻址的空闲会话。

## 5. 消息模型与投递

消息为 `{messageId, from, to, kind, body, ts, replyTo, recipients, context}`。
`context` 记录发送时的 cwd、repo、branch、status，让接收者只加载必要文件，在现有
计划上调整，而不是重新开始。

| kind | 语义 | 可唤醒空闲会话 |
|---|---|---|
| `chat` | 普通对端消息 | 否 |
| `request` | 期待回答 | 定向：是；广播：否 |
| `reply` | 由 `replyTo` 关联的答复 | 否 |
| `notice` | 单向通知 | 否 |
| `urgent` | 真实中断 | 是 |

`ask --wait N` 按 sender + `replyTo` 关联答复；仅为兼容旧 agent 接受空 replyTo。
同一 peer 的 notice/chat 绝不会被误吞为答案。`--wait 0` 立即返回，答复走正常
异步投递。

`agentbus reply` 从已接收历史、未 ack 收件箱或持久化最近日志中选择最新的可寻址
消息。人类 `user` / `user-web` 是虚拟收件人，由看板/日志承载，没有进程信箱。

### 投递梯子

| 路径 | 活动 turn | 已经空闲 |
|---|---|---|
| Claude 原生 inbox | 工具调用之间，按序进入 | request/urgent 原生开新 turn |
| Kimi Server API | `POST …/steer` | request/urgent 走幂等 `POST …/prompts` |
| DSH watcher | 长轮询 + `agent.steer` | request/urgent 同样 steer |
| Codex 异步 hook | 结果进入下一次模型请求 | Stop 使 watcher 失效，不唤醒 |
| 普通 hooks | prompt、Pre/PostToolUse、失败事件 | request/urgent 通过 Stop 延续 |

Kimi 工具事件是 observation-only：只刷新 presence 与原生配置，不消费 stdout 无法
展示的消息。`TurnStarted` 覆盖 API turn 与后台续跑；`SessionHeartbeat` 刷新发现。

已接收批次保留最近 5 条供复读。原生接收与 hook 注入都会生成逐收件人回执，可用
`agentbus receipt` 与 `/api/state` 查看。

## 6. 占用与故障恢复

原生推送流程：

1. 原子占用当前可见批次，最长 30 秒；
2. 以 6 秒外层超时执行推送；
3. 成功后移出收件箱，记录 `accepted` 与 driver；
4. 失败则释放占用，记录 `queued` 与截断错误。退避期间普通 hook 仍可投递。

其他 poll 看不到已占用消息。daemon 重启不恢复占用，并把持久化的 `pushing` 回执
改回 `queued`，所以崩溃可能重复投递，但不会静默确认。这是 at-least-once，不是
exactly-once。原生 token 仅存在于 wire：hook 一次性传给 daemon，绝不写入
`state.json` 或经 session API 返回。Claude 在 daemon 重启后需等下一次 hook 刷新
token 才恢复原生推送；期间消息安全排队。

## 7. Web 看板与 A2A

loopback 看板按 repo → worktree 分组，展示活动状态、原生 driver、广播/单点与
待回复，同时显示 queued/pushing/accepted 回执。“已接收未回”取代旧版容易误解的
“已读未回”。未读 pending 可删除，已接收历史不可改写。

- `GET /api/state`、`POST /api/send`、`POST /api/delete`；
- A2A Agent Card 与 `/a2a/<name>` 的 JSON-RPC `message/send`；
- 所有入口共享总线与可选 bearer token；
- 绑定失败只关闭 web，不影响 unix 总线。

## 8. MCP facade

`agentbus mcp` 通过 stdio JSON-RPC 暴露 who/announce/send/ask/inbox/receipt，
供无 hooks 的 Harness 使用。支持原生推送或生命周期 hooks 时优先使用后者，因为
MCP 工具 schema 会常驻上下文。

## 9. 防风暴

- 每 sender 20 条/分钟；
- 相同 from/to/body 60 秒去重；
- 默认只有 urgent 与定向 request 唤醒空闲会话；
- reply 与广播不会形成自动唤醒循环；
- ZCode 限制 Stop 延续，skill 建议每主题最多约 3 个来回；
- 有序原生 steering 不取消现有工具调用。

## 10. 可靠性

- 首次使用在文件锁下自动拉起 daemon；
- state 快照采用 tmp+rename 与 300ms 防抖；
- 未知会话在下一次 hook 静默重注册；
- hook 错误记录后 fail-open，不破坏宿主；
- 正文 8KB、收件箱 200、复读 5、最近日志 300；
- 原生推送超时、占用与降级均有上界；
- `agentbus doctor`、回执和看板暴露降级状态。

## 11. 安全模型

AgentBus 是单用户可信总线。unix socket 为 0600，同 uid 进程按约等于 ssh-agent
的信任级别处理。HTTP 默认只绑 loopback，可用 `AGENTBUS_WEB_TOKEN` 加 bearer
校验。Claude/Kimi 原生凭据只走 loopback，不进入日志/state/session API；自定义
Kimi URL 必须是 loopback HTTP。

peer 消息是协作信息，不是用户授权，不能批准权限、改写 Harness 配置，或证明破坏性/
越界操作合理。sender 名字是协作句柄，不是密码学身份。

## 12. 集成矩阵

| Agent | 接入 | 工作中 / 空闲投递 |
|---|---|---|
| Claude Code | 原生 inbox + hooks + skill，或插件 hooks | 工具间原生投递 / request 唤醒；hooks 降级 |
| Kimi Code | Server API + 生命周期 hooks + AGENTS.md + skill | steer / 幂等 prompt；hooks 降级 |
| DSH | 原生 Cordis 插件 + watcher | 长轮询 steer / request 唤醒 |
| Codex | 生命周期 hooks + 异步 watcher | 下一模型请求 / Stop 后不唤醒 |
| ZCode | 配置 hooks 或插件包 | 下一安全 hook / Stop 后不唤醒 |
| 人类 / 外部 | 看板、HTTP、A2A、MCP | 立即写入总线；投递取决于目标 |

## 13. 失败模式

| 场景 | 行为 |
|---|---|
| 原生 API 不可用 | queued 回执含错误，有限重试，hooks 降级 |
| daemon 被杀 | 重启恢复 state，占用重置为 queued |
| Harness 关闭 | PID/TTL 回收与名字墓碑 |
| 重复集成 | 占用/ack 防止原生与 hook 重复；不要同时安装两套 hook bundle |
| `ask` 超时 | 退出 1 并继续；迟到的答复仍可读取 |
| `ask --wait 0` | 排队即成功，答复异步到达 |
| web 端口占用 | 仅 web 被禁用 |

## 14. 未来方向

- 比 reply/pending 更丰富的处理确认；
- 采纳率与投递延迟指标；
- channels/topics 与更强的 worktree 冲突提示；
- 带真实鉴权的跨机器传输；
- 超过 300 条环形日志的搜索与历史回放。
