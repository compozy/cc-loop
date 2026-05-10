package loop

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildConfiguredCommandParsesStringCommand(t *testing.T) {
	t.Parallel()

	command, args, _, err := buildConfiguredCommand(`/bin/echo "hello world" 'and more'`, t.TempDir(), commandExpansion{})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}
	if command != "/bin/echo" {
		t.Fatalf("unexpected command %q", command)
	}
	if !slices.Equal(args, []string{"hello world", "and more"}) {
		t.Fatalf("unexpected args %#v", args)
	}
}

func TestBuildConfiguredCommandEscapesScalarVariables(t *testing.T) {
	t.Parallel()

	prompt := `ship it; rm -rf / "$HOME" --fake-flag glob-*`
	command, args, env, err := buildConfiguredCommand(`runner --prompt=$PROMPT $PROMPT`, t.TempDir(), commandExpansion{
		Values: map[string]string{
			"PROMPT": prompt,
		},
		EnvPrefix: "CC_LOOP_TEST_",
	})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}
	if command != "runner" {
		t.Fatalf("unexpected command %q", command)
	}
	want := []string{"--prompt=" + prompt, prompt}
	if !slices.Equal(args, want) {
		t.Fatalf("unexpected args %#v, want %#v", args, want)
	}
	if !slices.Contains(env, "CC_LOOP_TEST_PROMPT="+prompt) {
		t.Fatalf("expected prompt env in %#v", env)
	}
}

func TestBuildConfiguredCommandEscapesDoubleQuotedScalarVariables(t *testing.T) {
	t.Parallel()

	value := `quoted "value" with $HOME and \slash`
	_, args, _, err := buildConfiguredCommand(`runner "--value=$VALUE"`, t.TempDir(), commandExpansion{
		Values: map[string]string{
			"VALUE": value,
		},
	})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}
	if !slices.Equal(args, []string{"--value=" + value}) {
		t.Fatalf("unexpected args %#v", args)
	}
}

func TestBuildConfiguredCommandExpandsMultiArgVariables(t *testing.T) {
	t.Parallel()

	_, args, _, err := buildConfiguredCommand(`claude -p $MODEL_ARGV $REASONING_ARGV`, t.TempDir(), commandExpansion{
		Multi: map[string][]string{
			"MODEL_ARGV":     {"--model", "opus"},
			"REASONING_ARGV": {"--effort", "xhigh"},
		},
	})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}
	want := []string{
		"-p",
		"--model", "opus",
		"--effort", "xhigh",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("unexpected args %#v, want %#v", args, want)
	}
}

func TestBuildConfiguredCommandRejectsMultiArgVariableInsideDoubleQuotes(t *testing.T) {
	t.Parallel()

	_, _, _, err := buildConfiguredCommand(`claude "$MODEL_ARGV"`, t.TempDir(), commandExpansion{
		Multi: map[string][]string{
			"MODEL_ARGV": {"--model", "opus"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "multi-argument variable") {
		t.Fatalf("expected multi-argument variable error, got %v", err)
	}
}

func TestBuildConfiguredCommandLeavesUnknownVariablesLiteral(t *testing.T) {
	t.Setenv("HOME", "/should/not/expand")
	_, args, _, err := buildConfiguredCommand(`runner $HOME "$OTHER" \$ESCAPED`, t.TempDir(), commandExpansion{})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}
	want := []string{"$HOME", "$OTHER", "$ESCAPED"}
	if !slices.Equal(args, want) {
		t.Fatalf("unexpected args %#v, want %#v", args, want)
	}
}

func TestBuildConfiguredCommandResolvesRelativeExecutable(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	command, args, _, err := buildConfiguredCommand(`scripts/run --flag`, cwd, commandExpansion{})
	if err != nil {
		t.Fatalf("build command: %v", err)
	}
	if command != filepath.Join(cwd, "scripts/run") {
		t.Fatalf("unexpected command %q", command)
	}
	if !slices.Equal(args, []string{"--flag"}) {
		t.Fatalf("unexpected args %#v", args)
	}
}

func TestBuildConfiguredCommandRejectsInvalidOrEmptyCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		commandLine string
		wantError   string
	}{
		{name: "empty", commandLine: "   ", wantError: "command string is empty"},
		{name: "unmatched quote", commandLine: `runner "unterminated`, wantError: "parse command string"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, _, err := buildConfiguredCommand(tt.commandLine, t.TempDir(), commandExpansion{})
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
			}
		})
	}
}
