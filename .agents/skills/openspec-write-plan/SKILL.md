---
name: openspec-write-plan
description: Use when an OpenSpec change has proposal, design, and tasks artifacts ready and a Superpowers implementation plan needs to be created before execution.
license: MIT
compatibility: Requires openspec CLI and Superpowers writing-plans skill.
metadata:
    author: dyx
    version: "1.0.1"
---

Generate a detailed Superpowers implementation plan from an OpenSpec change's artifacts.

**Input**: Optionally specify a change name. Supports `--max-tasks N` (default 10) to control plan splitting.

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

    Parse the JSON to understand schema and artifact status.

    ```bash
    openspec instructions apply --change '<name>' --json
    ```

    Read all `contextFiles` (proposal, design, tasks, etc.).

    **If tasks artifact is missing or incomplete**: stop and suggest running `$openspec-propose (Codex) or /openspec-propose (other agents)` first.

3. **Parse tasks.md to extract task labels**

    Read the tasks file and extract all task entries with their labels (e.g., `1.1`, `2.3`). Each line matching `- [ ] <label> <description>` is a task. Record the label and description for each.

4. **Determine plan splitting**

    If `--max-tasks` is less than 1, treat as default (10). Count total tasks. If total > `--max-tasks`:
    - Calculate groups: `g = ceil(total / max-tasks)`
    - Distribute tasks evenly across groups (group sizes differ by at most 1)
    - Try to keep tasks sharing the same top-level label number (e.g., all `1.x` tasks) together (soft constraint)
    - Present the splitting plan to the user for confirmation:
        ```
        Total tasks: 21, max-tasks: 10 → 3 plans
        Plan 1: Tasks 1.1–2.3 (7 tasks)
        Plan 2: Tasks 2.4–4.1 (7 tasks)
        Plan 3: Tasks 4.2–5.3 (7 tasks)
        Proceed? [Y/n]
        ```
    - Wait for user confirmation before proceeding

5. **Call superpowers:writing-plans**

    Invoke the `superpowers:writing-plans` skill with these additional constraints:
    - Provide all OpenSpec artifacts as context (proposal, design, tasks)
    - **Skip the "Execution Handoff" section entirely** — do NOT prompt the user to choose between Subagent-Driven or Inline Execution. Do NOT invoke `superpowers:subagent-driven-development` or `superpowers:executing-plans` after plan generation. Control returns to this command for the output summary step.
    - **Require** that each generated Task includes an `<!-- openspec-task: LABEL -->` comment on the line directly preceding the `### Task N:` heading (no blank lines between), where LABEL matches the corresponding OpenSpec tasks.md label
    - Example:
        ```markdown
        <!-- openspec-task: 1.1 -->

        ### Task 1: Create HTML skeleton
        ```
    - One Superpowers task may map to exactly one OpenSpec task label
    - Multiple Superpowers tasks may share the same OpenSpec task label (when a coarse OpenSpec task maps to multiple fine-grained steps)

    **If the skill call fails** (Superpowers not installed): stop and display:

    > "Superpowers writing-plans skill not detected. Cannot create plan. Please install Superpowers first."

    Do not generate any plan files. Do not fall back to a different approach.

    **File naming:**
    - Single plan: `docs/superpowers/plans/YYYY-MM-DD-<change-name>.md`
    - Multiple plans: `docs/superpowers/plans/YYYY-MM-DD-<change-name>-tasks-<first>-<last>.md`

6. **Verify mapping coverage**

    After plan generation, scan the plan file(s) for all `<!-- openspec-task: LABEL -->` annotations. Check:
    - Every OpenSpec task label has at least one corresponding plan task
    - Every plan task annotation references a label that exists in tasks.md (catch typos like `1.11` vs `1.1`)
    - If any label is missing or invalid: warn the user and offer to regenerate the plan

7. **Output summary**

    ```
    ## Plan Created

    **Change:** <change-name>
    **Plan file(s):**
    - docs/superpowers/plans/<filename>.md (N tasks)

    **Mapping coverage:** All OpenSpec tasks covered ✓

    Ready to execute! Run `$openspec-execute-plan (Codex) or /openspec-execute-plan (other agents) <change-name>` to start implementation.
    This command will handle execution mode selection and sync progress back to OpenSpec tasks.
    ```

    **IMPORTANT:** Do NOT offer any other execution method. Do NOT suggest using `superpowers:subagent-driven-development` or `superpowers:executing-plans` directly. The only supported next step is `$openspec-execute-plan (Codex) or /openspec-execute-plan (other agents)`.

**Guardrails**

- Change names must match `[A-Za-z0-9_-]+` — reject any name with spaces, quotes, or special characters before substituting into shell commands
- Superpowers is a **hard dependency** — do not proceed without it
- Always load OpenSpec artifacts before calling superpowers:writing-plans
- Always verify mapping coverage after plan generation
- Do not modify any existing OpenSpec files
- Do not skip user confirmation for plan splitting
- Follow Superpowers plan file conventions (header, task structure, TDD steps)
- **Never** let superpowers:writing-plans perform its "Execution Handoff" — execution must go through `$openspec-execute-plan (Codex) or /openspec-execute-plan (other agents)` to ensure OpenSpec task sync
