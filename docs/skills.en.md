# Skills

Whale supports **local Agent Skills**: reusable instruction folders that teach
the agent a specific workflow, domain, or tool pattern.

A skill is a directory containing a `SKILL.md` file. Whale keeps skill names
and descriptions in the model-visible index and loads the full instructions
only when a skill is invoked or clearly matches the task.

---

## Installing Skills

Browse community skills at [skills.sh](https://skills.sh), then install with:

```bash
# Find a skill
npx skills find review

# Install at project scope
npx skills add https://github.com/mattpocock/skills --skill grill-me

# Install globally (available in every Whale workspace)
npx skills add vercel-labs/skills --skill find-skills -g
```

After installing, type `$` in the Whale TUI to find the new skill.

### Update installed skills

```bash
npx skills check
npx skills update
```

Whale picks up updated files the next time it scans skill directories.

---

## Skill Locations

Whale discovers skills from these directories (in order):

1. `.whale/skills`
2. `.agents/skills`
3. `~/.whale/skills`
4. `~/.agents/skills`

Workspace-level skills take precedence over user-global ones with the same name.

---

## Creating a Skill

Each skill lives in a directory named after the skill:

```text
~/.whale/skills/my-skill/
└── SKILL.md
```

`SKILL.md` must start with frontmatter:

```markdown
---
name: my-skill
description: Use this when Whale should follow my custom workflow.
when: Use when the user asks for my custom workflow.
requires:
  commands: [git]
  env: [GITHUB_TOKEN]
  mcp: [github]
---

# My Skill

Instructions for Whale go here.
```

| Field | Required | Description |
|---|---|---|
| `name` | ✅ | Letters, digits, and hyphens. Must match the directory name. |
| `description` | ✅ | Shown in the skill picker when typing `$`. |
| `when` | ❌ | Extra guidance so the model knows when to auto-load this skill. |
| `requires` | ❌ | Documents prerequisites (commands, env vars, MCP servers). Does not auto-install anything. |

---

## Using Skills

### In the TUI

1. Type `$` in the composer — a skill picker opens
2. Type to filter, press `Tab` or `Enter` to select
3. Finish your prompt: `$my-skill apply this workflow to the current task`

Or run `/skills` to:

- **List skills** — opens the same `$` picker
- **Enable/Disable Skills** — toggle skills on/off per project

Changes are saved automatically to `.whale/config.local.toml`.

### Via `load_skill` tool

The model can also load a skill automatically using the `load_skill` tool
when your task clearly matches a discovered skill.

---

## Disabling Skills

Disable from the `/skills` manager, or edit config directly:

```toml
[skills]
disabled = ["legacy-review"]
```

To override a project-wide disable:

```toml
[skills]
enabled = ["legacy-review"]
```

Disabled skills don't appear in the `$` picker. Explicit `$skill-name` or
`load_skill` calls return an error.

---

## Current Limitations

- No built-in install/update/uninstall commands (use `npx skills`)
- Skills are instruction-only — no script execution or trust management
- No automatic dependency or MCP setup
