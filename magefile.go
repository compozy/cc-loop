//go:build mage

package main

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

const (
	golangciLintVersion = "v1.64.8"
	goreleaserModule    = "github.com/goreleaser/goreleaser/v2@latest"
	binDir              = "bin"
	cliBinary           = "cc-loop"
	versionPackage      = "github.com/compozy/cc-loop/internal/version"
)

var Default = Verify

func Deps() error {
	return sh.RunV("go", "mod", "tidy")
}

func Fmt() error {
	files, err := goFiles(".")
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	args := append([]string{"-w"}, files...)
	return sh.RunV("gofmt", args...)
}

func Vet() error {
	return sh.RunV("go", "vet", "./...")
}

func Lint() error {
	return sh.RunV(
		"go",
		"run",
		"github.com/golangci/golangci-lint/cmd/golangci-lint@"+golangciLintVersion,
		"run",
		"./...",
	)
}

func Test() error {
	return sh.RunV("go", "test", "-race", "./...")
}

func Build() error {
	ldflags := buildLDFlags()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	if err := sh.RunV("go", "build", "-ldflags", ldflags, "./..."); err != nil {
		return err
	}
	return sh.RunV("go", "build", "-ldflags", ldflags, "-o", filepath.Join(binDir, cliBinary), "./cmd/"+cliBinary)
}

func ReleaseCheck() error {
	return sh.RunV("go", "run", goreleaserModule, "check")
}

func ReleaseSnapshot() error {
	return sh.RunV("go", "run", goreleaserModule, "release", "--snapshot", "--clean", "--skip=publish,sign,sbom")
}

func Verify() {
	mg.SerialDeps(Fmt, Vet, Lint, Test, Build)
}

func goFiles(root string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (name == "vendor" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

func buildLDFlags() string {
	version := gitOutput("describe", "--tags", "--always", "--dirty")
	if version == "" {
		version = "dev"
	}

	commit := gitOutput("rev-parse", "--short", "HEAD")
	if commit == "" {
		commit = "unknown"
	}

	date := time.Now().UTC().Format(time.RFC3339)

	return strings.Join([]string{
		"-X " + versionPackage + ".Version=" + version,
		"-X " + versionPackage + ".Commit=" + commit,
		"-X " + versionPackage + ".Date=" + date,
	}, " ")
}

func gitOutput(args ...string) string {
	cmd := exec.CommandContext(context.Background(), "git", args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
