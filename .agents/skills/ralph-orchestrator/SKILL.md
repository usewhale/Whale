---
name: ralph-orchestrator
description: "执行 Ralph feature 批次编排：使用一个整批 goal，由主 agent 驱动 `scripts/ralph/prd.json`，并使用开发、验证、git 三个子 agent 分离职责，替代 `scripts/ralph/ralph_fallback.py` 的主执行流程。适用于“跑 Ralph”“执行 prd.json 批次”“用 goal + 子agent 自动推进 stories”“替代 ralph_fallback.py”。"
---

# Ralph Orchestrator

运行 Ralph 批次时使用这个 skill。它负责执行已经存在的 `scripts/ralph/prd.json`，不负责生成这个文件。

忽略 web service、dashboard、端口和常驻脚本形态。执行模型固定为：主 agent 编排 + goal + developer / validator / committer / runtime-writer 子 agent。

本目录下的文档和脚本是 orchestrator 的唯一 source of truth。不要回退引用 `scripts/ralph/ralph_fallback.py` 相关 prompt。

## 何时使用

当用户要做以下事情时使用：

- 执行 `scripts/ralph/prd.json`
- 用子 agent 跑 Ralph stories
- 用 goal 驱动整批任务直到完成
- 替代 `scripts/ralph/ralph_fallback.py`

当用户只是要生成 `prd.json` 时，不用这个 skill，改用 `ralph`。

## 核心约束

- 主 agent 只负责选 story、选阶段、调度子 agent、调用本地只读脚本解析运行态、决定下一步
- 主 agent 通过本地脚本获取当前最高优先级 story，并通过本地脚本解析该 story 当前组合状态
- developer / validator / committer 只返回结构化结果，不直接修改任务文件
- runtime-writer 是唯一负责执行运行态写操作的子 agent
- developer / validator / committer / runtime-writer 都不直接手写 `scripts/ralph/progress.txt`
- 所有运行态检查通过本 skill 的本地脚本完成
- 所有 story 字段写回通过 runtime-writer 调用本 skill 的本地脚本完成
- `progress.txt` 追加通过 runtime-writer 调用本 skill 的本地脚本完成
- 每个脚本只允许处理当前 story，不允许批量改其他 story
- 每个阶段结果被主 agent 消费后，立即 `close_agent`
- 不因失败次数自动将 story 置为 `blocked=true`
- 只有任务文件中已经存在或由外部手动写入的 `blocked=true` 才会跳过该 story
- 对矛盾字段组合，主 agent 调用检查脚本得到 `inconsistent_state` 后应停止本轮并报告，不自动修复

## 运行态字段

orchestrator 依赖每个 story 具备这些运行态字段：

- `passes`
- `notes`
- `retryCount`
- `blocked`
- `developmentCompleted`
- `validationStatus`
- `commitStatus`

这些字段组合起来表达 story 的生命周期：

- `developmentCompleted`: 开发阶段是否完成
- `validationStatus`: 验证阶段当前状态
- `passes`: story 是否已彻底完成；只有开发、验证、提交三阶段都结束后才为 `true`
- `commitStatus`: 提交阶段当前状态

## 运行入口

1. 确定当前任务文件为 `scripts/ralph/prd.json`
2. 读取任务文件
   - `feature` -> `scripts/ralph/prd.json`
3. 读取 `scripts/ralph/progress.txt`
4. 读取仓库根目录 `AGENTS.md`，以及其中要求先读的补充文档
5. 检查当前分支是否等于任务文件中的 `branchName`
   - 如果不是，切换到该分支
   - 如果分支不存在，则从 `main` 创建它
6. 如果用户明确要求执行/跑批次/自动推进，创建一个整批 `goal`
   - 目标：让所有 story 最终都进入 `passes=true && validationStatus="passed" && commitStatus="committed"` 或 `blocked=true`

## 主循环

优先读取 [references/main-loop.md](references/main-loop.md)。

主 agent 每轮固定按脚本驱动：

1. 调用 `select_current_story.py`，从 `blocked!=true && passes!=true` 的 story 中选出当前最高优先级 story
2. 如果脚本返回 `all_resolved`，结束批次
3. 如果脚本返回 `inconsistent_state`，停止本轮并报告
4. 对选中的 `story_id` 调用 `resolve_story_phase.py`
5. 根据脚本返回的 `next_phase` 进入 `developer`、`validator` 或 `committer`

优先级规则：

- `priority` 数值越小优先级越高
- 不使用 `story id` 排序
- `priority` 缺失时按 `userStories` 原始顺序兜底
- 低优先级的待 commit / 待 validate story 不得抢占更高优先级的未完成 story

## 阶段执行模型

主 agent 对三个阶段都使用同一个固定流程：

1. 把当前 story 和必要上下文传给子 agent
2. 等待子 agent 返回结构化结果
3. 主 agent 基于阶段结果生成明确写入计划
4. 主 agent 启动一个新的 runtime-writer 子 agent 执行写回或追加
5. 主 agent 调用 `check_story_state.py` 确认当前阶段真实落盘结果
6. 主 agent 根据脚本返回的 `result` 决定下一步
7. 主 agent 立即 `close_agent`

### Developer

developer 只负责当前 story 的开发、必要检查和事实性摘要返回。主 agent 收到 developer 结果后，必须启动一个新的 runtime-writer 子 agent 写入：

developer 执行 story 前必须按 `references/developer-agent.md` 判断开发策略：高风险逻辑、状态流、权限或数据变更必须使用 `tdd` skill；纯 UI 微调、文案或样式调整不强制使用 `tdd` skill，浏览器验证优先；混合 story 同时覆盖对应验证。

1. 调用 `update_story_fields.py` 写当前 story：
   - `developmentCompleted=true`
   - 如需要重新验证，写 `validationStatus="pending"`（确保从失败修复后中断恢复时优先回到 validator）
   - `passes=false`
2. 调用 `append_progress.py` 追加 `progress.txt`
3. runtime-writer 完成后，主 agent 调用 `check_story_state.py --phase developer`
4. 仅在检查结果为 `advance` 时进入 validator

### Validator

validator 只负责当前 story 的验收与结构化验证结果。主 agent 收到 validator 结果后，必须启动一个新的 runtime-writer 子 agent 写入：

- 通过时调用 `update_story_fields.py` 写：
  - `validationStatus="passed"`
  - `notes=""`
  - `retryCount=0`
  - `passes=false`
- 失败时调用 `update_story_fields.py` 写：
  - `validationStatus="failed"`
  - `passes=false`
  - `notes=<失败详情模板>`
  - `retryCount += 1`

然后：

1. runtime-writer 完成后，主 agent 调用 `check_story_state.py --phase validator`
2. `advance` -> 进入 committer
3. `retry_developer` -> 下一轮 developer
4. `phase_incomplete` / `inconsistent_state` -> 本轮停止并报告

validator 失败时仍不自动写 `blocked=true`。

### Committer

committer 只负责当前 story 的 git 检查、暂存、提交与结构化 git 结果；如果没有代码改动，则不执行 `git commit` 并直接返回成功。主 agent 收到 committer 结果后，必须启动一个新的 runtime-writer 子 agent 写入：

- 成功时调用 `update_story_fields.py` 写：
  - `commitStatus="committed"`
  - `passes=true`
- 失败时调用 `update_story_fields.py` 写：
  - `commitStatus="failed"`
  - `passes=false`

然后：

1. runtime-writer 完成后，主 agent 调用 `check_story_state.py --phase committer`
2. `advance` -> 进入下一条 story
3. `retry_committer` -> 下一轮继续 committer
4. `phase_incomplete` / `inconsistent_state` -> 本轮停止并报告

## 本地脚本接口

运行态脚本位于 `.agents/skills/ralph-orchestrator/scripts/`。

选择当前应处理的最高优先级 story：

```bash
python .agents/skills/ralph-orchestrator/scripts/select_current_story.py \
  --task-file scripts/ralph/prd.json
```

解析当前 story 的组合状态与下一阶段：

```bash
python .agents/skills/ralph-orchestrator/scripts/resolve_story_phase.py \
  --task-file scripts/ralph/prd.json \
  --story-id US-003
```

检查当前 story 在某个阶段后的真实状态：

```bash
python .agents/skills/ralph-orchestrator/scripts/check_story_state.py \
  --task-file scripts/ralph/prd.json \
  --story-id US-003 \
  --phase validator
```

只更新当前 story 的允许运行态字段：

```bash
python .agents/skills/ralph-orchestrator/scripts/update_story_fields.py \
  --task-file scripts/ralph/prd.json \
  --story-id US-003 \
  --set validationStatus=failed \
  --set passes=false \
  --set notes="..." \
  --increment retryCount=1
```

按固定格式追加 `progress.txt`，并在显式开启时合并 `## Codebase Patterns`：

```bash
python .agents/skills/ralph-orchestrator/scripts/append_progress.py \
  --progress-file scripts/ralph/progress.txt \
  --story-id US-003 \
  --summary "..." \
  --changed-files-json '["a.ts","b.ts"]' \
  --pattern-candidates-json '["pattern 1"]' \
  --discovered-patterns-json '["story-specific pattern"]' \
  --traps-encountered-json '["trap 1"]' \
  --useful-context-json '["context 1"]' \
  --merge-codebase-patterns
```

`append_progress.py` 的 `--pattern-candidates-json` 保留旧调用兼容；旧调用只传 `pattern_candidates` 时，这些项会渲染到当前 progress entry 的 `发现的 patterns`。每条 progress entry 固定包含 `发现的 patterns`、`遇到的陷阱`、`有用的上下文` 三类学习项，空分类渲染为 `(无)`。

顶部 `## Codebase Patterns` 只在同时满足以下条件时合并：主 agent 的写入计划显式设置 `merge_codebase_patterns=true`，且清洗后的 `pattern_candidates` 非空。只有 `pattern_candidates` 参与顶部合并；`traps_encountered` 和 `useful_context` 只进入当前 story progress entry。已有顶部 bullet 保持在前，新 pattern 追加在后并按原顺序去重。runtime-writer 只执行主 agent 的显式写入计划，不自行推断是否 merge。

runtime-writer 只能通过这些脚本改运行态，不直接手写 JSON。主 agent 不直接执行写操作脚本。

## 子 agent 模板

只在需要 spawn 对应子 agent 时读取：

- 开发： [references/developer-agent.md](references/developer-agent.md)
- 验证： [references/validator-agent.md](references/validator-agent.md)
- 提交： [references/committer-agent.md](references/committer-agent.md)
- 写回： [references/runtime-writer-agent.md](references/runtime-writer-agent.md)

## 结束条件

满足任一条件时结束：

- 所有 story 都是 `passes=true` 且 `validationStatus="passed"` 且 `commitStatus="committed"`
- 其余未完成 story 全部已经是 `blocked=true`

如果用户中断或外部流程停止，保留当前运行态文件，不做额外清理。下次运行继续按 `priority` 重新选 story，并由脚本解析该 story 当前阶段。
