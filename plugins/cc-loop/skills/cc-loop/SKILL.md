---
name: cc-loop
description: Install or refresh the CC Loop runtime for structured [[CC_LOOP ...]] activation with min="...", rounds="...", or goal="...". Use only for cc-loop setup, status, uninstall, or activation guidance.
---

# CC Loop

## Procedures

**Step 1: Inspect current Claude Code state**
1. Read `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/settings.json` only if it exists so you can explain whether managed `cc-loop` hooks are already present.
2. Read `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/cc-loop/config.toml` only if it exists so you can describe continuation guidance, goal confirmation settings, Stop hook timeout, and any `pre_loop_continue` command.
3. When an active workspace is known, read the nearest `cc-loop.toml` from the current directory up to the workspace root if it exists. Treat it as a per-field project overlay over the global runtime config.
4. Do not hand-edit global hook files for normal setup; `cc-loop install` syncs managed hook commands into Claude Code `settings.json` while preserving unrelated hooks.

**Step 2: Install, refresh, or upgrade the runtime**
1. If `cc-loop` is not on `PATH`, install it:
   - `go install github.com/compozy/cc-loop/cmd/cc-loop@latest`
2. Execute:
   - `cc-loop install`
3. If the user asks to update an existing install, prefer:
   - `cc-loop upgrade`
   - or `cc-loop upgrade --version v0.1.1` for a pinned release.
4. Read the command output and report the runtime path, loop state path, managed hook settings path, and whether Claude Code plugin marketplace refresh ran or was skipped.
5. Tell the user to restart Claude Code or run `/reload-plugins` so plugin lifecycle hooks and skills are reloaded.

**Step 3: Explain activation**
1. Tell the user that loop activation must be the first line of the prompt.
2. Tell the user that the header must contain exactly one limiter:
   - `[[CC_LOOP name="release-stress-qa" min="6h"]]`
   - `[[CC_LOOP name="release-stress-qa" rounds="3"]]`
   - `[[CC_LOOP name="release-stress-qa" goal="ship only after verification"]]`
3. Tell the user that goal loops run a configurable headless confirmation command before continuing or completing; the public command returns normal text and the default uses `claude -p`, model `opus`, and effort `high`.
4. Tell the user that model and reasoning are separate: `confirm_model="opus"` and `confirm_reasoning_effort="xhigh"`.
5. Tell the user that cc-loop privately interprets confirmation text into the internal verdict through a fixed `claude -p --output-format json --json-schema ...` step; the default interpreter model is `haiku`.
6. Tell the user that the task prompt starts on the next line and remains the source task for every continuation.
7. Tell the user that loop state is persisted under `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/cc-loop/loops/` and goal-check metadata is appended to `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/cc-loop/runs.jsonl`.

## Commands

- `cc-loop install`: install or refresh the local runtime under `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/cc-loop/` and sync managed hook registrations into Claude Code `settings.json`.
- `cc-loop upgrade`: download the latest GitHub release, verify checksums, replace the local CLI binary, refresh the managed runtime/hooks, and refresh the Claude Code plugin marketplace when the `claude` CLI is available.
- `cc-loop upgrade --version v0.1.1`: install a pinned release with the same checks.
- `cc-loop status`: print active loop state as JSON.
- `cc-loop status --all`: include completed, superseded, and cut-short loops.
- `cc-loop uninstall`: remove the managed runtime directory and only the `cc-loop` managed hook registrations.
- `cc-loop.toml`: optional project-local runtime config discovered from the hook CWD up to the workspace root. Fields defined there override the global runtime config; omitted fields inherit global/default values.
- `[pre_loop_continue]`: optional cc-loop runtime hook configured with a shell-like `command = ""` string that is parsed to argv and run synchronously inside the Stop handler before an automatic continuation prompt is emitted. Project-local `command = ""` disables a global pre-loop command for that project.
- `[goal]`: optional defaults for goal confirmation, including `confirm_model`, `confirm_reasoning_effort`, `confirm_command`, `timeout_seconds`, `interpret_model`, `interpret_reasoning_effort`, `interpret_timeout_seconds`, and `max_output_bytes`.
- `[hooks].stop_timeout_seconds`: managed Stop hook timeout written during `cc-loop install`; rerun install and restart Claude Code after changing it.

## Error Handling

- If `go install` fails, confirm Go is installed and available on `PATH`.
- If `cc-loop install` fails while updating Claude Code `settings.json`, inspect that file for malformed JSON or filesystem permission problems.
- If activation does nothing after install, restart Claude Code or run `/reload-plugins`, then confirm the `cc-loop` plugin is installed and enabled.
