package pluginmeta

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPluginManifestAndMarketplaceMetadata(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	manifest := readJSONFile(t, filepath.Join(root, "plugins", "cc-loop", ".claude-plugin", "plugin.json"))
	if manifest["name"] != "cc-loop" {
		t.Fatalf("unexpected plugin name %#v", manifest["name"])
	}
	version, ok := manifest["version"].(string)
	if !ok {
		t.Fatalf("unexpected plugin version %#v", manifest["version"])
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(version) {
		t.Fatalf("plugin version must be semantic, got %q", version)
	}
	if manifest["skills"] != "./skills/" {
		t.Fatalf("unexpected skills path %#v", manifest["skills"])
	}
	if _, ok := manifest["hooks"]; ok {
		t.Fatal("manifest must not reference hooks/hooks.json because Claude Code auto-loads the standard hooks file")
	}
	assertRelativePluginPath(t, manifest["skills"].(string))

	hooks := readJSONFile(t, filepath.Join(root, "plugins", "cc-loop", "hooks", "hooks.json"))
	hooksRoot, ok := hooks["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks.json missing hooks object")
	}
	if _, ok := hooksRoot["UserPromptSubmit"]; !ok {
		t.Fatal("hooks.json missing UserPromptSubmit")
	}
	if _, ok := hooksRoot["Stop"]; !ok {
		t.Fatal("hooks.json missing Stop")
	}

	marketplace := readJSONFile(t, filepath.Join(root, ".claude-plugin", "marketplace.json"))
	plugins, ok := marketplace["plugins"].([]any)
	if !ok || len(plugins) != 1 {
		t.Fatalf("expected one marketplace plugin, got %#v", marketplace["plugins"])
	}
	entry, ok := plugins[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected marketplace entry %#v", plugins[0])
	}
	if entry["name"] != "cc-loop" {
		t.Fatalf("unexpected marketplace plugin name %#v", entry["name"])
	}
	if entry["version"] != version {
		t.Fatalf("marketplace version %#v must match plugin manifest version %q", entry["version"], version)
	}
	source, ok := entry["source"].(string)
	if !ok {
		t.Fatalf("unexpected source %#v", entry["source"])
	}
	if source != "./plugins/cc-loop" {
		t.Fatalf("unexpected source path %#v", source)
	}
	if !strings.HasPrefix(source, "./") {
		t.Fatalf("source path must start with ./, got %q", source)
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

func assertRelativePluginPath(t *testing.T, value string) {
	t.Helper()
	if !strings.HasPrefix(value, "./") {
		t.Fatalf("plugin path must start with ./, got %q", value)
	}
	if filepath.IsAbs(value) {
		t.Fatalf("plugin path must be relative, got %q", value)
	}
}
