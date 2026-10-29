# 测试与验证

## 1. 测试分层

| 层 | 位置 | 验证什么 | 依赖 |
|---|---|---|---|
| 单元 | `internal/bus/*_test.go` | 注册/命名/投递/回收/持久化/限速/去重/kind/ask/reply | Go |
| 端到端 | [test/e2e.sh](../test/e2e.sh) | 真 daemon + 双会话 hook JSON + Web + A2A + MCP + 重启 | Go、curl |
| 插件结构 | `claude plugin validate` / `plugin details` | 插件清单、hooks 是否被宿主接受 | Claude Code |
| 互操作 | [test/interop.sh](../test/interop.sh) | 真实 dsh ↔ claude 会话能否收发并作出反应 | dsh、claude、额度 |

## 2. 单元测试

```bash
go test ./...            # 全部
go test ./internal/bus -v
go test -race ./...      # 并发/竞态（可选，较慢）
```

## 3. 端到端（e2e）

```bash
./test/e2e.sh
```

脚本会在 `mktemp` 的 `AGENTBUS_HOME` 中自起 daemon（看板端口默认 18787，
可用 `AGENTBUS_WEB_PORT` 覆盖），退出时停止 daemon，不碰真实 `~/.agentbus`。
覆盖场景：

| 段 | 断言 |
|---|---|
| build | 二进制可构建 |
| daemon | 首次使用自动拉起，`status` 通告 web 地址 |
| SessionStart | 注册、名字、名册包含对端状态、会话契约（primer） |
| 投递 | PostToolUse 注入、ack 后不重复投递 |
| 投递梯子 | chat 不唤醒；urgent 唤醒并 flush 队列 |
| ask | 请求-答复往返，答复被消费不重复投递 |
| reply | 无需记 id，回复最近一条对端消息（kind=reply + replyTo） |
| notify | 单向通知、无回复语义 |
| 错误 | 未知收件人报错并给建议 |
| Web | `/api/state`、`/api/send`、内嵌页面与相对路径 |
| A2A | Agent Card、`message/send` 投递、未知方法 `-32601` |
| MCP | initialize / tools/list / tools/call 走真实总线 |
| 运维 | log、leave + 墓碑、PID 回收、重启持久化、人类以 `user` 发送 |

编写测试的注意点：

- 不要把 `cmd | grep -q` 直接串起来：`grep -q` 提前退出会让 Go 进程收到
  SIGPIPE，配合 `pipefail` 产生偶发 141；先捕获变量再用 `<<<` 匹配。
- 用 `--session sid:...` 构造确定身份；测试 daemon 用临时 HOME 与独立端口。

## 4. 插件包校验

```bash
claude plugin validate integrations/claude-code
claude plugin validate integrations/zcode
claude --plugin-dir "$PWD/integrations/claude-code/plugin" plugin details agentbus
```

期望 `plugin details` 显示 1 skill、5 hooks（SessionStart / UserPromptSubmit /
PostToolUse / Stop / SessionEnd），且 `Status: ✔ loaded`。

ZCode 没有可脚本化的 CLI，插件清单按 `.zcode-plugin/plugin.json` 规范编写；
本机只能做 JSON/结构校验与人工加载验证。

## 5. 互操作实测（dsh ↔ claude）

脚本：[../test/interop.sh](../test/interop.sh)。

```bash
./test/interop.sh                                # 预检，不花额度
RUN_INTEROP=1 ./test/interop.sh                  # DSH 接收 + Claude 接收
RUN_INTEROP=1 RUN_INTEROP_BASELINE=1 ./test/interop.sh   # 再加无 primer 基线
```

前置：`go`、`dsh`、`claude`、`python3`、`curl`；Claude 的本地路由要可达
（`ccr start`，默认 `127.0.0.1:3456`）；DSH 模型凭据来自
`DEEPSEEK_API_KEY` 或 `~/.dsh/.credentials.yaml`（脚本只读取、不打印）。

脚本在临时 `AGENTBUS_HOME` 中：

1. 起 daemon + 一个 `interop-sender` 会话；
2. **DSH**：`dsh headless --patch` 加载 `integrations/dsh/plugin`，等它注册
   后发一条 `request`，断言总线日志出现 `dsh-probe→…: dsh-ack-42`
   以及模型输出 `RECEIVED`；
3. **Claude**：写入只含 hooks 的 `--settings`，预注册 session 并预置
   `request`，运行 `claude -p "Say hello …"`，断言契约已注入且出现
   `claude-probe→…: claude-ack-7`；
4. **基线**（可选）：`AGENTBUS_PRIMER=0` 跑同一场景，只记录“回复/忽略”，
   不作为失败条件；
5. 结束停 daemon，保留日志目录并打印路径。

最近一次结果（2026-10-29）：DSH 插件、Claude primed 全部 PASS；无 primer
基线忽略 peer 消息。行为分析见 [adoption.md](adoption.md) §6。

### 长程并发场景（半自动，人工核对）

双 agent 并发任务不确定性高，没有固定断言脚本；按以下步骤复现，结果写入
[adoption.md](adoption.md) §6。

1. 建共享仓库：`TASK.md` 要求 calc + words 两半，且都要改
   `toolkit/__init__.py` 与 `README.md`；
2. 起 daemon；先起 DSH words 半（`DSH_PERMISSION_MODE=danger-full-access`），
   等它注册；再起 Claude calc 半（`--plugin-dir`），prompt **不提 agentbus**；
3. 运行中向两边各发一条 `agentbus send --from user --kind urgent <name>
   "reply 'ack-running' then continue"`；
4. 结束后跑 `python3 -m unittest discover -s tests`，核对 `__init__.py`/README
   内容与 `agentbus log`。

最近结果（2026-10-29）：双方自动 `who`/`announce`；DSH 发 2 条 request、Claude
3 条 reply（含分工协商与两条 urgent ack）；21 tests OK；无文件覆盖。过程中发现
并修复了 Claude Bash 身份、人类回复、flag 误用三类问题（见 adoption.md）。

## 6. ZCode hooks 实测（2026-10-29）

安装：`./scripts/install.sh --zcode-hooks`。用 `zcode -p`（桌面 bundle 的
`server-3.16/glm/zcode.cjs`）在 `/tmp/ab-zrepo` 起真实会话：

- SessionStart hook 注册成功，`agentbus who` 出现 `zcode` 会话；
- 会话运行中从 peer 发 `request`，hook 注入后模型执行 `agentbus reply`，
  总线日志为 `zprobe3→zpeer: z-ack-9`（带 replyTo）；
- 身份修复：ZCode 不给 Bash 传 session id，靠 hook 写入的 pid→sid 映射解析
  （见 [protocol.md](protocol.md) 身份表）；
- 中性任务（不提 agentbus）下模型会读取注入的名册并判断同目录 peer 冲突，
  但未主动 `announce`。

注意：裸 `zcode -p` 可能报 `Select a model before sending a message`
（桌面账号 provider 对独立 CLI 不可见），这是 CLI 限制；测试时临时启用
API-key provider 并还原，详见 [integrations/zcode.md](integrations/zcode.md)。

## 7. Kimi hooks 契约验证（2026-10-29）

本机 Kimi 7 天额度用尽（429），无法起真实模型会话；用真实 hook payload 验证：

```bash
printf '%s' '{"hook_event_name":"SessionStart","session_id":"kimi-sim-2",
  "cwd":"/tmp/ab-zrepo","client_type":"kimi_code_cli"}' | agentbus hook
# → 注册为 kimi 会话
printf '%s' '{"hook_event_name":"UserPromptSubmit",...}' | agentbus hook
# → 纯文本 peer 消息（Kimi 拼入上下文）
printf '%s' '{"hook_event_name":"Stop",...}' | agentbus hook
# → 有 urgent 时 exit 2，reason 在 stderr
```

安装见 [integrations/kimi.md](integrations/kimi.md)。额度恢复后用
`kimi -p "..."` 做一次 live smoke test 即可。

## 8. 看板显示 0 会话的排查

若 `agentbus who` 有会话但看板/API 为空：

```bash
curl -s http://127.0.0.1:8787/api/state | jq '.sessions|length'
# 找到占用 8787 的进程，核对它的 AGENTBUS_HOME
ss -ltnp | grep 8787   # 或读 /proc/net/tcp + /proc/<pid>/fd
```

常见原因：早期测试 daemon 仍占用 8787，而 `~/.agentbus` 的 daemon 绑定失败
（日志里是 `web disabled: bind: address already in use`）。停掉占用端口的
测试 daemon，再重启默认 daemon 即可。

## 9. 提交前检查清单

```bash
gofmt -l .
go vet ./...
go test ./...
./test/e2e.sh
git diff --check
```
