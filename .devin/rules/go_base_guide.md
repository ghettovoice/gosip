---
trigger: always_on
---
# Go Base Style Guide

This document outlines the coding style guidelines for Go projects,
based on Google's Go Style Guide with project-specific additions and modifications.

## Style Principles

The following attributes define readable Go code, in order of importance:

1. **Clarity**: The code's purpose and rationale is clear to the reader
2. **Simplicity**: The code accomplishes its goal in the simplest way possible
3. **Concision**: The code has a high signal-to-noise ratio
4. **Maintainability**: The code is written such that it can be easily maintained
5. **Consistency**: The code is consistent with the broader codebase

## Naming Conventions

### General Rules

- Use descriptive, self-explanatory names for variables, functions, classes, and modules
- Follow language-specific naming conventions (`snake_case` for Python, `camelCase` for JavaScript)
- Names should be concise yet descriptive
- Avoid abbreviations unless widely understood
- Use MixedCaps for exported names and mixedCaps for unexported names

### Package Names

- Package names must be concise and use only lowercase letters and numbers
- Multi-word package names should remain unbroken and in all lowercase (e.g., `tabwriter` instead of `tabWriter`)
- Avoid package names that are likely to be shadowed by commonly used local variable names
- Do not use underscores in package names except for test packages
- Avoid uninformative package names like `util`, `utility`, `common`, `helper`, `model`

### Function and Method Names

- Functions that return something have noun-like names
- Functions that do something have verb-like names
- Avoid the prefix `Get` for getter functions
- Do not repeat the package name in function names
- Do not repeat the receiver type in method names
- Do not repeat parameter names in function names

```go
// Good:
func (c *Config) WriteTo(w io.Writer) (int64, error)
func Parse(input string) (*Config, error)
func (c *Config) JobName(key string) (value string, ok bool)

// Bad:
func (c *Config) WriteConfigTo(w io.Writer) (int64, error)
func ParseYAMLConfig(input string) (*Config, error)
func (c *Config) GetJobName(key string) (value string, ok bool)
```

### Receiver Names

- Receiver variable names must be:
  - Short (typically 1-2 characters)
  - Consistent across the method set of a type
  - Not use `this`, `self`, or `me`
  - Use the type name's first letter for consistency

```go
// Good:
func (c *Config) WriteTo(w io.Writer) (int64, error)
func (s *Server) Start() error

// Bad:
func (this *Config) WriteTo(w io.Writer) (int64, error)
func (self *Server) Start() error
```

### Variable Names

- Use short variable names for local variables with limited scope
- Use longer, more descriptive names for variables with broader scope
- Use camelCase for variable names
- Avoid single-letter variable names except for loop counters and common mathematical variables

```go
// Good:
for i := 0; i < n; i++ {
    total += items[i].Value
}
config := NewDefaultConfig()
userCount := len(users)

// Bad:
for index := 0; index < numberOfItems; index++ {
    grandTotal += items[index].Value
}
c := NewDefaultConfig()
uc := len(users)
```

### Constants

- Use `UPPER_SNAKE_CASE` for exported constants
- Use `mixedCaps` for unexported constants
- Group related constants together

```go
// Good:
const (
    MaxRetries    = 3
    DefaultTimeout = 30 * time.Second
    maxBufferSize = 1024
)

// Bad:
const maxRetries = 3
const defaulttimeout = 30 * time.Second
```

## Code Organization

### File Structure

- Keep files focused on a single responsibility
- Place related functionality in the same package
- Use subdirectories for large packages with distinct concerns
- Keep files reasonably sized (typically under 1000 lines)

### Imports

- Group imports into three sections: standard library, third-party libraries, and project imports
- Sort imports within each section alphabetically
- Use blank imports only for side effects
- Avoid unused imports

```go
import (
    "context"
    "fmt"
    "time"

    "github.com/gin-gonic/gin"
    "go.uber.org/zap"

    "github.com/user/project/pkg1"
    "github.com/user/project/pkg2"
)
```

### Package Structure

- Each package should have a clear, single purpose
- Export only what is necessary for the package's API
- Keep internal implementation details unexported
- Use `internal` packages for code that should not be imported by other projects

## Code Style

### Formatting

- Use `gofmt` to format all Go code
- Use standard Go formatting conventions
- Keep lines reasonably short (typically under 120 characters)
- Use meaningful whitespace to improve readability

### Comments

- Write comments that explain why, not what
- Use godoc-style comments for exported symbols
- Keep comments up-to-date with the code
- Avoid redundant comments that restate what the code already says

```go
// Good:
// ParseConfig parses the configuration file and returns a Config struct.
// It validates required fields and returns an error if the configuration is invalid.
func ParseConfig(filename string) (*Config, error) {
    // ...
}

// Bad:
// ParseConfig parses the config file
func ParseConfig(filename string) (*Config, error) {
    // ...
}
```

### Error Handling

- Handle errors explicitly and immediately
- Use error wrapping to provide context
- Create descriptive error messages
- Use typed errors for recoverable errors

```go
// Good:
result, err := process(data)
if err != nil {
    return fmt.Errorf("failed to process data: %w", err)
}

// Bad:
result, err := process(data)
if err != nil {
    return err
}
```

### Functions

- Keep functions small and focused on a single responsibility
- Use descriptive function names
- Limit the number of parameters (typically 3-4 or fewer)
- Use structs for multiple related parameters
- Return errors as the last return value

```go
// Good:
func CreateUser(name, email string, age int) (*User, error) {
    // ...
}

// Better:
type CreateUserRequest struct {
    Name  string
    Email string
    Age   int
}

func CreateUser(req CreateUserRequest) (*User, error) {
    // ...
}
```

### Control Structures

- Use `if` statements for simple conditional logic
- Use `switch` statements for multiple conditions
- Avoid deeply nested control structures
- Use early returns to reduce nesting

```go
// Good:
func process(data []byte) error {
    if len(data) == 0 {
        return errors.New("empty data")
    }
    
    if data[0] != expectedPrefix {
        return errors.New("invalid prefix")
    }
    
    return doProcess(data)
}

// Bad:
func process(data []byte) error {
    if len(data) != 0 {
        if data[0] == expectedPrefix {
            return doProcess(data)
        } else {
            return errors.New("invalid prefix")
        }
    } else {
        return errors.New("empty data")
    }
}
```

## Concurrency

### Goroutines

- Use goroutines for concurrent operations
- Always handle goroutine lifecycle properly
- Use `sync.WaitGroup` to wait for multiple goroutines
- Avoid goroutine leaks

### Channels

- Use channels for communication between goroutines
- Prefer buffered channels for performance
- Close channels when done
- Use `select` for multiple channel operations

```go
// Good:
func worker(ctx context.Context, jobs <-chan Job, results chan<- Result) {
    for {
        select {
        case job, ok := <-jobs:
            if !ok {
                return
            }
            results <- processJob(job)
        case <-ctx.Done():
            return
        }
    }
}
```

### Synchronization

- Use `sync.Mutex` for protecting shared state
- Use `sync.RWMutex` for read-heavy workloads
- Use `atomic` operations for simple atomic operations
- Avoid data races

## Testing

**Note:** Testing guidelines are documented in `go_test_guide.md`.
This section focuses only on style aspects that affect production code.

- Design code to be testable
- Use dependency injection for testability
- Avoid tight coupling to external dependencies
- Write testable error messages

## Performance

### Memory Management

- Reuse buffers and objects when possible
- Use object pools for frequently allocated objects
- Avoid unnecessary allocations
- Use `sync.Pool` for temporary objects

### Algorithms

- Choose appropriate data structures
- Consider time and space complexity
- Profile before optimizing
- Avoid premature optimization

## Tooling and Quality Assurance

### golangci-lint

All code must pass `golangci-lint` checks. Run the linter before committing:

```bash
golangci-lint run
```

The project uses a specific configuration in `.golangci.yml`. Key rules include:

- Import organization
- Unused variable/parameter detection
- Error handling conventions
- Naming conventions
- Code complexity limits

### go fix

Use `go fix` to apply automatic fixes for deprecated APIs and language changes:

```bash
go fix ./...
```

This ensures code uses modern Go idioms and stays compatible with the Go version specified in `go.mod` (currently 1.26.0).

### Version Compatibility

- Use features available in the Go version specified in `go.mod` (1.26.0)
- Test with the minimum supported Go version
- Avoid experimental or unstable features
- Use stable APIs from the standard library

## Documentation

### Package Documentation

- Every package should have a package comment
- Package comments should describe the package's purpose
- Include usage examples in package comments
- Document important design decisions

### API Documentation

- Exported functions, types, and constants must have godoc comments
- Include parameter and return value descriptions
- Provide usage examples for complex APIs
- Document error conditions and edge cases

## Best Practices

### Error Messages

- Be specific and helpful
- Include relevant context
- Use lowercase for error messages (unless starting a sentence)
- Avoid exposing internal implementation details

### Logging

- Use structured logging
- Log at appropriate levels
- Include relevant context
- Avoid logging sensitive information

### Configuration

- Use configuration files for environment-specific settings
- Provide reasonable defaults
- Validate configuration values
- Document configuration options

### Dependencies

- Minimize external dependencies
- Use stable, well-maintained libraries
- Keep dependencies up to date
- Review security advisories

## Project-Specific Guidelines

### SIP Protocol Code

- Follow SIP RFC specifications strictly
- Use appropriate SIP terminology
- Handle protocol edge cases
- Maintain protocol compatibility

### Network Code

- Handle network errors gracefully
- Use appropriate timeouts
- Implement proper connection management
- Consider security implications

### Testing Infrastructure

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

## References

- [Google Go Style Guide](https://google.github.io/styleguide/go/)
- [Effective Go](https://golang.org/doc/effective_go.html)
- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- [Go Blog](https://blog.golang.org/)
- [golangci-lint Configuration](https://golangci-lint.run/usage/configuration/)
