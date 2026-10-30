# agentbus 文档（中文）

[English documentation](../README.md) · [项目主页（中文）](../../README.zh-CN.md) · [English README](../../README.md)

按角色选择阅读路径：

| 你是 | 先读 | 再读 |
|---|---|---|
| 想快速用起来 | [安装指南](install.md) | [integrations/](integrations/) 里对应 agent |
| 想理解设计取舍 | [design.md](design.md) | [research.md](research.md) |
| 要接新 harness | [protocol.md](protocol.md) | [integrations/generic.md](integrations/generic.md)、[adoption.md](adoption.md) |
| 关心 agent 会不会真的用 | [adoption.md](adoption.md) | [design.md](design.md) §5 |
| 改代码 / 跑验证 | [testing.md](testing.md) | [protocol.md](protocol.md) |

## 目录

| 文档 | 内容 |
|---|---|
| [install.md](install.md) | npm / 源码 / 各 agent 接线 / 卸载 / 排障 |
| [design.md](design.md) | 目标、token 经济学、架构、身份与生命周期、消息模型与投递梯子、Web/A2A/MCP 边界、防风暴、可靠性、安全、失败模式、未来方向 |
| [protocol.md](protocol.md) | 参考手册：CLI、hook JSON、unix socket op、消息类型、HTTP/A2A、MCP、环境变量 |
| [adoption.md](adoption.md) | 会话如何接受这套通讯：未铺垫时的行为、session contract、信任边界、自主协商时机、实测记录 |
| [testing.md](testing.md) | 单元 / e2e / 插件 / 互操作测试矩阵与复现命令 |
| [publishing.md](publishing.md) | 发布流程：版本、交叉编译、npm 发布、GitHub Release |
| [research.md](research.md) | 现有方案调研与自建理由 |
| [integrations/claude-code.md](integrations/claude-code.md) | Claude Code：hooks 或插件包 |
| [integrations/zcode.md](integrations/zcode.md) | ZCode：配置 hooks 或插件包 |
| [integrations/dsh.md](integrations/dsh.md) | DSH：原生插件（会话注入 + 工具） |
| [integrations/kimi.md](integrations/kimi.md) | Kimi Code：hooks + 全局 AGENTS.md + skill |
| [integrations/generic.md](integrations/generic.md) | 其他 CLI：skill 或 MCP |

## 维护约定

- 英文文档是主版本（`docs/`），中文是镜像（`docs/zh/`）。行为变化先改英文，
  再同步中文；代码块、命令、环境变量名保持原样。
- 行为或设计变化：先改 `design.md` / `protocol.md`，再同步根 README 与
  `integrations/`。
- 实测结果：写进 `adoption.md` 与 `testing.md`，标注日期、agent 版本、模型；
  没有复现命令的结论不算结论。
- 集成目录（`integrations/<agent>/`）只放可安装的资产（plugin、hooks 片段），
  说明统一放本文档树，避免两处漂移。
