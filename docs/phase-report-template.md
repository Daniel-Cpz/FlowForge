# Phase report template

Every completed phase gets its own `docs/reports/phase-N-report.md`. Retain
earlier reports; never reuse another phase's report. A report records only that
phase's scope, implementation, tests, failures, limitations, and Git references.
The root `README.md` describes the whole project. Both documents are required
and updated independently at phase completion.

This template is guidance, not completion evidence. Infrastructure work outside
a numbered phase uses a descriptive report; it cannot replace a phase report.
For example, `phase-automation-infrastructure-report.md` cannot be assigned to
`state.report`.

Replace every placeholder below with real evidence. `COMPLETED` requires all
gates in [the protocol](phase-automation.md). For unfinished work, an optional
draft must say `IN_PROGRESS` or `BLOCKED` and use a Progress Report title;
`state.report` remains null until the phase actually completes.

```markdown
# FlowForge Phase N Completion Report

## Phase
Phase N

## Status
COMPLETED

## Summary
What this phase delivers and its acceptance criteria.

## Prompt Source
Prompt Source: manual or automation
Prompt Path: null for manual, otherwise automation/prompts/phase-N.md
Record explicit user scope changes or an automated prompt superseded by manual instructions.

## Implemented
- Verified capabilities delivered by this phase.

## Not Implemented
- Scope not delivered, including unmet acceptance criteria.

## Experimental
- Experimental capabilities, or None.

## Planned
- Future work; never list it as Implemented.

## Tests
### Commands
- Exact commands, environment, and prerequisites.

### Results
- PASS / FAIL / NOT RUN, including skipped tests and reasons.

## Failure / Edge Case Validation
- Failure scenario, observed behavior, and evidence.

## Known Limitations
- Remaining limitations and whether they prevent completion.

## Git
- Branch: actual branch
- Commit: actual implementation/report commit SHA
- Tag: actual annotated tag or None
- GitHub Push Result: actual result, including failure if applicable

## Documentation Updated
- Project README: README.md (whole-project capabilities and navigation)
- Phase report: docs/reports/phase-N-report.md (this phase's evidence)
- Other docs: actual paths

## Next Recommended Phase
Phase N+1, recommendation only; no generated prompt is claimed.

## Notes
Relevant evidence and follow-up actions.
```

A commit cannot contain its own SHA. Write the report with an explicit pending
Git reference while state remains unfinished, commit it, then record the real
SHA in a follow-up metadata commit. If required, an annotated tag points to the
implementation/report commit. `state.commit` pins that checkpoint; the later
metadata commit records its identity. Never use a fake SHA.

Push the metadata commit before Automation consumes completion. If push fails,
record `GitHub push failed` and retain local evidence; do not claim remote sync.
