# cc-loop Agent Instructions

## Project Overview

`cc-loop` is a local-first Go CLI and Claude Code plugin that keeps explicitly
activated Claude Code tasks running until they satisfy a minimum duration, a
target number of deliberate rounds, or a goal-confirmation verdict.

The project ships as:

- a Go single-binary CLI at `cmd/cc-loop`;
- internal runtime packages under `internal/`;
- a Claude Code plugin bundle under `plugins/cc-loop`;
- a local Claude Code marketplace under `.claude-plugin/marketplace.json`.

Core product premise: every feature must remain usable from the CLI and from
Claude Code lifecycle hooks. Features are incomplete if they only work through
internal Go calls.

## Most Critical Rules

### Git Commands Restriction

- Never run destructive git commands without explicit user permission.
- Forbidden without explicit permission: `git restore`, `git checkout`, `git reset`, `git clean`, `git rm`.
- Do not restore, remove, or rewrite files that are unrelated to your task.
- If `git status` shows unexpected changes, assume they belong to the user or another agent.

### Plan Mode Persistence

- In Plan mode, after the user accepts a plan, always write the accepted plan to `.codex/plans/`.
- If the accepted plan changes later, update or append the corresponding file under `.codex/plans/`.

### Test Integrity

- Tests exist to discover bugs, not to create mock-driven confidence.
- When a test reveals unexpected behavior, fix production code instead of weakening the assertion.
- Final validation for meaningful behavior must include real integration, CLI, hook, filesystem, or end-to-end style checks where applicable.

## Memory Ledger

Maintain one Memory Ledger per agent session in `.codex/ledger/<YYYY-MM-DD>-MEMORY-<slug>.md`.

At the start of every assistant turn:

- read your own ledger file if it exists;
- scan other `*-MEMORY-*.md` files in `.codex/ledger/` for cross-agent awareness;
- treat other agents' ledgers as read-only.

Use this format:

```markdown
Goal (incl. success criteria):
Constraints/Assumptions:
Key decisions:
State:
Done:
Now:
Next:
Open questions (UNCONFIRMED if needed):
Working set (files/ids/commands):
```

In replies, begin with a brief Ledger Snapshot: Goal, Now/Next, and Open Questions.

## Greenfield OSS v1

- Treat this project as the public `cc-loop` implementation.
- Do not add compatibility aliases, migration bridges, fallback schemas, or references to prior project names unless the user explicitly asks.
- Renames must update code, plugin metadata, docs, tests, marketplace entries, and examples in the same change.
- Delete obsolete code instead of preserving unused compatibility paths.

## Critical Engineering Rules

- `make verify` must pass before completing any code, plugin, or behavior task.
- For Go changes, run `go vet ./...` before or as part of the final gate.
- `make lint` is strict `golangci-lint`; zero warnings and zero issues are acceptable.
- Never add dependencies by editing `go.mod` by hand. Use `go get`, then `go mod tidy`.
- Never use web search for local project code. Use `rg`, `rg --files`, `find`, and local file reads.
- Use official Claude Code documentation for Claude Code plugin, marketplace, settings, and hook behavior.
- Never ignore errors with `_` in production code or tests unless a short written justification is next to the discard.
- Do not commit local scratch, QA, runtime artifacts, coverage files, or generated test sandboxes unless the task explicitly requires them.
- Durable artifacts such as code, comments, docs, plans, commit messages, and this file must be in English.

## Skill Dispatch

Activate skills before writing code or durable project instructions. Use the smallest set that covers the task.

| Domain | Required Skills | Conditional Skills |
| --- | --- | --- |
| Go runtime, CLI, hooks, installer | `golang-pro` | `context7`, `find-docs` |
| Claude Code plugin metadata, lifecycle hooks, settings, marketplace behavior | `context7` or `find-docs` | `exa-web-search-free` |
| Release or scenario QA | `qa-report`, `qa-execution` | `golang-pro` |
| Architecture audit, dead code, duplication | `architectural-analysis` | `golang-pro` |

Every Go change requires `golang-pro`, even when the edit is small.

## Build Commands

Use the Makefile targets. They delegate to Mage and install Mage on demand when needed.

```bash
make deps
make fmt
make vet
make lint
make test
make build
make verify
make help
```

Run narrower commands while iterating, but the final gate for project changes is `make verify`.

## Surface Map

| Path | Responsibility |
| --- | --- |
| `cmd/cc-loop` | Binary entry point only. Keep business logic out of `main`. |
| `internal/cli` | Cobra commands, flags, stdin/stdout/stderr wiring, status JSON command behavior. |
| `internal/loop` | Activation parsing, hook processing, loop state, config, continuation decisions. |
| `internal/installer` | `CLAUDE_CONFIG_DIR` handling, runtime install/uninstall, Claude Code settings preservation. |
| `internal/updater` | Release download, checksum verification, runtime refresh, and Claude Code marketplace refresh. |
| `internal/pluginmeta` | Plugin and marketplace metadata validation tests. |
| `plugins/cc-loop` | Claude Code plugin bundle: manifest, lifecycle hooks, user-facing skill. |
| `.claude-plugin/marketplace.json` | Local Claude Code marketplace entry pointing to `./plugins/cc-loop`. |
| `.codex/plans` | Accepted plans only. |
| `.codex/ledger` | Agent session memory, usually not product documentation. |

## Product Invariants

- Activation only happens when the first prompt line is a valid `[[CC_LOOP ...]]` header.
- `name="..."` is required.
- Exactly one limiter is required: `min="..."`, `rounds="..."`, or `goal="..."`.
- `rounds` must be a positive integer.
- Supported durations include forms such as `30m`, `30min`, `1h 30m`, `2 hours`, and `45sec`.
- Loop state is isolated by Claude Code `session_id`.
- State persists under `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/cc-loop/loops/`.
- State writes must be atomic and leave a debuggable JSON shape.
- `status` output is JSON and should stay stable unless the change is intentional and documented.
- `cc-loop install` may create or update managed `cc-loop` runtime files and managed hook registrations in Claude Code `settings.json`.
- `cc-loop uninstall` removes only managed `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/cc-loop/` artifacts and only `cc-loop` managed hook registrations.
- Plugin lifecycle hooks come from `plugins/cc-loop/hooks/hooks.json`.
- The default goal confirmation model is `opus`; do not configure a different default confirmation model unless the user explicitly requests it.
- The project is local-first. Do not add telemetry or external services without an explicit product decision.

## Coding Style

- Keep packages small and explicit. Avoid package-level mutable state unless it is immutable configuration or version metadata.
- Keep `main` thin: construct the CLI, execute it, and exit.
- Cobra commands must be testable with injected args, streams, env, and filesystem roots. Do not call `os.Exit` from internal packages.
- Keep hook handlers protocol-safe: hook JSON goes to stdout; logs and diagnostics go to stderr or files.
- Preserve user configuration. Installer code must update only the keys and paths it owns.
- Prefer typed structs and JSON/TOML encoders over string concatenation for structured data.
- Use `filepath` and `os.UserHomeDir` style APIs for paths.
- Keep shell embedded in plugin metadata minimal, quoted, and tolerant when the runtime binary is not installed yet.
- Add comments only when they explain non-obvious behavior, protocol constraints, or failure modes.

## Testing

- Default to table-driven tests with subtests.
- Use `t.TempDir()` for filesystem tests.
- Use isolated `CLAUDE_CONFIG_DIR` values in installer, hook, and status tests. Never point tests at the user's real `~/.claude`.
- Use real files and real JSON/TOML parsing for config, store, installer, and plugin metadata tests.
- Assert both success output and failure behavior.
- Keep race-sensitive tests compatible with `go test -race ./...`.
- Add regression tests for bugs before or with the fix.
- Do not weaken tests to match broken behavior.

## Code Search Hierarchy

1. `rg` / `rg --files` for local code.
2. Local docs, README, plans, ledgers, and plugin metadata.
3. `context7`, `find-docs`, or official Claude Code docs for external technical documentation.
4. Web search only when authoritative docs are insufficient or the user explicitly asks.

## Commit Style

- Do not create commits unless the user asks.
- When committing, use exactly one prefix: `feat:`, `fix:`, `refactor:`, `docs:`, `test:`, or `build:`.
- Do not use `chore:`, `style:`, or `ci:`.
- Use `build:` for tooling, release, and CI changes.
- Run `make verify` before committing.

## QA Loop Reuse

- Prefer a fresh isolated QA home for a new independent QA pass.
- Never run provider-backed QA against the user's raw global `~/.claude`; isolate `CLAUDE_CONFIG_DIR`.
- Persist and report any reusable QA manifest path, lab root, runtime home, base URL if applicable, and verification evidence.

## Cross References

- Plugin manifest: `plugins/cc-loop/.claude-plugin/plugin.json`
- Hook config: `plugins/cc-loop/hooks/hooks.json`
- Plugin skill: `plugins/cc-loop/skills/cc-loop/SKILL.md`
- Local marketplace: `.claude-plugin/marketplace.json`
