# 自定义 Subagent

Subagent 是 Whale 临时启动的子智能体。你可以把它理解成一个有固定角色的小助手：

- `reviewer` 只负责审查代码
- `researcher` 只负责查资料和引用来源
- `architect` 只负责看设计和边界

主会话仍然由 Whale 控制。Subagent 只做一次明确的小任务，完成后把结果交回主会话。

---

## 什么时候需要自定义 Subagent

如果你只是想让 Whale 记住一些偏好，先看 [Skills](skills.md)。如果你想把多个步骤固定成脚本，先看 [Workflow](custom-workflows.md)。

适合自定义 subagent 的情况：

- 你经常让 Whale 做同一种角色判断，比如安全审查、API 设计审查、测试策略审查
- 你希望这个角色默认只读，或者只暴露一小组工具
- 你希望团队共享同一个 reviewer/architect/researcher 角色
- 你希望 workflow 里复用同一个角色，而不是每次都写很长的 prompt

不太适合的情况：

- 只是补充项目规则：用 `AGENTS.md` 或 rules/skills 更合适
- 只是想改主 agent 的语气：用配置里的自定义系统提示词
- 需要固定多步流程：用 workflow

---

## 最小例子：创建一个代码审查 Subagent

在项目根目录创建文件：

```text
.whale/agents/reviewer.md
```

写入：

```markdown
---
description: Review local code changes for bugs, regressions, and missing tests.
whenToUse: Use when the user asks for a code review or before merging local changes.
tools: workspace.read
permissionMode: read_only
---

You are a focused code reviewer.

Prioritize concrete bugs, behavior regressions, security risks, and missing tests.
Start with findings ordered by severity. Include file and line references when possible.
Do not rewrite code unless the main agent explicitly asks you to propose a patch.
```

然后重新打开 Whale，或者开始一个新会话。

现在你可以直接说：

```text
用 reviewer subagent 审查当前改动
```

Whale 会把 `reviewer` 当作一个可用的子智能体角色。需要时，主 agent 会通过 `spawn_subagent` 启动它。

---

## 文件放在哪里

Whale 会自动发现两个位置：

| 位置 | 适合谁 | 是否建议提交 |
|---|---|---|
| `.whale/agents/<name>.md` | 当前项目或团队共享 | 是 |
| `~/.whale/agents/<name>.md` | 你个人所有项目通用 | 否 |

同名时，项目级 `.whale/agents` 优先于全局 `~/.whale/agents`。

文件名就是默认名字。例如 `reviewer.md` 的角色名是 `reviewer`。也可以在 frontmatter 里显式写 `name`。

名字只能包含字母、数字和连字符，长度最多 64 个字符。推荐使用 kebab-case，例如 `security-reviewer`。

---

## Markdown 格式

一个 subagent 文件由两部分组成：

1. 顶部 `---` 之间的 frontmatter：描述名字、工具、权限等配置
2. 下面的正文：这个 subagent 的角色说明和工作方式

```markdown
---
name: security-reviewer
description: Review changes for security risks.
whenToUse: Use before merging authentication, authorization, or secret-handling changes.
tools:
  - workspace.read
  - shell.read
permissionMode: read_only
maxTurns: 8
---

You are a security reviewer.

Look for authorization bypasses, secret leaks, unsafe shell usage, injection risks,
and missing validation. Report evidence and uncertainty clearly.
```

`description` 是必填项。`whenToUse` 可选，但强烈建议写清楚，这样主 agent 更容易知道什么时候该用它。

---

## 常用字段

| 字段 | 作用 | 示例 |
|---|---|---|
| `name` | 显式指定 subagent 名字；不写时用文件名 | `security-reviewer` |
| `description` | 一句话说明这个角色做什么 | `Review local code changes.` |
| `whenToUse` | 什么时候应该使用它 | `Use before merging auth changes.` |
| `tools` | 允许使用的工具能力 | `workspace.read` |
| `disallowedTools` | 从允许工具里排除某些能力或工具 | `web.fetch` |
| `model` | 指定模型；通常不用写 | `deepseek-chat` |
| `effort` | 推理强度 | `high` |
| `permissionMode` | 权限模式 | `read_only` |
| `maxTurns` | 子会话最多轮数 | `8` |
| `skills` | 给这个 subagent 加载的技能名 | `review-skill` |
| `mcpServers` | 给这个 subagent 暴露的 MCP server | `github` |
| `initialPrompt` | 子会话开始前先注入的提示 | `Read the diff first.` |
| `memory` | 可用记忆范围 | `project` |
| `background` | 是否后台运行 | `true` |
| `isolation` | 是否使用 worktree 隔离 | `worktree` |

新手建议只先用这几个字段：

```yaml
description: ...
whenToUse: ...
tools: workspace.read
permissionMode: read_only
```

---

## 工具和权限怎么选

默认从最小权限开始。大多数 reviewer、architect、explainer 都应该只读。

常见 `tools`：

| 工具能力 | 能做什么 | 新手建议 |
|---|---|---|
| `workspace.read` | 读取项目文件、搜索代码 | 默认使用 |
| `shell.read` | 运行偏只读的 shell 命令 | 需要看 git diff、测试列表时使用 |
| `web.search` | 搜索网页 | research 类型使用 |
| `web.fetch` | 抓取网页内容 | research 类型使用 |
| `mcp.read` | 使用已配置 MCP 工具 | 需要 MCP 时使用 |
| `workspace.write` | 修改文件 | 谨慎使用 |
| `shell.run` | 执行命令 | 谨慎使用 |

权限模式：

| `permissionMode` | 含义 | 适合场景 |
|---|---|---|
| `read_only` | 只读，最安全 | 默认推荐 |
| `ask` | 需要敏感操作时询问 | 需要偶尔修改或执行命令 |
| `auto` | 自动接受部分操作 | 你信任这个 subagent 的编辑任务 |
| `trusted` | 更高信任级别 | 只给非常明确、受控的角色 |

如果你给 subagent `workspace.write` 或 `shell.run`，请同时明确写 `permissionMode`。不要把写权限给模糊角色。

---

## 在 Workflow 里使用自定义 Subagent

如果你已经有 `.whale/agents/reviewer.md`，workflow 可以这样调用：

```javascript
export const meta = {
  name: "review-with-custom-agent",
  description: "Review changes with the custom reviewer subagent",
};

export default async function main() {
  return agent("Review the current local changes.", {
    agent: { name: "reviewer" },
    label: "reviewer",
  });
}
```

也可以在 workflow 里临时写一个 agent 定义，不落盘：

```javascript
return agent("Check the API design for long-term maintainability.", {
  agent: {
    name: "api-architect",
    description: "Review API design and boundaries.",
    tools: ["workspace.read"],
    permissionMode: "read_only",
  },
});
```

落盘定义适合复用；临时定义适合某个 workflow 独有的角色。

---

## 插件里的 Subagent

如果你在写插件，也可以在插件目录下放：

```text
my-plugin/
└── agents/
    └── reviewer.md
```

安装并启用插件后，它会变成带插件前缀的角色，例如：

```text
my-plugin:reviewer
```

插件方式适合发布给别人安装。只给自己或团队项目用时，优先用 `.whale/agents`。

更多插件说明见 [Plugins](plugins.md)。

---

## 排错

| 问题 | 可能原因 | 解决方法 |
|---|---|---|
| Whale 没有使用我的 subagent | 文件不在 `.whale/agents` 或 `~/.whale/agents` | 检查路径，然后开新会话 |
| 提示 unsupported subagent role | 名字写错，或文件名不符合规则 | 确认角色名和文件名一致 |
| 文件被忽略 | Markdown 没有 frontmatter | 文件必须以 `---` 开头并有结束的 `---` |
| 提示 description is required | 没写 `description` | 增加 `description` |
| Subagent 不能执行命令 | 只给了 `workspace.read` | 加 `shell.read` 或 `shell.run`，并确认权限模式 |
| Subagent 不能改文件 | 没有 `workspace.write` | 只在确实需要时添加，并使用 `ask` 或更高权限模式 |

---

## 和 Skills、Workflow 的区别

| 能力 | 你在定义什么 | 适合 |
|---|---|---|
| Skill | 给主 agent 的知识、流程、偏好 | "遇到这种任务时按这个方法做" |
| Subagent | 一个可被启动的角色化子智能体 | "让 reviewer 单独审查这件事" |
| Workflow | 一段固定编排脚本 | "先并行研究，再综合，再复核" |

三者可以一起用：workflow 启动自定义 subagent，自定义 subagent 再加载特定 skill。
