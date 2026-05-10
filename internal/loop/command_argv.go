package loop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/shell"
	"mvdan.cc/sh/v3/syntax"
)

type commandExpansion struct {
	Values    map[string]string
	Multi     map[string][]string
	EnvPrefix string
}

func buildConfiguredCommand(commandLine string, cwd string, expansion commandExpansion) (string, []string, []string, error) {
	expanded, err := expandCommandLine(commandLine, expansion)
	if err != nil {
		return "", nil, nil, err
	}
	fields, err := shell.Fields(expanded, func(string) string { return "" })
	if err != nil {
		return "", nil, nil, fmt.Errorf("parse command string: %w", err)
	}
	if len(fields) == 0 {
		return "", nil, nil, fmt.Errorf("command string is empty")
	}
	command := strings.TrimSpace(fields[0])
	if command == "" {
		return "", nil, nil, fmt.Errorf("command executable is empty")
	}
	if strings.ContainsAny(command, `/\`) && !filepath.IsAbs(command) {
		command = filepath.Join(cwd, command)
	}
	return command, fields[1:], commandExpansionEnv(expansion), nil
}

func expandCommandLine(commandLine string, expansion commandExpansion) (string, error) {
	var output strings.Builder
	var quote byte

	for index := 0; index < len(commandLine); {
		character := commandLine[index]
		switch character {
		case '\'':
			if quote == 0 {
				quote = '\''
			} else if quote == '\'' {
				quote = 0
			}
			output.WriteByte(character)
			index++
			continue
		case '"':
			if quote == 0 {
				quote = '"'
			} else if quote == '"' {
				quote = 0
			}
			output.WriteByte(character)
			index++
			continue
		case '\\':
			output.WriteByte(character)
			index++
			if index < len(commandLine) {
				output.WriteByte(commandLine[index])
				index++
			}
			continue
		case '$':
			if quote == '\'' {
				output.WriteByte(character)
				index++
				continue
			}
			replacement, next, ok, err := expandCommandVariable(commandLine, index, quote, expansion)
			if err != nil {
				return "", err
			}
			if ok {
				output.WriteString(replacement)
				index = next
				continue
			}
			if quote == '"' {
				output.WriteString(`\$`)
			} else {
				output.WriteString(`\$`)
			}
			index++
			continue
		default:
			output.WriteByte(character)
			index++
		}
	}
	return output.String(), nil
}

func expandCommandVariable(commandLine string, start int, quote byte, expansion commandExpansion) (string, int, bool, error) {
	name, raw, next, ok := parseCommandVariable(commandLine, start)
	if !ok {
		return "", start, false, nil
	}

	if values, exists := expansion.Multi[name]; exists {
		if quote == '"' {
			if len(values) > 1 {
				return "", start, false, fmt.Errorf("multi-argument variable %s cannot be used inside double quotes", raw)
			}
			if len(values) == 0 {
				return "", next, true, nil
			}
			return escapeDoubleQuotedShell(values[0]), next, true, nil
		}
		quoted, err := quoteShellWords(values)
		if err != nil {
			return "", start, false, fmt.Errorf("quote command variable %s: %w", raw, err)
		}
		return strings.Join(quoted, " "), next, true, nil
	}
	if value, exists := expansion.Values[name]; exists {
		if quote == '"' {
			return escapeDoubleQuotedShell(value), next, true, nil
		}
		quoted, err := quoteShellWord(value)
		if err != nil {
			return "", start, false, fmt.Errorf("quote command variable %s: %w", raw, err)
		}
		return quoted, next, true, nil
	}
	if quote == '"' {
		return escapeDoubleQuotedShell(raw), next, true, nil
	}
	quoted, err := quoteShellWord(raw)
	if err != nil {
		return "", start, false, fmt.Errorf("quote command variable %s: %w", raw, err)
	}
	return quoted, next, true, nil
}

func parseCommandVariable(commandLine string, start int) (string, string, int, bool) {
	if start >= len(commandLine) || commandLine[start] != '$' {
		return "", "", start, false
	}
	if start+1 >= len(commandLine) {
		return "", "", start, false
	}
	if commandLine[start+1] == '{' {
		nameStart := start + 2
		if nameStart >= len(commandLine) || !isCommandVariableStart(rune(commandLine[nameStart])) {
			return "", "", start, false
		}
		index := nameStart + 1
		for index < len(commandLine) && isCommandVariablePart(rune(commandLine[index])) {
			index++
		}
		if index >= len(commandLine) || commandLine[index] != '}' {
			return "", "", start, false
		}
		return commandLine[nameStart:index], commandLine[start : index+1], index + 1, true
	}
	nameStart := start + 1
	if !isCommandVariableStart(rune(commandLine[nameStart])) {
		return "", "", start, false
	}
	index := nameStart + 1
	for index < len(commandLine) && isCommandVariablePart(rune(commandLine[index])) {
		index++
	}
	return commandLine[nameStart:index], commandLine[start:index], index, true
}

func isCommandVariableStart(value rune) bool {
	return value == '_' || (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
}

func isCommandVariablePart(value rune) bool {
	return isCommandVariableStart(value) || (value >= '0' && value <= '9')
}

func quoteShellWords(values []string) ([]string, error) {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		token, err := quoteShellWord(value)
		if err != nil {
			return nil, err
		}
		quoted = append(quoted, token)
	}
	return quoted, nil
}

func quoteShellWord(value string) (string, error) {
	quoted, err := syntax.Quote(value, syntax.LangBash)
	if err != nil {
		return "", fmt.Errorf("quote shell word: %w", err)
	}
	return quoted, nil
}

func escapeDoubleQuotedShell(value string) string {
	var output strings.Builder
	for _, character := range value {
		switch character {
		case '$', '`', '"', '\\':
			output.WriteByte('\\')
		}
		output.WriteRune(character)
	}
	return output.String()
}

func commandExpansionEnv(expansion commandExpansion) []string {
	env := os.Environ()
	prefix := strings.TrimSpace(expansion.EnvPrefix)
	if prefix == "" {
		return env
	}
	for key, value := range expansion.Values {
		env = append(env, prefix+key+"="+value)
	}
	for key, values := range expansion.Multi {
		env = append(env, prefix+key+"="+strings.Join(values, "\n"))
	}
	return env
}
