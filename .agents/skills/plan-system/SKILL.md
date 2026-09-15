---
name: plan-system
description: The docs/plans discipline — findings.md, then plan.md, then execution.md — for any non-trivial change (multi-file feature, refactor, investigation, incident). Use when starting such work, when asked to research, plan, or execute, or when docs/plans/ already has a folder for the task. Skip for trivial single-file fixes and pure Q&A.
---

# plan-system

Non-trivial work runs through one folder and three files, written in order:

```
docs/plans/<kebab-topic>-<YYYY-MM-DD>/   # date = start date; path frozen forever
  findings.md    # what is true      — research, evidence, verdict
  plan.md        # what we will do   — decisions, sequence, gates
  execution.md   # what happened     — records, deviations, proof
```

First, look for an existing folder for this task and read it, findings first.
Do not re-research what findings verified or re-argue what the plan decided.
Stopping early is fine: findings alone (research ending in a verdict), or
findings + execution when the verdict is small enough to be the plan.
Supporting material (proofs, scripts, measurements) lives in the same folder
under descriptive names, linked from the file that owns it. Live incidents are
not plans: an emergency gets `docs/incidents/` and the log-incident skill.

## Researching → findings.md

- Establish facts by reading code and measuring, never recollection. Masthead:
  status, the question and scope, baseline commit + date. Cite `file:line` on
  load-bearing claims.
- Keep rejected options with reasons, the verdict, risks (R1…Rn), open
  questions, what remains unmeasured, and what was verified sound (so nobody
  re-reviews it).
- **Append, never edit.** Later knowledge is a new dated section
  (`## 2026-09-01 finding — …`); superseded text is ~~struck through~~. Only
  the masthead status is rewritten, and it narrates history ("recorded 08-31;
  M0–M3 have since landed") rather than overwriting it.

## Planning → plan.md

- Open with: status; *"Companion to [findings.md](./findings.md); assumes it,
  does not restate rationale"*; a scope anchor (what is in, what stays out).
- Outcome invariants first. Then milestones (sequenced) or workstreams
  (parallel) — each a one-sentence goal, numbered steps naming exact files and
  symbols, and a terminal **Acceptance:** gate of checkable predicates. Tail:
  sequencing notes, risks by ID from findings, definition of done.
- Open questions stay in findings; the plan names at most an **Open input**
  and the milestone that must resolve it.
- **Correct, never adjust.** Ratified text is frozen; progress lives in
  execution.md. A wrong plan gets a dated correction — an amendment section
  here, or "Corrections to plan.md" in execution.md — never a silent rewrite.

## Executing → execution.md

- Masthead: start date, branch, baseline commit. Then a **Baseline** section
  recording pre-existing failures, so later green is interpretable.
- One section per plan unit, in the plan's vocabulary (`## M3 — …`): status,
  commits, what landed, what surprised. Work beyond the plan gets new headers,
  never edits to old ones. Append the record in the same commit as the code
  it describes.
- **Evidence, never a guess.** Complete means proven: test counts as fractions
  (`164/164`), measurements with units, hashes, log paths, commands run.
  Nothing is marked complete from memory or intent.
- Divergence is a named section, not a merge: **Deviations from this plan
  (recorded, not silently merged)**, **New findings the plan did not have**,
  plan-correction tables (`Plan claim | Reality | Consequence`). Attribute
  unrelated breakage; record absence too ("no gameplay code changed").

## Closing

Update the plan masthead; end execution with a closeout verification. When the
epic is over, tombstone it:

> **Status: CLOSED (2026-09)** — historical record; paths frozen; do not
> route new work here.

Promote decisions that outlive the plan to durable docs (ADRs, system docs)
and link them; the folder stays as the record of how they were made.
