---
name: openspec-execute-plan
description: Use when an OpenSpec change already has one or more Superpowers plan files and implementation should proceed while syncing completed work back to tasks.md.
license: MIT
compatibility: Requires openspec CLI and Superpowers skills (subagent-driven-development or executing-plans).
metadata:
    author: dyx
    version: "1.0.1"
---

Execute Superpowers implementation plans for an OpenSpec change, then sync completed tasks back to tasks.md.

**Input**: Optionally specify a change name (e.g., `$openspec-execute-plan (Codex) or /openspec-execute-plan (other agents) add-auth`). If omitted, check if it can be inferred from conversation context. If vague or ambiguous you MUST prompt for available changes.

**Steps**

1. **Select the change**

    If a name is provided, use it. Otherwise:
    - Infer from conversation context if the user mentioned a change
    - Auto-select if only one active change exists
    - If ambiguous, run `openspec list --json` to get available changes and prompt the user to select

    Always announce: "Using change: <name>" and how to override.

2. **Check status and load artifacts**

    ```bash
    openspec status --change '<name>' --json
    ```

    Read the change status and locate the tasks file.

3. **Detect plan files**

    Look for Superpowers plan files in `docs/superpowers/plans/`. Match files where the change name appears as an exact segment in the filename: `*-<change-name>.md` or `*-<change-name>-tasks-*.md`. Do not match partial prefixes (e.g., change `auth` must not match `auth-v2`).

4. **Decision tree**

    ```
    Plan exists?
    ├─ YES + Superpowers available → Step 5 (execute)
    ├─ YES + Superpowers unavailable → STOP: suggest installing Superpowers, or use $openspec-apply-change (Codex) or /openspec-apply-change (other agents) for direct implementation
    ├─ NO  + Superpowers available → Ask user: create plan first?
    │    ├─ Yes → Run $openspec-write-plan (Codex) or /openspec-write-plan (other agents) logic, then return to Step 3
    │    └─ No  → STOP: suggest running `$openspec-write-plan (Codex) or /openspec-write-plan (other agents)`
    └─ NO  + Superpowers unavailable → STOP: suggest installing Superpowers, or use $openspec-apply-change (Codex) or /openspec-apply-change (other agents) for direct implementation
    ```

    **Superpowers detection**: Attempt to invoke the Superpowers skill. If the call fails with "skill not found" or similar, treat as unavailable. For other errors (network, timeout, parameter errors), report the actual error to the user instead of silently treating as unavailable.

5. **Execute plans via Superpowers**

    For each plan file (in order if multiple):
    - Offer execution choice to user (ask once if multiple plans, with an "apply to all" option):

        ```
        Plan: <filename>
        Tasks: N

        Execution options:
        1. Subagent-Driven (recommended) — fresh subagent per task, review between tasks
        2. Inline Execution — batch execution with checkpoints

        Which approach? (applies to all remaining plans if multiple)
        ```

    - **Subagent-Driven**: invoke `superpowers:subagent-driven-development` with the plan file
    - **Inline Execution**: invoke `superpowers:executing-plans` with the plan file

    **If the skill call fails** (Superpowers not installed): stop and display:

    > "Superpowers skill not available. Please install Superpowers, or use `$openspec-apply-change (Codex) or /openspec-apply-change (other agents)` for direct implementation without plans."

6. **Sync OpenSpec tasks.md**

    After execution completes (or partially completes):

    a. **Parse all plan files** for this change. For each `<!-- openspec-task: LABEL -->` annotation followed by a `### Task N:` section, record:
    - The OpenSpec task label (LABEL)
    - Whether all steps in that plan task are checked (`[x]`)

    b. **Aggregate across files**: Group plan tasks by OpenSpec label. A label is "fully complete" only when **all** plan tasks with that label are checked `[x]` across **all** plan files.

    c. **Update tasks.md**: For each fully complete label, find the matching line in tasks.md by exact label match (e.g., `- [ ] 1.1 ` — label followed by a space, not prefix matching, so `1.1` does not match `1.10`) and change `- [ ]` to `- [x]`.

    d. **Report sync results**:

    ```
    ## Task Sync Results

    Synced to tasks.md:
    - [x] 1.1 Create index.html
    - [x] 1.2 Write CSS

    Still in progress:
    - [ ] 2.1 Implement data model (3/5 plan tasks complete)

    Overall: 2/3 OpenSpec tasks complete
    ```

    **Partial completion**: If only some plan tasks under a label are complete, do NOT mark the OpenSpec task as done. Report the partial progress.

7. **Show final status**

    Display:
    - Tasks synced this session
    - Overall OpenSpec progress
    - If all done: suggest `$openspec-verify-change (Codex) or /openspec-verify-change (other agents)` then `$openspec-archive-change (Codex) or /openspec-archive-change (other agents)`
    - If partially done: suggest continuing with `$openspec-execute-plan (Codex) or /openspec-execute-plan (other agents)`

**Guardrails**

- Change names must match `[A-Za-z0-9_-]+` — reject any name with spaces, quotes, or special characters before substituting into shell commands
- Superpowers is a **hard dependency** for execution — offer `$openspec-apply-change (Codex) or /openspec-apply-change (other agents)` as the non-Superpowers alternative
- Always sync tasks.md after execution, even if execution was partial
- Never mark an OpenSpec task complete unless ALL corresponding plan tasks are complete
- Do not modify any existing OpenSpec command files or Superpowers files
- Defer to Superpowers skills for git workflow conventions (worktree management, branch lifecycle)
