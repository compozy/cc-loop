package loop

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	ActivationPrefix = "[[CC_LOOP"

	LimitModeTime   = "time"
	LimitModeRounds = "rounds"
	LimitModeGoal   = "goal"
)

var (
	activationRE    = regexp.MustCompile(`^\[\[CC_LOOP(?P<body>[^\]]+)\]\]\s*$`)
	attributeRE     = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_-]*)="([^"]*)"`)
	durationTokenRE = regexp.MustCompile(`(?i)(\d+)\s*(seconds?|secs?|sec|s|minutes?|mins?|min|m|hours?|hrs?|hr|h|days?|day|d)\b`)
	roundRE         = regexp.MustCompile(`^[1-9][0-9]*$`)
	slugCleanupRE   = regexp.MustCompile(`[^a-z0-9]+`)
	slugCollapseRE  = regexp.MustCompile(`-{2,}`)
)

type Activation struct {
	Name               string
	Slug               string
	LimitMode          string
	TaskPrompt         string
	ActivationPrompt   string
	DurationText       string
	MinDurationSeconds int
	RoundsText         string
	TargetRounds       int
	GoalText           string
	ConfirmModel       string
	ConfirmReasoning   string
}

func LooksLikeActivation(prompt string) bool {
	lines := splitPromptLines(prompt)
	if len(lines) == 0 {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(lines[0]), ActivationPrefix)
}

func ExtractActivation(prompt string) (Activation, bool, error) {
	lines := splitPromptLines(prompt)
	if len(lines) == 0 {
		return Activation{}, false, nil
	}

	firstLine := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(firstLine, ActivationPrefix) {
		return Activation{}, false, nil
	}

	match := activationRE.FindStringSubmatch(firstLine)
	if match == nil {
		return Activation{}, false, fmt.Errorf("invalid CC_LOOP header syntax")
	}
	body := match[activationRE.SubexpIndex("body")]
	attributes := map[string]string{}
	for _, attr := range attributeRE.FindAllStringSubmatch(body, -1) {
		attributes[attr[1]] = attr[2]
	}

	name := strings.TrimSpace(attributes["name"])
	durationText := strings.TrimSpace(attributes["min"])
	roundsText := strings.TrimSpace(attributes["rounds"])
	goalText, hasGoal := attributes["goal"]
	confirmModel := strings.TrimSpace(attributes["confirm_model"])
	confirmReasoning := strings.TrimSpace(attributes["confirm_reasoning_effort"])
	if name == "" {
		return Activation{}, false, fmt.Errorf(`CC_LOOP header requires name="..."`)
	}
	limiters := 0
	if durationText != "" {
		limiters++
	}
	if roundsText != "" {
		limiters++
	}
	if hasGoal {
		limiters++
	}
	if limiters != 1 {
		return Activation{}, false, fmt.Errorf(`CC_LOOP header requires exactly one of min="...", rounds="...", or goal="..."`)
	}
	if !hasGoal && (confirmModel != "" || confirmReasoning != "") {
		return Activation{}, false, fmt.Errorf(`CC_LOOP header allows confirm_model and confirm_reasoning_effort only with goal="..."`)
	}
	if confirmReasoning != "" && !ValidReasoningEffort(confirmReasoning) {
		return Activation{}, false, fmt.Errorf("invalid confirm_reasoning_effort %q", confirmReasoning)
	}

	taskPrompt := ""
	if len(lines) > 1 {
		taskPrompt = strings.TrimLeft(strings.Join(lines[1:], "\n"), "\n")
	}

	activation := Activation{
		Name:             name,
		Slug:             Slugify(name),
		TaskPrompt:       taskPrompt,
		ActivationPrompt: prompt,
	}
	if durationText != "" {
		seconds, err := ParseDurationSeconds(durationText)
		if err != nil {
			return Activation{}, false, err
		}
		activation.LimitMode = LimitModeTime
		activation.DurationText = durationText
		activation.MinDurationSeconds = seconds
		return activation, true, nil
	}

	if hasGoal {
		activation.LimitMode = LimitModeGoal
		activation.GoalText = strings.TrimSpace(goalText)
		activation.ConfirmModel = confirmModel
		activation.ConfirmReasoning = confirmReasoning
		return activation, true, nil
	}

	rounds, err := ParseRounds(roundsText)
	if err != nil {
		return Activation{}, false, err
	}
	activation.LimitMode = LimitModeRounds
	activation.RoundsText = roundsText
	activation.TargetRounds = rounds
	return activation, true, nil
}

func ValidReasoningEffort(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func ParseDurationSeconds(value string) (int, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return 0, fmt.Errorf("invalid duration %q", value)
	}

	offset := 0
	total := 0
	matched := false
	for _, match := range durationTokenRE.FindAllStringSubmatchIndex(normalized, -1) {
		if strings.TrimSpace(normalized[offset:match[0]]) != "" {
			return 0, fmt.Errorf("invalid duration %q", value)
		}

		amount, err := strconv.Atoi(normalized[match[2]:match[3]])
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", value, err)
		}
		unitMultiplier, err := durationUnitMultiplier(normalized[match[4]:match[5]])
		if err != nil {
			return 0, err
		}
		total += amount * unitMultiplier
		offset = match[1]
		matched = true
	}

	if strings.TrimSpace(normalized[offset:]) != "" || !matched || total <= 0 {
		return 0, fmt.Errorf("invalid duration %q", value)
	}
	return total, nil
}

func ParseRounds(value string) (int, error) {
	normalized := strings.TrimSpace(value)
	if !roundRE.MatchString(normalized) {
		return 0, fmt.Errorf("invalid rounds %q", value)
	}
	rounds, err := strconv.Atoi(normalized)
	if err != nil || rounds <= 0 {
		return 0, fmt.Errorf("invalid rounds %q", value)
	}
	return rounds, nil
}

func Slugify(value string) string {
	lowered := strings.ToLower(strings.TrimSpace(value))
	slug := slugCleanupRE.ReplaceAllString(lowered, "-")
	slug = slugCollapseRE.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "loop"
	}
	return slug
}

func durationUnitMultiplier(unit string) (int, error) {
	switch lowered := strings.ToLower(unit); {
	case strings.HasPrefix(lowered, "s"):
		return 1, nil
	case strings.HasPrefix(lowered, "m"):
		return 60, nil
	case strings.HasPrefix(lowered, "h"):
		return 3600, nil
	case strings.HasPrefix(lowered, "d"):
		return 86400, nil
	default:
		return 0, fmt.Errorf("unsupported duration unit %q", unit)
	}
}

func splitPromptLines(prompt string) []string {
	if prompt == "" {
		return nil
	}
	normalized := strings.ReplaceAll(prompt, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.Split(normalized, "\n")
}
