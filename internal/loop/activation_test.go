package loop

import "testing"

func TestParseDurationSecondsAcceptsAliases(t *testing.T) {
	t.Parallel()

	cases := map[string]int{
		"30m":           1800,
		"30min":         1800,
		"1h 30m":        5400,
		"2 hours":       7200,
		"45sec":         45,
		"1D 2H 3MIN 4s": 93784,
	}
	for value, expected := range cases {
		value := value
		expected := expected
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			got, err := ParseDurationSeconds(value)
			if err != nil {
				t.Fatalf("parse duration: %v", err)
			}
			if got != expected {
				t.Fatalf("expected %d, got %d", expected, got)
			}
		})
	}
}

func TestExtractActivationRequiresExactlyOneLimit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		prompt string
	}{
		{
			name:   "missing limiter",
			prompt: `[[CC_LOOP name="qa"]]` + "\nRun QA.",
		},
		{
			name:   "duplicate limiters",
			prompt: `[[CC_LOOP name="qa" min="30m" rounds="3"]]` + "\nRun QA.",
		},
		{
			name:   "duplicate goal limiter",
			prompt: `[[CC_LOOP name="qa" rounds="3" goal="done"]]` + "\nRun QA.",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := ExtractActivation(tc.prompt)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestExtractActivationBuildsGoalActivation(t *testing.T) {
	t.Parallel()

	activation, ok, err := ExtractActivation(`[[CC_LOOP name="Goal QA" goal="ship when verified" confirm_model="opus" confirm_reasoning_effort="xhigh"]]` + "\nRun QA.")
	if err != nil {
		t.Fatalf("extract activation: %v", err)
	}
	if !ok {
		t.Fatal("expected activation")
	}
	if activation.LimitMode != LimitModeGoal {
		t.Fatalf("unexpected limit mode %q", activation.LimitMode)
	}
	if activation.GoalText != "ship when verified" {
		t.Fatalf("unexpected goal %q", activation.GoalText)
	}
	if activation.ConfirmModel != "opus" {
		t.Fatalf("unexpected confirm model %q", activation.ConfirmModel)
	}
	if activation.ConfirmReasoning != "xhigh" {
		t.Fatalf("unexpected confirm reasoning %q", activation.ConfirmReasoning)
	}
	if activation.TaskPrompt != "Run QA." {
		t.Fatalf("unexpected task prompt %q", activation.TaskPrompt)
	}
}

func TestExtractActivationAllowsEmptyGoal(t *testing.T) {
	t.Parallel()

	activation, ok, err := ExtractActivation(`[[CC_LOOP name="Goal QA" goal=""]]` + "\nRun QA.")
	if err != nil {
		t.Fatalf("extract activation: %v", err)
	}
	if !ok {
		t.Fatal("expected activation")
	}
	if activation.LimitMode != LimitModeGoal {
		t.Fatalf("unexpected limit mode %q", activation.LimitMode)
	}
}

func TestExtractActivationRejectsConfirmFieldsWithoutGoal(t *testing.T) {
	t.Parallel()

	_, _, err := ExtractActivation(`[[CC_LOOP name="qa" rounds="2" confirm_model="opus"]]` + "\nRun QA.")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestExtractActivationRejectsInvalidConfirmReasoning(t *testing.T) {
	t.Parallel()

	_, _, err := ExtractActivation(`[[CC_LOOP name="qa" goal="" confirm_reasoning_effort="huge"]]` + "\nRun QA.")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestExtractActivationRejectsInvalidRounds(t *testing.T) {
	t.Parallel()

	_, _, err := ExtractActivation(`[[CC_LOOP name="qa" rounds="0"]]` + "\nRun QA.")
	if err == nil {
		t.Fatal("expected invalid rounds error, got nil")
	}
}

func TestExtractActivationBuildsRoundActivation(t *testing.T) {
	t.Parallel()

	activation, ok, err := ExtractActivation(`[[CC_LOOP name="Release Stress QA" rounds="3"]]` + "\nRun QA.")
	if err != nil {
		t.Fatalf("extract activation: %v", err)
	}
	if !ok {
		t.Fatal("expected activation")
	}
	if activation.Name != "Release Stress QA" {
		t.Fatalf("unexpected name %q", activation.Name)
	}
	if activation.Slug != "release-stress-qa" {
		t.Fatalf("unexpected slug %q", activation.Slug)
	}
	if activation.LimitMode != LimitModeRounds {
		t.Fatalf("unexpected limit mode %q", activation.LimitMode)
	}
	if activation.TargetRounds != 3 {
		t.Fatalf("unexpected target rounds %d", activation.TargetRounds)
	}
	if activation.TaskPrompt != "Run QA." {
		t.Fatalf("unexpected task prompt %q", activation.TaskPrompt)
	}
}
