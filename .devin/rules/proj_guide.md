---
trigger: always_on
---
# Project-Specific Guidelines

## SIP Protocol Code

- Follow SIP RFC specifications strictly
- Use appropriate SIP terminology
- Handle protocol edge cases
- Maintain protocol compatibility

## Network Code

- Handle network errors gracefully
- Use appropriate timeouts
- Implement proper connection management
- Consider security implications

## Testing Infrastructure

- Use the testing patterns defined in `go_test_guide.md`
- Maintain test utilities in separate packages
- Keep tests fast and reliable
- Use table-driven tests for multiple scenarios

## Code Review Checklist

- [ ] Code follows naming conventions
- [ ] Functions are small and focused
- [ ] Error handling is comprehensive
- [ ] Comments explain why, not what
- [ ] Code is properly formatted
- [ ] Imports are organized correctly
- [ ] No unused variables or imports
- [ ] Concurrency is handled correctly
- [ ] Tests are adequate
- [ ] Documentation is complete
- [ ] Code passes `golangci-lint` checks
- [ ] Code uses appropriate Go version features
