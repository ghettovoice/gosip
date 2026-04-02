---
description: Run tests, lint and fix errors
---

# Test, Lint & Fix Workflow

Run tests and linter sequentially, fix any errors found.

## Steps

1. **Run tests**:

   ```bash
   go test -race -vet=all ./...
   ```

   - If tests fail, analyze the error output
   - Find the root cause in the code and fix it
   - Re-run tests until they pass

2. **Run linter**:

   ```bash
   golangci-lint run ./...
   ```

   - If linter reports errors, analyze each one
   - Fix all linter errors in the code
   - Re-run linter until it passes

3. **Run vulnarabilities check**

    ```bash
    govulncheck ./...
    ```

    - Review the report and analyze each finding
    - If the update affects a direct dependency and stays within the same minor version, apply the upgrade
    - If the update requires a new major version, provide a detailed report and instructions, then pause the task

4. **Final verification**:
   - After fixing all linter errors and vuln check issues, run all tests from step 1 again
   - Ensure linter or vulnarability fixes did not break any tests

## Fix Guidelines

- Fix the **root cause**, not symptoms
- Prefer **minimal changes**
- Preserve the project's **code style**
- Do not delete or weaken existing tests without explicit instruction
