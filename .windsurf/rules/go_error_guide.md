---
trigger: always_on
---

# Error Style Guidelines

This document outlines the error handling conventions and style guidelines for the project.

All error handling should use the `github.com/ghettovoice/gosip/internal/errors` package,
which provides a complete replacement for the standard `errors` package with enhanced tracing capabilities
and helpers.

## 1. Function Argument Validation Errors

Function argument validation errors must be created
using `errors.NewInvalidArgumentError` (no wrap) or `errors.NewInvalidArgumentErrorWrap` (with trace wrap).

```go
// Good:
if len(address) == 0 {
    return nil, errors.NewInvalidArgumentErrorWrap("address cannot be empty")
}
```

## 2. Sentinel Error Declaration

Sentinel errors are defined as constants using `errors.Error` type or a local alias.

```go
// Good:
const ErrConnectionClosed errors.Error = "connection closed"
const ErrInvalidState errors.Error = "invalid state"

// Using local alias:
type Error = errors.Error
const ErrTimeout Error = "timeout"

// Using the built-in sentinel:
var ErrInvalidArgument = errors.ErrInvalidArgument
```

## 3. Sentinel Error Message Style

Sentinel error messages should be short and concise.

```go
// Good:
const ErrConnectionClosed errors.Error = "connection closed"
const ErrInvalidFormat errors.Error = "invalid format"

// Bad:
const ErrConnectionClosed errors.Error = "the connection has been closed unexpectedly"
const ErrInvalidFormat errors.Error = "the provided data format is invalid and cannot be processed"
```

## 4. Error Wrapping Messages

Error wrapping messages (when needed) should be short and focused on the action where the error occurred.

```go
// Good:
return errors.Errorf("resolve IP: %w", err)
return errors.Errorf("parse header: %w", err)

// Bad:
return errors.Errorf("failed to resolve IP address from hostname: %w", err)
return errors.Errorf("an error occurred while parsing the SIP header: %w", err)

// For simple wrapping without additional context:
return errors.Wrap(err)
```

## 5. Error Trace Wrapping

All returned errors (except error constructor functions) must be wrapped with `errors.Wrap/Wrap2/Wrap3` etc,
or helpers with `Wrap` suffix like `errors.ErrorWrap`, `errors.ErrorfWrap`, `errors.PrefixWrap` etc.
There's no need to add action prefixes to every error since this will be captured in the return trace.
Additionally, use `errors.Wrap` when passing errors as arguments to other functions or sending them through channels.

```go
// Good:
func processRequest(req *Request) (any, error) {
    data, err := parseData(req)
    if err != nil {
        return nil, errors.Wrap(err) // No need for "parse data:" prefix
    }
    
    // processData(any) (any, error)
    return errors.Wrap2(processData(data))
}

func handleRequest(req *Request) {
    err := processRequest(req)
    if err != nil {
        // Wrap error when passing to another function
        processError(errors.Wrap(err))
        return
    }
}

// Error constructor function exception, use errors.GetCaller() to skip constructor from trace:
func NewClientError(base error) error {
    return errors.GetCaller().Errorf("%w: error text", base)
}

// Using errors.PrefixWrap for sentinel-based prefixing:
func processData(data []byte) error {
    if len(data) == 0 {
        return errors.PrefixWrap(ErrInvalidData, "empty input")
    }
    // ...
}
```

## 6. Multiple Error Handling

Use `errors.Join` to combine multiple errors into a single error.
No need to additionally wrap joined error, join helpers will wrap by itself.

```go
// Good:
var errs []error

if err1 != nil {
    errs = append(errs, err1)
}
if err2 != nil {
    errs = append(errs, err2)
}

if len(errs) > 0 {
    return errors.Join(errs...)
}

// With prefix:
return errors.JoinPrefix("validation failed", errs...)

// Join with trace wrap:
return errors.JoinWrap(errs...)
return errors.JoinPrefixWrap("there are errors:", errs...)
```

## 7. Error Type Checking

Use the provided utility functions for common error type checks.

```go
// Good:
if errors.IsTimeoutErr(err) {
    // Handle timeout
}

if errors.IsTemporaryErr(err) {
    // Handle temporary error
}

if errors.IsGrammarErr(err) {
    // Handle grammar error
}

if errors.IsNetError(err) {
    // Handle network error
}

// Standard error checking:
if errors.Is(err, ErrInvalidArgument) {
    // Handle invalid argument
}

var targetErr *SomeErrorType
if errors.As(err, &targetErr) {
    // Handle specific error type
}

// Type assertion helper:
if specificErr, ok := errors.AsType[*SomeErrorType](err); ok {
    // Handle specific error type
}
```

## Examples

### Complete Error Handling Example

```go
package sip

import (
    "github.com/ghettovoice/gosip/internal/errors"
)

// Local error alias
type Error = errors.Error

// Sentinel errors
const (
    ErrInvalidMessage  Error = "invalid message"
    ErrConnectionLost  Error = "connection lost"
    ErrTimeout         Error = "timeout"
)


func ProcessMessage(msg string) (any, error) {
    if len(msg) == 0 {
        return nil, errors.NewInvalidArgumentErrorWrap("message cannot be empty")
    }
    
    if !isValidFormat(msg) {
        return nil, errors.PrefixWrap(ErrInvalidMessage, "format validation failed")
    }
    
    result, err := parseMessage(msg)
    if err != nil {
        return nil, errors.Wrap(err) // No error prefix needed
    }
    
    return errors.Wrap2(handleResult(result))
}

func parseMessage(msg string) (*Message, error) {
    // Parse logic...
    if parsingError {
        return nil, errors.ErrorfWrap("parse message: %w", ErrInvalidFormat)
    }
    return &Message{}, nil
}

func handleResult(result *Message) (any, error) {
    res, err := sendToNetwork(result)
    if err != nil {
        return nil, errors.Wrap(err)
    }
    return res, nil
}

// Example with error prefixing
func validateInput(data []byte) error {
    var errs []error
    
    if len(data) == 0 {
        errs = append(errs, errors.Prefix(ErrInvalidData, "empty input")) // no trace wrap
    }
    if len(data) > MaxSize {
        errs = append(errs, errors.Prefix(ErrInvalidData, "size exceeded")) // no trace wrap
    }
    
    if len(errs) > 0 {
        return errors.JoinPrefixWrap("validation failed", errs...) // join with final error trace wrap
    }
    return nil
}
```

## Best Practices Summary

- Use `errors.NewInvalidArgumentError` for argument validation
- Define sentinel errors as constants with `errors.Error` type
- Keep sentinel error messages short and concise
- Use brief, action-focused wrapping messages when needed
- Use `errors.Prefix` for sentinel-based error prefixing (no need to wrap)
- Use `errors.Join`, `errors.JoinPrefix` for combining multiple errors (no need to wrap)
- Always wrap returned errors with `errors.Wrap*` (except error constructors),
  passing errors to other functions or channels
- Use `errors.*Wrap` helpers where appropriate to simplify error wrapping chain
- Use utility functions for common error type checks
- Avoid redundant action prefixes due to trace context
- The `errors` package is a complete replacement for std `errors` and `errtrace` packages
