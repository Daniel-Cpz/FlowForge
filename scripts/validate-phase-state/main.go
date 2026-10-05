// Command validate-phase-state checks the repository's phase automation contract.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type State struct {
	SchemaVersion      int     `json:"schema_version"`
	Project            string  `json:"project"`
	CurrentPhase       int     `json:"current_phase"`
	Status             string  `json:"status"`
	PromptSource       string  `json:"prompt_source"`
	PromptPath         *string `json:"prompt_path"`
	Report             *string `json:"report"`
	NextPrompt         *string `json:"next_prompt"`
	LastProcessedPhase int     `json:"last_processed_phase"`
	Branch             *string `json:"branch"`
	Commit             *string `json:"commit"`
	Tag                *string `json:"tag"`
	UpdatedAt          *string `json:"updated_at"`
}

var fields = map[string]bool{
	"schema_version": false, "project": false, "current_phase": false,
	"status": false, "prompt_source": false, "prompt_path": true,
	"report": true, "next_prompt": true,
	"last_processed_phase": false, "branch": true, "commit": true,
	"tag": true, "updated_at": true,
}

var fullSHA = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func main() {
	root := flag.String("root", ".", "repository root")
	stateFile := flag.String("state", "automation/state.json", "state file relative to repository root")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "FAIL: unexpected positional arguments")
		os.Exit(1)
	}
	if err := validateFile(*root, *stateFile); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("PASS: phase state is valid")
}

func validateFile(root, name string) error {
	path, err := regularFile(root, name)
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	s, err := decodeState(data)
	if err != nil {
		return err
	}
	return validate(root, s)
}

func decodeState(data []byte) (State, error) {
	var s State
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return s, errors.New("state must be a JSON object")
	}
	seen := make(map[string]bool)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return s, fmt.Errorf("invalid state key: %w", err)
		}
		name, ok := token.(string)
		if !ok {
			return s, errors.New("state keys must be strings")
		}
		nullable, known := fields[name]
		if !known || seen[name] {
			return s, fmt.Errorf("unknown or duplicate state field %q", name)
		}
		seen[name] = true
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return s, fmt.Errorf("field %s: %w", name, err)
		}
		if !nullable && bytes.Equal(raw, []byte("null")) {
			return s, fmt.Errorf("field %s cannot be null", name)
		}
	}
	if _, err := d.Token(); err != nil {
		return s, fmt.Errorf("invalid state object: %w", err)
	}
	var extra json.RawMessage
	if err := d.Decode(&extra); err != io.EOF {
		return s, errors.New("state must contain exactly one JSON document")
	}
	for name := range fields {
		if !seen[name] {
			return s, fmt.Errorf("missing state field %s", name)
		}
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("invalid state field type: %w", err)
	}
	return s, nil
}

func validate(root string, s State) error {
	if s.SchemaVersion != 1 || s.Project != "FlowForge" {
		return errors.New("expected schema_version 1 and project FlowForge")
	}
	if s.CurrentPhase < 1 {
		return errors.New("current_phase must be at least 1")
	}
	if s.LastProcessedPhase < 0 || s.LastProcessedPhase > s.CurrentPhase {
		return errors.New("last_processed_phase must be between 0 and current_phase")
	}
	switch s.Status {
	case "not_started", "in_progress", "blocked", "completed":
	default:
		return fmt.Errorf("unsupported status %q", s.Status)
	}
	for name, value := range map[string]*string{
		"branch": s.Branch, "commit": s.Commit, "tag": s.Tag,
		"report": s.Report, "next_prompt": s.NextPrompt, "updated_at": s.UpdatedAt,
		"prompt_path": s.PromptPath,
	} {
		if value != nil && (strings.TrimSpace(*value) == "" || strings.TrimSpace(*value) != *value) {
			return fmt.Errorf("%s must be null or a nonblank value without surrounding whitespace", name)
		}
	}
	switch s.PromptSource {
	case "manual":
		if s.PromptPath != nil {
			return errors.New("manual prompt_source requires prompt_path null")
		}
	case "automation":
		expected := fmt.Sprintf("automation/prompts/phase-%d.md", s.CurrentPhase)
		if s.PromptPath == nil || *s.PromptPath != expected {
			return fmt.Errorf("automation prompt_path must be %s for current_phase", expected)
		}
		if _, err := regularFile(root, *s.PromptPath); err != nil {
			return fmt.Errorf("prompt_path: %w", err)
		}
	default:
		return errors.New("prompt_source must be manual or automation")
	}
	if s.UpdatedAt != nil {
		if _, err := time.Parse(time.RFC3339Nano, *s.UpdatedAt); err != nil || !strings.HasSuffix(*s.UpdatedAt, "Z") {
			return errors.New("updated_at must be a UTC RFC3339 timestamp ending in Z")
		}
	}
	if s.Status != "completed" {
		if s.Report != nil || s.Commit != nil || s.Tag != nil || s.NextPrompt != nil {
			return errors.New("unfinished phase must keep report, commit, tag, and next_prompt null")
		}
		return nil
	}
	if s.Report == nil || s.Commit == nil || s.Branch == nil || s.UpdatedAt == nil {
		return errors.New("completed requires report, commit, branch, and updated_at")
	}
	expected := fmt.Sprintf("docs/reports/phase-%d-report.md", s.CurrentPhase)
	// Owner-authorized Phase 10 finalization preserves its historical BLOCKED
	// progress report and publishes a separate completion report (ADR 0012).
	if s.CurrentPhase == 10 && s.PromptSource == "manual" && *s.Report == "docs/reports/phase-10-completion.md" {
		expected = *s.Report
	}
	if *s.Report != expected {
		return fmt.Errorf("report must be %s for current_phase", expected)
	}
	path, err := regularFile(root, *s.Report)
	if err != nil {
		return fmt.Errorf("report: %w", err)
	}
	report, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read report: %w", err)
	}
	if err := reportIdentity(report, s.CurrentPhase); err != nil {
		return err
	}
	if !fullSHA.MatchString(*s.Commit) {
		return errors.New("commit must be a full lowercase Git SHA")
	}
	resolved, err := git(root, "rev-parse", "--verify", "--end-of-options", *s.Commit+"^{commit}")
	if err != nil || resolved != *s.Commit {
		return errors.New("commit must identify an existing Git commit")
	}
	if _, err := git(root, "check-ref-format", "refs/heads/"+*s.Branch); err != nil {
		return errors.New("branch must be a valid Git branch name")
	}
	// Branch existence is not required: CI may use a detached PR checkout.
	committedReport, err := git(root, "show", *s.Commit+":"+*s.Report)
	if err != nil {
		return errors.New("report must exist in the recorded commit")
	}
	if err := reportIdentity([]byte(committedReport), s.CurrentPhase); err != nil {
		return fmt.Errorf("recorded commit: %w", err)
	}
	if s.Tag != nil {
		ref := "refs/tags/" + *s.Tag
		if _, err := git(root, "check-ref-format", ref); err != nil {
			return errors.New("tag must be a valid Git tag name")
		}
		kind, err := git(root, "cat-file", "-t", ref)
		if err != nil || kind != "tag" {
			return errors.New("tag must exist and be annotated")
		}
		target, err := git(root, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
		if err != nil || target != *s.Commit {
			return errors.New("tag must resolve to the recorded commit")
		}
	}
	if s.NextPrompt != nil {
		if s.LastProcessedPhase != s.CurrentPhase {
			return errors.New("next_prompt requires last_processed_phase equal to current_phase")
		}
		// Do not overflow current_phase when calculating the next phase.
		if s.CurrentPhase == int(^uint(0)>>1) {
			return errors.New("current_phase cannot advance to a next phase")
		}
		expected := fmt.Sprintf("automation/prompts/phase-%d.md", s.CurrentPhase+1)
		if *s.NextPrompt != expected {
			return fmt.Errorf("next_prompt must be %s", expected)
		}
		if _, err := regularFile(root, *s.NextPrompt); err != nil {
			return fmt.Errorf("next_prompt: %w", err)
		}
	}
	return nil
}

func reportIdentity(data []byte, phase int) error {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != fmt.Sprintf("# FlowForge Phase %d Completion Report", phase) {
		return errors.New("report title must identify the current phase's Completion Report")
	}
	values := make(map[string]string)
	section := ""
	for _, raw := range lines[1:] {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimPrefix(line, "## ")
			if _, exists := values[section]; exists {
				return fmt.Errorf("report has duplicate section %s", section)
			}
			values[section] = ""
		} else if line != "" && section != "" && values[section] == "" {
			values[section] = line
		}
	}
	if values["Phase"] != fmt.Sprintf("Phase %d", phase) || values["Status"] != "COMPLETED" {
		return errors.New("report Phase and Status must match current_phase and COMPLETED")
	}
	for _, name := range []string{"Summary", "Implemented", "Not Implemented", "Experimental", "Planned", "Tests", "Failure / Edge Case Validation", "Known Limitations", "Git", "Documentation Updated", "Next Recommended Phase", "Notes"} {
		if values[name] == "" {
			return fmt.Errorf("report requires a nonempty %s section", name)
		}
	}
	return nil
}

func regularFile(root, name string) (string, error) {
	if filepath.IsAbs(name) || strings.Contains(name, "\\") || strings.Contains(name, ":") {
		return "", errors.New("path must be relative to the repository using forward slashes")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return "", fmt.Errorf("file does not exist: %s", name)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("file escapes repository root")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("expected regular file: %s", name)
	}
	return resolved, nil
}

func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	data, err := cmd.Output()
	return strings.TrimSpace(string(data)), err
}
