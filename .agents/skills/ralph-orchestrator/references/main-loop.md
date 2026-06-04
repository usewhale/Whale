# Main Loop

主 agent 负责整个 Ralph 批次的状态机。不要把这些职责交给子 agent。

本文件只描述主 agent 如何调度三阶段。developer、validator、committer 的具体执行规则以同目录下各自的 reference 文档为准。

## 启动前

1. 读取任务文件与 `progress.txt`
2. 读取根目录 `AGENTS.md`
3. 检查并切换 `branchName`
4. 只有在用户明确要求执行时才创建整批 `goal`
5. 记录当前批次类型、任务文件路径、runtime 文件黑名单与当前 branch，供后续子 agent 输入复用

## Story 选择顺序

主循环不再先按阶段桶全局找 story。固定改为：

1. 调用 `select_current_story.py`，从 `blocked!=true && passes!=true` 的 story 中选出当前最高优先级 story
2. 若返回 `all_resolved`，结束批次
3. 若返回 `inconsistent_state`，停止本轮并报告
4. 对该 `story_id` 调用 `resolve_story_phase.py`
5. 根据 `next_phase` 决定进入 `developer`、`validator` 或 `committer`

优先级规则：

- `priority` 数值越小优先级越高
- 不使用 `story id` 排序
- `priority` 缺失时按 `userStories` 原始顺序兜底
- 不允许因为低优先级 story 正处于 commit/validate 阶段，就抢占更高优先级 story

## 统一阶段流程

每个阶段都按固定流程执行：

1. 主 agent 显式传入当前 story 与必要上下文
2. 运行单个阶段子 agent
3. 等待结构化结果返回
4. 主 agent 基于阶段结果生成明确写入计划
5. 启动一个新的 runtime-writer 子 agent 执行运行态写回
6. 主 agent 调用 `check_story_state.py` 验证真实状态
7. 主 agent 根据脚本结果路由下一步
8. 主 agent 立即 `close_agent`

## Developer 调度

- 只给 developer 当前 story
- 显式传入当前 story 的完整 JSON，而不是让它自己重新挑 story
- 传入当前任务文件路径、`task_type`、`Codebase Patterns` 摘要、`AGENTS.md` 路径
- 允许它改产品代码、测试、配置
- 不允许它直接改任务文件
- 不允许它直接追加 `progress.txt`
- 要求它按项目需要运行质量检查
- 如果当前 story 的 `notes` 非空，要求它优先针对 notes 中的失败原因修复
- 不允许它提交 git

developer 返回后，主 agent 必须启动一个新的 runtime-writer 子 agent，并显式传入以下写入计划：

1. 调用 `update_story_fields.py --set developmentCompleted=true`
2. 如当前 story 是从验证失败回来的修复轮次，同时调用 `--set validationStatus=pending`
3. 调用 `update_story_fields.py --set passes=false`
4. 调用 `append_progress.py` 追加标准化 section，并传递 developer 返回的 `summary`、`changed_files`、`pattern_candidates`、`discovered_patterns`、`traps_encountered`、`useful_context`
5. 当 developer 返回的 `pattern_candidates` 非空时，写入计划必须设置 `merge_codebase_patterns=true`；为空时设置为 `false`
6. runtime-writer 成功返回后，主 agent 调用 `check_story_state.py --phase developer`

只有 `pattern_candidates` 参与顶部 `## Codebase Patterns` 合并。`discovered_patterns`、`traps_encountered`、`useful_context` 只写入当前 story 的 progress section。

路由规则：

- `advance` -> validator
- `phase_incomplete` / `inconsistent_state` -> 本轮停止并报告

## Validator 调度

- 只给 validator 当前 story
- 显式传入当前 story 的完整 JSON，而不是依赖 `progress.txt` 反推当前 story
- 传入当前任务文件路径、`task_type`、必要代码上下文
- 不允许它修代码
- 不允许它直接改任务文件
- 不允许它直接改 `progress.txt`
- 不允许它改 `blocked`
- 要求它严格逐条验证 acceptance criteria
- 如需浏览器验证，优先复用已有服务；只有服务不可用时才允许后台启动 dev server
- 失败时不自动写 `blocked=true`

validator 返回后，主 agent 必须启动一个新的 runtime-writer 子 agent，并显式传入以下写入计划：

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

runtime-writer 成功返回后，主 agent 调用 `check_story_state.py --phase validator`。

路由规则：

- `advance` -> committer
- `retry_developer` -> 下一轮 developer
- `phase_incomplete` / `inconsistent_state` -> 本轮停止并报告

## Committer 调度

- 只在验证通过后使用，或存在 pending commit 时使用
- 固定 `reasoning_effort=low`
- 显式传入当前 story 的 `id`、`title`、`git status --short`、runtime 文件黑名单
- 它自己决定暂存哪些实现文件
- 如果没有代码改动，它不执行 `git commit` 并直接返回成功
- 不允许它直接改任务文件
- 不允许它改产品代码或验证字段

committer 返回后，主 agent 必须启动一个新的 runtime-writer 子 agent，并显式传入以下写入计划：

- 成功时调用 `update_story_fields.py --set commitStatus=committed --set passes=true`
- 失败时调用 `update_story_fields.py --set commitStatus=failed --set passes=false`

runtime-writer 成功返回后，主 agent 调用 `check_story_state.py --phase committer`。

路由规则：

- `advance` -> 下一条 story
- `retry_committer` -> 下一轮 committer
- `phase_incomplete` / `inconsistent_state` -> 本轮停止并报告

git 失败不写失败详情到 `notes`。

## 运行态脚本

主 agent 使用这些只读脚本做状态解析与校验：

- `.agents/skills/ralph-orchestrator/scripts/select_current_story.py`
- `.agents/skills/ralph-orchestrator/scripts/resolve_story_phase.py`
- `.agents/skills/ralph-orchestrator/scripts/check_story_state.py`

runtime-writer 只能通过这些可变更脚本操作运行态：

- `.agents/skills/ralph-orchestrator/scripts/update_story_fields.py`
- `.agents/skills/ralph-orchestrator/scripts/append_progress.py`

禁止主 agent 或子 agent 直接手写 `scripts/ralph/prd.json`、`scripts/ralph/progress.txt`。
