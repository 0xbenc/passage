---
name: runbooks
description: Operator runbook discipline — writing and following standing ops runbooks (docs/ops/) and per-epic ship runbooks. Use when documenting a risky or recurring operation (backup, restore, migration, go-live, deploy), when preparing to ship an epic, or when asked to execute such an operation. Not for live emergencies (log-incident) or code-contribution gates (definition-of-done).
---

# runbooks

An operation a person will perform again — or once, at high stakes — gets a
runbook. Two flavors, one shape:

```
docs/ops/<task>-runbook.md                 # standing: backups, restores, go-lives
docs/plans/<epic>-<date>/ship-runbook.md   # per-epic: everything an operator does to ship it
```

Location follows the repo (`docs/ops/` where a docs tree exists; a
single-purpose repo's README can be the runbook). The shape is the contract:
name the **prerequisites, safety boundaries, commands, expected observations,
rollback, artifacts, owner, and tested revision**. Three rules:

- **The human pulls the trigger** — you do read-only reconnaissance and hand
  over the apply lines; a person runs every destructive step.
- **Name the trap** — open with the failure mode this runbook prevents, and
  the scope that survives untouched.
- **Order is safety** — sequence steps so nothing dangerous is reachable
  before the step that makes it correct; put a snapshot/undo point before
  the first destructive step.

## Writing one

1. **Masthead:** one sentence on what it does; owner; revision + date last
   actually run. Ship runbooks add a status ("**DEV DONE, PROD PENDING —
   2026-08-19**") and links to `plan.md` / `execution.md`.
2. **Cross-link neighbors** so the wrong runbook is never followed ("for a
   realm that was *lost* rather than emptied, see `…-dr.md`").
3. **Each step = exact command + expected observation** ("`df -h /` should
   read ~42%"). If you can't predict the output, it isn't ready to be a step.
4. **Classify commands** read-only vs state-writing; a validation-only
   command that changes tracked files is itself a failing gate.
5. **Recovery as a matrix** keyed by how far you got:
   `| Failure boundary | Required response |` — never "undo as needed".
6. **End by proving the end state** — re-check it directly, or deliberately
   re-break and confirm the fix catches it.

## Following one

Run recon and validation steps yourself; present each destructive step's
apply line to the human with its expected observation, and wait. Compare
every output against the runbook's expectation — a mismatch is a stop, not a
shrug. Record what was run and observed (in the epic's execution.md, or an
incident log if this became one). Afterward, update the runbook's
tested-revision; if reality diverged from a step, fix the runbook — a
drifted runbook is a defect.
