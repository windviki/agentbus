# 协议与接口参考

设计动机见 [design.md](design.md)；安装接线见 [integrations/](integrations/)。

## 1. 身份与命名

| 概念 | 规则 |
|---|---|
| 会话键 | 来自 hook JSON、`$CLAUDE_SESSION_ID`、`$CLAUDE_CODE_SESSION_ID` 或 `$AGENTBUS_SESSION_ID` 的 `sid:<uuid>`；按工作区隔离的 pid→sid 映射（支持父目录与旧格式回退）覆盖 Bash 子进程；最后兜底 `pid:<pid>:<starttime>` |
| 名字 | 自动或指定；规范化为 `[a-z0-9-]{1,32}`；冲突追加 `-2` 等 |
| 名字墓碑 | 离开后保留 1h |
| 人类 | 无会话身份时为 `user`；看板默认 `user-web` |

## 2. CLI

```
agentbus hook [--async-watch]           # 生命周期入口；异步模式供 Codex 使用
agentbus setup [--yes] [--agents claude,zcode,kimi,dsh,codex|all|none]
                                        # [--dry-run] [--uninstall] [--json]
agentbus register [--name bob] [--agent dsh] [--cwd DIR] [--print-name] [--json]
agentbus who [--all] [-v|--json]       # 正在工作；--all 含空闲
agentbus peers [--cwd DIR] [--all] [--json]
agentbus state working|idle
agentbus announce "<status>"
agentbus send [--json] <name|all> "<msg>" [--kind chat|request|reply|notice|urgent]
                                        # [--reply-to ID]
agentbus receipt <message-id> [--json] # 逐收件人投递回执
agentbus ask <name> "<question>" [--wait 120] # --wait 0 = 异步
agentbus reply "<msg>" [--to <name>]
agentbus notify <name|all> "<info>"
agentbus inbox [--wait 60] [--peek] [--json]
agentbus leave
agentbus log [-n 20]
agentbus primer
agentbus mcp
agentbus status | doctor
agentbus serve [--detach] | stop
```

Go flag 放在位置参数之前。`--session <key>` 可覆盖身份。

## 3. Hook 入口

输入：

```json
{ "hook_event_name": "PostToolUse", "session_id": "uuid", "cwd": "/repo" }
```

| 事件 | 状态 | 行为 / 输出 |
|---|---|---|
| `SessionStart` | idle | 注册 + 原生端点；支持时注入 primer + 名册 |
| `UserPromptSubmit` | working | poll(ack)；消息以 `additionalContext` 输出（Kimi 为纯文本） |
| `PreToolUse`、`PostToolUse`、`PostToolUseFailure`、`PostToolBatch` | working | 同上；Kimi 工具事件 observation-only，只 peek |
| `TurnStarted` | working | Kimi/API turn 开始；peek + 刷新端点，无输出 |
| `Stop` | idle | 先 peek，只对可唤醒消息消费并继续（`decision:block`；Kimi exit 2 + stderr） |
| `StopFailure`、`Interrupt` | idle | 标记 idle，不强制续跑 |
| `SessionHeartbeat` | 不变 | 刷新 presence/原生发现，不消费 |
| `SessionEnd` | — | 静默 leave |

`agentbus hook --async-watch` 只用于 Codex 的异步 `UserPromptSubmit` hook。
它以 `active_only=true` 长轮询最多 540 秒；Stop/Interrupt 使其空返回且不消费。
活动 turn 中收到消息时，`additionalContext` 进入下一次模型请求。异步完成不会
唤醒空闲 Codex。

DSH 插件内部 `Watch` 事件接受 `agentbus_state` 与 `wait_ms`。工作中使用
`active_only`；空闲使用 `wake_only`，只返回 urgent/定向 request。插件将返回的
reason 映射为 `agent.steer`。

## 4. Unix socket 协议

`$AGENTBUS_HOME/bus.sock`，0600，每行一个 JSON-lines 请求/响应。

请求字段：`op, id, want, agent, cwd, repo, branch, pid, pid_start, status,
state, delivery, to, from, body, kind, reply_to, wait_ms, ack, wake_only,
wake_policy, active_only, claim, driver, error, msg_id, n`。

| op | 关键字段 | 语义 |
|---|---|---|
| `ping` | — | daemon 存活 |
| `register` | `id, want, agent, cwd, state?, delivery?` | 注册/刷新；返回名字与其他会话 |
| `announce` | `id, status` | 更新公告 |
| `send` | `to, body, kind, reply_to`（`id` 或 `from`） | 排队；返回 `msg_id`、`recipients`、`dup` |
| `ask` | `id, to, body, wait_ms` | 定向 request；正数等待关联答复，0 排队即返回 |
| `reply` | `id, body, to?` | 回复最新可寻址消息并带 replyTo |
| `poll` | `id, ack, wait_ms, state?, delivery?, wake_only?, active_only?, claim?` | peek/消费/占用；已接收消息进入 5 条复读区 |
| `complete` | `id, claim, driver, error?` | 内部：接受或释放原生占用 |
| `receipt` | `msg_id` | `receipts{recipient: {state, driver, error, updated_at}}` |
| `state` | `id, state` | 显式上报 working/idle |
| `mine` | `id` | 最近已接收消息 |
| `who` / `status` | — | 全部存活会话，含推导的 `working` 与可选原生 `delivery` |
| `leave` | `id` | 离开并进入墓碑 |
| `log` | `n` | 最近环形日志，新的在前 |
| `shutdown` | — | 停止 daemon |

回执状态为 `queued`、`pushing`、`accepted`。`accepted` 仅表示 hook 或 Harness
API 接受批次，不表示模型已处理。占用不持久化；恢复后 pushing 回执改回 queued。

wire 中的 `delivery` 为 `{driver, address?, native_id?, wake?, token?}`。token 只允许
出现在这个请求里；session 对象、`/api/state` 与 `state.json` 均不序列化它。
`wake` 为 `request`（默认）、`urgent` 或 `all`。

活动推导：显式 working 可信 2 小时，idle 即空闲；未上报的会话只在最近 10 分钟
出现过时算 working。

限制：正文 8KB、收件箱 200、复读 5、最近日志 300、每 sender 20 条/分钟、60 秒
去重、原生占用 30 秒。

## 5. 消息模型

```json
{
  "messageId": "m17a...-3",
  "replyTo": "m17a...-2",
  "from": "bob",
  "to": "hazel-yorkie",
  "kind": "request",
  "recipients": 1,
  "body": "split files or tests?",
  "ts": 1730000000,
  "context": {"cwd": "/repo", "repo": "/repo/.git", "branch": "main", "status": "refactoring tests"}
}
```

广播 `to="all"` 向除 sender 外的每个在线会话各投一份，各自独立占用与回执。
`user` 与 `user-web` 是全局日志/看板中的虚拟人类收件人。`ask` 只消费来自目标
peer 且 `replyTo` 匹配的答复（空 replyTo 仅为旧版兼容）；其他消息保留排队。

Kimi 的 `client_type=kimi_code_cli` 选择纯文本 prompt 输出与 exit-2 Stop 延续。
工具事件仍是 observation-only，不消费消息。

## 6. 原生 Harness 端点

### Claude Code

hook 注册 `CLAUDE_CODE_MESSAGING_SOCKET` 与 `CLAUDE_CODE_MESSAGING_TOKEN`。
daemon 向每会话 unix socket 写入 auth frame 与有序 `priority=next` user frame，
并用 `session_id` 防止过期目标误投。Claude 在工作中于工具调用之间接收，空闲时
只允许 request/urgent 开新 turn。socket 写成功仅表示传输接收；目标 inbound
hold/refuse 策略仍然有效。

### Kimi Code

daemon 从 `$KIMI_CODE_HOME/server/instances` 发现存活 loopback 实例（桌面优先，
再按最新启动排序），并读取 `$KIMI_CODE_HOME/server.token`。活动会话走
`POST /api/v1/sessions/<id>/steer`；允许的空闲投递走带稳定 AgentBus `prompt_id`
的幂等 `POST …/prompts`。可用 `AGENTBUS_KIMI_URL` / `AGENTBUS_KIMI_TOKEN` 覆盖，
经 hook 传递；URL 必须是 loopback HTTP。没有存活 server 时降级普通 hooks。

### DSH / Codex / ZCode

DSH 使用插件长轮询 + `agent.steer`。Codex 使用异步 hooks，不再向活动 thread 另挂
一个 app-server。ZCode 使用生命周期 hooks。这样避免多个 app-server 同时拥有一个
会话。

## 7. HTTP 看板与 A2A

监听 loopback `127.0.0.1:${AGENTBUS_WEB_PORT:-8787}`；可选 bearer token 对所有
入口生效。

| 入口 | 说明 |
|---|---|
| `GET /` | 内嵌看板 |
| `GET /api/state` | `{ok, version, uptime, now, web_identity, sessions[], messages[], unread, receipts}` |
| `POST /api/send` | `{to, body, kind, from, replyTo?}` → `{ok, messageId, recipients, dup}` |
| `POST /api/delete` | 删除未读 pending 副本；已接收历史返回 409 |
| `GET /.well-known/agent-card.json` | 根 Agent Card |
| `GET /a2a/<name>/.well-known/agent-card.json` | 会话 Agent Card |
| `POST /a2a/<name>` | A2A JSON-RPC `message/send`，仅 text parts |

`receipts` 形状为 `{messageId: {recipient: {state, driver, error, updated_at}}}`。
A2A 排队后同步返回，不代表模型已处理。streaming/tasks/artifacts/push notification
不实现。

## 8. MCP（stdio）

协议 `2025-06-18`，换行分隔 JSON-RPC：initialize、ping、tools/list、tools/call。

| 工具 | 参数 | CLI |
|---|---|---|
| `agentbus_who` | `all?` | `who` |
| `agentbus_announce` | `status` | `announce` |
| `agentbus_send` | `to, body, kind?` | `send` |
| `agentbus_ask` | `to, body, wait_seconds?` | `ask` |
| `agentbus_inbox` | `peek?, wait_seconds?` | `inbox` |
| `agentbus_receipt` | `message_id` | `receipt` |

身份由 `/proc` 祖先链决定；同一 agent 进程的工具调用共享信箱。

## 9. 环境变量

| 变量 | 默认 | 作用 |
|---|---|---|
| `AGENTBUS_HOME` | `~/.agentbus` | 状态目录 |
| `AGENTBUS_AGENT` | 探测 | 强制 agent 标签 |
| `AGENTBUS_NAME` | 自动 | hook 注册期望名字 |
| `AGENTBUS_SESSION_ID` | 探测 | 身份覆盖 |
| `AGENTBUS_PRIMER` | 开 | `0` 不注入会话契约 |
| `AGENTBUS_WAKE` | `request` | `urgent` 仅 urgent；`all` 任意消息可唤醒 |
| `AGENTBUS_PUSH` | 开 | `0` 禁用原生推送注册，保留 hooks |
| `AGENTBUS_KIMI_URL` | 发现 | Kimi Server API loopback base URL |
| `AGENTBUS_KIMI_TOKEN` | 发现 | Kimi API token 覆盖（仅 wire） |
| `KIMI_CODE_HOME` | `~/.kimi-code` | Kimi 实例/token 发现根目录 |
| `AGENTBUS_WEB` | 开 | `0` 关闭看板/A2A |
| `AGENTBUS_WEB_PORT` | `8787` | web 端口 |
| `AGENTBUS_WEB_TOKEN` | 空 | HTTP bearer token |
| `AGENTBUS_BIN` | 取决于 launcher | npm launcher/DSH 插件使用的二进制 |
