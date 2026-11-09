---
trigger: glob
globs:
  - "**/*.go"
---

# Go Error Handling Guide

General Go error handling guidelines. Examples use the standard library `errors` and `fmt` packages.

## 1. Sentinel Error Declaration

Declare reusable sentinel errors once with `errors.New`, and name them with the `Err` prefix.
Use sentinels for conditions callers need to recognize, not for every error message.

```go
// Good:
var (
    ErrConnClosed = errors.New("connection closed")
    ErrInvalidState     = errors.New("invalid state")
)
```

## 2. Sentinel Error Message Style

Sentinel error messages should be short and concise.

```go
// Good:
var ErrInvalidFormat = errors.New("invalid format")

// Bad:
var ErrUnexpectedFailure = errors.New("an unexpected error has occurred during processing")
```

## 3. Error Wrapping Messages

Error wrapping messages (when needed) should be short and focused on the action where the error occurred.

```go
// Good:
return fmt.Errorf("resolve address: %w", err)
return fmt.Errorf("parse record: %w", err)

// Bad:
return fmt.Errorf("an error occurred while parsing the record: %w", err)
```

## 4. Propagation and Context

- Handle errors explicitly and return them as the final result
- Add context when it helps callers understand the failed operation
- Preserve an underlying cause with `fmt.Errorf` and `%w` when callers should be able to inspect it
- Return an error unchanged when the caller already has sufficient context; wrapping every return is not required
- Do not wrap nil errors, and return a nil error interface for success rather than a typed nil pointer
- Error tracing is a separate tooling choice, not a standard Go requirement
- Handle an error by returning it or recording it at the appropriate boundary; avoid redundant logging at every layer
- Validate inputs before performing side effects, and include only safe, relevant details in error messages

## 5. Multiple Error Handling

Use standard `errors.Join` when several errors must be reported without discarding their identities.
It ignores nil errors and returns nil when all inputs are nil; it does not add a stack trace.

```go
// Good:
var errs []error

if err1 != nil {
    errs = append(errs, err1)
}
if err2 != nil {
    errs = append(errs, err2)
}

return errors.Join(errs...)
```

When additional context is useful, wrap only a non-nil combined error:

```go
if err := errors.Join(errs...); err != nil {
    return fmt.Errorf("validate input: %w", err)
}
return nil
```

## 6. Error Type Checking

Use `errors.Is` for sentinel errors and `errors.As` for error types, including wrapped or joined errors.
Do not use message comparisons or direct type assertions when an underlying error may be wrapped.

```go
if errors.Is(err, ErrInvalidFormat) {
    // Handle invalid input.
}

var targetErr *FieldError
if errors.As(err, &targetErr) {
    // Handle specific error type
}
```

## 7. Typed Errors

Use a custom error type when callers need structured details.
Implement `Unwrap() error` when the type retains an underlying cause.

```go
type FieldError struct {
    Field string
    Err   error
}

func (e *FieldError) Error() string {
    return fmt.Sprintf("field %s: %v", e.Field, e.Err)
}

func (e *FieldError) Unwrap() error { return e.Err }

func validateCount(count int) error {
    if count < 0 {
        return &FieldError{Field: "count", Err: ErrInvalidFormat}
    }
    return nil
}
```

## 8. Messages and Public Contracts

- Keep messages concise, lowercase, and free of trailing punctuation unless a proper noun requires otherwise
- Prefer action-focused context such as `open file` or `decode record`; avoid redundant `failed to` prefixes
- Do not include secrets or unnecessary internal implementation details
- Treat exposed sentinels and error types as part of the API; document conditions callers can rely on
- Preserve error identity when translating between layers, unless intentionally hiding an implementation detail
- Use panics for unrecoverable programming errors, not ordinary invalid input or operational failures
- Check the supported Go version before using newer error APIs
