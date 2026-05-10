package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/compozy/cc-loop/internal/loop"
)

func TestVersionCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Execute(context.Background(), []string{"version"}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("execute version: %v\nstderr: %s", err, stderr.String())
	}
	assertContains(t, stdout.String(), "commit=")
	assertContains(t, stdout.String(), "date=")
}

func TestRootHelp(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Execute(context.Background(), []string{"--help"}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("execute help: %v\nstderr: %s", err, stderr.String())
	}
	assertContains(t, stdout.String(), "Claude Code lifecycle loop hooks")
	assertContains(t, stdout.String(), "install")
	assertContains(t, stdout.String(), "upgrade")
	assertContains(t, stdout.String(), "status")
}

func TestStatusCommandFiltersActiveRecords(t *testing.T) {
	t.Parallel()

	claudeConfigDir := filepath.Join(t.TempDir(), ".claude")
	paths, err := loop.NewPaths(claudeConfigDir)
	if err != nil {
		t.Fatalf("new paths: %v", err)
	}
	writeStatusLoop(t, paths, "active", loop.StatusActive)
	writeStatusLoop(t, paths, "completed", loop.StatusCompleted)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = Execute(context.Background(), []string{"--claude-config-dir", claudeConfigDir, "status"}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("execute status: %v\nstderr: %s", err, stderr.String())
	}

	var records []loop.StatusRecord
	if err := json.Unmarshal(stdout.Bytes(), &records); err != nil {
		t.Fatalf("decode status output: %v\nraw: %s", err, stdout.String())
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 active record, got %d", len(records))
	}
	if records[0].Name != "active" {
		t.Fatalf("expected active record, got %q", records[0].Name)
	}
}

func TestStatusCommandAllIncludesCompletedRecords(t *testing.T) {
	t.Parallel()

	claudeConfigDir := filepath.Join(t.TempDir(), ".claude")
	paths, err := loop.NewPaths(claudeConfigDir)
	if err != nil {
		t.Fatalf("new paths: %v", err)
	}
	writeStatusLoop(t, paths, "active", loop.StatusActive)
	writeStatusLoop(t, paths, "completed", loop.StatusCompleted)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = Execute(context.Background(), []string{"--claude-config-dir", claudeConfigDir, "status", "--all"}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("execute status --all: %v\nstderr: %s", err, stderr.String())
	}

	var records []loop.StatusRecord
	if err := json.Unmarshal(stdout.Bytes(), &records); err != nil {
		t.Fatalf("decode status output: %v\nraw: %s", err, stdout.String())
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
}

func TestUserPromptSubmitHookReadsStdinAndWritesNoOutputOnSuccess(t *testing.T) {
	t.Parallel()

	claudeConfigDir := filepath.Join(t.TempDir(), ".claude")
	repoRoot := t.TempDir()
	payload := `{"session_id":"sess-1","cwd":` + quoteJSON(t, repoRoot) + `,"prompt":"[[CC_LOOP name=\"qa\" rounds=\"2\"]]\nRun QA."}`

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Execute(context.Background(), []string{"--claude-config-dir", claudeConfigDir, "hook", "user-prompt-submit"}, strings.NewReader(payload), &stdout, &stderr)
	if err != nil {
		t.Fatalf("execute hook: %v\nstderr: %s", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no hook output, got %s", stdout.String())
	}

	paths, err := loop.NewPaths(claudeConfigDir)
	if err != nil {
		t.Fatalf("new paths: %v", err)
	}
	records, err := loop.IterLoopRecords(paths)
	if err != nil {
		t.Fatalf("iterate loops: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 loop, got %d", len(records))
	}
}

func TestStopHookWritesContinuationJSON(t *testing.T) {
	t.Parallel()

	claudeConfigDir := filepath.Join(t.TempDir(), ".claude")
	paths, err := loop.NewPaths(claudeConfigDir)
	if err != nil {
		t.Fatalf("new paths: %v", err)
	}
	activation, ok, err := loop.ExtractActivation(`[[CC_LOOP name="qa" rounds="2"]]` + "\nRun QA.")
	if err != nil {
		t.Fatalf("extract activation: %v", err)
	}
	if !ok {
		t.Fatal("expected activation")
	}
	record := loop.BuildLoopRecord("sess-1", t.TempDir(), t.TempDir(), activation, fixedTime())
	if err := loop.ReplaceLoopFile(loop.CreateLoopPath(paths, record.Slug, fixedTime()), record); err != nil {
		t.Fatalf("write loop: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.RuntimeConfigPath()), 0o755); err != nil {
		t.Fatalf("create runtime config dir: %v", err)
	}
	if err := os.WriteFile(paths.RuntimeConfigPath(), []byte(`[pre_loop_continue]
command = "/bin/sh -c 'printf cli-pre-loop'"
`), 0o644); err != nil {
		t.Fatalf("write runtime config: %v", err)
	}

	payload := `{"session_id":"sess-1","cwd":".","last_assistant_message":"Done."}`
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = Execute(context.Background(), []string{"--claude-config-dir", claudeConfigDir, "hook", "stop"}, strings.NewReader(payload), &stdout, &stderr)
	if err != nil {
		t.Fatalf("execute stop hook: %v\nstderr: %s", err, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode hook output: %v\nraw: %s", err, stdout.String())
	}
	if result["decision"] != "block" {
		t.Fatalf("expected block decision, got %#v", result)
	}
	assertContains(t, result["reason"].(string), "Round 2 of 2 begins now.")
	assertContains(t, result["reason"].(string), "pre_loop_continue output:")
	assertContains(t, result["reason"].(string), "cli-pre-loop")
}

func TestStopHookUsesProjectPreLoopConfigFromPayloadCWD(t *testing.T) {
	t.Parallel()

	claudeConfigDir := filepath.Join(t.TempDir(), ".claude")
	paths, err := loop.NewPaths(claudeConfigDir)
	if err != nil {
		t.Fatalf("new paths: %v", err)
	}
	repoRoot := filepath.Join(t.TempDir(), "repo")
	sessionCWD := filepath.Join(repoRoot, "nested")
	if err := os.MkdirAll(sessionCWD, 0o755); err != nil {
		t.Fatalf("create session cwd: %v", err)
	}
	activation, ok, err := loop.ExtractActivation(`[[CC_LOOP name="qa" rounds="2"]]` + "\nRun QA.")
	if err != nil {
		t.Fatalf("extract activation: %v", err)
	}
	if !ok {
		t.Fatal("expected activation")
	}
	record := loop.BuildLoopRecord("sess-1", repoRoot, repoRoot, activation, fixedTime())
	if err := loop.ReplaceLoopFile(loop.CreateLoopPath(paths, record.Slug, fixedTime()), record); err != nil {
		t.Fatalf("write loop: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.RuntimeConfigPath()), 0o755); err != nil {
		t.Fatalf("create runtime config dir: %v", err)
	}
	if err := os.WriteFile(paths.RuntimeConfigPath(), []byte(`[pre_loop_continue]
command = "/bin/sh -c 'printf cli-global-pre-loop'"
`), 0o644); err != nil {
		t.Fatalf("write runtime config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "cc-loop.toml"), []byte(`[pre_loop_continue]
command = "/bin/sh -c 'printf cli-local-pre-loop'"
`), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	payloadBytes, err := json.Marshal(loop.StopPayload{
		SessionID: "sess-1",
		CWD:       sessionCWD,
	})
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = Execute(context.Background(), []string{"--claude-config-dir", claudeConfigDir, "hook", "stop"}, bytes.NewReader(payloadBytes), &stdout, &stderr)
	if err != nil {
		t.Fatalf("execute stop hook: %v\nstderr: %s", err, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode hook output: %v\nraw: %s", err, stdout.String())
	}
	assertContains(t, result["reason"].(string), "cli-local-pre-loop")
	assertNotContains(t, result["reason"].(string), "cli-global-pre-loop")
}

func TestInstallCommandAcceptsSourceBinaryForTests(t *testing.T) {
	t.Parallel()

	claudeConfigDir := filepath.Join(t.TempDir(), ".claude")
	sourceBinary := filepath.Join(t.TempDir(), "cc-loop-source")
	if err := os.WriteFile(sourceBinary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write source binary: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Execute(context.Background(), []string{"--claude-config-dir", claudeConfigDir, "install", "--source-binary", sourceBinary}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("execute install: %v\nstderr: %s", err, stderr.String())
	}
	assertContains(t, stdout.String(), "Installed runtime binary")
	if _, err := os.Stat(filepath.Join(claudeConfigDir, "cc-loop", "bin", "cc-loop")); err != nil {
		t.Fatalf("expected installed runtime binary: %v", err)
	}
}

func writeStatusLoop(t *testing.T, paths loop.Paths, name string, status string) {
	t.Helper()
	activation := loop.Activation{
		Name:         name,
		Slug:         name,
		LimitMode:    loop.LimitModeRounds,
		TaskPrompt:   "Run QA.",
		RoundsText:   "2",
		TargetRounds: 2,
	}
	record := loop.BuildLoopRecord("sess-1", t.TempDir(), t.TempDir(), activation, fixedTime())
	record.Status = status
	if err := loop.ReplaceLoopFile(loop.CreateLoopPath(paths, record.Slug, fixedTime()), record); err != nil {
		t.Fatalf("write loop: %v", err)
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
}

func quoteJSON(t *testing.T, value string) string {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("quote JSON: %v", err)
	}
	return string(content)
}

func assertContains(t *testing.T, haystack string, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected %q to contain %q", haystack, needle)
	}
}

func assertNotContains(t *testing.T, haystack string, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("expected %q not to contain %q", haystack, needle)
	}
}
