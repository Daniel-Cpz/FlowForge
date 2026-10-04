# Phase automation protocol

GitHub repository state is the shared automation source of truth. Consumers read
one repository revision, starting at `automation/state.json`; they do not infer
completion from chat history, the project README, or report ordering.

## Independent documentation

| File | Responsibility |
|---|---|
| `README.md` | Whole-project overview, architecture, setup, current capabilities, Implemented / Experimental / Planned, and navigation |
| `docs/reports/phase-N-report.md` | Independent evidence for Phase N, tests, failures, limitations, and Git references |
| `docs/phase-report-template.md` | Shared report format; not a phase report |
| `docs/reports/phase-automation-infrastructure-report.md` | This infrastructure task's results; not numbered-phase completion evidence |

Every phase completion requires both a dedicated phase report and a project
README synchronized with actual capabilities. Neither substitutes for the
other. Historical phase reports remain independently accessible.

## Schema version 1

All fields are required, including nullable fields. Unsupported versions,
unknown fields, duplicate keys, wrong-case keys, and trailing JSON are rejected.

| Field | Meaning |
|---|---|
| `schema_version` | Integer `1`; change the version deliberately when changing this contract |
| `project` | Exactly `FlowForge` |
| `current_phase` | Positive integer for the current or just-completed phase; tracking starts at Phase 1 |
| `status` | Exactly `not_started`, `in_progress`, `completed`, or `blocked` |
| `report` | Null until completion; then exactly `docs/reports/phase-N-report.md` for `current_phase` |
| `next_prompt` | Null or exactly `automation/prompts/phase-(N+1).md`, an existing file written by external Automation |
| `last_processed_phase` | Integer from 0 through `current_phase`; external Automation advances it after reviewing the phase and creating the next prompt |
| `branch` | Actual work branch, or null if unknown; required on completion |
| `commit` | Null until completion; then an existing full Git commit SHA |
| `tag` | Null unless an annotated phase tag exists; on completion it must resolve to `commit` |
| `updated_at` | RFC3339 / ISO 8601 timestamp in UTC ending in `Z`; null only for uninitialized state |

Non-completed states keep `report`, `commit`, `tag`, and `next_prompt` null. Record
failures in a draft or descriptive report without assigning it to `state.report`.
A non-null `next_prompt` requires `completed` and
`last_processed_phase == current_phase`. Null is always valid.
Codex must not advance `last_processed_phase` to claim external review or invent
a next prompt.

## Completion gates and safe write order

1. Implement the phase scope and satisfy its acceptance criteria.
2. Pass the required tests and resolve release-blocking failures.
3. Write that phase's report and update the whole-project README independently.
4. Inspect `git status` and the intended diff for unintended or sensitive files.
5. Commit the implementation, README, and report; retrieve the real SHA.
6. Create an annotated phase tag only if the existing workflow requires one.
7. Fill the report's Git references in a follow-up metadata change. Finally write
   state with `completed`, its matching report, and real Git evidence.
8. Validate the prospective state before publishing it. Write through a temporary
   file in `automation/` and rename/replace it on the same filesystem, so readers
   cannot see partial JSON. Commit report metadata and state together, then push
   the branch and any required tag without force.

If a completion gate fails, retain `in_progress` or record `blocked`; never
publish premature completion. A push failure does not erase a real local commit,
but the report must record `GitHub push failed`. Remote consumers only see
successfully pushed state.

The tracked SHA pins the implementation/report checkpoint. A later metadata
commit records its identity; no file claims to contain its own commit SHA.
Consumers must fetch sufficient Git history and tags to verify references.
The validator checks schema, paths, report identity, and Git evidence. It cannot
prove scope completion, test results, or README accuracy; those are review gates.

## External Automation handoff

Act only when `status == completed` and
`last_processed_phase < current_phase`. Validate state and read the exact report
path. Review the phase report and project README independently. Persist the
next prompt first, then atomically record its path and advance
`last_processed_phase` in the same commit. Serialize processing or use an
expected-revision check when publishing: the counter alone is not a concurrency
lock. Re-reading an already processed phase generates no new prompt.

When an authorized next phase starts, set `current_phase` to N+1, set
`status` to `in_progress`, clear `report`, `commit`, `tag`, and `next_prompt`,
set the actual branch, and retain `last_processed_phase`. Keep historical reports
and prompts. This task adds the contract, not a scheduler or cross-chat workflow.

## Validation

From the repository root, with Go 1.26+ and Git available:

```sh
go run ./scripts/validate-phase-state
go test -count=1 ./scripts/validate-phase-state
# Validate a prospective state before replacing automation/state.json:
go run ./scripts/validate-phase-state -root . -state automation/state.next.json
```

The CLI exits nonzero with a diagnostic on failure. It rejects missing or
wrong-phase reports (including README paths), non-completion report identities,
invalid or missing commits, lightweight/mismatched tags, missing next prompts,
invalid counters, and paths escaping the repository through symlinks.
It uses only the Go standard library and the existing Git executable.

Make targets `validate-phase-state` and `test-phase-state` expose the commands.
CI and `scripts/check.sh` also validate state. With no local Go, the existing
tools image can run focused checks without application services:

```powershell
docker run --rm --mount "type=bind,source=$($PWD.Path),target=/src,readonly" -w /src -e GOTOOLCHAIN=local -e GOFLAGS=-mod=readonly flowforge-tools sh -c 'git config --global --add safe.directory /src && go test -count=1 ./scripts/validate-phase-state && go run ./scripts/validate-phase-state'
```

Phase 1 remains in progress. Its existing implementation edits need their own
review, tests, commit, and report before phase completion. This infrastructure
report does not certify Phase 1 completion or retrospectively certify Phase 0.
