# CC Loop Plugin

Claude Code plugin bundle for the `cc-loop` CLI.

## Contents

- `.claude-plugin/plugin.json`: Claude Code plugin manifest.
- `hooks/hooks.json`: Claude Code lifecycle hooks for `UserPromptSubmit` and `Stop`.
- `skills/cc-loop/SKILL.md`: user-facing setup and usage guidance.

The hook commands expect the managed runtime binary at:

```text
${CLAUDE_CONFIG_DIR:-$HOME/.claude}/cc-loop/bin/cc-loop
```

Run `cc-loop install` after installing the CLI. The installer also mirrors the
managed hook registrations into `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/settings.json`
while preserving unrelated user hooks and settings.

## Local Development

Validate the marketplace and plugin:

```bash
claude plugin validate .
claude plugin validate ./plugins/cc-loop
```

Load the plugin directly for one Claude Code session:

```bash
claude --plugin-dir ./plugins/cc-loop
```

Or install through the local marketplace:

```bash
claude plugin marketplace add /path/to/cc-loop
claude plugin install cc-loop@cc-loop-plugins
```

Restart Claude Code or run `/reload-plugins` after changing plugin files.

## Goal Loops

Goal loops confirm completion with a configurable headless Claude Code command
that returns normal text. The default confirmation command uses `claude -p`,
model `opus`, and effort `high`. `cc-loop` privately interprets the text through
a fixed `claude -p --output-format json --json-schema ...` step; the interpreter
defaults to `haiku` and effort `low`.
