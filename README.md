# CC Loop

`cc-loop` is a local-first Claude Code CLI and plugin that keeps explicitly
activated Claude Code tasks running until they satisfy a minimum duration, a
target number of deliberate rounds, or an independently confirmed goal.

The project follows the same runtime and packaging structure as the source loop project:

- a Go single-binary CLI at `cmd/cc-loop`;
- runtime packages under `internal/`;
- a Claude Code plugin bundle under `plugins/cc-loop`;
- a local Claude Code marketplace at `.claude-plugin/marketplace.json`;
- release, verification, and packaging automation through Make, Mage, and GoReleaser.

## Features

- Activation by header: loops start only when the first prompt line is a valid
  `[[CC_LOOP ...]]` header.
- Three exclusive limiters: `min="..."`, `rounds="..."`, or `goal="..."`.
- Claude Code lifecycle hooks: bundled `UserPromptSubmit` and `Stop` hooks.
- Local state: loop records live under `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/cc-loop/loops/`.
- Goal confirmation: a configurable headless Claude Code reviewer returns plain
  text, then `cc-loop` privately interprets that text into a structured verdict.
- Project overrides: a workspace `cc-loop.toml` can override the global runtime
  config on a per-field basis.

## Install

Install the CLI:

```bash
go install github.com/compozy/cc-loop/cmd/cc-loop@latest
cc-loop install
```

`cc-loop install` creates or refreshes:

- `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/cc-loop/bin/cc-loop`
- `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/cc-loop/loops/`
- `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/cc-loop/config.toml`
- managed `UserPromptSubmit` and `Stop` hook registrations in
  `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/settings.json`

It preserves unrelated Claude Code settings and hook entries.

## Claude Code Plugin

For marketplace-based installation, add this repository as a Claude Code
marketplace:

```bash
claude plugin marketplace add compozy/cc-loop
claude plugin install cc-loop@cc-loop-plugins
```

For local development:

```bash
claude plugin validate .
claude plugin marketplace add /path/to/cc-loop
claude plugin install cc-loop@cc-loop-plugins --scope user
```

During plugin development you can also load the plugin directly:

```bash
claude --plugin-dir ./plugins/cc-loop
```

Restart Claude Code, or run `/reload-plugins` in an active session, after
changing plugin files or managed hook settings.

## Usage

Minimum duration loop:

```text
[[CC_LOOP name="release-stress-qa" min="6h"]]
Run the full release QA plan and keep working until the time window has elapsed.
```

Round-count loop:

```text
[[CC_LOOP name="release-stress-qa" rounds="3"]]
Run three deliberate implementation and verification rounds before stopping.
```

Goal-confirmed loop:

```text
[[CC_LOOP name="release-stress-qa" goal="ship only after real verification"]]
Do the work, validate it with real evidence, and continue until the goal is confirmed.
```

Goal loops may override the confirmation model and reasoning effort:

```text
[[CC_LOOP name="release-stress-qa" goal="ship only after real verification" confirm_model="opus" confirm_reasoning_effort="xhigh"]]
```

`name="..."` is required. Exactly one of `min`, `rounds`, or `goal` is required.
`rounds` must be a positive integer. Durations support values such as `30m`,
`30min`, `1h 30m`, `2 hours`, and `45sec`.

## CLI

```bash
cc-loop install
cc-loop upgrade
cc-loop upgrade --version v0.1.1
cc-loop status
cc-loop status --all
cc-loop status --session-id <id>
cc-loop status --workspace-root <path>
cc-loop uninstall
cc-loop version
```

Use `--claude-config-dir <path>` to target an isolated Claude Code config
directory in tests or QA labs.

## Runtime Config

The global config lives at
`${CLAUDE_CONFIG_DIR:-$HOME/.claude}/cc-loop/config.toml`:

```toml
optional_skill_name = ""
optional_skill_path = ""
extra_continuation_guidance = ""

[hooks]
stop_timeout_seconds = 2700

[goal]
confirm_model = "opus"
confirm_reasoning_effort = "high"
confirm_command = "claude -p --permission-mode bypassPermissions --output-format text $MODEL_ARGV $REASONING_ARGV"
timeout_seconds = 2400
interpret_model = "haiku"
interpret_reasoning_effort = "low"
interpret_timeout_seconds = 120
max_output_bytes = 12000

[pre_loop_continue]
command = ""
cwd = "session_cwd"
timeout_seconds = 60
max_output_bytes = 12000
```

Projects may also define `cc-loop.toml` in the workspace. During an active loop,
`cc-loop` searches from the Claude Code hook CWD up to the resolved workspace
root and uses the nearest `cc-loop.toml` it finds. It never searches above the
workspace root.

Effective config precedence is:

```text
defaults -> global config -> nearest project cc-loop.toml
```

For project-local continuation context:

```toml
# ./cc-loop.toml
[pre_loop_continue]
command = ".claude/scripts/loop-context.sh --input $INPUT_FILE"
cwd = "workspace_root"
timeout_seconds = 30
max_output_bytes = 8000
```

## Goal Confirmation

The public confirmation command receives a read-only review prompt on stdin and
returns normal text. The default runner is:

```bash
claude -p --permission-mode bypassPermissions --output-format text --model opus --effort high
```

`cc-loop` then runs a fixed private Claude Code interpreter command using
`claude -p --output-format json --json-schema ...` to convert the review text
into the internal verdict. The interpreter defaults to `haiku` with `low`
effort. Users may configure only the interpreter model, reasoning effort, and
timeout.

Available placeholders in `confirm_command` include:

- `$PROMPT` and `$PROMPT_FILE`
- `$CONFIRM_OUTPUT_PATH`
- `$MODEL_ARGV` and `$REASONING_ARGV`
- `$MODEL`, `$REASONING_EFFORT`, `$WORKSPACE_ROOT`, `$CWD`, `$SESSION_ID`,
  `$LOOP_NAME`, `$LOOP_SLUG`, `$RUNS_LOG_PATH`, and `$CLAUDE_CONFIG_DIR`

The same values are exported with the `CC_LOOP_CONFIRM_` prefix.

## Uninstall

```bash
cc-loop uninstall
```

This removes only the managed
`${CLAUDE_CONFIG_DIR:-$HOME/.claude}/cc-loop/` runtime directory and the
`cc-loop` managed hook registrations from `settings.json`. It preserves
unrelated Claude Code settings, installed plugins, credentials, sessions, and
hook entries.

## Development

Use the Makefile targets:

```bash
make deps
make fmt
make vet
make lint
make test
make build
make verify
make release-check
make release-snapshot
```

`make verify` runs formatting, `go vet ./...`, `golangci-lint`, race-enabled
tests, and `go build ./...`.

## Privacy

`cc-loop` itself does not send data to a network service. It reads Claude Code
hook JSON from stdin, writes hook decisions to stdout, and stores loop state
locally under `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/cc-loop/`. Goal loops invoke
the local `claude` command, which uses the user's configured Claude Code
provider and authentication.
