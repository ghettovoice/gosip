---
trigger: glob
globs:
  - "**/*.go"
---
# Go Testing Style Guide

Based on Google Go Style Guide: <https://google.github.io/styleguide/go/>

## Test Naming

### Test Functions

- Use underscores in test function names only in `*_test.go` files
- Name tests after the behavior they cover; use the following patterns when they fit, not as a rigid grammar
- For a package-level function, use `Test<Function>` or `Test<Function>_<Scenario>`
- For a method, use `Test<Type>_<Method>` or `Test<Type>_<Method>_<Scenario>`
- The scenario suffix is optional when a test groups multiple named table cases or subtests
- Use a scenario suffix to distinguish separate test functions when their setup or behavior cannot be usefully
  grouped into one test
- A test for a type or service may use `Test<Type>` or `Test<Service>` with common initialization and named subtests
- For behavior spanning multiple types or functions, use a descriptive feature or scenario name;
  do not force the test into a single function or method naming pattern
- Avoid duplicate information between a top-level test name and its subtest names;
  the full test path should identify the behavior and case
- For black box tests, use package with `_test` suffix (preferred tests)

```go
// Good:
func TestParse(t *testing.T) { ... } // named table cases cover parsing behavior
func TestParse_InvalidInput(t *testing.T) { ... } // a separate scenario
func TestCharge_Success(t *testing.T) { ... }
func TestCharge_Declined(t *testing.T) { ... }
func TestService_Process(t *testing.T) { ... } // named table cases cover the method
func TestService_Process_ValidCard(t *testing.T) { ... }
func TestService(t *testing.T) { ... } // common setup and named subtests cover the service
func TestCheckout_RetriesAfterConnLoss(t *testing.T) { ... } // multiple components

// Bad:
func TestCase1(t *testing.T) { ... } // does not identify the behavior
func testChargeSuccess(t *testing.T) { ... } // not exported
```

### Test Files

- Go test files must end in `_test.go`
- Tests in the external `<package>_test` package are black box tests that use the public API
- Tests in the same package as the code under test are white box tests that may access unexported names
- The package declaration determines visibility; Go does not require a separate filename suffix for white box tests

### Test Packages

- For black box tests, use package with `_test` suffix
- For white box tests, use the same package as the code under test
- For test helper packages, use `test` suffix (e.g., `creditcardtest`)
- Avoid underscores in package names, except in test packages

```go
// Good:
package linkedlist_test     // black box tests
package creditcardtest      // test helpers

// Bad:
package linked_list_test
package testhelpers
```

## Test Structure

### Table-driven Tests

- Prefer table-driven approach for multiple test cases
- Structure test data in slices of structs
- Include descriptive names for each test case
- Common initialization is allowed, but each case must remain isolated; create or reset mutable fixtures per case,
  and only run cases in parallel when they are independent

```go
// Good:
tests := []struct {
    name    string
    input   string
    want    string
    wantErr bool
}{
    {"valid input", "valid", "result", false},
    {"invalid input", "invalid", "", true},
    {"empty input", "", "", true},
}

for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        got, err := Process(tt.input)
        if (err != nil) != tt.wantErr {
            t.Errorf("Process() error = %v, wantErr %v", err, tt.wantErr)
            return
        }
        if got != tt.want {
            t.Errorf("Process() = %v, want %v", got, tt.want)
        }
    })
}
```

### Subtests with t.Run()

- Use `t.Run()` for logical grouping of tests
- Subtest names should be descriptive and unique
- Each subtest should be independent

```go
// Good:
func TestService(t *testing.T) {
    t.Run("Charge operation", func(t *testing.T) {
        t.Run("success case", func(t *testing.T) { ... })
        t.Run("declined case", func(t *testing.T) { ... })
    })
    t.Run("Credit operation", func(t *testing.T) { ... })
}
```

## Test Doubles and Helpers

### Test Double Naming

- For simple cases, use short names (`Stub`, `Fake`, `Mock`, `Spy`)
- For multiple behaviors, name by behavior (`AlwaysCharges`, `AlwaysDeclines`)
- For multiple types, use type prefix (`StubService`, `StubStoredValue`)

```go
// Good:
type Stub struct{}
type AlwaysCharges struct{}
type AlwaysDeclines struct{}
type StubService struct{}
type SpyService struct{}

// Bad:
type StubServiceStub struct{}
type CreditCardServiceMock struct{}
```

### Test Helper Packages

- Mark test-only packages as `testonly` in build systems
- Use descriptive names for test helper types
- In tests, use prefixes for clarity when double juxtaposed with production types

```go
// Good:
var spyCC creditcardtest.Spy
var fakeDB creditcardtest.FakeDB

// Bad:
var cc creditcardtest.Spy  // unclear that this is a double
```

## Test Code Organization

### Location and Structure

- Use a `_test.go` filename and choose the package according to the required visibility
- Group related tests by source file or behavior, such as `source.go` -> `source_test.go`
- Use helper functions for repetitive setup/teardown

```go
// Good:
func setupTestService(t *testing.T) *Service {
    t.Helper()
    return &Service{...}
}

func TestService(t *testing.T) {
    svc := setupTestService(t)
    // tests...
}
```

### Test Context

- Use `t.Context()` as the base context inside tests and `b.Context()` inside benchmarks
- Derive scenario-specific contexts from the test context, for example `context.WithCancel(t.Context())`
- Do not use `context.Background()` or `context.TODO()` as the base context of a test or benchmark
- A test context is already canceled when `t.Cleanup` runs; derive cleanup contexts with
  `context.WithoutCancel(t.Context())`, wrapped in `context.WithCancel` or `context.WithTimeout` as needed
- `context.Background()` remains acceptable only where no test context exists (for example, a runnable `Example`)
  or where the behavior under test requires a genuinely independent context; document such cases

```go
// Good:
func TestService(t *testing.T) {
    ctx, cancel := context.WithCancel(t.Context())
    t.Cleanup(cancel)

    t.Cleanup(func() {
        // The test context is already canceled when cleanup runs.
        shutdownCtx, stop := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
        defer stop()
        _ = svc.Shutdown(shutdownCtx)
    })
    // ...
}

// Bad:
func TestService(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    // ...
}
```

### Test Independence and Parallelism

- Ensure tests are completely independent from each other
- All fully independent tests and subtests should run in parallel with `t.Parallel()` where possible
- Avoid shared state between tests
- Each test should be able to run in isolation

```go
// Good:
func TestService_Charge(t *testing.T) {
    t.Parallel()
    
    tests := []struct{
        name string
        // ...
    }{
        // test cases...
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            // test implementation...
        })
    }
}

// Bad:
func TestService(t *testing.T) {
    // shared state that affects multiple tests
    sharedService := &Service{}
    
    t.Run("test1", func(t *testing.T) {
        sharedService.DoSomething() // affects sharedService
    })
    
    t.Run("test2", func(t *testing.T) {
        sharedService.DoSomethingElse() // depends on previous test
    })
}
```

### Error Message Standards

- Include package name in error messages for black box tests
- Always follow "got ..., want ..." pattern where applicable
- Use %+v format for complex structures in error messages

```go
// Good (black box test):
t.Errorf("pkg.FunctionName(%q) = %v, want %v", input, got, want)

// Good (method call):
t.Errorf("val.Method() = %+v, want %+v", got, want)

// Good (field check):
t.Errorf("val.Field = %q, want %q", got.Field, want.Field)

// Bad:
t.Errorf("wrong result: got %v", got)
t.Errorf("method returned %v", got)
```

### Comparing Results

- Choose comparisons that express the behavior under test, rather than incidental implementation details
- Use direct comparisons or standard helpers for simple values
- Use a readable structural diff when comparing complex values, and label its direction correctly
- Use `errors.Is` for sentinel errors and `errors.As` for error types instead of comparing error messages
- Do not expose or compare private fields in a black box test solely to make an assertion easier

### Mock and Stub Reuse

- Avoid creating excessive mocks and stubs
- Reuse existing test doubles by grouping common functionality
- Create shared test helper packages for frequently used doubles
- Prefer simple stubs over complex mocks when possible

```go
// Good (shared test helper package):
// package servicetest
type CommonStub struct {
    // shared functionality
}

// In test files:
import "example.com/app/servicetest"

func TestService_A(t *testing.T) {
    stub := servicetest.NewCommonStub()
    // test A...
}

func TestService_B(t *testing.T) {
    stub := servicetest.NewCommonStub()
    // test B...
}

// Bad (duplicate mocks):
func TestService_A(t *testing.T) {
    mock := &ServiceMockA{ /* duplicate implementation */ }
    // test A...
}

func TestService_B(t *testing.T) {
    mock := &ServiceMockB{ /* similar duplicate implementation */ }
    // test B...
}

// Good (simple stub when possible):
type simpleStub struct{}
func (s *simpleStub) Method() string { return "fixed" }

// Bad (overly complex mock when simple stub suffices):
type complexMock struct {
    calls []string
    mu    sync.Mutex
    // unnecessary complexity for simple case
}
```

## Benchmarks and Examples

### Benchmarks

- Use `b.ResetTimer()` after setup
- Avoid allocations in benchmark loop
- Provide meaningful input sizes

```go
// Good:
func BenchmarkProcess(b *testing.B) {
    input := generateLargeInput()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        Process(input)
    }
}
```

### Examples

- Provide runnable examples for API
- Examples should compile and run
- Use `// Output:` comments for verification

```go
// Good:
func ExampleProcess() {
    result, err := Process("input")
    if err != nil {
        fmt.Println("Error:", err)
        return
    }
    fmt.Println(result)
    // Output: result
}
```

## General Principles

### Clarity and Simplicity

- Tests should be easy to read and understand
- Avoid unnecessary complexity in tests
- Focus on tested behavior, not implementation details

### Maintainability

- Tests should be easy to modify and extend
- Avoid brittle tests that break during refactoring
- Use descriptive names for maintainability

### Consistency

- Follow consistent naming patterns throughout the test suite
- Use consistent structure for similar tests
- Maintain consistent error handling patterns

## Prohibited Practices

### Avoid

- Redundant comments that restate the obvious
- Names that do not identify the tested behavior
- Hardcoded values without explanation
- Tests that depend on external state or timing
- Excessive complexity in test setup

```go
// Bad:
func TestSomething(t *testing.T) {
    // Test the function
    result := Something(1, 2, 3)
    if result != 6 {
        t.Error("wrong result")  // unclear message
    }
}

// Good:
func TestSomething_AddsThreeNumbers(t *testing.T) {
    const (
        a, b, c = 1, 2, 3
        want    = 6
    )
    got := Something(a, b, c)
    if got != want {
        t.Errorf("Something(%d, %d, %d) = %d, want %d", a, b, c, got, want)
    }
}
```

## References

- [Google Go Style Guide](https://google.github.io/styleguide/go/)
