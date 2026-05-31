# 自定义 Workflow

本指南介绍如何编写、调试和分享自定义 workflow 脚本。

> **Claude Code 兼容**
>
> Whale 的 workflow 脚本格式与 **Claude Code raw script 完全兼容**。
> 以下所有 API（`agent`、`parallel`、`pipeline`、`phase`、`log`、`budget`、`args`）
> 与 Claude Code 的全局函数一致。为 Claude Code 编写的 workflow 脚本
> 可以直接复制到 `.whale/workflows/`（项目级）
> 或 `~/.whale/workflows/`（全局）下运行。

---

## 快速开始

### 1. 创建 workflow 文件

Workflows 可以放在两个位置：

- **项目级**（推荐，可版本控制）：`.whale/workflows/<name>.js`
- **全局**（个人所有项目可用）：`~/.whale/workflows/<name>.js`

例如，创建一个项目级 workflow：

```
.whale/workflows/my-workflow.js
```

### 2. 写入基础结构

```javascript
export const meta = {
  name: "my-workflow",
  description: "一句话描述这个 workflow 的用途",
  phases: [
    { title: "收集", detail: "收集需要的信息" },
    { title: "分析", detail: "分析结果并生成报告" },
  ],
};

export default async function main(args) {
  const input = args || "默认输入";

  phase("收集");
  const data = await agent(`收集关于 ${input} 的信息`, {
    label: "collector",
    schema: {
      type: "object",
      required: ["findings"],
      properties: {
        findings: { type: "array", items: { type: "string" } },
      },
    },
  });

  phase("分析");
  const report = await agent(`基于这些发现生成报告: ${JSON.stringify(data)}`, {
    label: "analyst",
  });

  return report;
}
```

### 3. 命名规则

- 文件名必须为 **kebab-case**（如 `my-workflow.js`）
- `meta.name` 必须与文件名一致（不含 `.js`）
- 名称只能包含小写字母、数字和连字符

### 4. 使用

在对话中描述你的需求，或者直接说"跑一下 my-workflow"，
Whale 会自动识别并调用该 workflow。

---

## 全局 API 参考

### `agent(prompt, opts?)`

启动一个子 agent。

```javascript
const result = await agent("分析这段代码", {
  label: "code-reviewer",        // 面板中显示的名字
  phase: "审查",                 // 覆盖当前 phase
  model: "deepseek-chat",        // 可选，指定模型
  schema: { /* JSON Schema */ }, // 约束结构化输出
  capabilities: [],              // 可选，限制工具权限
  max_tool_iters: 10,            // 最大工具调用轮次
  max_tool_calls: 20,            // 最大工具调用次数
});
```

#### 使用 JSON Schema 约束输出

```javascript
const result = await agent("列出 3 个改进建议", {
  schema: {
    type: "object",
    required: ["suggestions"],
    properties: {
      suggestions: {
        type: "array",
        items: {
          type: "object",
          required: ["title", "impact"],
          properties: {
            title:  { type: "string" },
            impact: { type: "string", enum: ["high", "medium", "low"] },
            detail: { type: "string" },
          },
        },
      },
    },
  },
});
// result.suggestions 是带类型约束的数组
```

### `parallel(thunks)`

并发执行多个 agent，等待所有完成。

```javascript
const [resultA, resultB] = await parallel([
  () => agent("分析方案 A", { label: "analysis-a" }),
  () => agent("分析方案 B", { label: "analysis-b" }),
]);
```

**注意：** thunk 必须是 `() => agent(...)` 箭头函数，不能直接传 `agent(...)` 返回的 promise。

### `pipeline(items, ...stages)`

流式处理：每项独立经过各阶段，无 barrier。

```javascript
const results = await pipeline(
  items,
  (item) => agent(`审查: ${item}`),
  (review) => agent(`打分: ${review}`),
);
```

`pipeline()` 适合"每项独立处理"的场景；
`parallel()` 适合"需要全部结果才能进行下一步"的场景。

### `workflow(name, args?)`

调用另一个 workflow（最多嵌套一层）。

```javascript
const deepResult = await workflow("deep-research", "量子计算的现状");
```

### `phase(title)`

标记当前阶段，UI 面板会显示进度。

```javascript
phase("数据收集");
// ... agents ...
phase("数据分析");
// ... agents ...
```

### `log(message)`

在面板中输出日志信息。

```javascript
log(`已处理 ${count} 条记录`);
```

### `budget`

控制 token 预算。

```javascript
if (budget.remaining() < 5000) {
  log("预算不足，跳过详细分析");
  return fallbackResult;
}
```

- `budget.total` — 总预算（未设置则为 `null`）
- `budget.spent()` — 已消耗 tokens
- `budget.remaining()` — 剩余 tokens

### `args`

从调用时传入的只读参数。

```javascript
export default async function main(args) {
  const topic = args?.topic || "默认主题";
}
```

---

## 完整示例

### 多视角代码审查

```javascript
export const meta = {
  name: "review-code",
  description: "从多个维度审查代码变更",
  phases: [
    { title: "审查", detail: "并行从 3 个视角审查" },
    { title: "综合", detail: "汇总审查结果" },
  ],
};

export default async function main(args) {
  phase("审查");
  const perspectives = await parallel([
    () => agent("审查这段代码的正确性和边界条件", {
      label: "correctness",
      schema: {
        type: "object",
        properties: {
          issues: { type: "array", items: { type: "string" } },
          score:  { type: "number" },
        },
      },
    }),
    () => agent("审查这段代码的安全隐患", {
      label: "security",
      schema: {
        type: "object",
        properties: {
          issues: { type: "array", items: { type: "string" } },
          score:  { type: "number" },
        },
      },
    }),
    () => agent("审查这段代码的性能和可维护性", {
      label: "performance",
      schema: {
        type: "object",
        properties: {
          issues: { type: "array", items: { type: "string" } },
          score:  { type: "number" },
        },
      },
    }),
  ]);

  phase("综合");
  const summary = await agent(
    `综合以下审查结果，给出最终建议：\n${JSON.stringify(perspectives)}`,
    { label: "synthesizer" },
  );

  return { perspectives, summary };
}
```

### 循环直到枯竭（Loop-until-dry）

```javascript
export const meta = {
  name: "find-all-issues",
  description: "持续发现代码问题直到枯竭",
  phases: [{ title: "扫描", detail: "多轮扫描未引用代码" }],
};

export default async function main(args) {
  phase("扫描");
  const allIssues = [];
  let emptyRounds = 0;
  const MAX_EMPTY_ROUNDS = 2;

  for (let round = 1; round <= 10; round++) {
    const result = await agent(
      `找出未引用的符号（已知已发现的：${allIssues.join(", ")}）`,
      { label: `round-${round}` },
    );
    if (!result || !result.length) {
      emptyRounds++;
      if (emptyRounds >= MAX_EMPTY_ROUNDS) break;
      continue;
    }
    emptyRounds = 0;
    allIssues.push(...result);
  }

  return { totalIssues: allIssues.length, issues: allIssues };
}
```

---

## Claude Code 兼容性对照

| 差异点 | Claude Code | Whale |
|---|---|---|
| 脚本格式 | raw script（`export const meta`） | 完全一致 |
| 全局 API | `agent` / `parallel` / `pipeline` / `workflow` / `phase` / `log` / `budget` / `args` | 完全一致 |
| `agent()` options | `schema` / `label` / `phase` / `model` / `capabilities` / `max_tool_iters` / `max_tool_calls` | 完全一致 |
| 项目级路径 | `.claude/workflows/` | `.whale/workflows/` |
| 全局路径 | `~/.claude/workflows/` | `~/.whale/workflows/` |
| 高级特性 | — | resume、budget 控制 |

**迁移方法：** 直接把 `.claude/workflows/<name>.js` 复制到 `.whale/workflows/<name>.js` 即可运行。

---

## 排错

| 问题 | 原因 | 解决 |
|---|---|---|
| `script must begin with export const meta` | 脚本开头格式不对 | 确保第一行非注释代码是 `export const meta = { ... }` |
| `invalid workflow filename` | 文件名不是 kebab-case | 用 `my-workflow.js` ✅，不要用 `MyWorkflow.js` ❌ |
| `filename must match meta.name` | 文件名与 `meta.name` 不一致 | 保持 `my-workflow.js` ⇔ `name: "my-workflow"` |
| `agent call limit exceeded` | 超 workflow 最大 agent 调用数 | 增加 budget 或减少 agent 数量 |
| `workflow() cannot be called from within` | workflow 嵌套超过 1 层 | 只能主 workflow 调子 workflow，不能子调子 |
