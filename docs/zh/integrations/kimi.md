# Kimi Code 集成

Kimi Code 使用两条互补路径：**loopback Server API** 负责原生 steer/prompt 投递，
生命周期 hooks 负责注册、名册注入、活动状态与降级。

## 安装

```bash
npm install -g @aixyzstudio/agentbus-cli
agentbus setup --agents kimi
```

源码使用 `./scripts/install.sh --agents kimi`，之后新开 Kimi 会话。setup：

- 安装 skill，并在 `~/.kimi-code/AGENTS.md` 写入会话契约；
- 在 `~/.kimi-code/config.toml` 管理 9 个 `[[hooks]]`：`SessionStart`、
  `UserPromptSubmit`、`TurnStarted`、`PostToolUse`、`Stop`、`StopFailure`、
  `Interrupt`、`SessionHeartbeat`、`SessionEnd`；
- 备份触碰文件，重复执行保持幂等。

Kimi 忽略 SessionStart stdout，所以首个 `UserPromptSubmit` 只注入一次工作区名册。
工具事件是 observation-only：只刷新 presence 与原生配置，不消费 stdout 无法展示
的消息。

## 原生 Server API 投递

daemon 从 `$KIMI_CODE_HOME/server/instances` 发现运行实例（桌面优先，再按最新
启动排序），通过 `GET /api/v1/meta` 验证，并读取
`$KIMI_CODE_HOME/server.token`。

- 工作中：`POST /api/v1/sessions/<session_id>/steer` 注入当前 turn，不取消工具；
- 空闲且为定向 request/urgent：`POST …/prompts` 使用稳定 AgentBus `prompt_id`
  开启 turn，重试幂等；
- 空闲普通消息：保留到自然边界。

没有兼容 server 可达时降级 prompt/Stop hooks。AgentBus 不会隐式再启动一个 Kimi
server。需要显式选择已有 loopback server 时：

```bash
export AGENTBUS_KIMI_URL=http://127.0.0.1:58627
export AGENTBUS_KIMI_TOKEN=...   # 已可自动发现时可省略
```

URL 必须是 loopback HTTP。token 只从 hook 传给 daemon，不写入 state，也不经 API
返回。`AGENTBUS_PUSH=0` 禁用原生注册。Kimi Server API 仍为 experimental，运行实例
的 `/openapi.json` 是对应版本的最终依据。

## Hooks、身份与回执

`UserPromptSubmit` 输出 Kimi 可注入的纯文本；`Stop` 用退出码 2 和 stderr 对可唤醒
消息续跑。`TurnStarted` 覆盖 API 与后台 turn，`StopFailure`/`Interrupt` 防止 working
状态滞留，`SessionHeartbeat` 刷新原生发现。

Bash 没有 session id 环境变量，hook 在 `$AGENTBUS_HOME/pidmap` 保存私有 pid→sid
映射，CLI 通过祖先进程解析身份。

`accepted` 只表示 Kimi API 接受 steer/prompt，不表示模型已行动。传输状态看
`agentbus receipt`，处理状态看 reply/pending。

官方参考：[Server API](https://www.kimi.com/code/docs/en/kimi-code-cli/reference/server-api.html)、
[hooks](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/hooks.html)、
[流式期间 steering](https://www.kimi.com/code/docs/en/kimi-code-cli/guides/interaction.html)。
