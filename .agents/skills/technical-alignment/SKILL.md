---
name: technical-alignment
description: "基于完整业务需求文档和 explore 查明的当前代码事实，由 Claude 通过 claude-proposal-consensus 先产出技术方案，OpenAI agent 评审达成共识后，整理当前实现事实、事实来源、实现入口、接口 / 服务 / 数据 / 状态 / 权限 / 兼容 / 验证方案，并输出已确认技术对齐文档。适用于用户说技术对齐、技术方案对齐、实现口径对齐、整理技术口径，或希望先确认怎么落但暂不生成最终技术合同。"
---

# Technical Alignment

把完整业务需求文档和 `explore` 查明的代码事实交给 Claude 先提出技术方案，再由 OpenAI agent 评审达成共识，最后整理成可快速审核、可支撑后续技术合同展开的已确认技术对齐文档。

此 skill 只负责编排、核验、压缩和落文档：不要扩展业务需求，不要生成最终技术合同，不要拆执行任务，不要修改业务代码，不要自行首发技术方案。

## 输入与产物

- 初始必需输入：已确认的完整业务需求文档。默认路径为 `docs/需求文档/<feature-name>/02-business-requirement.md`
- 可选输入：用户给出的页面、接口、旧实现、截图、技术限制、代码路径，或已存在的流程产物
- 可选视觉输入：若 `docs/需求文档/<feature-name>/ui-prototype/` 存在，读取其中的原型图（`.png`）和 HTML 文件，作为页面布局和视觉风格的确定参考。原型图已由非技术用户确认，不需要重新讨论 UI 取舍
- 流程必需产物：`explore` 结果、`claude-proposal-consensus` 的 `APPROVED` 最终共识方案、`claude-consensus` 的 `APPROVED` 复审结果
- 最终产物：已确认技术对齐文档 `docs/需求文档/<feature-name>/03-technical-alignment.md`

如果用户已经提供流程必需产物，可以复用并核验；如果未提供，必须按本 skill 编排生成。找不到完整业务需求文档时，要求用户提供；不要根据原始需求、零散口述需求或单独业务对齐稿直接生成技术对齐稿。

## 路径规则

- 完整业务需求文档优先读取用户显式传入的文档地址
- 如果用户没有显式传入地址，优先读取 `docs/需求文档/<feature-name>/02-business-requirement.md`
- 如果用户直接在对话中提供完整业务需求文档内容，可以直接使用该内容
- 已确认技术对齐文档默认输出为 `docs/需求文档/<feature-name>/03-technical-alignment.md`
- 推荐保存 explore 摘要到 `docs/需求文档/<feature-name>/03-technical-alignment.explore.md`
- 推荐保存方案共识摘要到 `docs/需求文档/<feature-name>/03-technical-alignment.proposal-consensus.md`
- 推荐保存复审摘要到 `docs/需求文档/<feature-name>/03-technical-alignment.review.md`
- 如果需要从路径提取功能名，优先使用 `docs/需求文档/<feature-name>/` 的目录名

## 状态机

- `NO_BUSINESS_DOC -> BLOCKED`
- `BUSINESS_DOC_READY -> EXPLORE`
- `EXPLORE_READY -> PROPOSAL_CONSENSUS`
- `PROPOSAL_APPROVED -> DRAFT_ALIGNMENT`
- `DRAFT_SAVED -> CLAUDE_REVIEW`
- `REVIEW_APPROVED -> DONE`
- 任一阶段出现不可补足的缺失、冲突或工具不可用：`BLOCKED -> STOP`

## 工作流程

1. 读取并确认完整业务需求文档：
   - 只继承已确认的业务目标、范围、流程、规则和验收口径
   - 不重新扩展需求，不改写产品口径
   - 若 `docs/需求文档/<feature-name>/ui-prototype/` 存在，读取其中的原型图和 HTML 文件，作为页面布局与视觉的确定参考
2. 读取项目约束：
   - 先读根级 `AGENTS.md`
   - 按 `AGENTS.md` 的阅读顺序读取相关 architecture / codebase 文档
   - 据此确定 `explore` 的只读侦察边界
   - 不在触发 `explore` 前自行展开大量代码阅读
3. 必须触发 `explore` skill 做只读代码现实侦察：
   - 侦察范围必须覆盖与本功能有关的前端入口 / 页面 / 组件、后端接口 / 服务、数据结构、状态 / 枚举、权限校验点、兼容边界、测试入口和风险
   - 要求 `explore` 产出关键文件表、当前实现事实摘要、事实来源、复用点、风险和建议阅读文件
   - 如果 subagent 不可用，按 `explore` 的本地 fallback 流程执行，但仍必须产出同等结构的关键文件表和事实摘要
   - `explore` 的职责止于事实侦察和阅读建议，不做技术方案取舍
4. 消化 `explore` 结果，并对关键结论做必要核验；当前实现事实只保留会影响技术落地的事实：
   - 前端现状：入口、页面、组件、状态、交互和关键调用点。若 `ui-prototype/` 存在，以此处的视觉布局作为实现目标，将现有代码结构对照原型图进行评估
   - 后端现状：接口、服务、权限校验点、任务或事件
   - 数据现状：相关表、字段、状态、枚举、历史数据和兼容约束
   - 测试现状：相关单元、接口、前端或回归测试入口；无则明确写“无”
   - 差异：当前行为与业务目标的关键差异，以及不能破坏的历史语义、兼容边界和项目约束
5. 必须触发 `claude-proposal-consensus` skill，让 Claude 先产出技术方案：
   - 传入完整业务需求文档、`explore` 结果、`ui-prototype/` 原型（如存在）、相关 architecture / codebase 事实、必要本地核验结论和用户显式限制
   - 读取 `references/proposal-consensus-prompt.md`，使用其中任务口径要求 Claude 输出完整技术方案
   - OpenAI agent 在该 skill 的循环中只做评审和反馈，要求 Claude 修订完整方案，直到结论为 `APPROVED` 或 `BLOCKED`
   - 如果结论为 `BLOCKED`，停止流程，向用户说明阻塞点和当前最佳方案，不强行生成技术对齐稿
6. 将 `claude-proposal-consensus` 的 `APPROVED` 最终共识方案整理为技术对齐稿：
   - 读取 `references/output-template.md`
   - 只做压缩、结构化、引用事实核验和格式转换
   - 不新增 Claude 共识方案以外的技术取舍；如发现共识方案与业务需求或代码事实冲突，必须重新进入 `claude-proposal-consensus` 反馈修订
   - 保留 Claude 共识方案中会改变实现方向、复用边界、数据 / 权限 / 接口方案或验收可落地性的关键问题
7. 如仍有关键技术不确定点，最多提出 1-3 个问题。每个问题必须附推荐答案；问题必须来自 Claude 共识方案或后续事实核验，且会改变实现方向、复用边界、数据 / 权限 / 接口方案或验收可落地性。
8. 保存技术对齐稿前，按 `references/output-template.md` 的质量门禁自检；如果问题会阻塞文档成立，则先只问问题，不强行生成。
9. 成功保存技术对齐稿后，必须触发 `claude-consensus` skill 进行文件复审：
   - 读取 `references/review-prompt.md`
   - 传入 `02-business-requirement.md`、`explore` 结果、`claude-proposal-consensus` 最终共识方案和相关代码 / architecture / codebase 事实作为只读参考
   - `03-technical-alignment.md` 是唯一可写目标
10. 只有复审结论为 `APPROVED` 后，才能以复审后的技术对齐稿作为下一阶段已确认技术对齐文档输入；如果复审结论为 `BLOCKED`，必须停止流程，向用户说明阻塞点，并等待用户补充、确认或允许补充查证。

## 提问规则

- 问题要少而关键，最多 3 个；如果超过 3 个，先问最上游的 1-3 个。
- 每个问题都必须说明推荐答案，格式为“推荐：...”。
- 不问能通过代码、文档、截图或用户已给材料判断的问题。
- 只问会改变实现方向、复用边界、数据 / 权限 / 接口方案或验收可落地性的技术问题。
- 不问纯执行顺序、任务拆分、命名偏好或不影响方案的实现细节。
- 不用“是否需要考虑...”这类泛问题；改成具体决策，例如“导出任务是否复用现有异步任务表？推荐：复用，避免新增并行状态体系。”

## 阻塞规则

- 缺少完整业务需求文档：`BLOCKED`，要求用户提供或先走需求收口流程。
- `explore` 无法执行且无法按 fallback 得到同等结构事实摘要：`BLOCKED`。
- `claude-proposal-consensus` 不可用、无法达成 `APPROVED` 或产物与业务需求 / 代码事实冲突：`BLOCKED`；不能由 OpenAI 自行替代 Claude 首轮方案。
- 技术对齐稿缺少事实来源或当前实现事实：`BLOCKED`。
- `claude-consensus` 不可用或复审未 `APPROVED`：`BLOCKED`；不能把未复审稿作为下一阶段输入。

## 引用文件

- 输出模板、压缩规则、保存前质量门禁：`references/output-template.md`
- Claude 首轮方案共识任务口径：`references/proposal-consensus-prompt.md`
- Claude 复审任务口径和结论处理：`references/review-prompt.md`

## 禁止内容

- 不要写 PRD。
- 不要写 user stories、完整验收矩阵或执行 story 拆分。
- 不要生成最终技术合同。
- 不要生成或修改执行批次文件。
- 不要修改业务代码。
- 不要扩展业务需求。
- 不要把原始需求、零散口述需求或单独业务对齐稿当成已确认完整业务需求文档。
- 不要让 `technical-alignment` 自行首发技术方案；技术方案必须先由 Claude 通过 `claude-proposal-consensus` 产出，并经 OpenAI agent 评审到 `APPROVED`。
- 不要把 `explore` 的侦察结论直接当最终方案；最终方案必须来自 `claude-proposal-consensus` 的 `APPROVED` 共识方案。
- 不要提出能通过代码、文档、截图或用户已给材料查清的问题。
- 复审时不要允许 `claude-consensus` 修改完整业务需求文档、`explore` 结果、`claude-proposal-consensus` 结果、代码文件、架构文档或 codebase 文档。
