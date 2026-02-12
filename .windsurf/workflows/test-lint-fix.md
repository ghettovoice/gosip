---
description: Run tests, lint and fix errors
---

# Test, Lint & Fix Workflow

Run tests and linter sequentially, fix any errors found.

## Steps

1. **Run tests**:

   ```bash
   make test
   ```

   - If tests fail, analyze the error output
   - Find the root cause in the code and fix it
   - Re-run tests until they pass

2. **Run linter**:

   ```bash
   make lint
   ```

   - If linter reports errors, analyze each one
   - Fix all linter errors in the code
   - Re-run linter until it passes

3. **Final verification**:
   - After fixing all linter errors, run `make test` again
   - Ensure linter fixes did not break any tests

## Fix Guidelines

- Fix the **root cause**, not symptoms
- Prefer **minimal changes**
- Preserve the project's **code style**
- Do not delete or weaken existing tests without explicit instruction
