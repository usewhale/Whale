# Custom Subagents

A subagent is a child agent that Whale starts for one bounded task. Think of it as a named helper with a focused role:

- `reviewer` reviews code
- `researcher` gathers source-backed information
- `architect` checks design boundaries

The main Whale session still stays in control. A subagent does its task, returns a result, and then the main agent decides what to do next.

---

## When to Create One

If you only want Whale to remember preferences, start with [Skills](skills.en.md). If you want a fixed multi-step process, start with [Workflows](custom-workflows.en.md).

Custom subagents are useful when:

- You often ask Whale for the same kind of role-based judgment, such as security review, API design review, or test strategy.
- You want that role to use only a small set of tools.
- You want a reviewer, architect, or researcher role shared by the team.
- You want workflows to reuse a named role instead of repeating a long prompt each time.

They are less useful when:

- You only need project rules. Use `AGENTS.md`, rules, or skills instead.
- You only want to change the main agent's tone. Use the custom system prompt setting.
- You need a fixed sequence of steps. Use a workflow.

---

## Minimal Example: A Code Review Subagent

Create this file at the project root:

```text
.whale/agents/reviewer.md
```

Write:

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

Then restart Whale or start a new session.

Now you can say:

```text
Use the reviewer subagent to review my current changes.
```

Whale can now treat `reviewer` as an available subagent role. When appropriate, the main agent starts it with `spawn_subagent`.

---

## Where Files Go

Whale discovers subagents from two locations:

| Path | Scope | Commit it? |
|---|---|---|
| `.whale/agents/<name>.md` | Current project or team | Yes |
| `~/.whale/agents/<name>.md` | Your personal global agents | No |

If both exist with the same name, the project-level `.whale/agents` definition wins.

The filename is the default role name. For example, `reviewer.md` becomes `reviewer`. You can also set `name` in the frontmatter.

Names may contain letters, digits, and hyphens, up to 64 characters. Kebab-case names such as `security-reviewer` are recommended.

---

## Markdown Format

A subagent file has two parts:

1. Frontmatter between `---` lines: name, tools, permissions, and other settings.
2. Body text: the role instructions for the subagent.

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

`description` is required. `whenToUse` is optional, but strongly recommended because it helps the main agent know when to use the role.

---

## Common Fields

| Field | What it does | Example |
|---|---|---|
| `name` | Explicit subagent name. Defaults to the filename. | `security-reviewer` |
| `description` | One-line summary of the role | `Review local code changes.` |
| `whenToUse` | When the role should be used | `Use before merging auth changes.` |
| `tools` | Tool capabilities to allow | `workspace.read` |
| `disallowedTools` | Capabilities or tools to remove | `web.fetch` |
| `model` | Model override. Usually omit this. | `deepseek-chat` |
| `effort` | Reasoning effort | `high` |
| `permissionMode` | Permission mode | `read_only` |
| `maxTurns` | Max child-session turns | `8` |
| `skills` | Skills loaded into this subagent | `review-skill` |
| `mcpServers` | MCP servers exposed to this subagent | `github` |
| `initialPrompt` | Prompt injected before the task | `Read the diff first.` |
| `memory` | Memory scope | `project` |
| `background` | Run in the background | `true` |
| `isolation` | Worktree isolation | `worktree` |

For a first custom subagent, start with only:

```yaml
description: ...
whenToUse: ...
tools: workspace.read
permissionMode: read_only
```

---

## Choosing Tools and Permissions

Start with least privilege. Most reviewers, architects, and explainers should be read-only.

Common `tools` values:

| Capability | What it allows | Beginner guidance |
|---|---|---|
| `workspace.read` | Read and search project files | Good default |
| `shell.read` | Run read-oriented shell commands | Use for git diff, listing tests, and similar checks |
| `web.search` | Search the web | Use for research roles |
| `web.fetch` | Fetch web pages | Use for research roles |
| `mcp.read` | Use configured MCP tools | Use only when needed |
| `workspace.write` | Edit files | Use carefully |
| `shell.run` | Run shell commands | Use carefully |

Permission modes:

| `permissionMode` | Meaning | Good for |
|---|---|---|
| `read_only` | Read-only and safest | Default recommendation |
| `ask` | Ask before sensitive operations | Occasional edits or commands |
| `auto` | Auto-accept some operations | Trusted editing tasks |
| `trusted` | Higher-trust mode | Very explicit, controlled roles |

If you give a subagent `workspace.write` or `shell.run`, set `permissionMode` explicitly. Avoid giving write-capable tools to vague roles.

---

## Using a Custom Subagent in a Workflow

If you already have `.whale/agents/reviewer.md`, a workflow can call it like this:

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

You can also define a one-off subagent directly inside a workflow:

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

Use saved files for reusable roles. Use inline definitions for roles that only belong to one workflow.

---

## Subagents in Plugins

Plugin authors can also add:

```text
my-plugin/
└── agents/
    └── reviewer.md
```

After the plugin is installed and enabled, that role gets a plugin prefix:

```text
my-plugin:reviewer
```

Plugins are best when you want to package a role for other people. For your own project or team, prefer `.whale/agents`.

See [Plugins](plugins.en.md) for plugin details.

---

## Troubleshooting

| Problem | Likely cause | Fix |
|---|---|---|
| Whale does not use my subagent | File is not under `.whale/agents` or `~/.whale/agents` | Check the path and start a new session |
| `unsupported subagent role` | Name typo or invalid filename | Check the role name and filename |
| File is ignored | Markdown has no frontmatter | Start the file with `---` and close the frontmatter with `---` |
| `description is required` | Missing `description` | Add `description` |
| Subagent cannot run commands | Only `workspace.read` is allowed | Add `shell.read` or `shell.run`, then check permission mode |
| Subagent cannot edit files | No `workspace.write` capability | Add it only when needed and use `ask` or a higher permission mode |

---

## Subagents vs Skills vs Workflows

| Feature | What you define | Good for |
|---|---|---|
| Skill | Knowledge, process, or preference for the main agent | "When this task appears, follow this method." |
| Subagent | A named child-agent role | "Have the reviewer inspect this separately." |
| Workflow | A fixed orchestration script | "Research in parallel, synthesize, then verify." |

They can work together: a workflow can start a custom subagent, and that subagent can load specific skills.
