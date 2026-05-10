package releaseconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const releasePRModule = "github.com/compozy/releasepr@v0.0.21"

func TestReleaseWorkflowCreatesReleasePullRequests(t *testing.T) {
	t.Parallel()

	workflow := readRepoFile(t, ".github", "workflows", "release.yml")

	assertContains(t, workflow, "release-pr:")
	assertContains(t, workflow, "name: Create/Update Release PR")
	assertContains(t, workflow, "PR_RELEASE_MODULE: "+releasePRModule)
	assertContains(t, workflow, "INITIAL_VERSION: \"v0.1.0\"")
	assertContains(t, workflow, "actions: write")
	assertContains(t, workflow, "pull-requests: write")
	assertContains(t, workflow, "mode:")
	assertContains(t, workflow, "head_ref:")
	assertContains(t, workflow, "pr_number:")
	assertContains(t, workflow, "go run \"${{ env.PR_RELEASE_MODULE }}\" \"${args[@]}\"")
	assertContains(t, workflow, "pr-release --enable-rollback --ci-output")
	assertContains(t, workflow, "has_release_pr=true")
	assertContains(t, workflow, "has_release_pr=false")
	assertContains(t, workflow, "release_branch=$branch")
}

func TestReleaseWorkflowDispatchesReleasePullRequestChecks(t *testing.T) {
	t.Parallel()

	workflow := readRepoFile(t, ".github", "workflows", "release.yml")

	assertContains(t, workflow, "Sync Plugin Manifest Version")
	assertContains(t, workflow, "plugins/cc-loop/.claude-plugin/plugin.json")
	assertContains(t, workflow, `payload["version"] = os.environ["RELEASE_BRANCH"].removeprefix("release/v")`)
	assertContains(t, workflow, "Dispatch Release PR Checks")
	assertContains(t, workflow, "steps.pr_release.outputs.has_release_pr == 'true'")
	assertContains(t, workflow, "RELEASE_BRANCH: ${{ steps.pr_release.outputs.release_branch }}")
	assertContains(t, workflow, "branch=\"$RELEASE_BRANCH\"")
	assertContains(t, workflow, "gh workflow run ci.yml --ref \"$branch\"")
	assertContains(t, workflow, "gh workflow run release.yml")
	assertContains(t, workflow, "-f mode=dry-run")
	assertContains(t, workflow, "-f head_ref=\"$branch\"")
	assertContains(t, workflow, "-f pr_number=\"$pr_number\"")
}

func TestReleaseWorkflowDryRunsReleasePullRequests(t *testing.T) {
	t.Parallel()

	workflow := readRepoFile(t, ".github", "workflows", "release.yml")

	assertContains(t, workflow, "dry-run:")
	assertContains(t, workflow, "name: Dry-Run Release Check")
	assertContains(t, workflow, "startsWith(github.event.pull_request.title, 'release: Release ')")
	assertContains(t, workflow, "startsWith(github.event.pull_request.title, 'ci(release): Release ')")
	assertContains(t, workflow, "inputs.mode == 'dry-run'")
	assertContains(t, workflow, "ref: ${{ github.event_name == 'workflow_dispatch' && inputs.head_ref || github.ref }}")
	assertContains(t, workflow, "GITHUB_HEAD_REF: ${{ github.event_name == 'workflow_dispatch' && inputs.head_ref || github.head_ref }}")
	assertContains(t, workflow, "GITHUB_ISSUE_NUMBER: ${{ github.event_name == 'workflow_dispatch' && inputs.pr_number || github.event.pull_request.number }}")
	assertContains(t, workflow, "go run \"${{ env.PR_RELEASE_MODULE }}\" dry-run --ci-output")
}

func TestReleaseWorkflowPublishesFromReleaseBodyAfterMerge(t *testing.T) {
	t.Parallel()

	workflow := readRepoFile(t, ".github", "workflows", "release.yml")

	assertContains(t, workflow, "release:")
	assertContains(t, workflow, "name: Publish Release")
	assertContains(t, workflow, "startsWith(github.event.head_commit.message, 'release:')")
	assertContains(t, workflow, "startsWith(github.event.head_commit.message, 'ci(release):')")
	assertContains(t, workflow, "git cliff --bumped-version")
	assertContains(t, workflow, "git push origin \"$tag\"")
	assertContains(t, workflow, "--release-notes=RELEASE_BODY.md")
	assertNotContains(t, workflow, "--release-notes=RELEASE_NOTES.md")
	assertNotContains(t, workflow, "goreleaser-pro")
	assertNotContains(t, workflow, "GORELEASER_KEY")
	assertNotContains(t, workflow, "tags:\n      - \"v*\"")
}

func TestReleaseWorkflowAvoidsBrokenReleasePRModules(t *testing.T) {
	t.Parallel()

	workflow := readRepoFile(t, ".github", "workflows", "release.yml")
	brokenModules := []string{
		"github.com/compozy/releasepr@v0.0.17",
		"github.com/compozy/releasepr@v0.0.18",
		"github.com/compozy/releasepr@v0.0.19",
		"github.com/compozy/releasepr@v0.0.20",
	}
	for _, module := range brokenModules {
		module := module
		t.Run(module, func(t *testing.T) {
			t.Parallel()
			assertNotContains(t, workflow, module)
		})
	}
}

func TestReleaseConfigAlignsWithReleasePRTooling(t *testing.T) {
	t.Parallel()

	cliff := readRepoFile(t, "cliff.toml")
	goreleaser := readRepoFile(t, ".goreleaser.yml")

	assertContains(t, cliff, `initial_tag = "v0.1.0"`)
	assertContains(t, cliff, `{ message = "^ci\\(release\\):", skip = true }`)
	assertContains(t, goreleaser, "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_")
	assertContains(t, goreleaser, `- "^ci\\(release\\): "`)
	assertNotContains(t, goreleaser, "goreleaser-pro")
	assertNotContains(t, goreleaser, "pro: true")
}

func TestReleaseNotesDirectoryIsTracked(t *testing.T) {
	t.Parallel()

	content := readRepoFile(t, ".release-notes", ".gitkeep")
	if strings.TrimSpace(content) != "" {
		t.Fatalf("expected .release-notes/.gitkeep to be empty, got %q", content)
	}
}

func readRepoFile(t *testing.T, path ...string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(append([]string{repoRoot(t)}, path...)...))
	if err != nil {
		t.Fatalf("read repo file %s: %v", filepath.Join(path...), err)
	}
	return string(content)
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

func assertContains(t *testing.T, content string, needle string) {
	t.Helper()
	if !strings.Contains(content, needle) {
		t.Fatalf("expected content to contain %q", needle)
	}
}

func assertNotContains(t *testing.T, content string, needle string) {
	t.Helper()
	if strings.Contains(content, needle) {
		t.Fatalf("expected content not to contain %q", needle)
	}
}
