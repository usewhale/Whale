# Skills

Whale supports local Agent Skills: reusable instruction folders that teach the
agent a specific workflow, domain, or tool pattern.

A skill is a directory containing a `SKILL.md` file. Whale keeps only skill names
and descriptions in the model-visible skill index. The full `SKILL.md` body is
loaded only when a skill is invoked or clearly matches the task.

## Skill Locations

Whale discovers skills from these directories:

- `.whale/skills`
- `.agents/skills`
- `~/.whale/skills`
- `~/.agents/skills`

Workspace skills are discovered before user-global skills, so a project can
override a global skill with the same name.

## Installing Skills

Whale does not ship a built-in skill installer yet, but it is compatible with
the open Agent Skills ecosystem. Browse skills at <https://skills.sh>, then
install them with the `skills` CLI:

```bash
npx skills find review
npx skills add https://github.com/mattpocock/skills --skill grill-me
```

The `skills` CLI can install skills at project scope, usually under
`.agents/skills`, or at user scope, usually under `~/.agents/skills`. Whale
discovers both locations. Use the CLI's global flag when you want the skill
available in every Whale workspace:

```bash
npx skills add vercel-labs/skills --skill find-skills -g
```

After installing, reopen `/skills` or type `$` in the Whale TUI to find the new
skill. If a `$` picker was already open before installation, close it with
`Esc` and open it again.

The external CLI also provides update commands:

```bash
npx skills check
npx skills update
```

Whale will pick up updated files the next time it scans the skill directories.

## Creating a Skill

Each skill lives in a directory named after the skill:

```text
~/.whale/skills/my-skill/
└── SKILL.md
```

`SKILL.md` must start with frontmatter containing `name` and `description`:

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

The skill name must use letters, digits, and hyphens, and the directory name
must match the `name` field.

`when` and `requires` are optional. Whale uses them to show when a skill fits
and what setup is missing. They do not execute scripts, install dependencies, or
grant extra permissions.

## Invoking Skills

There are two ways to start using a skill in the TUI.

Type `$` in the composer to search local skills. Pick a skill with `Tab` or
`Enter`; Whale inserts the selected `$skill-name` into the composer so you can
finish the prompt:

```text
$my-skill apply this workflow to the current task
```

Run `/skills` in the TUI to open the Skills menu:

- `List skills` opens the same `$` picker. Selecting a skill inserts
  `$skill-name` into the composer; it does not run the skill immediately.
- `Enable/Disable Skills` opens the searchable manager for turning skills on or
  off.

The manager supports:

- `↑/↓` selects a skill
- typing filters the list
- `Space` or `Enter` toggles the selected skill
- `Esc` closes the manager

Changes are saved automatically to `.whale/config.local.toml`.

Whale stores the original `$my-skill ...` message as the visible user turn and
injects the loaded skill instructions as hidden context for that turn. The
`loaded skill: ...` notice is kept in status/logs instead of being added to the
chat transcript.

The model can also use the read-only `load_skill` tool when the task clearly
matches a discovered skill. This lets Whale load global skills without relaxing
the workspace boundary on `read_file`.

## Disabling Skills

Disable skills from the `/skills` manager. Whale stores the result in the
project local `.whale/config.local.toml`:

```toml
[skills]
disabled = ["legacy-review"]
```

If shared project config disables a skill, enabling it locally writes an
override:

```toml
[skills]
enabled = ["legacy-review"]
```

Disabled skills do not appear in the `$` picker. Explicit `$legacy-review` or
`load_skill` attempts return a disabled-skill error.

## Current Limitations

Whale's first skill implementation is instruction-only.

It does not currently provide:

- skill install, update, or uninstall commands
- custom `skills_paths` configuration
- script execution or trust management
- automatic dependency installation or MCP setup

Use `npx skills` or put skills directly in one of the discovery directories
above.
