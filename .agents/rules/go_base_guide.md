---
trigger: glob
globs:
  - "**/*.go"
---
# Go Base Style Guide

This document outlines the coding style guidelines for Go projects,
based on Google's Go Style Guide.

## Style Principles

The following attributes define readable Go code, in order of importance:

1. **Clarity**: The code's purpose and rationale is clear to the reader
2. **Simplicity**: The code accomplishes its goal in the simplest way possible
3. **Concision**: The code has a high signal-to-noise ratio
4. **Maintainability**: The code is written such that it can be easily maintained
5. **Consistency**: The code is consistent with the broader codebase

## Naming Conventions

### General Rules

- Use descriptive, self-explanatory names for variables, functions, types, and packages
- Names should be concise yet descriptive
- Use `PascalCase` for exported names and `camelCase` for unexported names

### Abbreviations

- Prefer clear exported names that are understandable without reading the implementation
- Allow widely established abbreviations in exported names, such as `Addr`, `Conn`, `ID`, `URL`, and `URI`,
  as well as standard protocol terminology
- Prefer short names in unexported code when their meaning is clear within the scope of use
  (e.g., `req`, `res`, and `tx` in a short function)
- Use more descriptive names for unexported fields, functions, and variables with broader scope;
  being unexported alone does not justify an unclear abbreviation
- Preserve conventional initialism casing, such as `RequestID`, `ParseURI`, and `requestID`

### Package Names

- Package names must be concise and use only lowercase letters and numbers
- Multi-word package names should remain unbroken and in all lowercase (e.g., `tabwriter` instead of `tabWriter`)
- Avoid package names that are likely to be shadowed by commonly used local variable names
- Do not use underscores in package names except for test packages
- Avoid uninformative package names like `util`, `utility`, `common`, `helper`, `model`

### Function and Method Names

- Prefer noun-like names for simple accessors and queries, and verb-like names for actions
- Prefer names without `Get` for methods that simply expose a receiver property
- Do not mechanically remove `Get` from data extraction helpers, lookups, or pool operations;
  name these functions according to their role
- Avoid repeating package, receiver, or parameter names when the surrounding context already makes the subject clear
- Keep a type or resource name when it disambiguates what a function creates or operates on;
  clarity is more important than mechanically eliminating repeated words
- Keep method names required by standard or external interfaces, such as `Error()`, even if they repeat the type name

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

- Choose short receiver names that remain clear within the method; 1-2 characters are common, not a hard limit
- Base the name on the type or its role; the literal first letter of the full type name is not mandatory
- Names such as `req`, `res`, `tx`, `r`, and `o` are acceptable when their role is clear
- Use a more descriptive name if shortening it would introduce ambiguity
- Keep the same receiver name across the method set of a type
- Do not use `this`, `self`, or `me`

```go
// Good:
func (c *Config) WriteTo(w io.Writer) (int64, error)
func (s *Server) Start() error
func (req *Request) Validate() error

// Bad:
func (this *Config) WriteTo(w io.Writer) (int64, error)
func (self *Server) Start() error
```

### Variable Names

- Use short variable names for local variables with limited scope
- Use longer, more descriptive names for variables with broader scope
- Use `camelCase` for local and unexported variables, and `PascalCase` for exported variables
- Use conventional single-letter names when their meaning is clear from a small scope or a familiar Go convention,
  such as `r`/`w` for I/O, `b` for bytes, `t` for tests, `k`/`v` for map entries, and loop or mathematical variables
- Avoid short names that require the reader to search outside the local context for their meaning
- Conventional type parameter names such as `T`, `K`, and `V` do not need to be expanded

```go
// Good:
for i := 0; i < n; i++ {
    total += items[i].Value
}
config := NewDefaultConfig()
userCount := len(users)

// Bad when their purpose is not clear in a broader scope:
x := NewDefaultConfig()
uc := len(users)
```

### Constants

- Use `PascalCase` for exported constants
- Use `camelCase` for unexported constants
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

- Separate standard library imports from other imports; follow the chosen formatter for any additional grouping
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

    "example.com/app/storage"
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
- Wrap errors when useful context would otherwise be lost
- Create descriptive error messages
- Use typed errors for recoverable errors
- Preserve causes with `%w` and inspect errors with `errors.Is` or `errors.As`
- See [Go Error Handling Guide](go_error_guide.md) for detailed error handling patterns

```go
// Good:
result, err := process(data)
if err != nil {
    return fmt.Errorf("process data: %w", err)
}

// Also valid when the caller already has sufficient context:
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

### Formatting and Static Analysis

- Format Go code with `gofmt`
- Run relevant tests and static analysis such as `go vet`
- Follow the formatter and linter configuration selected by the codebase
- Review automated fixes instead of accepting unrelated changes

### go fix

Use `go fix` when updating deprecated APIs or language constructs, and review the resulting changes:

```bash
go fix ./...
```

This ensures code uses modern Go idioms and stays compatible with the Go version specified in `go.mod`.

### Version Compatibility

- Use features available in the Go version specified in `go.mod`
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
- Follow the casing and punctuation rules in [Go Error Handling Guide](go_error_guide.md)
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

## References

- [Google Go Style Guide](https://google.github.io/styleguide/go/)
- [Effective Go](https://golang.org/doc/effective_go.html)
- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- [Go Blog](https://blog.golang.org/)
