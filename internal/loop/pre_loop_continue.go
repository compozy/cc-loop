package loop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	PreLoopContinueCWDSession   = "session_cwd"
	PreLoopContinueCWDWorkspace = "workspace_root"

	DefaultPreLoopContinueTimeoutSeconds = 60
	DefaultPreLoopContinueMaxOutputBytes = 12000
	DefaultStopHookTimeoutSeconds        = 2700
	DefaultGoalTimeoutSeconds            = 2400
	DefaultGoalInterpretTimeoutSeconds   = 120
	DefaultGoalMaxOutputBytes            = 12000
	DefaultGoalConfirmModel              = "opus"
	DefaultGoalConfirmReasoningEffort    = "high"
	DefaultGoalInterpretModel            = "haiku"
	DefaultGoalInterpretReasoningEffort  = "low"
	GoalHookTimeoutGraceSeconds          = 30
)

func DefaultGoalConfirmCommand() string {
	return "claude -p --permission-mode bypassPermissions --output-format text $MODEL_ARGV $REASONING_ARGV"
}

func defaultGoalInterpreterClaudePrint() string {
	return "claude -p --permission-mode bypassPermissions --output-format json --json-schema $INTERPRET_SCHEMA $INTERPRET_MODEL_ARGV $INTERPRET_REASONING_ARGV"
}

type PreLoopContinueInput struct {
	EventName          string      `json:"event_name"`
	SessionID          string      `json:"session_id"`
	CWD                string      `json:"cwd"`
	WorkspaceRoot      string      `json:"workspace_root"`
	Loop               LoopRecord  `json:"loop"`
	Stop               StopPayload `json:"stop"`
	ContinuationReason string      `json:"continuation_reason"`
	RemainingSeconds   *int        `json:"remaining_seconds,omitempty"`
	Aggressive         bool        `json:"aggressive"`
	ContinuingAt       string      `json:"continuing_at"`
}

type preLoopContinueRunResult struct {
	Output    string
	Warning   string
	Truncated bool
}

type limitedOutputBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedOutputBuffer) Write(payload []byte) (int, error) {
	if b.limit <= 0 {
		b.truncated = true
		return len(payload), nil
	}
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(payload), nil
	}
	if len(payload) > remaining {
		if _, err := b.buffer.Write(payload[:remaining]); err != nil {
			return 0, fmt.Errorf("write limited output: %w", err)
		}
		b.truncated = true
		return len(payload), nil
	}
	if _, err := b.buffer.Write(payload); err != nil {
		return 0, fmt.Errorf("write limited output: %w", err)
	}
	return len(payload), nil
}

func (b *limitedOutputBuffer) String() string {
	return b.buffer.String()
}

func appendPreLoopContinue(ctx context.Context, paths Paths, cfg PreLoopContinueConfig, payload StopPayload, record LoopRecord, remainingSeconds *int, aggressive bool, reason string, now time.Time) string {
	cfg = normalizeRuntimeConfig(RuntimeConfig{PreLoopContinue: cfg}).PreLoopContinue
	if strings.TrimSpace(cfg.Command) == "" {
		return reason
	}

	result := runPreLoopContinue(ctx, paths, cfg, payload, record, remainingSeconds, aggressive, reason, now)
	if result.Warning != "" {
		return strings.TrimSpace(reason + "\n\npre_loop_continue warning:\n" + result.Warning)
	}

	output := strings.TrimSpace(result.Output)
	if output == "" {
		return reason
	}
	if result.Truncated {
		output = strings.TrimSpace(fmt.Sprintf("%s\n[output truncated after %d bytes]", output, cfg.MaxOutputBytes))
	}
	return strings.TrimSpace(reason + "\n\npre_loop_continue output:\n" + output)
}

func runPreLoopContinue(ctx context.Context, paths Paths, cfg PreLoopContinueConfig, payload StopPayload, record LoopRecord, remainingSeconds *int, aggressive bool, reason string, now time.Time) preLoopContinueRunResult {
	cwd, err := resolvePreLoopContinueCWD(cfg, payload, record)
	if err != nil {
		return preLoopContinueRunResult{Warning: err.Error()}
	}

	input := PreLoopContinueInput{
		EventName:          "pre_loop_continue",
		SessionID:          record.SessionID,
		CWD:                cwd,
		WorkspaceRoot:      record.WorkspaceRoot,
		Loop:               record,
		Stop:               payload,
		ContinuationReason: reason,
		RemainingSeconds:   remainingSeconds,
		Aggressive:         aggressive,
		ContinuingAt:       ISOFormat(now),
	}
	stdin, err := json.Marshal(input)
	if err != nil {
		return preLoopContinueRunResult{Warning: fmt.Sprintf("encode input JSON: %v", err)}
	}
	if err := os.MkdirAll(paths.RuntimeRoot(), 0o755); err != nil {
		return preLoopContinueRunResult{Warning: fmt.Sprintf("create runtime directory: %v", err)}
	}
	inputFile, err := os.CreateTemp(paths.RuntimeRoot(), "pre-loop-input-*.json")
	if err != nil {
		return preLoopContinueRunResult{Warning: fmt.Sprintf("create input file: %v", err)}
	}
	inputPath := inputFile.Name()
	cleanup := func() {
		_ = os.Remove(inputPath)
	}
	defer cleanup()
	if _, err := inputFile.Write(stdin); err != nil {
		_ = inputFile.Close()
		return preLoopContinueRunResult{Warning: fmt.Sprintf("write input file: %v", err)}
	}
	if err := inputFile.Close(); err != nil {
		return preLoopContinueRunResult{Warning: fmt.Sprintf("close input file: %v", err)}
	}

	values := map[string]string{
		"INPUT_JSON":          string(stdin),
		"INPUT_FILE":          inputPath,
		"WORKSPACE_ROOT":      record.WorkspaceRoot,
		"CWD":                 cwd,
		"SESSION_ID":          record.SessionID,
		"LOOP_NAME":           record.Name,
		"LOOP_SLUG":           record.Slug,
		"CONTINUATION_REASON": reason,
		"RUNS_LOG_PATH":       paths.RunsLogPath(),
		"CLAUDE_CONFIG_DIR":   paths.ClaudeConfigDir,
	}
	command, args, env, err := buildConfiguredCommand(cfg.Command, cwd, commandExpansion{
		Values:    values,
		EnvPrefix: "CC_LOOP_PRE_LOOP_",
	})
	if err != nil {
		return preLoopContinueRunResult{Warning: err.Error()}
	}

	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout limitedOutputBuffer
	stdout.limit = cfg.MaxOutputBytes
	cmd := exec.CommandContext(runCtx, command, args...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard

	err = cmd.Run()
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return preLoopContinueRunResult{Warning: fmt.Sprintf("command timed out after %s", timeout)}
	}
	if err != nil {
		return preLoopContinueRunResult{Warning: fmt.Sprintf("command failed: %s", describeCommandError(err))}
	}

	return preLoopContinueRunResult{
		Output:    stdout.String(),
		Truncated: stdout.truncated,
	}
}

func resolvePreLoopContinueCWD(cfg PreLoopContinueConfig, payload StopPayload, record LoopRecord) (string, error) {
	sessionCWD := strings.TrimSpace(payload.CWD)
	if sessionCWD == "" {
		sessionCWD = strings.TrimSpace(record.CWD)
	}
	workspaceRoot := strings.TrimSpace(record.WorkspaceRoot)

	var selected string
	switch strings.TrimSpace(cfg.CWD) {
	case "", PreLoopContinueCWDSession:
		selected = sessionCWD
	case PreLoopContinueCWDWorkspace:
		selected = workspaceRoot
		if selected == "" {
			selected = sessionCWD
		}
	default:
		return "", fmt.Errorf("invalid cwd %q; expected %q or %q", cfg.CWD, PreLoopContinueCWDSession, PreLoopContinueCWDWorkspace)
	}
	if selected == "" {
		return "", fmt.Errorf("cannot resolve cwd for pre_loop_continue")
	}
	abs, err := filepath.Abs(selected)
	if err != nil {
		return "", fmt.Errorf("resolve cwd %q: %w", selected, err)
	}
	return abs, nil
}

func describeCommandError(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Sprintf("exit status %d", exitErr.ExitCode())
	}
	return err.Error()
}
