---
trigger: model_decision
description: When preparing to make a git commit
---

# Commit Message Guidelines

When preparing to make a `git commit`, please adhere to these guidelines.

Write commit messages in English. When presenting a proposed message, format it in a code block.
Keep the subject short and limited to one sentence, using this format:

`prefix: short description (maximum one sentence)`

## Subject and Body

- If a commit contains exactly one change and the subject fully describes it, the subject alone is sufficient
- If a commit contains multiple changes, add a body after a blank line and list the main changes as bullets
- Describe the actual changes; do not pad the body with a repetition of the subject or an exhaustive file list
- Do not add generated-by signatures or bot co-author trailers

Single-change example:

```text
fix: correct the listener receiver name
```

Multiple-change example:

```text
refactor: clarify naming conventions

- Rename ambiguous private fields and utility functions
- Update test names and references
- Document naming exceptions
```

## Commit Prefixes

- `feat`: Introduce a new feature
- `fix`: Fix a bug or issue  
- `tweak`: Make minor adjustments or improvements
- `style`: Update code style or formatting
- `refactor`: Restructure code without changing functionality
- `perf`: Improve performance or efficiency
- `test`: Add or update tests
- `docs`: Update documentation
- `chore`: Perform maintenance tasks or updates
- `ci`: Change CI/CD configuration
- `build`: Modify build system or dependencies
- `revert`: Revert a previous commit
- `hotfix`: Apply an urgent bug fix
- `init`: Initialize a new project or feature
- `merge`: Merge branches
- `wip`: Mark work in progress
- `release`: Prepare for a release
