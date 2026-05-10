package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compozy/cc-loop/internal/loop"
)

func TestUpgradeDownloadsVerifiesInstallsAndRefreshesMarketplace(t *testing.T) {
	archiveName, binaryName, err := releaseArchiveName("0.1.1", "darwin", "arm64")
	if err != nil {
		t.Fatalf("archive name: %v", err)
	}
	archiveContent := tarGzipArchive(t, binaryName, []byte("new-binary\n"))
	checksum := sha256.Sum256(archiveContent)
	checksumsContent := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(checksum[:]), archiveName))

	server := releaseServer(t, "v0.1.1", map[string][]byte{
		archiveName:     archiveContent,
		"checksums.txt": checksumsContent,
	})

	claudeConfigDir := filepath.Join(t.TempDir(), ".claude")
	paths, err := loop.NewPaths(claudeConfigDir)
	if err != nil {
		t.Fatalf("new paths: %v", err)
	}
	targetBinary := filepath.Join(t.TempDir(), "bin", "cc-loop")
	if err := os.MkdirAll(filepath.Dir(targetBinary), 0o755); err != nil {
		t.Fatalf("create target dir: %v", err)
	}
	if err := os.WriteFile(targetBinary, []byte("old-binary\n"), 0o755); err != nil {
		t.Fatalf("write old target: %v", err)
	}
	claudeLog := filepath.Join(t.TempDir(), "claude-args.log")
	fakeClaude := writeFakeClaude(t, claudeLog)

	messages, err := Upgrade(context.Background(), paths, Options{
		Version:      "v0.1.1",
		APIBaseURL:   server.URL,
		TargetBinary: targetBinary,
		ClaudeBinary: fakeClaude,
		GOOS:         "darwin",
		GOARCH:       "arm64",
	})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	if got := readFile(t, targetBinary); got != "new-binary\n" {
		t.Fatalf("expected target binary updated, got %q", got)
	}
	if got := readFile(t, paths.RuntimeBinaryPath()); got != "new-binary\n" {
		t.Fatalf("expected managed runtime updated, got %q", got)
	}
	if got := readFile(t, claudeLog); !strings.Contains(got, "plugin marketplace add compozy/cc-loop@v0.1.1") {
		t.Fatalf("expected marketplace add command, got %q", got)
	}
	if got := readFile(t, claudeLog); !strings.Contains(got, "plugin marketplace update cc-loop-plugins") {
		t.Fatalf("expected marketplace update command, got %q", got)
	}

	joined := strings.Join(messages, "\n")
	assertContains(t, joined, "Downloaded cc-loop v0.1.1")
	assertContains(t, joined, "Verified checksum")
	assertContains(t, joined, "Updated CLI binary")
	assertContains(t, joined, "Installed runtime binary")
	assertContains(t, joined, "Refreshed Claude Code plugin marketplace")
}

func TestUpgradeReplacesMarketplaceWhenConfiguredFromDifferentSource(t *testing.T) {
	archiveName, binaryName, err := releaseArchiveName("0.1.2", "darwin", "arm64")
	if err != nil {
		t.Fatalf("archive name: %v", err)
	}
	archiveContent := tarGzipArchive(t, binaryName, []byte("newer-binary\n"))
	checksum := sha256.Sum256(archiveContent)
	checksumsContent := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(checksum[:]), archiveName))

	server := releaseServer(t, "v0.1.2", map[string][]byte{
		archiveName:     archiveContent,
		"checksums.txt": checksumsContent,
	})

	claudeConfigDir := filepath.Join(t.TempDir(), ".claude")
	paths, err := loop.NewPaths(claudeConfigDir)
	if err != nil {
		t.Fatalf("new paths: %v", err)
	}
	targetBinary := filepath.Join(t.TempDir(), "bin", "cc-loop")
	if err := os.MkdirAll(filepath.Dir(targetBinary), 0o755); err != nil {
		t.Fatalf("create target dir: %v", err)
	}
	if err := os.WriteFile(targetBinary, []byte("old-binary\n"), 0o755); err != nil {
		t.Fatalf("write old target: %v", err)
	}
	claudeLog := filepath.Join(t.TempDir(), "claude-args.log")
	fakeClaude := writeConflictingMarketplaceClaude(t, claudeLog)

	messages, err := Upgrade(context.Background(), paths, Options{
		Version:      "v0.1.2",
		APIBaseURL:   server.URL,
		TargetBinary: targetBinary,
		ClaudeBinary: fakeClaude,
		GOOS:         "darwin",
		GOARCH:       "arm64",
	})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	if got := readFile(t, targetBinary); got != "newer-binary\n" {
		t.Fatalf("expected target binary updated, got %q", got)
	}
	logLines := strings.Split(strings.TrimSpace(readFile(t, claudeLog)), "\n")
	expected := []string{
		"plugin marketplace add compozy/cc-loop@v0.1.2",
		"plugin marketplace remove cc-loop-plugins",
		"plugin marketplace add compozy/cc-loop@v0.1.2",
		"plugin marketplace update cc-loop-plugins",
	}
	if strings.Join(logLines, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("unexpected claude commands\nexpected: %#v\nactual: %#v", expected, logLines)
	}

	joined := strings.Join(messages, "\n")
	assertContains(t, joined, "Replaced existing Claude Code plugin marketplace cc-loop-plugins")
	assertContains(t, joined, "Restart Claude Code")
}

func TestUpgradeRejectsChecksumMismatchBeforeReplacingBinary(t *testing.T) {
	t.Parallel()

	archiveName, binaryName, err := releaseArchiveName("0.1.1", "darwin", "arm64")
	if err != nil {
		t.Fatalf("archive name: %v", err)
	}
	archiveContent := tarGzipArchive(t, binaryName, []byte("new-binary\n"))
	checksumsContent := []byte(strings.Repeat("0", sha256.Size*2) + "  " + archiveName + "\n")
	server := releaseServer(t, "v0.1.1", map[string][]byte{
		archiveName:     archiveContent,
		"checksums.txt": checksumsContent,
	})

	claudeConfigDir := filepath.Join(t.TempDir(), ".claude")
	paths, err := loop.NewPaths(claudeConfigDir)
	if err != nil {
		t.Fatalf("new paths: %v", err)
	}
	targetBinary := filepath.Join(t.TempDir(), "bin", "cc-loop")
	if err := os.MkdirAll(filepath.Dir(targetBinary), 0o755); err != nil {
		t.Fatalf("create target dir: %v", err)
	}
	if err := os.WriteFile(targetBinary, []byte("old-binary\n"), 0o755); err != nil {
		t.Fatalf("write old target: %v", err)
	}

	_, err = Upgrade(context.Background(), paths, Options{
		Version:         "0.1.1",
		APIBaseURL:      server.URL,
		TargetBinary:    targetBinary,
		GOOS:            "darwin",
		GOARCH:          "arm64",
		SkipMarketplace: true,
	})
	if err == nil {
		t.Fatal("expected checksum error")
	}
	assertContains(t, err.Error(), "checksum mismatch")
	if got := readFile(t, targetBinary); got != "old-binary\n" {
		t.Fatalf("expected target binary preserved, got %q", got)
	}
	if _, err := os.Stat(paths.RuntimeBinaryPath()); !os.IsNotExist(err) {
		t.Fatalf("expected runtime not installed, stat err: %v", err)
	}
}

func TestReleaseArchiveName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		goos       string
		goarch     string
		archive    string
		binaryName string
	}{
		{
			name:       "darwin arm64",
			goos:       "darwin",
			goarch:     "arm64",
			archive:    "cc-loop_0.1.1_darwin_arm64.tar.gz",
			binaryName: "cc-loop",
		},
		{
			name:       "linux amd64",
			goos:       "linux",
			goarch:     "amd64",
			archive:    "cc-loop_0.1.1_linux_x86_64.tar.gz",
			binaryName: "cc-loop",
		},
		{
			name:       "windows amd64",
			goos:       "windows",
			goarch:     "amd64",
			archive:    "cc-loop_0.1.1_windows_x86_64.zip",
			binaryName: "cc-loop.exe",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			archive, binaryName, err := releaseArchiveName("0.1.1", tt.goos, tt.goarch)
			if err != nil {
				t.Fatalf("release archive name: %v", err)
			}
			if archive != tt.archive {
				t.Fatalf("expected archive %q, got %q", tt.archive, archive)
			}
			if binaryName != tt.binaryName {
				t.Fatalf("expected binary %q, got %q", tt.binaryName, binaryName)
			}
		})
	}
}

func releaseServer(t *testing.T, tag string, assets map[string][]byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/compozy/cc-loop/releases/") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"tag_name":%q,"assets":[`, tag)
			index := 0
			for name := range assets {
				if index > 0 {
					fmt.Fprint(w, ",")
				}
				fmt.Fprintf(w, `{"name":%q,"browser_download_url":"%s/assets/%s"}`, name, serverURL(r), name)
				index++
			}
			fmt.Fprint(w, `]}`)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			name := strings.TrimPrefix(r.URL.Path, "/assets/")
			content, ok := assets[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if _, err := w.Write(content); err != nil {
				t.Errorf("write response: %v", err)
			}
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func tarGzipArchive(t *testing.T, binaryName string, binaryContent []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	header := &tar.Header{
		Name: "cc-loop/" + binaryName,
		Mode: 0o755,
		Size: int64(len(binaryContent)),
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := io.Copy(tarWriter, bytes.NewReader(binaryContent)); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buffer.Bytes()
}

func writeFakeClaude(t *testing.T, logPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CC_LOOP_TEST_CLAUDE_LOG\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("CC_LOOP_TEST_CLAUDE_LOG", logPath)
	return path
}

func writeConflictingMarketplaceClaude(t *testing.T, logPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	markerPath := filepath.Join(t.TempDir(), "first-add-failed")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$CC_LOOP_TEST_CLAUDE_LOG"
if [ "$*" = "plugin marketplace add compozy/cc-loop@v0.1.2" ] && [ ! -f "$CC_LOOP_TEST_CONFLICT_MARKER" ]; then
  touch "$CC_LOOP_TEST_CONFLICT_MARKER"
  printf "Error: marketplace 'cc-loop-plugins' is already added from a different source; remove it before adding this source\n" >&2
  exit 1
fi
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("CC_LOOP_TEST_CLAUDE_LOG", logPath)
	t.Setenv("CC_LOOP_TEST_CONFLICT_MARKER", markerPath)
	return path
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
