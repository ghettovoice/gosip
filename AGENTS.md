# AGENTS.md

This document provides quick access to the main development guidelines and style
guides for the gosip project. All contributors and AI agents should follow these
guidelines when working on the codebase.

## Core Guidelines

### Go Base Style Guide

**File:** [`.agents/rules/go_base_guide.md`](.agents/rules/go_base_guide.md)

General Go coding style guidelines based on Google's Go Style Guide. Covers:

- Naming conventions (packages, functions, variables, constants)
- Code organization and file structure
- Import management
- Error handling patterns
- Concurrency best practices
- Performance considerations
- Documentation standards

### Go Testing Style Guide

**File:** [`.agents/rules/go_test_guide.md`](.agents/rules/go_test_guide.md)

General Go testing conventions and best practices. Includes:

- Test naming conventions
- Table-driven test patterns
- Test doubles and helpers
- Test organization and structure
- Benchmark and example guidelines
- Error message standards
- Mock and stub reuse practices

### Go Error Handling Guide

**File:** [`.agents/rules/go_error_guide.md`](.agents/rules/go_error_guide.md)

General Go error handling conventions using standard-library APIs. Covers:

- Function argument validation errors
- Sentinel error declaration and usage
- Error wrapping message styles
- Error inspection with `errors.Is` and `errors.As`
- Typed errors and preservation of underlying causes
- Multiple errors, message style, and public error contracts

### Project-specific Guidelines

**File:** [`.agents/rules/proj_guide.md`](.agents/rules/proj_guide.md)

Project-specific guidelines and conventions. Covers:

- SIP protocol code conventions
- Accepted naming abbreviations and constructor names
- Error construction, classification, and tracing with repository packages
- Network code considerations
- Test file naming, shared utilities, and go-cmp comparisons
- Formatting, linting, and verification requirements
- Code review checklist

### Commit Messages Guidelines

**File:** [`.agents/rules/commit_message.md`](.agents/rules/commit_message.md)

Commit subjects, prefixes, and optional bodies. Multiple-change commits include
a body listing their main changes.

## Usage Guidelines

1. **Before making changes:** Read the relevant guidelines from the files above
2. **During development:** Follow the coding standards and conventions outlined
3. **Before committing:** Verify all guidelines have been followed
