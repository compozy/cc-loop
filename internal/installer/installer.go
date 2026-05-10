package installer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/compozy/cc-loop/internal/loop"
)

type Options struct {
	SourceBinary string
}

func Install(paths loop.Paths, opts Options) ([]string, error) {
	messages := make([]string, 0)
	if err := os.MkdirAll(paths.RuntimeBinDir(), 0o755); err != nil {
		return nil, fmt.Errorf("create runtime bin directory: %w", err)
	}
	if err := os.MkdirAll(paths.LoopsDir(), 0o755); err != nil {
		return nil, fmt.Errorf("create loop state directory: %w", err)
	}

	sourceBinary := opts.SourceBinary
	if strings.TrimSpace(sourceBinary) == "" {
		resolved, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve current executable: %w", err)
		}
		sourceBinary = resolved
	}
	if err := installRuntimeBinary(sourceBinary, paths.RuntimeBinaryPath()); err != nil {
		return nil, err
	}
	messages = append(messages, fmt.Sprintf("Installed runtime binary at %s", paths.RuntimeBinaryPath()))

	configCreated, err := ensureRuntimeConfig(paths)
	if err != nil {
		return nil, err
	}
	if configCreated {
		messages = append(messages, fmt.Sprintf("Created optional runtime config at %s", paths.RuntimeConfigPath()))
	} else {
		messages = append(messages, fmt.Sprintf("Preserved existing runtime config at %s", paths.RuntimeConfigPath()))
	}

	hookSync, err := ensureManagedHookConfig(paths)
	if err != nil {
		return nil, err
	}
	if hookSync.PluginHooksEnabled {
		if hookSync.RemovedManagedHooks {
			messages = append(messages, fmt.Sprintf("Skipped managed hook config because cc-loop plugin hooks are enabled; removed duplicate managed hook registrations from %s", paths.HooksPath()))
		} else {
			messages = append(messages, fmt.Sprintf("Skipped managed hook config because cc-loop plugin hooks are enabled in %s", paths.HooksPath()))
		}
	} else {
		messages = append(messages, fmt.Sprintf("Updated managed hook config at %s", paths.HooksPath()))
	}

	messages = append(messages,
		fmt.Sprintf("Ensured loop state directory exists at %s", paths.LoopsDir()),
		fmt.Sprintf("Claude Code config directory: %s", paths.ClaudeConfigDir),
		"Restart Claude Code after installing or updating the cc-loop plugin.",
	)
	return messages, nil
}

func Uninstall(paths loop.Paths) ([]string, error) {
	messages := make([]string, 0, 3)
	removedHooks, err := removeManagedHookConfig(paths)
	if err != nil {
		return nil, err
	}
	if removedHooks {
		messages = append(messages, fmt.Sprintf("Removed managed hook registrations from %s", paths.HooksPath()))
	} else {
		messages = append(messages, fmt.Sprintf("No managed hook registrations were present in %s", paths.HooksPath()))
	}

	runtimeRoot := paths.RuntimeRoot()
	if _, err := os.Stat(runtimeRoot); err != nil {
		if os.IsNotExist(err) {
			messages = append(messages, fmt.Sprintf("No managed runtime directory found at %s", runtimeRoot))
		} else {
			return nil, fmt.Errorf("stat runtime directory %q: %w", runtimeRoot, err)
		}
	} else {
		if err := os.RemoveAll(runtimeRoot); err != nil {
			return nil, fmt.Errorf("remove runtime directory %q: %w", runtimeRoot, err)
		}
		messages = append(messages, fmt.Sprintf("Removed managed runtime directory %s", runtimeRoot))
	}
	messages = append(messages, "Left unrelated Claude Code settings unchanged.")
	return messages, nil
}

func ensureRuntimeConfig(paths loop.Paths) (bool, error) {
	if _, err := os.Stat(paths.RuntimeConfigPath()); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat runtime config %q: %w", paths.RuntimeConfigPath(), err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.RuntimeConfigPath()), 0o755); err != nil {
		return false, fmt.Errorf("create runtime config directory: %w", err)
	}
	if err := os.WriteFile(paths.RuntimeConfigPath(), []byte(loop.DefaultRuntimeConfig), 0o644); err != nil {
		return false, fmt.Errorf("write runtime config: %w", err)
	}
	return true, nil
}

func installRuntimeBinary(source string, destination string) error {
	absSource, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("resolve source binary %q: %w", source, err)
	}
	if _, err := os.Stat(absSource); err != nil {
		return fmt.Errorf("stat source binary %q: %w", absSource, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create runtime binary directory: %w", err)
	}
	if samePath(absSource, destination) {
		return nil
	}
	return copyFile(absSource, destination, 0o755)
}

func copyFile(source string, destination string, mode os.FileMode) error {
	sourceFile, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source %q: %w", source, err)
	}
	defer sourceFile.Close()

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file for %q: %w", destination, err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

	if _, err := io.Copy(temp, sourceFile); err != nil {
		_ = temp.Close()
		return fmt.Errorf("copy %q to %q: %w", source, destination, err)
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod temp file for %q: %w", destination, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp file for %q: %w", destination, err)
	}
	if err := os.Rename(tempName, destination); err != nil {
		return fmt.Errorf("replace %q: %w", destination, err)
	}
	return nil
}

func samePath(left string, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr == nil && rightErr == nil {
		return os.SameFile(leftInfo, rightInfo)
	}
	leftAbs, leftAbsErr := filepath.Abs(left)
	rightAbs, rightAbsErr := filepath.Abs(right)
	return leftAbsErr == nil && rightAbsErr == nil && leftAbs == rightAbs
}
