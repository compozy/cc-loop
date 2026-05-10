package loop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	GoalCheckOutcomeCompleted     = "completed"
	GoalCheckOutcomeIncomplete    = "incomplete"
	GoalCheckOutcomeError         = "error"
	GoalCheckOutcomeTimeout       = "timeout"
	GoalCheckOutcomeInvalidOutput = "invalid_output"
)

type GoalCheckVerdict struct {
	Completed         bool     `json:"completed"`
	Confidence        float64  `json:"confidence"`
	Reason            string   `json:"reason"`
	MissingWork       []string `json:"missing_work"`
	NextRoundGuidance string   `json:"next_round_guidance"`
}

type goalCheckResult struct {
	Outcome                  string
	Verdict                  GoalCheckVerdict
	ReviewText               string
	Model                    string
	ReasoningEffort          string
	CommandName              string
	CommandArgCount          int
	InterpretModel           string
	InterpretReasoningEffort string
	InterpretDuration        time.Duration
	Duration                 time.Duration
	Warning                  string
	ErrorSummary             string
}

type goalCheckFiles struct {
	ConfirmPromptPath   string
	ConfirmOutputPath   string
	InterpretSchemaPath string
	InterpretOutputPath string
}

type goalCommandRun struct {
	CommandName     string
	CommandArgCount int
	Stdout          string
	Stderr          string
	TimedOut        bool
	Timeout         time.Duration
	Duration        time.Duration
}

type goalCheckLogEvent struct {
	EventName                     string   `json:"event_name"`
	CheckedAt                     string   `json:"checked_at"`
	SessionID                     string   `json:"session_id"`
	LoopName                      string   `json:"loop_name"`
	LoopSlug                      string   `json:"loop_slug"`
	LoopPath                      string   `json:"loop_path"`
	LimitMode                     string   `json:"limit_mode"`
	ContinueCount                 int      `json:"continue_count"`
	GoalCheckCount                int      `json:"goal_check_count"`
	ConfirmModel                  string   `json:"confirm_model,omitempty"`
	ConfirmReasoning              string   `json:"confirm_reasoning_effort,omitempty"`
	ConfirmCommand                string   `json:"confirm_command,omitempty"`
	ConfirmCommandArgCount        int      `json:"confirm_command_arg_count,omitempty"`
	InterpretModel                string   `json:"interpret_model,omitempty"`
	InterpretReasoning            string   `json:"interpret_reasoning_effort,omitempty"`
	InterpretDurationMilliseconds int64    `json:"interpret_duration_ms,omitempty"`
	DurationMilliseconds          int64    `json:"duration_ms"`
	Outcome                       string   `json:"outcome"`
	Confidence                    float64  `json:"confidence,omitempty"`
	Reason                        string   `json:"reason,omitempty"`
	MissingWork                   []string `json:"missing_work,omitempty"`
	MissingWorkCount              int      `json:"missing_work_count"`
	Warning                       string   `json:"warning,omitempty"`
	ErrorSummary                  string   `json:"error_summary,omitempty"`
	ContinuationEmitted           bool     `json:"continuation_emitted"`
	PreLoopContinueActive         bool     `json:"pre_loop_continue_active"`
}

func runGoalCheck(ctx context.Context, paths Paths, cfg GoalConfig, payload StopPayload, record LoopRecord, now time.Time) (result goalCheckResult) {
	started := time.Now()
	resolved := resolveGoalCheckConfig(cfg, payload, record)
	result = goalCheckResult{
		Outcome:                  GoalCheckOutcomeError,
		Model:                    resolved.ConfirmModel,
		ReasoningEffort:          resolved.ConfirmReasoningEffort,
		InterpretModel:           resolved.InterpretModel,
		InterpretReasoningEffort: resolved.InterpretReasoningEffort,
	}
	defer func() {
		result.Duration = time.Since(started)
	}()

	workspaceRoot := strings.TrimSpace(record.WorkspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = strings.TrimSpace(record.CWD)
	}
	if workspaceRoot == "" {
		result.Warning = "cannot resolve workspace root for goal confirmation"
		result.ErrorSummary = result.Warning
		return result
	}
	absWorkspaceRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		result.Warning = fmt.Sprintf("resolve workspace root: %v", err)
		result.ErrorSummary = result.Warning
		return result
	}

	if err := os.MkdirAll(paths.RuntimeRoot(), 0o755); err != nil {
		result.Warning = fmt.Sprintf("create runtime directory: %v", err)
		result.ErrorSummary = result.Warning
		return result
	}
	prompt := buildGoalCheckPrompt(record, now)
	files, cleanup, err := prepareGoalCheckFiles(paths, prompt)
	if err != nil {
		result.Warning = err.Error()
		result.ErrorSummary = result.Warning
		return result
	}
	defer cleanup()

	multi := map[string][]string{
		"MODEL_ARGV":     nil,
		"REASONING_ARGV": nil,
	}
	if resolved.ConfirmModel != "" {
		multi["MODEL_ARGV"] = []string{"--model", resolved.ConfirmModel}
	}
	if resolved.ConfirmReasoningEffort != "" {
		multi["REASONING_ARGV"] = []string{"--effort", resolved.ConfirmReasoningEffort}
	}
	values := map[string]string{
		"PROMPT":              prompt,
		"PROMPT_FILE":         files.ConfirmPromptPath,
		"CONFIRM_OUTPUT_PATH": files.ConfirmOutputPath,
		"MODEL":               resolved.ConfirmModel,
		"REASONING_EFFORT":    resolved.ConfirmReasoningEffort,
		"WORKSPACE_ROOT":      absWorkspaceRoot,
		"CWD":                 strings.TrimSpace(record.CWD),
		"SESSION_ID":          record.SessionID,
		"LOOP_NAME":           record.Name,
		"LOOP_SLUG":           record.Slug,
		"RUNS_LOG_PATH":       paths.RunsLogPath(),
		"CLAUDE_CONFIG_DIR":   paths.ClaudeConfigDir,
	}
	confirmRun, err := runGoalCommand(ctx, resolved.ConfirmCommand, absWorkspaceRoot, prompt, resolved.TimeoutSeconds, resolved.MaxOutputBytes, commandExpansion{
		Values:    values,
		Multi:     multi,
		EnvPrefix: "CC_LOOP_CONFIRM_",
	})
	result.CommandName = confirmRun.CommandName
	result.CommandArgCount = confirmRun.CommandArgCount
	if confirmRun.TimedOut {
		result.Outcome = GoalCheckOutcomeTimeout
		result.Warning = fmt.Sprintf("goal confirmation timed out after %s", confirmRun.Timeout)
		result.ErrorSummary = result.Warning
		return result
	}
	if err != nil {
		result.Warning = fmt.Sprintf("goal confirmation failed: %s", describeCommandError(err))
		result.ErrorSummary = result.Warning
		if stderrText := strings.TrimSpace(confirmRun.Stderr); stderrText != "" {
			result.ErrorSummary = strings.TrimSpace(result.ErrorSummary + ": " + truncateText(stderrText, resolved.MaxOutputBytes))
		}
		return result
	}

	reviewOutput, err := readGoalCheckOutput(files.ConfirmOutputPath, confirmRun.Stdout)
	if err != nil {
		result.Outcome = GoalCheckOutcomeInvalidOutput
		result.Warning = fmt.Sprintf("read goal confirmation output: %v", err)
		result.ErrorSummary = result.Warning
		return result
	}
	reviewText := strings.TrimSpace(truncateText(string(reviewOutput), resolved.MaxOutputBytes))
	if reviewText == "" {
		result.Outcome = GoalCheckOutcomeInvalidOutput
		result.Warning = "read goal confirmation output: empty review text"
		result.ErrorSummary = result.Warning
		return result
	}
	result.ReviewText = reviewText

	interpretPrompt := buildGoalInterpretPrompt(record, now, prompt, reviewText)
	interpretMulti := map[string][]string{
		"INTERPRET_MODEL_ARGV":     nil,
		"INTERPRET_REASONING_ARGV": nil,
	}
	if resolved.InterpretModel != "" {
		interpretMulti["INTERPRET_MODEL_ARGV"] = []string{"--model", resolved.InterpretModel}
	}
	if resolved.InterpretReasoningEffort != "" {
		interpretMulti["INTERPRET_REASONING_ARGV"] = []string{"--effort", resolved.InterpretReasoningEffort}
	}
	interpretValues := map[string]string{
		"INTERPRET_SCHEMA_PATH":      files.InterpretSchemaPath,
		"INTERPRET_SCHEMA":           goalCheckOutputSchema,
		"INTERPRET_OUTPUT_PATH":      files.InterpretOutputPath,
		"INTERPRET_MODEL":            resolved.InterpretModel,
		"INTERPRET_REASONING_EFFORT": resolved.InterpretReasoningEffort,
		"WORKSPACE_ROOT":             absWorkspaceRoot,
		"CWD":                        strings.TrimSpace(record.CWD),
		"SESSION_ID":                 record.SessionID,
		"LOOP_NAME":                  record.Name,
		"LOOP_SLUG":                  record.Slug,
		"RUNS_LOG_PATH":              paths.RunsLogPath(),
		"CLAUDE_CONFIG_DIR":          paths.ClaudeConfigDir,
	}
	interpretRun, err := runGoalCommand(ctx, defaultGoalInterpreterClaudePrint(), absWorkspaceRoot, interpretPrompt, resolved.InterpretTimeoutSeconds, resolved.MaxOutputBytes, commandExpansion{
		Values:    interpretValues,
		Multi:     interpretMulti,
		EnvPrefix: "CC_LOOP_INTERPRET_",
	})
	result.InterpretDuration = interpretRun.Duration
	if interpretRun.TimedOut {
		result.Outcome = GoalCheckOutcomeTimeout
		result.Warning = fmt.Sprintf("goal interpretation timed out after %s", interpretRun.Timeout)
		result.ErrorSummary = result.Warning
		return result
	}
	if err != nil {
		result.Warning = fmt.Sprintf("goal interpretation failed: %s", describeCommandError(err))
		result.ErrorSummary = result.Warning
		if stderrText := strings.TrimSpace(interpretRun.Stderr); stderrText != "" {
			result.ErrorSummary = strings.TrimSpace(result.ErrorSummary + ": " + truncateText(stderrText, resolved.MaxOutputBytes))
		}
		return result
	}

	interpretOutput, err := readGoalCheckOutput(files.InterpretOutputPath, interpretRun.Stdout)
	if err != nil {
		result.Outcome = GoalCheckOutcomeInvalidOutput
		result.Warning = fmt.Sprintf("read goal interpretation output: %v", err)
		result.ErrorSummary = result.Warning
		return result
	}
	if len(interpretOutput) > resolved.MaxOutputBytes {
		interpretOutput = interpretOutput[:resolved.MaxOutputBytes]
	}
	verdict, err := decodeGoalVerdict(interpretOutput)
	if err != nil {
		result.Outcome = GoalCheckOutcomeInvalidOutput
		result.Warning = fmt.Sprintf("decode goal interpretation output: %v", err)
		result.ErrorSummary = result.Warning
		return result
	}
	result.Verdict = verdict
	if verdict.Completed {
		result.Outcome = GoalCheckOutcomeCompleted
	} else {
		result.Outcome = GoalCheckOutcomeIncomplete
	}
	return result
}

func resolveGoalCheckConfig(cfg GoalConfig, payload StopPayload, record LoopRecord) GoalConfig {
	resolved := cfg
	if record.ConfirmModel != nil && strings.TrimSpace(*record.ConfirmModel) != "" {
		resolved.ConfirmModel = strings.TrimSpace(*record.ConfirmModel)
	} else if strings.TrimSpace(resolved.ConfirmModel) == "" && strings.TrimSpace(payload.Model) != "" {
		resolved.ConfirmModel = strings.TrimSpace(payload.Model)
	}
	if record.ConfirmReasoning != nil && strings.TrimSpace(*record.ConfirmReasoning) != "" {
		resolved.ConfirmReasoningEffort = strings.TrimSpace(*record.ConfirmReasoning)
	}
	if strings.TrimSpace(resolved.ConfirmCommand) == "" {
		resolved.ConfirmCommand = DefaultGoalConfirmCommand()
	}
	if resolved.TimeoutSeconds <= 0 {
		resolved.TimeoutSeconds = DefaultGoalTimeoutSeconds
	}
	if resolved.InterpretTimeoutSeconds <= 0 {
		resolved.InterpretTimeoutSeconds = DefaultGoalInterpretTimeoutSeconds
	}
	if resolved.MaxOutputBytes <= 0 {
		resolved.MaxOutputBytes = DefaultGoalMaxOutputBytes
	}
	if !ValidReasoningEffort(resolved.ConfirmReasoningEffort) {
		resolved.ConfirmReasoningEffort = DefaultGoalConfirmReasoningEffort
	}
	if !ValidReasoningEffort(resolved.InterpretReasoningEffort) {
		resolved.InterpretReasoningEffort = DefaultGoalInterpretReasoningEffort
	}
	return resolved
}

func runGoalCommand(ctx context.Context, commandLine string, cwd string, stdin string, timeoutSeconds int, maxOutputBytes int, expansion commandExpansion) (goalCommandRun, error) {
	command, args, env, err := buildConfiguredCommand(commandLine, cwd, expansion)
	if err != nil {
		return goalCommandRun{}, err
	}
	result := goalCommandRun{
		CommandName:     filepath.Base(command),
		CommandArgCount: len(args),
		Timeout:         time.Duration(timeoutSeconds) * time.Second,
	}
	runCtx, cancel := context.WithTimeout(ctx, result.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, command, args...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var stdout limitedOutputBuffer
	var stderr limitedOutputBuffer
	stdout.limit = maxOutputBytes
	stderr.limit = maxOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	started := time.Now()
	err = cmd.Run()
	result.Duration = time.Since(started)
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.TimedOut = true
		return result, runCtx.Err()
	}
	return result, err
}

func prepareGoalCheckFiles(paths Paths, prompt string) (goalCheckFiles, func(), error) {
	schemaFile, err := os.CreateTemp(paths.RuntimeRoot(), "goal-interpret-schema-*.json")
	if err != nil {
		return goalCheckFiles{}, func() {}, fmt.Errorf("create goal interpretation schema file: %w", err)
	}
	interpretOutputFile, err := os.CreateTemp(paths.RuntimeRoot(), "goal-interpret-output-*.json")
	if err != nil {
		_ = schemaFile.Close()
		_ = os.Remove(schemaFile.Name())
		return goalCheckFiles{}, func() {}, fmt.Errorf("create goal interpretation output file: %w", err)
	}
	confirmOutputFile, err := os.CreateTemp(paths.RuntimeRoot(), "goal-confirm-output-*.txt")
	if err != nil {
		_ = schemaFile.Close()
		_ = interpretOutputFile.Close()
		_ = os.Remove(schemaFile.Name())
		_ = os.Remove(interpretOutputFile.Name())
		return goalCheckFiles{}, func() {}, fmt.Errorf("create goal confirmation output file: %w", err)
	}
	promptFile, err := os.CreateTemp(paths.RuntimeRoot(), "goal-confirm-prompt-*.txt")
	if err != nil {
		_ = schemaFile.Close()
		_ = interpretOutputFile.Close()
		_ = confirmOutputFile.Close()
		_ = os.Remove(schemaFile.Name())
		_ = os.Remove(interpretOutputFile.Name())
		_ = os.Remove(confirmOutputFile.Name())
		return goalCheckFiles{}, func() {}, fmt.Errorf("create goal confirmation prompt file: %w", err)
	}
	cleanup := func() {
		_ = os.Remove(schemaFile.Name())
		_ = os.Remove(interpretOutputFile.Name())
		_ = os.Remove(confirmOutputFile.Name())
		_ = os.Remove(promptFile.Name())
	}
	if _, err := schemaFile.WriteString(goalCheckOutputSchema); err != nil {
		_ = schemaFile.Close()
		_ = interpretOutputFile.Close()
		_ = confirmOutputFile.Close()
		_ = promptFile.Close()
		cleanup()
		return goalCheckFiles{}, func() {}, fmt.Errorf("write goal interpretation schema file: %w", err)
	}
	if _, err := promptFile.WriteString(prompt); err != nil {
		_ = schemaFile.Close()
		_ = interpretOutputFile.Close()
		_ = confirmOutputFile.Close()
		_ = promptFile.Close()
		cleanup()
		return goalCheckFiles{}, func() {}, fmt.Errorf("write goal confirmation prompt file: %w", err)
	}
	if err := schemaFile.Close(); err != nil {
		_ = interpretOutputFile.Close()
		_ = confirmOutputFile.Close()
		_ = promptFile.Close()
		cleanup()
		return goalCheckFiles{}, func() {}, fmt.Errorf("close goal interpretation schema file: %w", err)
	}
	if err := interpretOutputFile.Close(); err != nil {
		_ = confirmOutputFile.Close()
		_ = promptFile.Close()
		cleanup()
		return goalCheckFiles{}, func() {}, fmt.Errorf("close goal interpretation output file: %w", err)
	}
	if err := confirmOutputFile.Close(); err != nil {
		_ = promptFile.Close()
		cleanup()
		return goalCheckFiles{}, func() {}, fmt.Errorf("close goal confirmation output file: %w", err)
	}
	if err := promptFile.Close(); err != nil {
		cleanup()
		return goalCheckFiles{}, func() {}, fmt.Errorf("close goal confirmation prompt file: %w", err)
	}
	files := goalCheckFiles{
		ConfirmPromptPath:   promptFile.Name(),
		ConfirmOutputPath:   confirmOutputFile.Name(),
		InterpretSchemaPath: schemaFile.Name(),
		InterpretOutputPath: interpretOutputFile.Name(),
	}
	return files, cleanup, nil
}

func buildGoalCheckPrompt(record LoopRecord, now time.Time) string {
	goalText := ""
	if record.GoalText != nil {
		goalText = strings.TrimSpace(*record.GoalText)
	}
	task := strings.TrimSpace(record.TaskPrompt)
	if task == "" {
		task = strings.TrimSpace(record.ActivationPrompt)
	}
	latestMessage := ""
	if record.LastAssistantMessage != nil {
		latestMessage = strings.TrimSpace(*record.LastAssistantMessage)
	}
	lines := []string{
		"You are the cc-loop goal confirmation reviewer.",
		"Inspect the current workspace in read-only mode and decide whether the original task is actually complete.",
		"Do not edit files, run formatters that write files, apply patches, or perform destructive actions.",
		"Return concise plain text, not JSON.",
		"State whether the task appears complete, cite concrete evidence, list missing work if any, and give next steps if incomplete.",
		"",
		fmt.Sprintf("Checked at: %s", ISOFormat(now)),
		fmt.Sprintf("Loop name: %s", record.Name),
		fmt.Sprintf("Loop slug: %s", record.Slug),
		fmt.Sprintf("Workspace root: %s", record.WorkspaceRoot),
	}
	if goalText != "" {
		lines = append(lines, "", "Additional goal:", goalText)
	}
	if task != "" {
		lines = append(lines, "", "Original task:", task)
	}
	if latestMessage != "" {
		lines = append(lines, "", "Latest assistant message:", latestMessage)
	}
	lines = append(lines,
		"",
		"Decision rules:",
		"- Say the work is complete only when the requested work is actually done and no required follow-up remains.",
		"- If incomplete, list concrete missing work and next-round guidance.",
		"- If uncertain, say so clearly and describe what evidence is missing.",
	)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func buildGoalInterpretPrompt(record LoopRecord, now time.Time, confirmationPrompt string, reviewText string) string {
	goalText := ""
	if record.GoalText != nil {
		goalText = strings.TrimSpace(*record.GoalText)
	}
	task := strings.TrimSpace(record.TaskPrompt)
	if task == "" {
		task = strings.TrimSpace(record.ActivationPrompt)
	}
	latestMessage := ""
	if record.LastAssistantMessage != nil {
		latestMessage = strings.TrimSpace(*record.LastAssistantMessage)
	}
	lines := []string{
		"You are the private cc-loop goal confirmation interpreter.",
		"Convert the plain-text reviewer output into JSON matching the provided schema.",
		"Use only the task context and reviewer output below. Do not inspect files, run commands, or infer hidden evidence.",
		"Return only JSON matching the provided schema.",
		"",
		fmt.Sprintf("Checked at: %s", ISOFormat(now)),
		fmt.Sprintf("Loop name: %s", record.Name),
		fmt.Sprintf("Loop slug: %s", record.Slug),
	}
	if goalText != "" {
		lines = append(lines, "", "Additional goal:", goalText)
	}
	if task != "" {
		lines = append(lines, "", "Original task:", task)
	}
	if latestMessage != "" {
		lines = append(lines, "", "Latest assistant message:", latestMessage)
	}
	if confirmationPrompt != "" {
		lines = append(lines, "", "Reviewer instructions that produced the text:", confirmationPrompt)
	}
	lines = append(lines,
		"",
		"Reviewer output:",
		reviewText,
		"",
		"Interpretation rules:",
		"- Set completed=true only when the reviewer clearly says the work is complete and cites adequate evidence.",
		"- Set completed=false when the reviewer reports missing work, uncertainty, failed checks, absent evidence, or ambiguous status.",
		"- Preserve concrete missing work and next-round guidance from the reviewer when available.",
		"- Use confidence from 0 to 1 based on how clearly the reviewer supports the decision.",
	)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func decodeGoalVerdict(content []byte) (GoalCheckVerdict, error) {
	trimmed := trimJSONFence(content)
	if len(trimmed) == 0 {
		return GoalCheckVerdict{}, fmt.Errorf("empty output")
	}
	verdict, found, err := decodeGoalVerdictValue(trimmed, 0)
	if err != nil {
		return GoalCheckVerdict{}, err
	}
	if !found {
		return GoalCheckVerdict{}, fmt.Errorf("unsupported Claude JSON output shape: no goal verdict found")
	}
	return verdict, nil
}

func decodeGoalVerdictValue(content []byte, depth int) (GoalCheckVerdict, bool, error) {
	if depth > 8 {
		return GoalCheckVerdict{}, false, fmt.Errorf("nested Claude JSON output is too deep")
	}
	trimmed := trimJSONFence(content)
	if len(trimmed) == 0 {
		return GoalCheckVerdict{}, false, nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err == nil && object != nil {
		return decodeGoalVerdictObject(object, depth)
	}

	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err == nil {
		return decodeGoalVerdictArray(items, depth)
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return decodeGoalVerdictValue([]byte(text), depth+1)
	}

	var verdict GoalCheckVerdict
	if err := json.Unmarshal(trimmed, &verdict); err != nil {
		return GoalCheckVerdict{}, false, err
	}
	return verdict, true, nil
}

func decodeGoalVerdictObject(object map[string]json.RawMessage, depth int) (GoalCheckVerdict, bool, error) {
	if isGoalVerdictObject(object) {
		verdict, err := decodeRawGoalVerdict(object)
		return verdict, true, err
	}

	if verdict, found, err := decodeStructuredOutputObject(object, depth); found || err != nil {
		return verdict, found, err
	}

	for _, key := range []string{"structured_output", "output", "data", "message", "result"} {
		raw, ok := object[key]
		if !ok {
			continue
		}
		verdict, found, err := decodeGoalVerdictRaw(raw, depth+1)
		if found || err != nil {
			return verdict, found, err
		}
	}

	if raw, ok := object["content"]; ok {
		return decodeGoalVerdictRaw(raw, depth+1)
	}
	return GoalCheckVerdict{}, false, nil
}

func decodeStructuredOutputObject(object map[string]json.RawMessage, depth int) (GoalCheckVerdict, bool, error) {
	objectType := readJSONString(object["type"])
	name := readJSONString(object["name"])
	if objectType == "tool_use" && name == "StructuredOutput" {
		raw, ok := object["input"]
		if !ok {
			return GoalCheckVerdict{}, true, fmt.Errorf("structured output tool call missing input")
		}
		return decodeGoalVerdictRaw(raw, depth+1)
	}
	if objectType == "structured_output" {
		for _, key := range []string{"data", "input", "result"} {
			raw, ok := object[key]
			if !ok {
				continue
			}
			return decodeGoalVerdictRaw(raw, depth+1)
		}
		return GoalCheckVerdict{}, true, fmt.Errorf("structured output payload missing data")
	}
	return GoalCheckVerdict{}, false, nil
}

func decodeGoalVerdictArray(items []json.RawMessage, depth int) (GoalCheckVerdict, bool, error) {
	var firstErr error
	for _, item := range items {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(item, &object); err != nil || object == nil {
			continue
		}
		if readJSONString(object["type"]) != "tool_use" || readJSONString(object["name"]) != "StructuredOutput" {
			continue
		}
		verdict, found, err := decodeGoalVerdictObject(object, depth+1)
		if found && err == nil {
			return verdict, true, nil
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	for _, item := range items {
		verdict, found, err := decodeGoalVerdictRaw(item, depth+1)
		if found && err == nil {
			return verdict, true, nil
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return GoalCheckVerdict{}, true, firstErr
	}
	return GoalCheckVerdict{}, false, nil
}

func decodeGoalVerdictRaw(raw json.RawMessage, depth int) (GoalCheckVerdict, bool, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return decodeGoalVerdictValue([]byte(text), depth+1)
	}
	return decodeGoalVerdictValue(raw, depth+1)
}

func decodeRawGoalVerdict(object map[string]json.RawMessage) (GoalCheckVerdict, error) {
	for _, field := range []string{"completed", "confidence", "reason", "missing_work", "next_round_guidance"} {
		if _, ok := object[field]; !ok {
			return GoalCheckVerdict{}, fmt.Errorf("missing required field %q", field)
		}
	}
	content, err := json.Marshal(object)
	if err != nil {
		return GoalCheckVerdict{}, fmt.Errorf("marshal goal verdict: %w", err)
	}
	var verdict GoalCheckVerdict
	if err := json.Unmarshal(content, &verdict); err != nil {
		return GoalCheckVerdict{}, err
	}
	verdict.Reason = strings.TrimSpace(verdict.Reason)
	verdict.NextRoundGuidance = strings.TrimSpace(verdict.NextRoundGuidance)
	for index, item := range verdict.MissingWork {
		verdict.MissingWork[index] = strings.TrimSpace(item)
	}
	if verdict.Confidence < 0 {
		verdict.Confidence = 0
	}
	if verdict.Confidence > 1 {
		verdict.Confidence = 1
	}
	if verdict.Reason == "" {
		return GoalCheckVerdict{}, fmt.Errorf("missing reason")
	}
	return verdict, nil
}

func isGoalVerdictObject(object map[string]json.RawMessage) bool {
	for _, field := range []string{"completed", "confidence", "reason", "missing_work", "next_round_guidance"} {
		if _, ok := object[field]; ok {
			return true
		}
	}
	return false
}

func readJSONString(raw json.RawMessage) string {
	var value string
	if len(raw) == 0 {
		return ""
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func trimJSONFence(content []byte) []byte {
	trimmed := bytes.TrimSpace(content)
	trimmed = bytes.TrimPrefix(trimmed, []byte("```json"))
	trimmed = bytes.TrimPrefix(trimmed, []byte("```"))
	trimmed = bytes.TrimSuffix(trimmed, []byte("```"))
	return bytes.TrimSpace(trimmed)
}

func readGoalCheckOutput(outputPath string, stdout string) ([]byte, error) {
	output, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("read output file: %w", err)
	}
	if len(bytes.TrimSpace(output)) > 0 {
		return output, nil
	}
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil, fmt.Errorf("empty output file and stdout")
	}
	return []byte(stdout), nil
}

func AppendGoalCheckLog(paths Paths, loopPath string, record LoopRecord, result goalCheckResult, continuationEmitted bool, preLoopContinueActive bool, checkedAt time.Time) error {
	event := goalCheckLogEvent{
		EventName:                     "goal_check",
		CheckedAt:                     ISOFormat(checkedAt),
		SessionID:                     record.SessionID,
		LoopName:                      record.Name,
		LoopSlug:                      record.Slug,
		LoopPath:                      loopPath,
		LimitMode:                     ResolveLimitMode(record),
		ContinueCount:                 record.ContinueCount,
		GoalCheckCount:                record.GoalCheckCount,
		ConfirmModel:                  result.Model,
		ConfirmReasoning:              result.ReasoningEffort,
		ConfirmCommand:                result.CommandName,
		ConfirmCommandArgCount:        result.CommandArgCount,
		InterpretModel:                result.InterpretModel,
		InterpretReasoning:            result.InterpretReasoningEffort,
		InterpretDurationMilliseconds: result.InterpretDuration.Milliseconds(),
		DurationMilliseconds:          result.Duration.Milliseconds(),
		Outcome:                       result.Outcome,
		Confidence:                    result.Verdict.Confidence,
		Reason:                        truncateText(result.Verdict.Reason, 1000),
		MissingWork:                   truncateStringSlice(result.Verdict.MissingWork, 20, 500),
		MissingWorkCount:              len(result.Verdict.MissingWork),
		Warning:                       truncateText(result.Warning, 1000),
		ErrorSummary:                  truncateText(result.ErrorSummary, 1000),
		ContinuationEmitted:           continuationEmitted,
		PreLoopContinueActive:         preLoopContinueActive,
	}
	return AppendJSONL(paths.RunsLogPath(), event)
}

func AppendJSONL(path string, payload any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create JSONL parent directory: %w", err)
	}
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open JSONL log %q: %w", path, err)
	}
	defer handle.Close()
	encoder := json.NewEncoder(handle)
	if err := encoder.Encode(payload); err != nil {
		return fmt.Errorf("append JSONL log %q: %w", path, err)
	}
	return nil
}

func truncateText(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes]
}

func truncateStringSlice(values []string, maxItems int, maxItemBytes int) []string {
	if maxItems <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) > maxItems {
		values = values[:maxItems]
	}
	output := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		output = append(output, truncateText(value, maxItemBytes))
	}
	return output
}

const goalCheckOutputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["completed", "confidence", "reason", "missing_work", "next_round_guidance"],
  "properties": {
    "completed": { "type": "boolean" },
    "confidence": { "type": "number", "minimum": 0, "maximum": 1 },
    "reason": { "type": "string" },
    "missing_work": {
      "type": "array",
      "items": { "type": "string" }
    },
    "next_round_guidance": { "type": "string" }
  }
}
`
