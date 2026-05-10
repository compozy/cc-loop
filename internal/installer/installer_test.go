package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/compozy/cc-loop/internal/loop"
)

func TestInstallCreatesManagedFiles(t *testing.T) {
	t.Parallel()

	paths := mustPaths(t)
	sourceBinary := filepath.Join(t.TempDir(), "cc-loop-source")
	if err := os.WriteFile(sourceBinary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write source binary: %v", err)
	}
	if err := os.MkdirAll(paths.ClaudeConfigDir, 0o755); err != nil {
		t.Fatalf("create Claude Code config dir: %v", err)
	}
	writeJSONDoc(t, paths.HooksPath(), map[string]any{
		"env": map[string]any{
			"KEEP_ME": "true",
		},
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "python3 ./custom_stop.py",
						},
					},
				},
			},
		},
	})

	messages, err := Install(paths, Options{SourceBinary: sourceBinary})
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	if _, err := os.Stat(paths.RuntimeBinaryPath()); err != nil {
		t.Fatalf("expected runtime binary: %v", err)
	}
	if _, err := os.Stat(paths.LoopsDir()); err != nil {
		t.Fatalf("expected loops dir: %v", err)
	}
	if _, err := os.Stat(paths.RuntimeConfigPath()); err != nil {
		t.Fatalf("expected runtime config: %v", err)
	}
	settingsDoc := readJSONFile(t, paths.SettingsPath())
	env, ok := settingsDoc["env"].(map[string]any)
	if !ok || env["KEEP_ME"] != "true" {
		t.Fatalf("expected unrelated settings to be preserved, got %#v", settingsDoc["env"])
	}
	assertHookCommandPresent(t, settingsDoc, "Stop", "python3 ./custom_stop.py")
	assertHookCommandPresent(t, settingsDoc, "Stop", managedHookCommand("stop"))
	assertHookCommandPresent(t, settingsDoc, "UserPromptSubmit", managedHookCommand("user-prompt-submit"))

	joined := strings.Join(messages, "\n")
	assertContains(t, joined, "Installed runtime binary")
	assertContains(t, joined, "Updated managed hook config")
	assertContains(t, joined, "Restart Claude Code")
}

func TestInstallSkipsManagedHooksWhenPluginHooksAreEnabled(t *testing.T) {
	t.Parallel()

	paths := mustPaths(t)
	sourceBinary := writeSourceBinary(t)
	if err := os.MkdirAll(paths.ClaudeConfigDir, 0o755); err != nil {
		t.Fatalf("create Claude Code config dir: %v", err)
	}
	writeJSONDoc(t, paths.HooksPath(), map[string]any{
		"enabledPlugins": map[string]any{
			ccLoopPluginID: true,
		},
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "python3 ./custom_stop.py",
						},
						map[string]any{
							"type":    "command",
							"command": managedHookCommand("stop"),
						},
					},
				},
			},
			"UserPromptSubmit": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": managedHookCommand("user-prompt-submit"),
						},
					},
				},
			},
		},
	})

	messages, err := Install(paths, Options{SourceBinary: sourceBinary})
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	settingsDoc := readJSONFile(t, paths.SettingsPath())
	enabledPlugins, ok := settingsDoc["enabledPlugins"].(map[string]any)
	if !ok || enabledPlugins[ccLoopPluginID] != true {
		t.Fatalf("expected enabled plugin state preserved, got %#v", settingsDoc["enabledPlugins"])
	}
	assertHookCommandPresent(t, settingsDoc, "Stop", "python3 ./custom_stop.py")
	assertHookCommandCount(t, settingsDoc, "Stop", managedHookCommand("stop"), 0)
	assertHookCommandCount(t, settingsDoc, "UserPromptSubmit", managedHookCommand("user-prompt-submit"), 0)

	joined := strings.Join(messages, "\n")
	assertContains(t, joined, "Skipped managed hook config because cc-loop plugin hooks are enabled")
	assertContains(t, joined, "removed duplicate managed hook registrations")
}

func TestInstallPreservesExistingRuntimeConfig(t *testing.T) {
	t.Parallel()

	paths := mustPaths(t)
	sourceBinary := writeSourceBinary(t)
	if err := os.MkdirAll(filepath.Dir(paths.RuntimeConfigPath()), 0o755); err != nil {
		t.Fatalf("create runtime dir: %v", err)
	}
	customConfig := `extra_continuation_guidance = "keep this"`
	if err := os.WriteFile(paths.RuntimeConfigPath(), []byte(customConfig), 0o644); err != nil {
		t.Fatalf("write runtime config: %v", err)
	}

	if _, err := Install(paths, Options{SourceBinary: sourceBinary}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := readFile(t, paths.RuntimeConfigPath()); got != customConfig {
		t.Fatalf("expected runtime config preserved, got %q", got)
	}
}

func TestInstallIsIdempotentForManagedHooks(t *testing.T) {
	t.Parallel()

	paths := mustPaths(t)
	sourceBinary := writeSourceBinary(t)

	if _, err := Install(paths, Options{SourceBinary: sourceBinary}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if _, err := Install(paths, Options{SourceBinary: sourceBinary}); err != nil {
		t.Fatalf("second install: %v", err)
	}

	hooksDoc := readJSONFile(t, paths.HooksPath())
	assertHookCommandCount(t, hooksDoc, "Stop", managedHookCommand("stop"), 1)
	assertHookCommandCount(t, hooksDoc, "UserPromptSubmit", managedHookCommand("user-prompt-submit"), 1)
}

func TestInstallUsesConfiguredStopHookTimeout(t *testing.T) {
	t.Parallel()

	paths := mustPaths(t)
	sourceBinary := writeSourceBinary(t)
	if err := os.MkdirAll(filepath.Dir(paths.RuntimeConfigPath()), 0o755); err != nil {
		t.Fatalf("create runtime config dir: %v", err)
	}
	if err := os.WriteFile(paths.RuntimeConfigPath(), []byte("[hooks]\nstop_timeout_seconds = 1234\n"), 0o644); err != nil {
		t.Fatalf("write runtime config: %v", err)
	}

	if _, err := Install(paths, Options{SourceBinary: sourceBinary}); err != nil {
		t.Fatalf("install: %v", err)
	}

	hooksDoc := readJSONFile(t, paths.HooksPath())
	assertHookTimeout(t, hooksDoc, "Stop", managedHookCommand("stop"), 1234)
	assertHookTimeout(t, hooksDoc, "UserPromptSubmit", managedHookCommand("user-prompt-submit"), 30)
}

func TestUninstallRemovesOnlyManagedHooksAndRuntime(t *testing.T) {
	t.Parallel()

	paths := mustPaths(t)
	if err := os.MkdirAll(paths.RuntimeRoot(), 0o755); err != nil {
		t.Fatalf("create runtime root: %v", err)
	}
	if err := os.MkdirAll(paths.ClaudeConfigDir, 0o755); err != nil {
		t.Fatalf("create Claude Code config dir: %v", err)
	}
	writeJSONDoc(t, paths.HooksPath(), map[string]any{
		"env": map[string]any{
			"KEEP_ME": "true",
		},
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "python3 ./custom_stop.py",
						},
						map[string]any{
							"type":    "command",
							"command": managedHookCommand("stop"),
						},
					},
				},
			},
			"UserPromptSubmit": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": managedHookCommand("user-prompt-submit"),
						},
					},
				},
			},
		},
	})

	messages, err := Uninstall(paths)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(paths.RuntimeRoot()); !os.IsNotExist(err) {
		t.Fatalf("expected runtime removed, stat err: %v", err)
	}
	settingsDoc := readJSONFile(t, paths.SettingsPath())
	env, ok := settingsDoc["env"].(map[string]any)
	if !ok || env["KEEP_ME"] != "true" {
		t.Fatalf("expected unrelated settings to be preserved, got %#v", settingsDoc["env"])
	}
	assertHookCommandPresent(t, settingsDoc, "Stop", "python3 ./custom_stop.py")
	assertHookCommandCount(t, settingsDoc, "Stop", managedHookCommand("stop"), 0)
	assertHookCommandCount(t, settingsDoc, "UserPromptSubmit", managedHookCommand("user-prompt-submit"), 0)

	joined := strings.Join(messages, "\n")
	assertContains(t, joined, "Removed managed hook registrations")
	assertContains(t, joined, "Removed managed runtime directory")
}

func TestStatusRecordsJSONRoundTrip(t *testing.T) {
	t.Parallel()

	record := loop.StatusRecord{
		LoopRecord: loop.LoopRecord{
			Version:   loop.RecordVersion,
			SessionID: "sess-1",
			Name:      "qa",
			Status:    loop.StatusActive,
		},
		Path: "/tmp/loop.json",
	}

	content, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal status record: %v", err)
	}
	assertContains(t, string(content), `"path":"/tmp/loop.json"`)
}

func TestManagedHooksTemplateMatchesBundledPluginHooks(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	expected := readJSONFile(t, filepath.Join(root, "plugins", "cc-loop", "hooks", "hooks.json"))
	actual := normalizeJSONDoc(t, managedHooksTemplate(loop.DefaultStopHookTimeoutSeconds))
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("managed hooks template diverged from bundled plugin hooks\nactual: %#v\nexpected: %#v", actual, expected)
	}
}

func mustPaths(t *testing.T) loop.Paths {
	t.Helper()
	paths, err := loop.NewPaths(filepath.Join(t.TempDir(), ".claude"))
	if err != nil {
		t.Fatalf("new paths: %v", err)
	}
	return paths
}

func writeSourceBinary(t *testing.T) string {
	t.Helper()
	sourceBinary := filepath.Join(t.TempDir(), "cc-loop-source")
	if err := os.WriteFile(sourceBinary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write source binary: %v", err)
	}
	return sourceBinary
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func assertContains(t *testing.T, haystack string, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected %q to contain %q", haystack, needle)
	}
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(content, &payload); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return payload
}

func writeJSONDoc(t *testing.T, path string, payload map[string]any) {
	t.Helper()
	content, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertHookCommandPresent(t *testing.T, hooksDoc map[string]any, eventName string, command string) {
	t.Helper()
	if countHookCommand(t, hooksDoc, eventName, command) == 0 {
		t.Fatalf("expected command %q under hooks.%s", command, eventName)
	}
}

func assertHookCommandCount(t *testing.T, hooksDoc map[string]any, eventName string, command string, expected int) {
	t.Helper()
	if count := countHookCommand(t, hooksDoc, eventName, command); count != expected {
		t.Fatalf("expected command %q under hooks.%s %d time(s), got %d", command, eventName, expected, count)
	}
}

func assertHookTimeout(t *testing.T, hooksDoc map[string]any, eventName string, command string, expected int) {
	t.Helper()
	hook := findHookCommand(t, hooksDoc, eventName, command)
	timeout, ok := hook["timeout"].(float64)
	if !ok {
		t.Fatalf("expected timeout for command %q under hooks.%s, got %#v", command, eventName, hook["timeout"])
	}
	if int(timeout) != expected {
		t.Fatalf("expected timeout %d for command %q under hooks.%s, got %v", expected, command, eventName, timeout)
	}
}

func countHookCommand(t *testing.T, hooksDoc map[string]any, eventName string, command string) int {
	t.Helper()
	hooksRoot, ok := hooksDoc["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks document missing hooks object: %#v", hooksDoc["hooks"])
	}
	matcherGroups, ok := hooksRoot[eventName].([]any)
	if !ok {
		return 0
	}
	count := 0
	for _, matcherGroupAny := range matcherGroups {
		matcherGroup, ok := matcherGroupAny.(map[string]any)
		if !ok {
			t.Fatalf("hooks.%s entry is not an object: %#v", eventName, matcherGroupAny)
		}
		hooks, ok := matcherGroup["hooks"].([]any)
		if !ok {
			t.Fatalf("hooks.%s.hooks is not a list: %#v", eventName, matcherGroup["hooks"])
		}
		for _, hookAny := range hooks {
			hook, ok := hookAny.(map[string]any)
			if !ok {
				t.Fatalf("hooks.%s hook entry is not an object: %#v", eventName, hookAny)
			}
			if hook["command"] == command {
				count++
			}
		}
	}
	return count
}

func findHookCommand(t *testing.T, hooksDoc map[string]any, eventName string, command string) map[string]any {
	t.Helper()
	hooksRoot, ok := hooksDoc["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks document missing hooks object: %#v", hooksDoc["hooks"])
	}
	matcherGroups, ok := hooksRoot[eventName].([]any)
	if !ok {
		t.Fatalf("hooks.%s missing", eventName)
	}
	for _, matcherGroupAny := range matcherGroups {
		matcherGroup, ok := matcherGroupAny.(map[string]any)
		if !ok {
			t.Fatalf("unexpected matcher group %#v", matcherGroupAny)
		}
		hooks, ok := matcherGroup["hooks"].([]any)
		if !ok {
			continue
		}
		for _, hookAny := range hooks {
			hook, ok := hookAny.(map[string]any)
			if !ok {
				t.Fatalf("unexpected hook %#v", hookAny)
			}
			if hook["command"] == command {
				return hook
			}
		}
	}
	t.Fatalf("expected command %q under hooks.%s", command, eventName)
	return nil
}

func normalizeJSONDoc(t *testing.T, payload any) map[string]any {
	t.Helper()
	content, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal JSON doc: %v", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(content, &normalized); err != nil {
		t.Fatalf("unmarshal normalized JSON doc: %v", err)
	}
	return normalized
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatal("could not find repo root")
		}
		wd = parent
	}
}
