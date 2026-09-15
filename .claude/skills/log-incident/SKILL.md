---
name: log-incident
description: Live-incident logging discipline — open docs/incidents/<symptom>-<date>/log.md at first report and write it as the incident unfolds. Use the moment something is down, full, corrupted, unreachable, or degraded in a real environment, or when asked to investigate a suspected outage. Not for planned work (plan-system) or routine debugging of code under development.
---

# log-incident

A live incident gets one folder and one file, opened at first report — before
diagnosis, before understanding:

```
docs/incidents/<kebab-symptom>-<YYYY-MM-DD>/
  log.md    # the whole incident: symptom, timeline, root cause, follow-ups
```

Date = opened; path frozen forever; never deleted. Captured output and
handed-over scripts live alongside. Three rules:

- **Log first, understand later** — write during the incident, not after.
- **Suspected until confirmed** — CONFIRMED requires the command and output.
- **The human pulls the trigger** — you do read-only recon and hand over the
  apply lines; a person runs every destructive step.

## Write log.md in this order

1. **Masthead:** `# Incident log — <symptom> (suspected)`; Opened date;
   Status; Reported by, quoting the report verbatim.
2. **Affected system:** facts with source and freshness ("from
   `inventory.yaml`, pulled ff-only 2026-09-14"), including neighbors that
   could be the real culprit.
3. **Symptom:** mark **CONFIRMED** only once command output shows it.
4. **Hypotheses:** likely causes, ranked.
5. **Diagnosis commands:** a read-only recon block handed to whoever has
   access — written before any remediation.
6. **Timeline:** dated, append-only. Corrections are new entries, never
   edits — including your own mistakes ("agent error + false alarm: …
   Corrected: z2 = .39").
7. **Remediation:** titled *"proposed — do not run until diagnosis
   confirms"*; every destructive command states its blast radius ("destroys
   all unused images/volumes — confirm no wanted named volumes first").
8. **Root cause:** only with evidence, ideally before/after ("32G/32G/100%
   → 13G used/42% after the prune").
9. **Follow-ups:** checkboxes with owner and blockers. Unrelated discoveries
   become separate incidents, not scope creep.

## Closing

Set `Status: RESOLVED <date> — follow-ups open below`; the incident isn't
finished while boxes are unchecked. Promote what it taught: a recurrence gets
a runbook section; prevention work bigger than a checkbox becomes a
plan-system epic.
