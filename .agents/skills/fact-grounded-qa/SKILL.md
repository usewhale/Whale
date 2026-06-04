---
name: fact-grounded-qa
description: Use this skill for strictly fact-grounded answers about a codebase, repository, configuration, documentation, or git history. It requires verifiable evidence, forbids speculation, gap-filling, recommendations, implementation, network lookup, and decision-making, and requires unclear or unsupported points to be listed explicitly.
---

# Fact-Grounded Q&A

## Purpose

Answer user questions using only facts that can be verified from available local evidence. Do not infer missing intent, fill gaps, guess causes, recommend, choose options, or decide what the user should do next.

If a requested fact cannot be verified, say exactly:

```markdown
I cannot verify this from the available facts.
```

Always include `Unknowns / Ambiguities / Unverified Points`, even when the answer appears fully verified.

## When To Use

Use this skill when the user asks for fact-only answers about:

- Codebase behavior, architecture, dependencies, APIs, data flow, configuration, schemas, tests, or build files.
- Repository history, diffs, blame, commits, or documented changes.
- Existing documentation, comments, plans, or local instructions.
- Questions phrased as "only based on facts", "do not guess", "do not infer", "cite evidence", "if uncertain list it", or equivalent wording.

## When Not To Use

Do not use this skill for:

- Implementing, editing, deleting, or generating code or files.
- Creating plans, strategies, roadmaps, product decisions, technical recommendations, prioritization, or tradeoff choices.
- Creative writing, UI design, brainstorming, or speculative analysis.
- Questions requiring current external information, web research, registries, remote APIs, websites, online documentation, or private services.
- Any task where the user wants action rather than read-only Q&A.

## Fact Hierarchy

Use the highest available tier for each claim. If tiers conflict, report the conflict instead of resolving it by assumption.

- **Tier 1: Current repository facts.** Source code, tests, configuration, schema files, lockfiles, build files, generated types, and directory structure in the current workspace.
- **Tier 2: Read-only git evidence.** `git log`, `git show`, `git diff`, `git blame`, tags, branches, and commit metadata.
- **Tier 3: Documentary evidence.** README files, AGENTS.md, design docs, plans, issue templates, inline comments, and other prose. Mark these as documentary only because they may be stale.

Excluded sources:

- Model training knowledge.
- External websites, online documentation, package registries, or remote APIs.
- Rumor, analogy, convention, personal experience, or "likely" reasoning.
- Unverified assumptions about runtime state, production data, user intent, or environment.

## Allowed Actions

Only read-only local investigation is allowed:

- Read files and list directories.
- Search files with tools such as `rg`, `find`, or language-aware search.
- Inspect read-only git history with commands such as `git status`, `git diff`, `git log`, `git show`, and `git blame`.
- Run read-only commands that do not build, test, lint, migrate, generate files, start services, access the network, or mutate state.

## Prohibited Actions

Do not:

- Create, edit, move, rename, or delete files.
- Run application code, dev servers, tests, builds, linters, formatters, generators, package scripts, migrations, seeders, deployment commands, or background services.
- Access the network, package registries, remote APIs, websites, or online documentation.
- Read `.env` files, secrets, tokens, API keys, private keys, credentials, certificates, password stores, or secret-management outputs.
- Run state-changing commands, including package installs, database commands, cache writes, code generation, git checkout, git reset, git commit, git add, or commands that modify permissions or system state.
- Use destructive commands or commands whose side effects are unclear.

## Behavioral Rules

- Every factual claim in `Answer` must be backed by available local evidence before it is shown.
- Do not show evidence citations, evidence lists, or an `Evidence` section in the final answer.
- Prefer direct evidence over documentary descriptions.
- Separate what is verified from what is unknown.
- If evidence is partial, say what is verified and list the rest under `Unknowns / Ambiguities / Unverified Points`.
- If there are multiple fact-supported explanations, list them without selecting a winner unless the evidence directly establishes one.
- Do not use words such as "probably", "likely", "should", "best", or "recommend" unless quoting evidence; these usually signal unsupported inference or decision-making.
- Do not treat comments, README text, or plans as proof that implementation matches them. Mark them as Tier 3 documentary evidence.
- Do not summarize beyond what the checked evidence supports.

## Decision Discipline

When the user asks "which option should we choose", "what should I do", "is this the right approach", or similar decision-oriented questions:

- Do not choose for the user.
- Provide only verified facts about each option if local evidence exists.
- State `I cannot verify this from the available facts.` for unsupported criteria.
- Put unresolved criteria, missing measurements, and ambiguous ownership under `Unknowns / Ambiguities / Unverified Points`.

## Required Output Format

Use this exact structure:

```markdown
## Answer

[Only verified facts. Do not show evidence. If not answerable: "I cannot verify this from the available facts."]

## Unknowns / Ambiguities / Unverified Points

- [Unknown or ambiguous point] — [Evidence needed to resolve it]

## Scope Limitations

- [What cannot be accessed or verified, and why]
```

Evidence must be gathered and checked before writing `Answer`, but it must not be displayed in the final output. `Unknowns / Ambiguities / Unverified Points` must never be omitted. Omit `Scope Limitations` only when there are no meaningful access, evidence, or verification limits.

## Examples

These are illustrative examples only. Their paths, files, and behaviors are not facts about the current repository.

### Fully Verifiable Question

User asks: "Which file defines the API route for creating invoices?"

```markdown
## Answer

The invoice creation route is defined in `src/routes/invoices.ts`, where `router.post("/invoices", createInvoice)` registers the POST handler.

## Unknowns / Ambiguities / Unverified Points

- Whether this route is enabled in production — Need environment-specific routing or deployment configuration evidence.
```

### Partially Verifiable Question

User asks: "Does checkout retry failed payments three times, and was that added last month?"

```markdown
## Answer

The current checkout code sets `maxAttempts` to `3` for payment retries. I cannot verify this from the available facts: whether the behavior was added last month.

## Unknowns / Ambiguities / Unverified Points

- Whether the retry behavior was added last month — Need read-only git history for the relevant file and commits.
```

### Multiple Explanations Without Choosing

User asks: "Should we migrate from the legacy queue to the new queue?"

```markdown
## Answer

I cannot choose whether to migrate. The repository contains both a legacy queue consumer and a new queue consumer. The legacy consumer is referenced by the current worker entrypoint. The new consumer is documented as experimental.

## Unknowns / Ambiguities / Unverified Points

- Whether the new queue meets production requirements — Need verified test, load, reliability, and deployment evidence.
- Whether migration is preferred by maintainers — Need explicit owner decision or accepted planning record.
```
