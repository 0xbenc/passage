---
name: agents-doc
description: The repo-guide discipline — one canonical AGENTS.md routing agents to everything, CLAUDE.md as a pointer stub. Use when creating or editing AGENTS.md/CLAUDE.md, setting up a new repo, adding a routing block after importing llmrc skills, or deciding where a rule or convention should be written down. Not for content that has its own home (plans, runbooks, system docs) — this doc only routes there.
---

# agents-doc

Every repo has one always-loaded guide. It is the enforcement layer: skills
trigger probabilistically, the agent doc is read every session. Three rules:

- **One source, two names** — `AGENTS.md` is canonical; `CLAUDE.md` is a
  pointer stub, never a second draft.
- **A map, not a manual** — say where things live and link there; never
  restate what has a home ("epic working-state goes in `docs/plans/`,
  not here").
- **Name what's reserved** — human-only actions (push, publish, destructive
  ops, product taste) by name, plus explicit autonomy bands. Never silently
  choose for the owner.

## Creating the pair

`CLAUDE.md` is exactly:

```markdown
# CLAUDE.md

The repo guide lives in [AGENTS.md](./AGENTS.md) — single source of truth;
this file points Claude Code at it so rules don't drift between copies.

@AGENTS.md
```

`AGENTS.md` uses these sections — skip what the repo doesn't need:
a **routing table** (`| What | Where |`); **ALWAYS / NEVER** (breaking one
is a regression, not a style preference); **playbooks** keyed by intent;
**commands** with the Definition of Done as an ordered gate list;
**reserved & autonomy**; **gotchas**; and the **skills block** below.

If the repo already has a lone CLAUDE.md, move its content to AGENTS.md and
leave the stub. If it has drifted twins, merge into AGENTS.md, then stub.

## The skills block

After llmrc skills are imported, add this and keep it matching what
`.claude/skills/` / `.agents/skills/` actually contain (list only skills
that are present):

```markdown
## Skills (vendored from llmrc)

Read-only — edit in llmrc, reseed with `./llmrc/update.sh`.

- plan-system — any non-trivial change runs findings → plan → execution in `docs/plans/`
- log-incident — a live emergency opens `docs/incidents/<symptom>-<date>/log.md` first
- definition-of-done — every gate runs, in order, before a change is called done
- runbooks — risky or recurring operations get a written, followed runbook
- agents-doc — how this file itself works
```

## Maintenance

The nearest AGENTS.md governs the files being touched. Keeping the map
accurate is part of the Definition of Done for anything that moves what it
points at; a stale route is a defect.
