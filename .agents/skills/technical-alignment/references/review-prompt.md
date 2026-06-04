# Claude Consensus Review Prompt

Use this task wording when invoking `claude-consensus` after `03-technical-alignment.md` has been saved.

## 复审上下文

- `02-business-requirement.md` 完整业务需求文档：只读参考，不允许修改。
- `explore` 结果：只读参考，包含关键文件表、当前实现事实摘要、事实来源、复用点、风险和建议阅读文件，不允许当成最终方案直接照抄。
- `claude-proposal-consensus` 最终共识方案：只读参考，是技术方案来源。
- 相关代码、architecture / codebase 文档、接口或数据结构：只读参考，不允许修改。
- `03-technical-alignment.md` 技术对齐稿：唯一允许修改的文件。

```text
复审 technical-alignment 的产物。

输入：
- 完整业务需求文档：<business-requirement-path>，只读参考
- explore 结果：<explore-summary-or-path>，只读参考
- claude-proposal-consensus 最终共识方案：<proposal-consensus-summary-or-path>，只读参考
- 相关代码 / 架构 / codebase 文档：<reference-paths-or-summary>，只读参考
- 技术对齐稿：<technical-alignment-path>，唯一可修改文件

要求：
- 只允许修改技术对齐稿。
- 不允许修改完整业务需求文档、explore 结果、代码文件、架构文档或 codebase 文档。
- 检查技术对齐稿是否忠实继承完整业务需求文档，没有扩展业务需求。
- 检查技术方案是否来自 claude-proposal-consensus 的 APPROVED 最终共识方案，并忠实基于 explore 查明的代码现实。
- 检查技术对齐稿是否包含事实来源，且当前事实是否足以支撑后续技术合同。
- 检查当前实现事实是否充分、准确，是否遗漏关键入口、组件、接口、服务、数据结构、状态、枚举、权限校验点、测试入口或兼容边界。
- 检查技术方案是否能在当前代码库成立，方案假设是否有代码事实支撑。
- 检查是否遗漏接口、数据、状态、权限、兼容、验证等合同生成必需信息。
- 检查是否存在只有方向性描述但没有落地口径的模糊表述。
- 检查复用边界、不采用方案、影响范围、风险与约束是否清楚。
- 检查是否包含固定的数据库变化章节，且数据库结构变更、状态 / 枚举、迁移和历史数据兼容没有被混写或遗漏；无变化时是否明确写无。
- 检查权限 / 安全边界是否区分前端展示边界与后端兜底边界。
- 检查是否把待确认技术问题写成确定方案。
- 通过后，技术对齐稿才能作为下一阶段的已确认技术对齐文档输入。
```

## 复审结论处理

- `APPROVED`：以复审后的技术对齐稿作为下一阶段已确认技术对齐文档输入。
- `APPROVED_WITH_NOTES` 或 `REVISE`：由 `claude-consensus` subagent 根据 Claude 复审反馈只修改技术对齐稿，并继续复审直到 `APPROVED` 或 `BLOCKED`。
- `BLOCKED`：不要继续进入下一阶段；向用户说明阻塞点，并等待用户补充、确认或允许补充查证。
