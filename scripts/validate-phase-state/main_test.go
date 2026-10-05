package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }

func pendingState() State {
	return State{SchemaVersion: 1, Project: "FlowForge", CurrentPhase: 1,
		Status: "in_progress", PromptSource: "manual", Branch: ptr("codex/phase1-api-correctness"),
		UpdatedAt: ptr("2026-10-04T17:07:09Z")}
}

func reportFixture() string {
	return "# FlowForge Phase 1 Completion Report\n\n## Phase\nPhase 1\n\n## Status\nCOMPLETED\n" +
		"\n## Summary\nFixture evidence.\n\n## Implemented\n- Fixture.\n\n## Not Implemented\n- None.\n" +
		"\n## Experimental\n- None.\n\n## Planned\n- Phase 2.\n\n## Tests\n### Commands\n- Fixture.\n" +
		"\n## Failure / Edge Case Validation\n- Fixture.\n\n## Known Limitations\n- Fixture.\n" +
		"\n## Git\n- Commit: pending checkpoint.\n\n## Documentation Updated\n- README.md\n" +
		"\n## Next Recommended Phase\nPhase 2\n\n## Notes\nFixture only.\n"
}

func writeFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (string, State) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("Git is required for validator tests")
	}
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "codex/phase1-api-correctness"},
		{"config", "user.name", "Phase State Test"},
		{"config", "user.email", "phase-state@example.invalid"},
	} {
		if _, err := git(root, args...); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture(t, root, "README.md", "# Fixture project\n")
	writeFixture(t, root, "docs/reports/phase-1-report.md", reportFixture())
	if _, err := git(root, "add", "README.md", "docs/reports/phase-1-report.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(root, "-c", "commit.gpgsign=false", "commit", "-m", "test checkpoint"); err != nil {
		t.Fatal(err)
	}
	sha, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	s := pendingState()
	s.Status, s.Commit, s.Report = "completed", ptr(sha), ptr("docs/reports/phase-1-report.md")
	return root, s
}

func TestPhaseState(t *testing.T) {
	root, complete := fixture(t)
	tests := []struct {
		name string
		edit func(*State)
		want string
	}{
		{"valid completed", func(s *State) {}, ""},
		{"completed without report", func(s *State) { s.Report = nil }, "completed requires"},
		{"completed without commit", func(s *State) { s.Commit = nil }, "completed requires"},
		{"phase mismatch", func(s *State) { s.CurrentPhase = 2 }, "phase-2-report.md"},
		{"missing report", func(s *State) { s.CurrentPhase = 3; s.Report = ptr("docs/reports/phase-3-report.md") }, "file does not exist"},
		{"README as report", func(s *State) { s.Report = ptr("README.md") }, "report must be"},
		{"infrastructure as report", func(s *State) { s.Report = ptr("docs/reports/phase-automation-infrastructure-report.md") }, "report must be"},
		{"missing next prompt", func(s *State) { s.NextPrompt = ptr("automation/prompts/phase-2.md"); s.LastProcessedPhase = 1 }, "file does not exist"},
		{"counter ahead", func(s *State) { s.LastProcessedPhase = 2 }, "last_processed_phase"},
		{"negative counter", func(s *State) { s.LastProcessedPhase = -1 }, "last_processed_phase"},
		{"zero phase", func(s *State) { s.CurrentPhase = 0 }, "current_phase"},
		{"invented status", func(s *State) { s.Status = "done" }, "unsupported status"},
		{"wrong schema", func(s *State) { s.SchemaVersion = 2 }, "schema_version"},
		{"wrong project", func(s *State) { s.Project = "Other" }, "project"},
		{"short commit", func(s *State) { s.Commit = ptr("abc123") }, "full lowercase"},
		{"nonexistent commit", func(s *State) { s.Commit = ptr(strings.Repeat("0", 40)) }, "existing Git commit"},
		{"missing timestamp", func(s *State) { s.UpdatedAt = nil }, "completed requires"},
		{"invalid timestamp", func(s *State) { s.UpdatedAt = ptr("yesterday") }, "UTC RFC3339"},
		{"nonUTC timestamp", func(s *State) { s.UpdatedAt = ptr("2026-10-05T04:07:09+11:00") }, "UTC RFC3339"},
		{"invalid branch", func(s *State) { s.Branch = ptr("bad..branch") }, "valid Git branch"},
		{"blank branch", func(s *State) { s.Branch = ptr(" ") }, "nonblank"},
		{"missing tag", func(s *State) { s.Tag = ptr("phase-1-complete") }, "annotated"},
		{"unprocessed prompt", func(s *State) { s.NextPrompt = ptr("automation/prompts/phase-2.md") }, "last_processed_phase equal"},
		{"wrong prompt phase", func(s *State) { s.NextPrompt = ptr("automation/prompts/phase-3.md"); s.LastProcessedPhase = 1 }, "phase-2.md"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := complete
			tc.edit(&s)
			err := validate(root, s)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("want diagnostic %q, got %v", tc.want, err)
			}
		})
	}
	for _, status := range []string{"not_started", "in_progress", "blocked"} {
		t.Run("valid "+status, func(t *testing.T) {
			s := pendingState()
			s.Status = status
			if err := validate(root, s); err != nil {
				t.Fatal(err)
			}
			s.Report = complete.Report
			if err := validate(root, s); err == nil {
				t.Fatal("unfinished phase must reject completion references")
			}
		})
	}
}

func TestDecodeState(t *testing.T) {
	data, err := json.Marshal(pendingState())
	if err != nil {
		t.Fatal(err)
	}
	valid := string(data)
	tests := []struct{ name, data string }{
		{"missing field", strings.Replace(valid, `"report":null,`, "", 1)},
		{"duplicate field", strings.Replace(valid, `"status":"in_progress"`, `"status":"completed","status":"in_progress"`, 1)},
		{"unknown field", strings.Replace(valid, `"status":`, `"extra":null,"status":`, 1)},
		{"wrong case", strings.Replace(valid, `"status":`, `"Status":`, 1)},
		{"null integer", strings.Replace(valid, `"schema_version":1`, `"schema_version":null`, 1)},
		{"string integer", strings.Replace(valid, `"schema_version":1`, `"schema_version":"1"`, 1)},
		{"null project", strings.Replace(valid, `"project":"FlowForge"`, `"project":null`, 1)},
		{"wrong report type", strings.Replace(valid, `"report":null`, `"report":42`, 1)},
		{"trailing document", valid + "{}"},
		{"malformed JSON", valid[:len(valid)-1]},
		{"array", "[]"},
		{"missing prompt source", strings.Replace(valid, `"prompt_source":"manual",`, "", 1)},
		{"missing prompt path", strings.Replace(valid, `"prompt_path":null,`, "", 1)},
		{"null prompt source", strings.Replace(valid, `"prompt_source":"manual"`, `"prompt_source":null`, 1)},
		{"wrong source type", strings.Replace(valid, `"prompt_source":"manual"`, `"prompt_source":42`, 1)},
		{"wrong path type", strings.Replace(valid, `"prompt_path":null`, `"prompt_path":42`, 1)},
		{"wrong source case", strings.Replace(valid, `"prompt_source":`, `"Prompt_source":`, 1)},
		{"duplicate source", strings.Replace(valid, `"prompt_source":"manual"`, `"prompt_source":"manual","prompt_source":"automation"`, 1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeState([]byte(tc.data)); err == nil {
				t.Fatal("invalid schema accepted")
			}
		})
	}
	root := t.TempDir()
	writeFixture(t, root, "automation/state.json", valid)
	if err := validateFile(root, "automation/state.json"); err != nil {
		t.Fatal(err)
	}
}

func TestPhase10ManualFinalizationPreservesProgress(t *testing.T) {
	root, s := fixture(t)
	originalCommit := s.Commit
	writeFixture(t, root, "docs/reports/phase-10-report.md", "# FlowForge Phase 10 Progress Report\n\n## Status\nBLOCKED\n")
	completion := strings.ReplaceAll(reportFixture(), "Phase 1", "Phase 10")
	writeFixture(t, root, "docs/reports/phase-10-completion.md", completion)
	writeFixture(t, root, "automation/prompts/phase-10.md", "# Historical automated prompt\n")
	if _, err := git(root, "add", "docs/reports", "automation/prompts"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(root, "-c", "commit.gpgsign=false", "commit", "-m", "manual finalization fixture"); err != nil {
		t.Fatal(err)
	}
	sha, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	s.CurrentPhase, s.LastProcessedPhase = 10, 9
	s.Commit, s.Report = ptr(sha), ptr("docs/reports/phase-10-completion.md")
	for _, tc := range []struct {
		name string
		edit func(*State)
		want string
	}{
		{"manual completion", func(s *State) {}, ""},
		{"automated exception rejected", func(s *State) {
			s.PromptSource, s.PromptPath = "automation", ptr("automation/prompts/phase-10.md")
		}, "report must be"},
		{"other phase rejected", func(s *State) { s.CurrentPhase = 9 }, "report must be"},
		{"progress cannot certify completion", func(s *State) { s.Report = ptr("docs/reports/phase-10-report.md") }, "Completion Report"},
		{"report absent in checkpoint", func(s *State) { s.Commit = originalCommit }, "recorded commit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := s
			tc.edit(&candidate)
			err := validate(root, candidate)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	writeFixture(t, root, "docs/reports/phase-10-completion.md", strings.Replace(completion, "COMPLETED", "IN_PROGRESS", 1))
	if err := validate(root, s); err == nil {
		t.Fatal("unfinished separate report accepted")
	}
}

func TestPromptSources(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "automation/prompts/phase-3.md", "# Phase 3 prompt fixture\n")
	for _, tc := range []struct {
		name   string
		source string
		path   *string
		phase  int
		valid  bool
	}{
		{"manual without file", "manual", nil, 3, true},
		{"manual with path", "manual", ptr("automation/prompts/phase-3.md"), 3, false},
		{"automation without path", "automation", nil, 3, false},
		{"automation existing current path", "automation", ptr("automation/prompts/phase-3.md"), 3, true},
		{"automation missing file", "automation", ptr("automation/prompts/phase-2.md"), 2, false},
		{"automation previous phase", "automation", ptr("automation/prompts/phase-2.md"), 3, false},
		{"automation next phase", "automation", ptr("automation/prompts/phase-4.md"), 3, false},
		{"unsupported source", "auto", nil, 3, false},
		{"missing source", "", nil, 3, false},
		{"escape path", "automation", ptr("../outside.md"), 3, false},
		{"absolute path", "automation", ptr("/tmp/phase-3.md"), 3, false},
		{"Windows drive", "automation", ptr("C:/phase-3.md"), 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := pendingState()
			s.CurrentPhase, s.PromptSource, s.PromptPath = tc.phase, tc.source, tc.path
			// Source selection does not imply the phase was externally processed.
			s.LastProcessedPhase = 0
			if err := validate(root, s); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, got %v", tc.valid, err)
			}
		})
	}
	for _, status := range []string{"not_started", "in_progress", "blocked"} {
		s := pendingState()
		s.Status = status
		if err := validate(root, s); err != nil {
			t.Fatal(err)
		}
		s.PromptSource = ""
		if err := validate(root, s); err == nil {
			t.Fatal("source required in", status)
		}
	}
}

func TestCurrentPromptAndNextPromptAreIndependent(t *testing.T) {
	root, s := fixture(t)
	writeFixture(t, root, "docs/reports/phase-3-report.md", strings.ReplaceAll(reportFixture(), "Phase 1", "Phase 3"))
	if _, err := git(root, "add", "docs/reports/phase-3-report.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(root, "-c", "commit.gpgsign=false", "commit", "-m", "phase 3 fixture"); err != nil {
		t.Fatal(err)
	}
	sha, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "automation/prompts/phase-3.md", "# Current phase fixture\n")
	writeFixture(t, root, "automation/prompts/phase-4.md", "# Next phase fixture\n")
	s.CurrentPhase, s.LastProcessedPhase = 3, 3
	s.Commit, s.Report = ptr(sha), ptr("docs/reports/phase-3-report.md")
	s.PromptSource, s.PromptPath = "automation", ptr("automation/prompts/phase-3.md")
	s.NextPrompt = ptr("automation/prompts/phase-4.md")
	if err := validate(root, s); err != nil {
		t.Fatal(err)
	}
	s.NextPrompt = s.PromptPath
	if err := validate(root, s); err == nil {
		t.Fatal("current path accepted as next prompt")
	}
	s.NextPrompt, s.PromptPath = ptr("automation/prompts/phase-4.md"), ptr("automation/prompts/phase-4.md")
	if err := validate(root, s); err == nil {
		t.Fatal("next path accepted as current prompt")
	}
}

func TestPromptPathConfinement(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeFixture(t, root, "automation/prompts/placeholder", "fixture")
	writeFixture(t, outside, "prompt.md", "# External fixture\n")
	path := filepath.Join(root, "automation", "prompts", "phase-1.md")
	if err := os.Symlink(filepath.Join(outside, "prompt.md"), path); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	s := pendingState()
	s.PromptSource, s.PromptPath = "automation", ptr("automation/prompts/phase-1.md")
	if err := validate(root, s); err == nil || !strings.Contains(err.Error(), "escapes repository") {
		t.Fatal("prompt symlink escape accepted", err)
	}
}

func TestReportIdentity(t *testing.T) {
	for _, report := range []string{
		strings.Replace(reportFixture(), "Phase 1 Completion", "Phase 2 Completion", 1),
		strings.Replace(reportFixture(), "COMPLETED", "BLOCKED", 1),
		strings.Replace(reportFixture(), "## Phase\nPhase 1", "## Phase\nPhase 2", 1),
		strings.Replace(reportFixture(), "## Planned\n- Phase 2.", "", 1),
		reportFixture() + "\n## Status\nBLOCKED\n",
	} {
		if err := reportIdentity([]byte(report), 1); err == nil {
			t.Fatal("invalid report identity accepted")
		}
	}
}

func TestPromptAndTags(t *testing.T) {
	root, s := fixture(t)
	writeFixture(t, root, "automation/prompts/phase-2.md", "# Phase 2\n")
	s.NextPrompt, s.LastProcessedPhase = ptr("automation/prompts/phase-2.md"), 1
	if err := validate(root, s); err != nil {
		t.Fatal(err)
	}
	if _, err := git(root, "-c", "tag.gpgsign=false", "tag", "-a", "phase-1-complete", "-m", "test phase checkpoint"); err != nil {
		t.Fatal(err)
	}
	s.Tag = ptr("phase-1-complete")
	if err := validate(root, s); err != nil {
		t.Fatal(err)
	}
	if _, err := git(root, "tag", "lightweight"); err != nil {
		t.Fatal(err)
	}
	s.Tag = ptr("lightweight")
	if err := validate(root, s); err == nil {
		t.Fatal("lightweight tag accepted")
	}
	if _, err := git(root, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "later checkpoint"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(root, "-c", "tag.gpgsign=false", "tag", "-a", "later-tag", "-m", "different checkpoint"); err != nil {
		t.Fatal(err)
	}
	s.Tag = ptr("later-tag")
	if err := validate(root, s); err == nil {
		t.Fatal("tag targeting a different commit accepted")
	}
}

func TestReportMustBeCommitted(t *testing.T) {
	root, s := fixture(t)
	writeFixture(t, root, "docs/reports/phase-2-report.md", strings.ReplaceAll(reportFixture(), "Phase 1", "Phase 2"))
	s.CurrentPhase, s.Report = 2, ptr("docs/reports/phase-2-report.md")
	if err := validate(root, s); err == nil || !strings.Contains(err.Error(), "recorded commit") {
		t.Fatalf("uncommitted report accepted: %v", err)
	}
}

func TestConfinedPaths(t *testing.T) {
	root, s := fixture(t)
	outside := t.TempDir()
	writeFixture(t, outside, "report.md", reportFixture())
	if _, err := regularFile(root, outside+"/report.md"); err == nil {
		t.Fatal("absolute path accepted")
	}
	writeFixture(t, root, "directory/placeholder", "fixture")
	if _, err := regularFile(root, "directory"); err == nil {
		t.Fatal("directory accepted as report")
	}
	reportPath := filepath.Join(root, "docs", "reports", "phase-1-report.md")
	if err := os.Remove(reportPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "report.md"), reportPath); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := validate(root, s); err == nil || !strings.Contains(err.Error(), "escapes repository") {
		t.Fatalf("outside symlink accepted: %v", err)
	}
}
