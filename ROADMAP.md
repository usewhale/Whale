# Whale Roadmap

> English readers: this roadmap is maintained in Chinese. Please use translation tools or open an issue if you need an English version.

这个 roadmap 主要面向想参与 Whale 的贡献者。它不是发布承诺，而是把当前最值得投入的方向拆成**可讨论**、可认领、可验证的 todo。

**如果你觉得当前 roadmap 有任何需要改进的地方请大胆的提出来，我们也还是在不断学习的过程中。**

Whale 的核心定位保持不变：DeepSeek-native、terminal-first、便宜且适合长会话的本地编程 Agent。

## 方向总览

- [ ] TUI 稳定性与本地 telemetry
- [ ] Windows 支持
- [ ] 中文优先的文档体系
- [ ] 测试体系完善
- [ ] Subagent 能力优化
- [ ] Token 消耗与缓存命中率对比
- [ ] 常用 slash command 工作流
- [ ] 图片识别(可以不是 DeepSeek 的模型)

## TUI 稳定性与本地 Telemetry

Whale 首先要变成一个稳定、顺滑、出错时看得清楚的终端工具。现在最影响体验的不是缺大功能，而是 TUI 交互、信息流和失败诊断还不够成熟。

- [ ] 定位并修复 TUI 卡顿问题
- [ ] 优化流式输出时的刷新节奏，减少输入框、状态栏、聊天区互相影响
- [ ] 梳理信息流分层：用户消息、模型回答、thinking、tool call、tool result、错误、状态提示要更容易区分
- [ ] 优化主题配色，保证暗色终端、浅色终端和低对比环境都可读
- [ ] 改善 approval、diff、shell 输出、MCP 错误、subagent 进度的展示
- [ ] 增加本地 telemetry，用于观察 tool call 成功率、失败原因、耗时、重试次数、token 使用和缓存命中
- [ ] 改善 tool call 失败后的错误分类和提示
- [ ] 完善失败重试策略，区分可重试错误、参数错误、权限拒绝、用户取消和模型需要 replan 的情况
- [ ] 增加调试入口，方便用户和贡献者快速收集问题现场

可拆 issue：

- [ ] TUI 卡顿复现与 profiling
- [ ] 重新设计 tool call/result 的展示层级
- [ ] 增加 tool call telemetry 记录
- [ ] 增加失败原因分类统计
- [ ] 改善 MCP/tool 错误在 TUI 中的可读性

## Windows 支持

Windows 支持不能只停留在能编译。真正的支持要包括安装、shell、路径、终端、CI、release 和文档。

- [x] 增加 Windows CI，覆盖全仓库测试编译检查与基础 shell runtime 测试
- [x] 验证 Windows 上 shell 命令执行行为，包括 `cmd`、PowerShell、Git Bash 的取舍（已加入 shared shell resolver 与基础 runtime 测试）
- [ ] 修复路径处理、换行、编码、终端尺寸、按键事件等 Windows 差异
- [x] 增加 Windows release asset，并在 release workflow 中校验必需 asset
- [x] 增加 PowerShell 安装脚本或明确推荐安装方式
- [x] 文档中明确 Windows 当前支持范围和已知限制
- [ ] 增加 Windows 下 `whale doctor` 检查项
- [ ] 验证 Windows 用户让 Whale 编译本地项目时，stderr/stdout 能正常返回给模型

可拆 issue：

- [x] Windows CI 基线
- [x] Windows shell resolver
- [x] Windows install 文档
- [x] Windows release asset
- [ ] Windows terminal/TUI smoke test

## 中文优先的文档体系

Whale 的主要用户和贡献者很可能先读中文。文档应该先把中文写扎实，再补英文。

- [ ] 增加架构文档，说明 CLI、TUI、App、Agent、Tools、MCP、Skills 的关系
- [ ] 增加快速使用文档，覆盖安装、setup、doctor、TUI、exec、resume、ask、plan
- [ ] 增加主流厂商配置文档，例如 DeepSeek 官方、阿里云百炼、火山、硅基流动、OpenAI-compatible endpoint
- [ ] 增加 MCP 配置教程和常见 server 示例
- [ ] 增加 Skills 使用、安装、编写和禁用教程
- [ ] 增加贡献指南：如何搭环境、跑测试、定位 TUI bug、写 eval、提交 PR
- [ ] 增加调试指南：如何看日志、telemetry、usage、session、MCP 状态
- [ ] 补齐 FAQ：API key、模型选择、缓存、费用、Windows、终端兼容、常见错误

可拆 issue：

- [ ] `docs/architecture.md`
- [ ] `docs/getting-started.md`
- [ ] `docs/providers.md`
- [ ] `docs/debugging.md`
- [ ] `docs/contributing.zh-CN.md`

## 测试体系完善

Whale 已经有不少 Go 单测和 offline eval，但 TUI 交互、端到端行为和 benchmark 还需要系统化。测试体系要服务于真实回归，而不是只追求数量。

- [ ] 补齐核心包单元测试：config、session、policy、tools、agent、mcp、skills、telemetry
- [ ] 增加 TUI 行为测试，覆盖输入、排队、approval、slash picker、skills picker、session picker、interrupt
- [ ] 增加 TUI render/golden 测试，固定关键界面在窄屏、宽屏、中英文混排下的输出
- [ ] 完善 eval harness 文档，让贡献者能添加新的 deterministic eval
- [ ] 增加更多回归 eval：tool 参数修复、失败恢复、ask/plan 模式、MCP 错误、subagent 结果汇总
- [ ] 整理 SWE-bench 跑法，明确它是外部 benchmark，不替代本地回归测试
- [ ] 增加 live smoke 的边界说明，只用于真实 API 验证，不作为常规 CI 必跑项
- [ ] 在 PR 模板中明确不同类型改动需要跑哪些测试

可拆 issue：

- [ ] TUI golden test helper
- [ ] compact（自动与手动） 测试
- [ ] eval task 编写指南
- [ ] ask/plan 模式回归 eval
- [ ] MCP 失败恢复 eval
- [ ] SWE-bench 使用说明

## Subagent 能力优化

当前 subagent 还比较弱，适合先做可靠性和可观测性，再考虑更复杂的多 agent 编排。

- [ ] 明确 subagent 的使用边界：只读探索、review、research、还是可以扩展到更强任务
- [ ] 改善 subagent prompt，让它更会返回结构化、有用、可执行的结论
- [ ] 改善 subagent 进度展示，避免用户不知道它在做什么
- [ ] 增加 subagent 的 token、耗时、失败率统计
- [ ] 增加 subagent 取消和超时处理
- [ ] 支持更清晰的 role，例如 `explore`、`review`、`research`
- [ ] 研究是否需要 fork session 或独立上下文，而不是只做一次性工具调用
- [ ] 增加 subagent 相关 eval，验证它确实比主 agent 单独搜索更有用

可拆 issue：

- [ ] subagent prompt 优化
- [ ] subagent telemetry
- [ ] subagent TUI progress
- [ ] subagent timeout/cancel
- [ ] subagent eval cases

## Token 消耗与缓存命中率对比

Whale 的差异化之一是 DeepSeek 成本和 prefix cache。需要用可信数据讲清楚它和其他 agent 的差别。

- [ ] 设计对比任务集：读代码、改 bug、跑测试、重构、小型多轮任务、大仓库探索
- [ ] 记录 Whale 的 token 消耗、缓存命中、耗时、tool call 次数和成功率
- [ ] 对比 pi、Codex CLI、Claude Code、DeepSeek-TUI、Aider 等常见 agent
- [ ] 区分模型价格、缓存命中、上下文策略和工具调用策略对成本的影响
- [ ] 输出可复现 benchmark 脚本
- [ ] 输出中文报告，说明 Whale 在哪些任务上更便宜，哪些任务上还不够好
- [ ] 避免把一次性测试结果写成永久结论，报告中标注日期、版本、模型和配置

可拆 issue：

- [ ] benchmark 任务集设计
- [ ] token/cache 采集格式
- [ ] Whale vs pi 成本对比
- [ ] Whale vs Codex/Claude Code 成本对比
- [ ] 中文 benchmark 报告

## 常用 Slash Command 以及 @ 等工作流闭环

新增 slash command 要服务真实工作流，不应该为了数量扩展命令面。每个命令都需要明确：解决什么问题、是否只是别的命令的别名、是否需要持久状态、是否能测试。

- [ ] `/review`：审查当前 git diff 或指定范围，输出问题优先的 review
- [ ] `/fork`：从当前会话派生新会话，用于尝试另一条路线
- [ ] `/cwd`：查看或切换当前工作目录；也要评估是否放在 `/status` 或状态栏更合适
- [ ] `/btw`：定义清楚语义后再做，避免变成不明确的杂项命令
- [ ] 支持 `/@` 等场景操作
- [ ] 支持 rules 等配置

可拆 issue：

- [ ] `/review` 设计与实现
- [ ] `/fork` 会话语义设计
- [ ] `/cwd` 是否应该成为 slash command
- [ ] `/btw` 设计与实现
- [ ] 支持 `/@` 等场景操作
- [ ] 支持 rules 等配置

## 图片识别

目前还不支持图片识别，但这在开发场景里经常使用。

- [ ] 调研支持图片输入的视觉模型及其 API（）
- [ ] 在 TUI 中支持粘贴或拖入图片，转为 base64 注入 prompt 上下文
- [ ] 等 DeepSeek 的模型支持通过 API 进行图片识别了，再接入
- [ ] 集成后增加对应的 TUI render 展示与回归测试

可拆 issue：

- [ ] 图片识别视觉模型调研
- [ ] TUI 图片粘贴/拖入交互
- [ ] 图片转 base64 注入 prompt 流程
- [ ] 图片识别集成测试

## 暂时不做

- [ ] 不急着做大型 dashboard
- [ ] 暂时不做 hook （已有部分代码，但是过早了）
- [ ] 不引入过于新颖、复杂的 agent 设计
