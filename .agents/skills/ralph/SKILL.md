---
name: ralph
description: "将 PRD 转换为 prd.json 格式，供 Ralph 自主 agent 系统使用。当你已有 PRD 并需要将其转换为 Ralph 的 JSON 格式时使用。触发词：将prd 转成 prd.json"
---

# Ralph PRD Converter

将现有 PRD 转换为 Ralph 用于自主执行的 prd.json 格式。

---

## 工作流程

优先读取用户显式指定的 PRD 或技术合同文件；若用户未显式指定，再获取匹配的 PRD（markdown 文件或文本）并将其转换为 ralph 目录中的 `prd.json` (保存到当前项目跟路径下/scripts/ralph/prd.json)。

若用户未显式指定技术合同，优先读取匹配的技术合同 `docs/需求文档/<feature-name>/04-technical-contract.md`，并用它校验 story 不违反“不采用”“不影响”“权限 / 安全边界”“复用约束”。保持当前 `prd.json` 顶层 schema 不变；技术约束可落入 story `description`、`acceptanceCriteria` 和 `notes`。如果没有匹配到技术合同，保持当前 Ralph 流程，但在输出说明中明确未使用技术合同约束。

---

## PRD 与技术合同匹配

跨 skill 关联文档时使用确定性匹配，不猜测：

- 如果用户显式提供了 PRD 或技术合同路径，优先使用用户指定文件。
- 只有在用户未显式提供输入文件时，才从用户明确给出的功能名、PRD 文件名 `tasks/prd-<feature-name>.md`、`docs/需求文档/<feature-name>/` 目录名或技术合同文件名 `docs/需求文档/<feature-name>/04-technical-contract.md` 提取 `<feature-name>`。
- PRD 匹配路径：`tasks/prd-<feature-name>.md`。
- 技术合同匹配路径：`docs/需求文档/<feature-name>/04-technical-contract.md`。
- 如果只找到唯一候选且上下文明确指向同一功能，可以使用该候选。
- 如果存在多个候选、功能名无法确定、或 PRD 与技术合同无法对应到同一 `<feature-name>`，必须停止并询问用户，不得猜测。

使用技术合同时：

- 每个 story 必须遵守技术合同的“不采用”“不影响”“权限 / 安全边界”“复用约束”。
- 不能为了拆分方便新增技术合同未允许的新入口、新接口、新状态或跨模块影响。
- 必要的实现约束写入 story `description`、`acceptanceCriteria` 或 `notes`。
- 不改变 `prd.json` 顶层 schema；story 需包含 Ralph/orchestrator 运行所需的标准运行态字段。

---

## 输出格式

```json
{
  "project": "[Project Name]",
  "branchName": "ralph/[feature-name-kebab-case]",
  "description": "[Feature description from PRD title/intro]",
  "userStories": [
    {
      "id": "US-001",
      "title": "[Story title]",
      "description": "As a [user], I want [feature] so that [benefit]",
      "acceptanceCriteria": [
        "Criterion 1",
        "Criterion 2",
        "Typecheck passes"
      ],
      "priority": 1,
      "passes": false,
      "notes": "",
      "retryCount": 0,
      "blocked": false,
      "developmentCompleted": false,
      "validationStatus": "pending",
      "commitStatus": "pending"
    }
  ]
}
```

---

## Story 大小：第一规则

**每个 story 必须能在一次 Ralph 迭代（一个 context window）中完成。**

Ralph 每次迭代都会生成一个新的 Claude code 实例，没有之前工作的记忆。如果 story 太大，LLM 在完成之前会用完 context，并产生损坏的代码。

### 合适大小的 stories：
- 添加 database 列和 migration
- 向现有页面添加 UI component
- 使用新逻辑更新 server action
- 向列表添加 filter dropdown

### 太大（需要拆分）：
- "构建整个 dashboard" - 拆分为：schema、queries、UI components、filters
- "添加 authentication" - 拆分为：schema、middleware、login UI、session handling
- "重构 API" - 拆分为每个 endpoint 或 pattern 一个 story

**经验法则：** 如果你无法用 2-3 句话描述这个变更，那就太大了。

---

## Story 排序：依赖优先

Stories 按 priority 顺序执行。较早的 stories 不能依赖于较晚的。

**正确顺序：**
1. Schema/database 变更（migrations）
2. Server actions / backend logic
3. 使用 backend 的 UI components
4. 聚合数据的 Dashboard/summary views

**错误顺序：**
1. UI component（依赖于尚不存在的 schema）
2. Schema 变更

---

## Acceptance Criteria：必须可验证

每个标准必须是 Ralph 可以检查的内容，而不是模糊的内容。

### 好的标准（可验证）：
- "向 tasks 表添加 `status` 列，默认值为 'pending'"
- "Filter dropdown 有选项：All、Active、Completed"
- "点击删除显示确认对话框"
- "Typecheck 通过"
- "Tests 通过"

### 不好的标准（模糊）：
- "工作正常"
- "用户可以轻松执行 X"
- "良好的 UX"
- "处理边缘情况"

### 始终作为最终标准包含：
```
"Typecheck passes"
```

对于具有可测试逻辑的 stories，还应包含：
```
"Tests pass"
```

### 对于更改 UI 的 stories，还应包含：
```
"Verify in browser using agent-browser"
```

Frontend stories 在视觉验证之前不算完成。Ralph 将使用 agent-browser 导航到页面，与 UI 交互，并确认更改有效。

---

## 转换规则

1. **每个 user story 成为一个 JSON 条目**
2. **IDs**：顺序（US-001、US-002 等）
3. **Priority**：基于依赖顺序，然后是文档顺序
4. **所有 stories**：`passes: false`、空的 `notes`、`retryCount: 0`、`blocked: false`、`developmentCompleted: false`、`validationStatus: "pending"`、`commitStatus: "pending"`
5. **branchName**：从功能名称派生，kebab-case，前缀为 `ralph/`
6. **始终添加**："Typecheck passes" 到每个 story 的 acceptance criteria
7. **如存在技术合同**：校验 story 不违反技术合同边界，并把必要约束写入既有字段

---

## 拆分大型 PRD

如果 PRD 有大型功能，请拆分它们：

**原始：**
> "添加用户通知系统"

**拆分为：**
1. US-001: 向 database 添加 notifications 表
2. US-002: 创建用于发送通知的 notification service
3. US-003: 向 header 添加 notification bell 图标
4. US-004: 创建 notification dropdown panel
5. US-005: 添加 mark-as-read 功能
6. US-006: 添加 notification preferences 页面

每个都是一个可以独立完成和验证的专注变更。

---

## 示例

**输入 PRD：**
```markdown
# Task Status Feature

Add ability to mark tasks with different statuses.

## Requirements
- Toggle between pending/in-progress/done on task list
- Filter list by status
- Show status badge on each task
- Persist status in database
```

**输出 prd.json：**
```json
{
  "project": "任务应用",
  "branchName": "ralph/task-status",
  "description": "任务状态功能 - 使用状态指示器跟踪任务进度",
  "userStories": [
    {
      "id": "US-001",
      "title": "向任务表添加状态字段",
      "description": "作为开发者，我需要在数据库中存储任务状态。",
      "acceptanceCriteria": [
        "添加 status 列：'pending' | 'in_progress' | 'done' (默认 'pending')",
        "成功生成并运行 migration",
        "Typecheck 通过"
      ],
      "priority": 1,
      "passes": false,
      "notes": "",
      "retryCount": 0,
      "blocked": false,
      "developmentCompleted": false,
      "validationStatus": "pending",
      "commitStatus": "pending"
    },
    {
      "id": "US-002",
      "title": "在任务卡片上显示状态徽章",
      "description": "作为用户，我想一眼看到任务状态。",
      "acceptanceCriteria": [
        "每个任务卡片显示彩色状态徽章",
        "徽章颜色：灰色=pending，蓝色=in_progress，绿色=done",
        "Typecheck 通过",
        "使用 agent-browser 在浏览器中验证"
      ],
      "priority": 2,
      "passes": false,
      "notes": "",
      "retryCount": 0,
      "blocked": false,
      "developmentCompleted": false,
      "validationStatus": "pending",
      "commitStatus": "pending"
    },
    {
      "id": "US-003",
      "title": "向任务列表行添加状态切换",
      "description": "作为用户，我想直接从列表更改任务状态。",
      "acceptanceCriteria": [
        "每行有状态下拉菜单或切换按钮",
        "更改状态后立即保存",
        "UI 更新无需刷新页面",
        "Typecheck 通过",
        "使用 agent-browser 在浏览器中验证"
      ],
      "priority": 3,
      "passes": false,
      "notes": "",
      "retryCount": 0,
      "blocked": false,
      "developmentCompleted": false,
      "validationStatus": "pending",
      "commitStatus": "pending"
    },
    {
      "id": "US-004",
      "title": "按状态过滤任务",
      "description": "作为用户，我想过滤列表以仅查看特定状态。",
      "acceptanceCriteria": [
        "过滤下拉菜单：All | Pending | In Progress | Done",
        "过滤状态持久化在 URL params 中",
        "Typecheck 通过",
        "使用 agent-browser 在浏览器中验证"
      ],
      "priority": 4,
      "passes": false,
      "notes": "",
      "retryCount": 0,
      "blocked": false,
      "developmentCompleted": false,
      "validationStatus": "pending",
      "commitStatus": "pending"
    }
  ]
}
```

---

## 归档之前的运行

**在编写新的 prd.json 之前，检查是否存在来自不同功能的现有文件：**

1. 如果存在，读取当前的 `prd.json`
2. 检查 `branchName` 是否与新功能的 branch name 不同
3. 如果不同且 `progress.txt` 在 header 之外有内容：
   - 创建归档压缩包：`archive/YYYY-MM-DD-feature-name.zip`
   - 将当前的 `prd.json` 和 `progress.txt` 打包写入该 zip
   - 使用新的 header 重置 `progress.txt`

**ralph.sh 脚本会在你运行它时自动处理此操作**，但如果你在运行之间手动更新 prd.json，请先归档。

---

## 保存前检查清单

在编写 prd.json 之前，验证：

- [ ] **之前的运行已归档**（如果 prd.json 存在且 branchName 不同，请先归档）
- [ ] 每个 story 可以在一次迭代中完成（足够小）
- [ ] Stories 按依赖顺序排序（schema 到 backend 到 UI）
- [ ] 如存在技术合同，story 未违反“不采用”“不影响”“权限 / 安全边界”“复用约束”
- [ ] 每个 story 都有 "Typecheck passes" 作为标准
- [ ] UI stories 有 "Verify in browser using agent-browser" 作为标准
- [ ] Acceptance criteria 是可验证的（不模糊）
- [ ] 没有 story 依赖于后面的 story
- [ ] 每个 story 包含 `retryCount: 0`、`blocked: false`、`developmentCompleted: false`、`validationStatus: "pending"`、`commitStatus: "pending"` 字段

---

## 写入后：JSON 自动修复与验证（必须执行）

**每次写入 prd.json 之后，必须立即运行修复脚本**，以防止 LLM 输出中的未转义引号、多余逗号等问题导致解析失败。

### 执行步骤

```bash
python3 .claude/skills/ralph/scripts/repair_prd_json.py
```

脚本接受可选的路径参数（默认为 `scripts/ralph/prd.json`）：

```bash
# 指定自定义路径
python3 .claude/skills/ralph/scripts/repair_prd_json.py path/to/prd.json
```

脚本会自动安装 `json-repair`（若未安装），修复文件后覆盖写回，并打印结果。

### 脚本位置

`.claude/skills/ralph/scripts/repair_prd_json.py`

### 说明

- `json-repair` 自动修复 LLM 生成 JSON 的常见问题：未转义内嵌双引号、多余逗号、括号不匹配等
- `ensure_ascii=False` 保留中文字符，不转成 `\uXXXX` 转义序列
- 修复后再做一次 `json.loads()` 二次验证，确保结果绝对合法
- 修复失败则报错退出，不覆盖原文件
