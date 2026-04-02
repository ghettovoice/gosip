# AGENTS.md

This document provides quick access to the main development guidelines and style guides for the gosip project. All contributors and AI agents should follow these guidelines when working on the codebase.

## Core Guidelines

### Go Base Style Guide

**File:** `.windsurf/rules/go_base_guide.md`

Comprehensive Go coding style guidelines based on Google's Go Style Guide with project-specific additions. Covers:

- Naming conventions (packages, functions, variables, constants)
- Code organization and file structure
- Import management
- Error handling patterns
- Concurrency best practices
- Performance considerations
- Documentation standards

### Go Testing Style Guide

**File:** `.windsurf/rules/go_test_guide.md`

Testing conventions and best practices for the project. Includes:

- Test naming conventions
- Table-driven test patterns
- Test doubles and helpers
- Test organization and structure
- Benchmark and example guidelines
- Error message standards
- Mock and stub reuse practices

### Go Error Handling Guide

**File:** `.windsurf/rules/go_error_guide.md`

Error handling conventions and style guidelines. Covers:

- Function argument validation errors
- Sentinel error declaration and usage
- Error wrapping message styles
- Error trace wrapping with internal errors package
- Complete error handling examples
- Best practices summary

### golangci-lint Compliance

**File:** `.windsurf/rules/golangci_lint_compliance.md`

Mandatory linting requirements for all code changes. Specifies:

- Required actions after code modifications
- Common issues to address (imports, naming, error handling)
- Integration with development workflow
- Exception handling guidelines
- Tool configuration details

## Usage Guidelines

1. **Before making changes:** Read the relevant guidelines from the files above
2. **During development:** Follow the coding standards and conventions outlined
3. **After changes:** Run `golangci-lint run ./...` to ensure compliance
4. **Before committing:** Verify all guidelines have been followed

## Quick Reference Commands

```bash
# Run linter to check compliance
golangci-lint run ./...
# or
make lint

# Run all tests
go test -race -vet=all -timeout=30s ./...
# or
make test

# Apply automatic fixes
go fix ./...
```

## Project Context

This is a Go SIP (Session Initiation Protocol) library implementation. The guidelines above ensure code quality, consistency, and maintainability across the entire codebase. All contributors, including AI agents, should adhere to these standards when working on any part of the project.
