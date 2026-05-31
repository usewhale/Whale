# Custom Workflows

This guide shows how to write, debug, and share custom workflow scripts.

> **Claude Code Compatible**
>
> Whale's workflow script format is **fully compatible with Claude Code raw scripts**.
> All of the following APIs (`agent`, `parallel`, `pipeline`, `phase`, `log`, `budget`, `args`)
> match Claude Code's global functions exactly. Workflow scripts written for
> Claude Code can be copied to `.whale/workflows/` (project-level)
> or `~/.whale/workflows/` (user-global) and run as-is.

---

## Quick Start

### 1. Create a workflow file

Workflows can be placed in two locations:

- **Project-level** (recommended, version-controlled): `.whale/workflows/<name>.js`
- **User-global** (available across all projects): `~/.whale/workflows/<name>.js`

For example, create a project-level workflow:

```
.whale/workflows/my-workflow.js
```

### 2. Write the basic structure

```javascript
export const meta = {
  name: "my-workflow",
  description: "One-line description of what this workflow does",
  phases: [
    { title: "Collect", detail: "Gather information" },
    { title: "Analyze", detail: "Analyze results and generate a report" },
  ],
};

export default async function main(args) {
  const input = args || "default input";

  phase("Collect");
  const data = await agent(`Gather information about ${input}`, {
    label: "collector",
    schema: {
      type: "object",
      required: ["findings"],
      properties: {
        findings: { type: "array", items: { type: "string" } },
      },
    },
  });

  phase("Analyze");
  const report = await agent(
    `Generate a report based on these findings: ${JSON.stringify(data)}`,
    { label: "analyst" },
  );

  return report;
}
```

### 3. Naming rules

- Filename must be **kebab-case** (e.g., `my-workflow.js`)
- `meta.name` must match the filename (without `.js`)
- Names may only contain lowercase letters, digits, and hyphens

### 4. Using it

Describe what you need in the conversation, or say "run my-workflow" —
Whale will auto-detect and invoke the workflow by name.

---

## Global API Reference

### `agent(prompt, opts?)`

Spawns a sub-agent.

```javascript
const result = await agent("Review this code", {
  label: "code-reviewer",         // Display name in the panel
  phase: "Review",                // Override the current phase
  model: "deepseek-chat",         // Optional, specify a model
  schema: { /* JSON Schema */ },  // Constrain structured output
  capabilities: [],               // Optional, restrict tool access
  max_tool_iters: 10,             // Max tool-call rounds
  max_tool_calls: 20,             // Max total tool calls
});
```

#### Using JSON Schema for structured output

```javascript
const result = await agent("List 3 improvement suggestions", {
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
// result.suggestions is a typed array
```

### `parallel(thunks)`

Runs multiple agents concurrently and waits for all to finish.

```javascript
const [resultA, resultB] = await parallel([
  () => agent("Analyze option A", { label: "analysis-a" }),
  () => agent("Analyze option B", { label: "analysis-b" }),
]);
```

**Note:** thunks must be `() => agent(...)` arrow functions, not `agent(...)` promises directly.

### `pipeline(items, ...stages)`

Streams each item through a series of stages independently, with no barrier.

```javascript
const results = await pipeline(
  items,
  (item) => agent(`Review: ${item}`),
  (review) => agent(`Score: ${review}`),
);
```

`pipeline()` suits "process each item independently" scenarios;
`parallel()` suits "need all results before proceeding" scenarios.

### `workflow(name, args?)`

Calls another workflow (limited to one level of nesting).

```javascript
const deepResult = await workflow("deep-research", "Current state of quantum computing");
```

### `phase(title)`

Marks the current phase — the panel shows progress accordingly.

```javascript
phase("Data Collection");
// ... agents ...
phase("Data Analysis");
// ... agents ...
```

### `log(message)`

Emits a log line visible in the panel.

```javascript
log(`Processed ${count} records`);
```

### `budget`

Controls the token budget.

```javascript
if (budget.remaining() < 5000) {
  log("Budget low, skipping detailed analysis");
  return fallbackResult;
}
```

- `budget.total` — Total budget (`null` if not set)
- `budget.spent()` — Tokens consumed so far
- `budget.remaining()` — Tokens remaining

### `args`

Read-only arguments passed in when the workflow was invoked.

```javascript
export default async function main(args) {
  const topic = args?.topic || "default topic";
}
```

---

## Complete Examples

### Multi-perspective code review

```javascript
export const meta = {
  name: "review-code",
  description: "Review code changes from multiple perspectives",
  phases: [
    { title: "Review", detail: "Parallel review from 3 perspectives" },
    { title: "Synthesize", detail: "Combine review results" },
  ],
};

export default async function main(args) {
  phase("Review");
  const perspectives = await parallel([
    () => agent("Review this code for correctness and edge cases", {
      label: "correctness",
      schema: {
        type: "object",
        properties: {
          issues: { type: "array", items: { type: "string" } },
          score:  { type: "number" },
        },
      },
    }),
    () => agent("Review this code for security vulnerabilities", {
      label: "security",
      schema: {
        type: "object",
        properties: {
          issues: { type: "array", items: { type: "string" } },
          score:  { type: "number" },
        },
      },
    }),
    () => agent("Review this code for performance and maintainability", {
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

  phase("Synthesize");
  const summary = await agent(
    `Synthesize the following review results into final recommendations:\n${JSON.stringify(perspectives)}`,
    { label: "synthesizer" },
  );

  return { perspectives, summary };
}
```

### Loop-until-dry

```javascript
export const meta = {
  name: "find-all-issues",
  description: "Keep discovering issues until no new ones surface",
  phases: [{ title: "Scan", detail: "Multi-round scan for unreferenced code" }],
};

export default async function main(args) {
  phase("Scan");
  const allIssues = [];
  let emptyRounds = 0;
  const MAX_EMPTY_ROUNDS = 2;

  for (let round = 1; round <= 10; round++) {
    const result = await agent(
      `Find unreferenced symbols (already found: ${allIssues.join(", ")})`,
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

## Claude Code Compatibility

| Difference | Claude Code | Whale |
|---|---|---|
| Script format | raw script (`export const meta`) | Identical |
| Global API | `agent` / `parallel` / `pipeline` / `workflow` / `phase` / `log` / `budget` / `args` | Identical |
| `agent()` options | `schema` / `label` / `phase` / `model` / `capabilities` / `max_tool_iters` / `max_tool_calls` | Identical |
| Project-level path | `.claude/workflows/` | `.whale/workflows/` |
| User-global path | `~/.claude/workflows/` | `~/.whale/workflows/` |
| Advanced features | — | resume, budget control |

**Migration:** Simply copy `.claude/workflows/<name>.js` to `.whale/workflows/<name>.js` and it works.

---

## Troubleshooting

| Problem | Cause | Fix |
|---|---|---|
| `script must begin with export const meta` | Wrong script header | First non-comment line must be `export const meta = { ... }` |
| `invalid workflow filename` | Not kebab-case | Use `my-workflow.js` ✅, not `MyWorkflow.js` ❌ |
| `filename must match meta.name` | File name vs meta.name mismatch | Keep `my-workflow.js` ⇔ `name: "my-workflow"` |
| `agent call limit exceeded` | Over the workflow's max agent calls | Increase budget or reduce agents |
| `workflow() cannot be called from within` | Nesting > 1 level | Only the main workflow can call sub-workflows |
