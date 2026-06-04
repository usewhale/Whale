# Claude Proposal Consensus Prompt

Use this task wording when invoking `claude-proposal-consensus`.

```text
请基于完整业务需求文档和 explore 查明的代码现实，先由 Claude 产出 technical-alignment 的完整技术方案，并由 OpenAI agent 评审迭代到 APPROVED 或 BLOCKED。

输入：
- 完整业务需求文档：<business-requirement-path-or-content>
- explore 结果：<explore-summary-or-path>
- 相关代码 / 架构 / codebase 文档：<reference-paths-or-summary>
- 用户显式技术限制：<constraints-or-none>

要求：
- Claude 是技术方案首轮产出者。
- OpenAI agent 只做评审、反馈和最终 APPROVED / BLOCKED 判断。
- 技术方案必须忠实继承完整业务需求文档，不扩展业务需求。
- 技术方案必须基于 explore 查明的代码现实，明确事实来源、当前实现事实、前端承载入口和关键交互、后端入口和服务边界、接口请求 / 响应 / 错误 / 兼容、数据库结构 / 字段 / 状态 / 枚举 / 迁移 / 历史数据策略、权限校验点、关键数据流 / 状态流 / 失败流、复用边界、不采用方案、影响范围、风险与约束、验证方向。
- 权限 / 安全边界必须区分前端展示边界与后端兜底边界。
- 必须输出数据库变化章节；涉及数据库变化时单列写清表 / 字段、状态 / 枚举、迁移 / 兼容，无变化也要明确写无数据库结构或迁移变更。
- 如果存在会改变实现方向的问题，最多列出 1-3 个，并给出推荐答案。
- 不要修改代码、业务需求文档、explore 结果或项目文档。
```

## 结论处理

- `APPROVED`：以最终共识方案作为技术方案来源，整理生成 `03-technical-alignment.md`。
- `BLOCKED`：停止流程，向用户说明阻塞点、当前最佳方案和需要用户确认的具体问题。
- 未达成 `APPROVED` 前，不得保存最终技术对齐稿，不得进入后续复审或下一阶段。
