package version

import (
	"runtime/debug"
	"testing"
)

func TestResolveUsesBuildInfoForGoInstallVersion(t *testing.T) {
	originalVersion := Version
	originalCommit := Commit
	originalDate := Date
	t.Cleanup(func() {
		Version = originalVersion
		Commit = originalCommit
		Date = originalDate
	})

	Version = "dev"
	Commit = "none"
	Date = "unknown"

	info := &debug.BuildInfo{
		Main: debug.Module{
			Version: "v0.1.1",
		},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "6e2cd9345a19cd994a5131c4e8b17008f862d3fe"},
			{Key: "vcs.time", Value: "2026-05-02T21:26:44Z"},
		},
	}

	version, commit, date := resolve(info, true)
	if version != "0.1.1" {
		t.Fatalf("expected version 0.1.1, got %q", version)
	}
	if commit != "6e2cd93" {
		t.Fatalf("expected short commit 6e2cd93, got %q", commit)
	}
	if date != "2026-05-02T21:26:44Z" {
		t.Fatalf("expected build date from build info, got %q", date)
	}
}

func TestResolvePrefersInjectedReleaseMetadata(t *testing.T) {
	originalVersion := Version
	originalCommit := Commit
	originalDate := Date
	t.Cleanup(func() {
		Version = originalVersion
		Commit = originalCommit
		Date = originalDate
	})

	Version = "0.1.1"
	Commit = "abc1234"
	Date = "2026-05-02T21:27:42Z"

	info := &debug.BuildInfo{
		Main: debug.Module{
			Version: "v0.2.0",
		},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "ffffffffffffffffffffffffffffffffffffffff"},
			{Key: "vcs.time", Value: "2026-06-01T00:00:00Z"},
		},
	}

	version, commit, date := resolve(info, true)
	if version != "0.1.1" {
		t.Fatalf("expected injected version, got %q", version)
	}
	if commit != "abc1234" {
		t.Fatalf("expected injected commit, got %q", commit)
	}
	if date != "2026-05-02T21:27:42Z" {
		t.Fatalf("expected injected date, got %q", date)
	}
}
