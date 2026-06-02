# Dynamic Workflows

Whale supports **dynamic workflows**: JavaScript scripts that orchestrate multiple
sub-agents deterministically. The script controls the flow — loops, fan-outs, barriers —
while each `agent()` call does the actual LLM work.

If you want reusable reviewer, researcher, or architect roles first, see
[Custom Subagents](agents.en.md).

> **Claude Code Compatible**
>
> Whale's workflow script format is **fully compatible with Claude Code raw scripts**.
> `.js` workflow files written for Claude Code can be copied directly to
> `.whale/workflows/` (project-level) or `~/.whale/workflows/` (user-global)
> and used as-is, with no changes to the script content.

---

## When to Use a Workflow

| Aspect | Chat | Workflow |
|---|---|---|
| Who decides what runs next | The model, turn by turn | The script |
| Where intermediate results live | Conversation context | Script variables |
| Repeatability | Ad-hoc each time | Orchestration is codified |
| Scale | A few agent calls per turn | Dozens to hundreds of agents per run |
| Interruption | Loses context, restarts | Resumable within the same session |

Good use cases:

- **Fan-out research** — Search multiple angles in parallel, cross-verify findings
- **Multi-perspective review** — Review code/design from several lenses, then synthesize
- **Pipeline processing** — Feed items through stages (extract → transform → load)
- **Adversarial verification** — Spawn independent skeptics to refute each finding
- **Loop-until-dry** — Keep finding candidates until consecutive rounds surface nothing new

---

## How a Workflow Runs

- **Isolated execution** — The script runs in a QuickJS sandbox, separate from
  your conversation context
- **Resumable** — Within the same session, completed `agent()` calls return cached
  results; only changed or new calls run live
- **No host APIs** — The script cannot access the filesystem, network, or
  `require()` directly; all I/O happens through `agent()` leaves
- **Limits:**
  - Up to **3 concurrent agents** by default
  - Configurable agent call caps
  - Optional **token budget** to cap total completion tokens

---

## Built-in Workflow

### `deep-research`

Deep research harness — fans out web searches across several angles, fetches sources,
adversarially verifies claims, and synthesizes a cited report.

```
Phases: Scope → Search → Fetch → Verify → Synthesize
```

---

## Saving Workflows for Reuse

Whale discovers workflow scripts from two locations:

| Location | Scope | Shared via |
|---|---|---|
| `.whale/workflows/<name>.js` | **Project-level** | Version control, team-wide |
| `~/.whale/workflows/<name>.js` | **User-global** | Available across all projects |

> Migrating from Claude Code: just copy `.claude/workflows/<name>.js` to
> either location above.

Project-level workflows override user-global ones with the same name.
Saved workflows are auto-discovered — describe what you need in the
conversation and Whale will offer to run it by name.

---

## Managing Runs

Use `/workflows` to open the workflow panel.

- `↑` / `↓` — Select a phase or agent
- `Enter` / `→` — Drill into prompt, tool calls, and result
- `Esc` — Back out one level
- `j` / `k` — Scroll within agent detail
- `p` — Pause/resume
- `x` — Stop running agent or entire workflow

---

## Requirements

Available on all paid plans (DeepSeek API). The feature is disabled by default,
and can be enabled per project.

### Configuration Toggles

Run `/config` in the TUI to manage workflow settings:

- `Dynamic workflows` (`workflows.enabled`) controls the workflow runtime,
  `workflow` tool, catalog hints, and `/workflows` panel integration.
- `Workflow keyword trigger` (`workflows.keyword_trigger_enabled`) only controls
  whether catalog hints encourage automatic workflow use. Turning it off still
  allows manually running workflows.

In `/config`, `Space` toggles the selected item and creates an unsaved change;
press `Enter` or `Ctrl+S` to save. Saved changes are written to the current
project's personal config file:

```toml
# .whale/config.local.toml
[workflows]
enabled = true
keyword_trigger_enabled = true
```

`.whale/config.local.toml` only affects the current workspace and should not be
committed. To share defaults with the team, put the same `[workflows]` settings
in `.whale/config.toml`.
