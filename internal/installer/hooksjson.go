package installer

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/compozy/cc-loop/internal/loop"
)

const (
	stopStatusMessage        = "Evaluating the Claude Code loop"
	userPromptStatusMessage  = "Preparing the Claude Code loop"
	userPromptTimeoutSeconds = 30
)

func managedHooksTemplate(stopTimeoutSeconds int) map[string]any {
	if stopTimeoutSeconds <= 0 {
		stopTimeoutSeconds = loop.DefaultStopHookTimeoutSeconds
	}
	return map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"command":       managedHookCommand("stop"),
							"statusMessage": stopStatusMessage,
							"timeout":       stopTimeoutSeconds,
							"type":          "command",
						},
					},
				},
			},
			"UserPromptSubmit": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"command":       managedHookCommand("user-prompt-submit"),
							"statusMessage": userPromptStatusMessage,
							"timeout":       userPromptTimeoutSeconds,
							"type":          "command",
						},
					},
				},
			},
		},
	}
}

func managedHookCommand(subcommand string) string {
	return fmt.Sprintf(
		`bin="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/%s/bin/%s"; if [ -x "$bin" ]; then "$bin" hook %s; else exit 0; fi`,
		loop.RuntimeName,
		loop.RuntimeName,
		subcommand,
	)
}

func ensureManagedHookConfig(paths loop.Paths) error {
	cfg := loop.LoadRuntimeConfig(paths)
	template := managedHooksTemplate(cfg.Hooks.StopTimeoutSeconds)
	existing, err := loadHookConfig(paths.HooksPath())
	if err != nil {
		return err
	}
	merged, err := mergeManagedHooks(existing, template)
	if err != nil {
		return err
	}
	if err := loop.AtomicWriteJSON(paths.HooksPath(), merged); err != nil {
		return fmt.Errorf("write hooks config %q: %w", paths.HooksPath(), err)
	}
	return nil
}

func removeManagedHookConfig(paths loop.Paths) (bool, error) {
	if _, err := os.Stat(paths.HooksPath()); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat hooks config %q: %w", paths.HooksPath(), err)
	}
	existing, err := loadHookConfig(paths.HooksPath())
	if err != nil {
		return false, err
	}
	cleaned, removed, err := removeManagedHooks(existing, managedHooksTemplate(loop.DefaultStopHookTimeoutSeconds))
	if err != nil {
		return false, err
	}
	if !removed {
		return false, nil
	}
	if err := loop.AtomicWriteJSON(paths.HooksPath(), cleaned); err != nil {
		return false, fmt.Errorf("write hooks config %q: %w", paths.HooksPath(), err)
	}
	return true, nil
}

func loadHookConfig(path string) (map[string]any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{"hooks": map[string]any{}}, nil
		}
		return nil, fmt.Errorf("read hooks config %q: %w", path, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(content, &payload); err != nil {
		return nil, fmt.Errorf("decode hooks config %q: %w", path, err)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return payload, nil
}

func managedCommands(templateDoc map[string]any) (map[string]struct{}, error) {
	hooksRoot, err := hooksRoot(templateDoc)
	if err != nil {
		return nil, err
	}
	commands := make(map[string]struct{})
	for eventName, matcherGroupsAny := range hooksRoot {
		matcherGroups, ok := matcherGroupsAny.([]any)
		if !ok {
			return nil, fmt.Errorf("template hooks.%s must be a list", eventName)
		}
		for _, matcherGroupAny := range matcherGroups {
			matcherGroup, ok := matcherGroupAny.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("template hooks.%s entries must be objects", eventName)
			}
			hooksAny, ok := matcherGroup["hooks"]
			if !ok {
				continue
			}
			hooks, ok := hooksAny.([]any)
			if !ok {
				return nil, fmt.Errorf("template hooks.%s hooks must be a list", eventName)
			}
			for _, hookAny := range hooks {
				hook, ok := hookAny.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("template hooks.%s hook entries must be objects", eventName)
				}
				command, _ := hook["command"].(string)
				hookType, _ := hook["type"].(string)
				if hookType == "command" && command != "" {
					commands[command] = struct{}{}
				}
			}
		}
	}
	return commands, nil
}

func removeManagedHooks(existingDoc map[string]any, templateDoc map[string]any) (map[string]any, bool, error) {
	if existingDoc == nil {
		existingDoc = map[string]any{}
	}
	hooksRootDoc, err := hooksRoot(existingDoc)
	if err != nil {
		return nil, false, err
	}
	managed, err := managedCommands(templateDoc)
	if err != nil {
		return nil, false, err
	}

	changed := false
	for eventName, matcherGroupsAny := range hooksRootDoc {
		matcherGroups, ok := matcherGroupsAny.([]any)
		if !ok {
			return nil, false, fmt.Errorf("hooks.%s must be a list", eventName)
		}
		filteredGroups := make([]any, 0, len(matcherGroups))
		eventChanged := false
		for _, matcherGroupAny := range matcherGroups {
			matcherGroup, ok := matcherGroupAny.(map[string]any)
			if !ok {
				return nil, false, fmt.Errorf("hooks.%s entries must be objects", eventName)
			}
			hooksAny, ok := matcherGroup["hooks"]
			if !ok {
				filteredGroups = append(filteredGroups, matcherGroupAny)
				continue
			}
			hooks, ok := hooksAny.([]any)
			if !ok {
				return nil, false, fmt.Errorf("hooks.%s hooks must be a list", eventName)
			}
			remainingHooks := make([]any, 0, len(hooks))
			removedFromGroup := false
			for _, hookAny := range hooks {
				hook, ok := hookAny.(map[string]any)
				if !ok {
					return nil, false, fmt.Errorf("hooks.%s hook entries must be objects", eventName)
				}
				command, _ := hook["command"].(string)
				hookType, _ := hook["type"].(string)
				if hookType == "command" {
					if _, ok := managed[command]; ok {
						removedFromGroup = true
						eventChanged = true
						changed = true
						continue
					}
				}
				remainingHooks = append(remainingHooks, hookAny)
			}
			if removedFromGroup && len(remainingHooks) == 0 {
				continue
			}
			if removedFromGroup {
				nextGroup := cloneMap(matcherGroup)
				nextGroup["hooks"] = remainingHooks
				filteredGroups = append(filteredGroups, nextGroup)
				continue
			}
			filteredGroups = append(filteredGroups, matcherGroupAny)
		}
		if eventChanged && len(filteredGroups) == 0 {
			delete(hooksRootDoc, eventName)
			continue
		}
		hooksRootDoc[eventName] = filteredGroups
	}

	return existingDoc, changed, nil
}

func mergeManagedHooks(existingDoc map[string]any, templateDoc map[string]any) (map[string]any, error) {
	cleanedDoc, _, err := removeManagedHooks(existingDoc, templateDoc)
	if err != nil {
		return nil, err
	}
	existingHooksRoot, err := hooksRoot(cleanedDoc)
	if err != nil {
		return nil, err
	}
	templateHooksRoot, err := hooksRoot(templateDoc)
	if err != nil {
		return nil, err
	}
	for eventName, matcherGroupsAny := range templateHooksRoot {
		matcherGroups, ok := matcherGroupsAny.([]any)
		if !ok {
			return nil, fmt.Errorf("template hooks.%s must be a list", eventName)
		}
		existingGroupsAny, ok := existingHooksRoot[eventName]
		if !ok {
			existingHooksRoot[eventName] = append([]any{}, matcherGroups...)
			continue
		}
		existingGroups, ok := existingGroupsAny.([]any)
		if !ok {
			return nil, fmt.Errorf("hooks.%s must be a list", eventName)
		}
		existingHooksRoot[eventName] = append(existingGroups, matcherGroups...)
	}
	return cleanedDoc, nil
}

func hooksRoot(doc map[string]any) (map[string]any, error) {
	if doc == nil {
		doc = map[string]any{}
	}
	hooksAny, ok := doc["hooks"]
	if !ok {
		hooks := map[string]any{}
		doc["hooks"] = hooks
		return hooks, nil
	}
	hooks, ok := hooksAny.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("hooks.json must contain a top-level hooks object")
	}
	return hooks, nil
}

func cloneMap(input map[string]any) map[string]any {
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}
