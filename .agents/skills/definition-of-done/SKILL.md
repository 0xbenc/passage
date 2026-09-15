---
name: definition-of-done
description: The Definition of Done discipline — an ordered list of exact runnable commands that decides when a change is complete. Use before claiming any change is done, when asked whether work is finished, or when adding a DoD section to a repo's AGENTS.md/CLAUDE.md/CONTRIBUTING.md. Not for exploratory work-in-progress status updates.
---

# definition-of-done

A repo declares "done" as an ordered list of exact commands. Three rules:

- **Commands, not intentions** — every gate is runnable, with its expected
  outcome named ("green", "clean"), never "code reviewed" or "works locally".
- **All of them, in order** — partial green is not done; the order is part
  of the contract.
- **Green is not proof of everything** — gates carry caveats about what they
  don't establish ("mock green alone does not prove model behavior").

## Before claiming a change is done

1. Find the repo's DoD: a "Definition of Done" / "Quality Gates" section in
   AGENTS.md, CLAUDE.md, or CONTRIBUTING.md.
2. Run every gate, in the listed order, including conditional gates whose
   trigger matches your change ("if the change touches prompts, also run…").
3. Report the result per gate — including the caveated parts a green run
   does not prove. A failed or skipped gate means the change is not done;
   say so plainly.
4. Honor the floors: a behavior change or bug fix lands with a named
   regression test; coverage doesn't drop below a recorded baseline; docs
   for anything touched are part of done.

Your own judgment of "enough" never substitutes for the list.

## When the repo has no DoD

Derive the candidate from what already exists — the CI workflow, package
scripts, Makefile targets — and propose a section shaped like this:

```markdown
## Definition of Done

A change is done when ALL of:

1. `npm run typecheck` — clean
2. `npm test` — green; behavior changes land with a named regression test
3. `npm run lint` — clean
4. Docs updated for anything touched

If the change touches <X>, also run `<command>` and report the result —
<why the ordinary gates don't cover it>.
```

Never invent gates the repo doesn't have; never omit ones it does. Put the
canonical copy where agents read first, and keep any human copy a pointer or
generated twin — not a second draft.
