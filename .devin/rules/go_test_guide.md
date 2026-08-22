---
trigger: always_on
---
# Go Testing Style Guide

Based on Google Go Style Guide: <https://google.github.io/styleguide/go/>

## Test Naming

### Test Functions

- Use underscores in test function names only in `*_test.go` files
- Test names should describe the test scenario
- Use `Test[FunctionName]_[Scenario]` format for unit tests
- For black box tests, use package with `_test` suffix (preferred tests)

```go
// Good:
func TestCharge_Success(t *testing.T) { ... }
func TestCharge_Declined(t *testing.T) { ... }
func TestService_Process_ValidCard(t *testing.T) { ... }

// Bad:
func TestCharge(t *testing.T) { ... } // too generic
func testChargeSuccess(t *testing.T) { ... } // not exported
```

### Test Packages

- For black box tests, use package with `_test` suffix
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

- Tests should be in the same package as the code under test (or in `_test` package)
- Group related tests together and by source file (`source_file.go` → `source_file_test.go`)
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

### Comparison with go-cmp

- Use `cmp.Diff` from `github.com/google/go-cmp` for comparing complex structures
- Include diff in error messages when using cmp.Diff
- Use `cmpopts.EquateErrors()` for error comparisons
- Use `cmp.AllowUnexported` for comparing structs with unexported fields

```go
// Good (complex structures):
import "github.com/google/go-cmp/cmp"

if diff := cmp.Diff(want, got); diff != "" {
    t.Errorf("val.Method() mismatch (-want +got):\n%s", diff)
}

// Good (with error message pattern):
if diff := cmp.Diff(want, got); diff != "" {
    t.Errorf("val.Method() = %+v, want %+v\ndiff (-got +want):\n%v", got, want, diff)
}

// Good (error comparisons):
import "github.com/google/go-cmp/cmp/cmpopts"

if diff := cmp.Diff(wantErr, gotErr, cmpopts.EquateErrors()); diff != "" {
    t.Errorf("Function() error mismatch (-want +got):\n%s", diff)
}

// Good (unexported fields):
type unexportedFields struct{}

if diff := cmp.Diff(want, got, cmp.AllowUnexported(unexportedFields{})); diff != "" {
    t.Errorf("Struct mismatch (-want +got):\n%s", diff)
}

// Bad (manual field comparison):
if got.Field1 != want.Field1 || got.Field2 != want.Field2 {
    t.Errorf("struct mismatch")
}
```

### Mock and Stub Reuse

- Avoid creating excessive mocks and stubs
- Reuse existing test doubles by grouping common functionality
- Create shared test helper packages for frequently used doubles
- Prefer simple stubs over complex mocks when possible

```go
// Good (shared test helper package):
// package testhelpers
type CommonStub struct {
    // shared functionality
}

// In test files:
import "path/to/project/testhelpers"

func TestService_A(t *testing.T) {
    stub := testhelpers.NewCommonStub()
    // test A...
}

func TestService_B(t *testing.T) {
    stub := testhelpers.NewCommonStub()
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

- Follow consistent naming patterns in the project
- Use consistent structure for similar tests
- Maintain consistent error handling patterns

## Prohibited Practices

### Avoid

- Redundant comments that restate the obvious
- Too generic test names (`TestFunc`, `TestMethod`)
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
