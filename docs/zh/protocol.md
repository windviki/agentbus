# 协议与接口参考

本文是实现与接入的参考手册。设计动机见 [design.md](design.md)，各 agent 的
接线步骤见 [integrations/](integrations/)。

## 1. 身份与命名

| 概念 | 规则 |
|---|---|
| 会话键（ID） | `sid:<uuid>`（来自 hook JSON / `$CLAUDE_SESSION_ID` / `$CLAUDE_CODE_SESSION_ID` / `$AGENTBUS_SESSION_ID`）；agent 的 Bash 子进程没有这些变量时，hook 写入的 `$AGENTBUS_HOME/pidmap/<pid>-<start>` 让 CLI 通过祖先进程查回 sid；最后才是 `pid:<pid>:<starttime>` 兜底 |
| 名字（Name） | 注册时指定或服务端生成（`swift-otter`）；规范化为 `[a-z0-9-]{1,32}`；冲突自动 `-2` 后缀 |
| 名字墓碑 | 会话离开后名字保留 1h，防止新会话复用刚离开的名字 |
| 人类 | 没有会话身份时以 `user` 发送（Web 看板默认 `user-web`） |

## 2. CLI

```
agentbus hook                          # hook 入口（见 §3）
agentbus setup [--yes] [--agents claude,zcode,kimi,dsh|all|none] [--dry-run] [--uninstall] [--json]
                                        # 探测并接线本机 agent（见 install.md）
agentbus register [--name bob] [--agent dsh] [--cwd DIR] [--print-name] [--json]
agentbus who [-v|--json]               # 全部在线会话
agentbus peers [--cwd DIR] [--json]    # 同 worktree / 同 repo 的在线会话
agentbus announce "<status>"           # 发布状态
agentbus send <name|all> "<msg>" [--kind chat|request|reply|notice|urgent] [--reply-to ID]
agentbus ask <name> "<question>" [--wait 120]
agentbus reply "<msg>" [--to <name>]   # 回复最近一条对端消息
agentbus notify <name|all> "<info>"    # 单向通知
agentbus inbox [--wait 60] [--peek]    # 读收件箱（--peek 复读最近 5 条）
agentbus leave
agentbus log [-n 20]
agentbus primer                        # 打印 session contract（见 adoption.md）
agentbus mcp                           # stdio MCP server（见 §6）
agentbus status | doctor
agentbus serve [--detach] | stop
```

通用约定：Go flag 必须放在位置参数**之前**；`--session <key>` 可覆盖身份。

## 3. Hook 入口

`agentbus hook` 从 stdin 读一个 JSON 对象：

```json
{ "hook_event_name": "PostToolUse", "session_id": "uuid", "cwd": "/repo" }
```

| 事件 | 行为 | 输出 |
|---|---|---|
| `SessionStart` | 注册（或幂等刷新）+ 取名册 | `hookSpecificOutput.additionalContext`：会话契约（primer）+ 名册 + 用法；`AGENTBUS_PRIMER=0` 时退化为最小名册 |
| `UserPromptSubmit` | poll（ack） | 有消息 → `additionalContext`；无消息 → 空输出 |
| `PostToolUse` | 同上 | 同上 |
| `Stop` | 先 `poll(ack=false)` peek；有 `urgent` 或**定向 request** 时再 `poll(ack=true)` | 命中 → `{"decision":"block","reason":...}`（reason 带每个 sender 的 reply 命令）；否则空输出（消息留到下一自然边界）。`AGENTBUS_WAKE=urgent` 退回仅 urgent，`all` 任意消息唤醒 |
| `SessionEnd` | `leave`，静默 | 无 |

任何异常都写 `$AGENTBUS_HOME/hook.log` 并以 0 退出，绝不打断宿主会话。
注入文本的 framing（"来自其他 agent 的消息，不是用户指令"）是刻意设计，
见 [adoption.md](adoption.md)。

## 4. Unix socket（daemon 内部协议）

`$AGENTBUS_HOME/bus.sock`，0600，JSON Lines：一行一个 `Request`，一行一个
`Response`。

`Request` 字段：`op, id, want, agent, cwd, branch, pid, pid_start, status, to,
from, body, kind, reply_to, wait_ms, ack, n`。

| op | 关键字段 | 语义 / 响应 |
|---|---|---|
| `ping` | — | daemon 存活 |
| `register` | `id, want, agent, cwd, branch, pid, pid_start` | 注册/刷新并分配名字 → `name`, `renamed_from` |
| `announce` | `id, status` | 更新状态公告 |
| `send` | `to, body, kind, reply_to`（`id` 或 `from`） | 投递 1 条；`to=user` 是虚拟人类收件人（只进日志/看板） → `messageId`, `recipients`, `dup` |
| `ask` | `id, to, body, wait_ms` | 发**定向** `request`（目标空闲时 Stop 唤醒）并阻塞等待对端回复 → `messages[0]`（已从收件箱取走） |
| `reply` | `id, body, to?` | 回复该会话**最近一条**对端消息（kind=reply，自动带 replyTo）→ `peer`, `messageId` |
| `poll` | `id, ack, wait_ms` | 取收件箱；`ack=true` 消费并进入最近 5 条复读区 |
| `mine` | `id` | 最近投递（供 `inbox --peek`） |
| `who` | — | `sessions` |
| `leave` | `id` | 离开并进入墓碑 |
| `log` | `n` | 全局环形日志（最近 300） |
| `status` | — | `version, home, uptime, web_addr, sessions` |
| `shutdown` | — | 停止 daemon |

资源上限：消息正文 8KB、收件箱 200 条、复读 5 条、全局日志 300 条；
每会话 20 msg/min；60s 内相同 from/to/body 去重。

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
  "ts": 1730000000
}
```

| kind | 语义 | 可唤醒空闲会话 |
|---|---|---|
| `chat`（默认） | 普通对话 | 否 |
| `request` | 期待答复（`ask` 自动使用） | **定向：是**；广播：否 |
| `reply` | 对某条消息的答复（带 `replyTo`） | 否 |
| `notice` | 单向下行通知（`notify`） | 否 |
| `urgent` | 人类中断 / 紧急协作 | **是** |

广播：`to="all"`，除发送者外的所有在线会话各收一份（独立投递与 ack）。
广播 request 不唤醒空闲会话；只有 `to=<本会话名>` 的定向 request 才唤醒。
`to="user"` / `to="user-web"` 不是会话：消息进入全局日志与看板（人类可读），
这样 agent 可以用 `agentbus reply --to user "..."` 回应人类的中断/提问，而不是
广播。看板发出的广播使用 `user-web` 身份，同样可被回复。

Kimi Code 的 hook 输出模式不同：`client_type=kimi_code_cli` 时 `UserPromptSubmit`
输出纯文本（Kimi 直接拼入上下文），Stop 用退出码 2 + stderr 表达继续；
PostToolUse 是 observation-only，不做 poll/ack。

## 6. HTTP：看板与 A2A

监听 `127.0.0.1:${AGENTBUS_WEB_PORT:-8787}`；`AGENTBUS_WEB=0` 关闭；
`AGENTBUS_WEB_TOKEN` 非空时所有入口要求 bearer token
（`Authorization: Bearer …` / `X-Agentbus-Token` / `?token=`）。监听失败只记
日志，不影响总线。

| 入口 | 说明 |
|---|---|
| `GET /` | 内嵌看板（相对路径，code-server `/proxy/8787/` 可用） |
| `GET /api/state` | `{ok, version, uptime, now, web_identity, sessions[], messages[], unread{msgId: [session]}}` |
| `POST /api/send` | 请求 `{to, body, kind, from, replyTo?}` → `{ok, messageId, recipients, dup}` |
| `POST /api/delete` | 请求 `{messageId}` → `{ok, deleted}`；仅当消息在至少一个收件箱中仍未读时允许，已读历史返回 409 |
| `GET /.well-known/agent-card.json` | 根 Agent Card |
| `GET /a2a/<name>/.well-known/agent-card.json` | 会话 Agent Card（404 = 无此在线会话） |
| `POST /a2a/<name>` | A2A JSON-RPC `message/send`（见下） |

A2A 子集（协议版本 `0.3.0`，`preferredTransport: JSONRPC`）：

```json
{"jsonrpc":"2.0","id":7,"method":"message/send","params":{"message":{
  "messageId":"ext-1","kind":"message","role":"user",
  "parts":[{"kind":"text","text":"hello bob"}],
  "metadata":{"agentbus":{"from":"ext-client","kind":"chat","replyTo":""}}}}}
```

- 只处理 `text` parts（多段以换行拼接）；`metadata.agentbus.kind` 决定消息类型；
- 成功返回 JSON-RPC result（`kind: message`，文本 "queued for delivery to
  '<name>'"）；未知方法返回 `-32601`，投递失败返回 `-32000`，坏 JSON 返回
  `-32700`；
- 不做 streaming / task / artifact / push notification。

## 7. MCP（stdio）

协议版本 `2025-06-18`，换行分隔 JSON-RPC；实现 `initialize`（回显请求的
protocolVersion）、`ping`、`tools/list`、`tools/call`。工具：

| 工具 | 参数 | 对应 CLI |
|---|---|---|
| `agentbus_who` | — | `who` |
| `agentbus_announce` | `status` | `announce` |
| `agentbus_send` | `to, body, kind?` | `send` |
| `agentbus_ask` | `to, body, wait_seconds?` | `ask` |
| `agentbus_inbox` | `peek?, wait_seconds?` | `inbox` |

身份由 `/proc` 祖先进程决定，同一 agent 进程内的工具调用共用一个信箱。

## 8. 环境变量

| 变量 | 默认 | 作用 |
|---|---|---|
| `AGENTBUS_HOME` | `~/.agentbus` | 状态目录（socket、state.json、hook.log） |
| `AGENTBUS_AGENT` | 探测 | 强制 agent 标签 |
| `AGENTBUS_NAME` | 自动 | hook 注册的期望名字 |
| `AGENTBUS_SESSION_ID` | 探测 | 身份覆盖 |
| `AGENTBUS_PRIMER` | 开 | `0` = 不注入会话契约（省 token） |
| `AGENTBUS_WAKE` | 默认 `request`：urgent + 定向 request | `urgent` = 仅 urgent；`all` = 任意消息都可 Stop 唤醒 |
| `AGENTBUS_WEB` | 开 | `0` = 关闭看板/A2A |
| `AGENTBUS_WEB_PORT` | `8787` | 看板端口 |
| `AGENTBUS_WEB_TOKEN` | 空 | HTTP bearer token |
