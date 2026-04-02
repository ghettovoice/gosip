---
trigger: always_on
description: After adding or modifying any Go code in the project
---

# golangci-lint Compliance Rule

After adding or modifying any Golang code in the project, you must ensure full compliance with golangci-lint rules.

## Required Actions

### 1. Follow golangci-lint Rules During Development

- Write code that complies with all golangci-lint rules from the start
- Use the project's `.golangci.yml` configuration as reference
- Pay special attention to:
  
  - Import organization and unused imports
  - Error handling conventions
  - Naming conventions (variables, functions, packages)
  - Code complexity limits
  - Formatting and style guidelines

### 2. Run golangci-lint After Changes

After completing any code modifications, you must run golangci-lint to verify compliance:

```bash
golangci-lint run ./...
```

### 3. Fix All Lint Issues

- If golangci-lint reports any warnings or errors in the code you added/modified, you must fix ALL of them
- Do not ignore or suppress lint issues unless absolutely necessary
- For any suppression, provide clear justification in comments
- Re-run golangci-lint after fixes to ensure clean output

### 4. Verification Checklist

Before considering any code change complete, verify:

- [ ] `golangci-lint run` produces no output (clean run)
- [ ] All added/modified files pass lint checks
- [ ] No new lint issues introduced
- [ ] Code follows project's style guidelines
- [ ] Imports are properly organized

## Common Issues to Address

### Import Management

- Remove unused imports
- Group imports correctly (standard, third-party, project)
- Sort imports alphabetically within groups

### Error Handling

- Handle errors explicitly
- Use proper error wrapping
- Follow project's error handling patterns

### Naming Conventions

- Use descriptive names
- Follow Go naming conventions (camelCase, MixedCaps)
- Avoid abbreviations unless widely understood

### Code Quality

- Keep functions small and focused
- Avoid excessive complexity
- Use proper formatting (gofmt)

## Integration with Development Workflow

This rule applies to:

- New function implementations
- Modifications to existing code
- Test file changes
- Configuration updates
- Documentation code examples

## Exception Handling

Only suppress lint rules when:

- The rule conflicts with specific project requirements
- The suppression is temporary and documented
- Alternative approaches have been considered

Use `//nolint:%linter_name%` comments sparingly and always explain the reason.

## Tool Configuration

The project uses `.golangci.yml` for configuration. Key settings include:

- Enabled linters for comprehensive code quality checks
- Custom rules for project-specific requirements
- Performance optimizations for large codebases

Always ensure your code works with the current configuration without requiring changes to the lint setup.
