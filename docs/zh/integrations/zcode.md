# ZCode 集成

ZCode 支持与 Claude Code 相同的 hook 事件（`SessionStart`、
`UserPromptSubmit`、`PostToolUse`、`Stop` —— 但**没有** `SessionEnd`），
另有两处 ZCode 特有细节：配置文件里的 hook 位于 `~/.zcode/cli/config.json`
的 `hooks.events.*` 下，并且**必须**通过 `hooks.enabled: true` 启用；插件形式
的 hook（`hooks/hooks.json`）会自行注册，无需开关。

## 1. 安装与接线（自动化）

```bash
npm install -g agentbus-cli
agentbus setup --agents zcode
```

源码 checkout 请改用 `./scripts/install.sh --agents zcode`。setup 步骤会把
skill 安装到 `~/.agents/skills/agentbus/`（ZCode 会扫描这个共享目录；不会
复制到 `~/.zcode/skills`，以免目录里出现重复条目），并把四个 hook 事件合并
进 `~/.zcode/cli/config.json`，同时设置 `hooks.enabled: true`（原文件保留为
`config.json.agentbus-bak`）。之后请新开一个 ZCode 会话；运行中的会话不会
重新加载 hook。

## 2. 备选方案（任选其一）

### A. 插件包（源码 checkout）

```bash
./scripts/install.sh --plugin     # 刷新内置 skill，打印路径
```

在 ZCode 中：**Settings → Plugin Management → add local marketplace**
`<agentbus repo>/integrations/zcode`，然后安装 `agentbus`。该插件包声明了
`.zcode-plugin/plugin.json` 与 `hooks/hooks.json`，因此 hook runner 会自动启用。

### B. 把 hooks 合并进 config.json

合并进 `~/.zcode/cli/config.json`（用户级）或 `<repo>/.zcode/config.json`
（工作区级）：

```json
{
  "hooks": {
    "enabled": true,
    "events": {
      "SessionStart": [
        { "hooks": [ { "type": "command", "command": "agentbus hook", "timeoutMs": 15000 } ] }
      ],
      "UserPromptSubmit": [
        { "hooks": [ { "type": "command", "command": "agentbus hook", "timeoutMs": 5000 } ] }
      ],
      "PostToolUse": [
        { "hooks": [ { "type": "command", "command": "agentbus hook", "timeoutMs": 5000 } ] }
      ],
      "Stop": [
        { "hooks": [ { "type": "command", "command": "agentbus hook", "timeoutMs": 5000 } ] }
      ]
    }
  }
}
```

或者运行 `agentbus setup --agents zcode` 自动合并（会先备份）。
**不要同时使用 A 和 B** —— 每条消息都会投递两次。

说明：

- ZCode 的 `Stop` hook 每次 stop 最多可以请求继续三次，这恰好给任何唤醒
  循环设了上界。
- 空闲会话会被 `urgent` 以及直接发给它的 `request` 唤醒 ——
  **前提是 Stop hook 仍会触发**；chat / notice / reply 和广播 request 则
  等到下一个 prompt 或 tool 调用。用 `agentbus reply "..."` 回复（指定某个
  对端时用 `agentbus send <name> ...`）。`AGENTBUS_WAKE=urgent` 可恢复旧的
  仅 urgent 行为。
- 限制：如果 request 到达时 ZCode **已经停在 prompt 上**，不会有 hook 运行，
  消息会一直排队到下一次 prompt 或 tool 调用。ZCode 没有提供宿主级的唤醒
  API，因此对完全空闲的 ZCode 会话，`agentbus ask` 可能超时。看板会把它
  显示为 pending request；人类可以提醒该会话，或者对端先继续工作，之后用
  `agentbus inbox --peek` 读取答复。DSH 的原生插件确实有 idle steer 路径，
  因此 DSH 目标能在约 5s 内被唤醒。
- `${CLAUDE_SESSION_ID}` 会作为环境变量注入到 hooks 中 —— `agentbus hook`
  会自动读取它。
- 存活状态：ZCode 的 node 进程通过 `/proc` 祖先关系识别；进程退出即回收
  会话，无需任何显式 logout（不存在 `SessionEnd` 事件）。
- `SessionStart` hook 默认注入会话契约（`AGENTBUS_PRIMER=0` 可关闭）；
  理由见 [../adoption.md](../adoption.md)。
- 看板位于 `http://127.0.0.1:8787/`（在 code-server 后面是 `/proxy/8787/`）；
  `agentbus status` 会打印实际 URL。

## 从 Bash 工具获取身份

ZCode 不会把会话 id 导出给 Bash 工具子进程，所以 agent 运行的
`agentbus send/reply` 本来会看起来像人类（“user”）。hook 会在每个事件上把
`agent process pid -> sid` 记录到 `$AGENTBUS_HOME/pidmap/` 下；CLI 命令通过
回溯到那个祖先进程并读取映射来确定身份（用 pid 启动时间防止复用）。已完成
端到端验证：发给一个在线 ZCode 会话的 request 由 hook 投递，agent 的
`agentbus reply` 以 `zprobe3→zpeer: z-ack-9` 出现，`replyTo` 正确。

## 验证与 headless 注意事项

本机已验证的内容（2026-10-29，ZCode 3.15/3.16 桌面版 bundle）：

- `zcode -p` 会话注册为在线的 `zcode` 会话，并出现在 `agentbus who` 中；
- 会话运行期间排队的对端 `request` 会被注入，模型通过 `agentbus reply`
  回复；
- 一个中性任务（“create notes.txt”）仍然会让模型检查目录，并对它在注入
  名册里看到的对端会话进行推理。

注意事项：**裸** `zcode -p` 进程即使桌面应用已配置模型，也可能打印
`Select a model before sending a message`。这是 CLI 的限制：桌面账户的
provider 对独立 CLI 不可见，除非有可用的模型/provider 选择。它不影响桌面
会话（桌面会话会提供该选择），也与 agentbus 无关。做 headless 冒烟测试时，
可以临时启用 `~/.zcode/v2/provider_config.json` 里的 API-key provider
（测试后恢复）。

## 排障：Bash 工具调用一直处于 “pending”

ZCode 对一条 assistant 消息里发出的多个工具调用可能以受限的并发度执行。
如果某个 `agentbus send ...` 的 Bash 调用显示 `pending`，而它所属会话仍有
`Agent` / `SendMessage` 部分处于 `running` 状态，那么该命令**尚未开始** ——
还没有对应的 OS 进程，也不会有任何内容到达 daemon。`agentbus send` 一旦
真正运行，几毫秒内就会完成。

诊断：检查该会话的 tool parts（例如在 ZCode 的会话数据库里），看同一条
消息中是否还有别的工具调用在运行；如果其中一个是耗时的 subagent，就等它
结束（或中断该回合），再在单独的回合里发送状态。这是 ZCode 的工具调度，
不是 agentbus 卡住了。
