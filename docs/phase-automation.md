# Phase automation protocol

GitHub repository state is the shared automation source of truth. Consumers read
one repository revision, starting at `automation/state.json`; they do not infer
completion from chat history, the project README, or report ordering.

The current shared entry branch is `codex/phase1-api-correctness` in
`Daniel-Cpz/FlowForge`, through owner-authorized Phase 10 finalization. Read state,
README, report and prompt at one revision of that actual branch; do not guess
branch names or assume default `main` contains phase state. `main` still contains
Phase 0 before the owner-authorized final release merge. The owner explicitly
authorized release review, safe main merge and v1.0.0 publication on 2026-10-06;
[review evidence](reports/v1.0.0-release-review.md) records the separate release
gates. After verified publication, main is the shared source. Retaining this branch avoids a
second competing state source; its historical name does not define current_phase.

## Independent documentation

| File | Responsibility |
|---|---|
| `README.md` | Whole-project overview, architecture, setup, current capabilities, Implemented / Experimental / Planned, and navigation |
| Phase completion report (normally `docs/reports/phase-N-report.md`; manual Phase 10 `docs/reports/phase-10-completion.md`) | Independent evidence for Phase N, tests, failures, limitations, and Git references |
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
| `schema_version` | Integer `1`; prompt-source fields are finalized into v1 before any formal external consumer is present |
| `project` | Exactly `FlowForge` |
| `current_phase` | Positive integer for the current or just-completed phase; tracking starts at Phase 1 |
| `status` | Exactly `not_started`, `in_progress`, `completed`, or `blocked` |
| `prompt_source` | Required string, exactly `manual` or `automation`, identifying the current phase's prompt |
| `prompt_path` | Null for manual; for automation exactly `automation/prompts/phase-N.md` matching `current_phase`, an existing confined regular file |
| `report` | Null until completion; then `docs/reports/phase-N-report.md` for `current_phase`; owner-authorized manual Phase 10 finalization uses `docs/reports/phase-10-completion.md` to preserve the historical progress report (ADR 0012) |
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

## Prompt Source

Both manual and automation-generated phase prompts are valid and use identical
implementation, testing, README, report, Git and state gates. A manual prompt
provided directly by the user sets `prompt_source: "manual"`, `prompt_path: null`.
There is no required archived prompt file. An automated prompt sets
`prompt_source: "automation"` and records an existing current-phase file, such as
`automation/prompts/phase-4.md` when `current_phase` is 4. The validator applies
the existing repository confinement and regular-file checks, including symlink
escape rejection. Required source enums do not accept aliases or null; even
`not_started` represents a phase with its source already selected in this v1.

`prompt_path` explains where the current phase originated. `next_prompt` remains
the externally prepared **next** phase's prompt and follows its existing rules.
Neither is inferred from `last_processed_phase`, which records external review
only. Manual phases do not require that review to select a prompt or start work.

If an unstarted automated prompt exists and the user supplies explicit manual
instructions, keep the old file as history, set manual/null, and record in the
phase report: "Automated prompt existed but was superseded by explicit user
instructions." Do not silently combine conflicting prompts. Once a phase is
`in_progress`, newly generated automated text must not change its scope. Only an
explicit user scope change may do so, and the report records it. This is a
consumer obligation; this repository provides no external automation runner.

An explicit manual start can replace an unperformed automatic handoff. External
Automation processes only the **current completed** state; after the user has
entered the next phase it must not generate a late prompt for that already
started phase, roll back current_phase or rewrite its source. Use an expected
revision check when publishing transitions, not just the processed counter.

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
set the actual branch, and retain `last_processed_phase`. When claiming the
automated next prompt, its old `next_prompt` becomes the new `prompt_path`, with
`prompt_source: "automation"`. When the user directly starts it, use manual/null;
an automated file need not exist. Keep historical reports and prompts. The
protocol adds no scheduler or cross-chat workflow.

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

Phase 1's source was manual with null prompt_path. Phases 2 and 3 use automation
with their corresponding automation/prompts/phase-N.md. Each report and machine-readable state
certify completion only after all gates pass.
The infrastructure report alone does not certify a numbered phase or Phase 0.

## Phase 10 owner finalization and release readiness

The owner's explicit 2026-10-05 manual finalization supersedes the automated
Phase 10 completion scope. [ADR 0012](decisions/0012-v1-local-production-acceptance.md)
requires production-like Docker acceptance, real PostgreSQL/Redis, TLS/auth,
failure recovery, persistence, backup/restore, CI and race/config checks.
Real VPS/EC2, SSH/Environment deployment, GHCR publication and public DNS/ACME
are optional, not v1.0.0 completion gates. Keep the old prompt and BLOCKED
`docs/reports/phase-10-report.md` unchanged; publish the new manual completion
at `docs/reports/phase-10-completion.md` with the same report-identity, confined
file and committed Git evidence checks. No state fields or schema are added.

Codex retains `last_processed_phase=9`: it represents external review, not local
execution completion. External Automation may record review of Phase 10 by
advancing it to 10 with `next_prompt=null`; roadmap 0–10 is exhausted, so it must
not generate Phase 11. Finalization publishes v1.0.0 READY on the shared branch;
main merge and the annotated v1.0.0 release tag follow external review and an
explicit final release decision. The owner has now authorized that release task;
its independent review is separate from GPT Automation report consumption and
does not advance the counter. The phase tag remains null; the version tag pins
the release commit, not the earlier phase-report checkpoint. No cloud-named
Phase 10 tag is required. After release, no automatic next phase is permitted.
