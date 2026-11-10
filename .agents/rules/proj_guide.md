---
trigger: glob
globs:
  - "**/*.go"
---
# Project-Specific Guidelines

These are the gosip-specific additions to the general Go guides.
For this repository, these rules take precedence over conflicting generic examples,
including the standard-library examples in [go_error_guide.md](go_error_guide.md).

## General Guides

- [Go base style](go_base_guide.md)
- [Go testing](go_test_guide.md)
- [Go error handling](go_error_guide.md)
- [Commit messages](commit_message.md)

## Naming Conventions

- Use full words in exported names unless an abbreviation is established in Go or the protocol terminology
- Accepted abbreviations include `Addr`, `Conn`, `Std`, `Proto`, `Ver`, `Recv`, `Auth`, `Stats`, `Params`,
  `Elem`, and `Seq`; do not expand them merely because a name is exported
- Preserve standard initialisms such as `ID`, `URL`, `URI`, `IP`, `DNS`, `SIP`, `TCP`, `UDP`, `TLS`,
  `SCTP`, `WS`, `WSS`, `SRV`, `NAPTR`, `MIME`, `ABNF`, `TTL`, `RTT`, and `EOF`
- Preserve protocol names and parameters such as `CSeq`, `CallID`, `MAddr`, `RPort`, `LR`, `QOP`,
  `CNonce`, `RspAuth`, `AuthzServer`, `MaxForwards`, and `MinExpires`
- Keep RFC timer names such as `T1`, `T2`, `T4`, and `TimerA`
- Keep meaningful constructor subjects: `transport.NewConnOrientedTransport` distinguishes
  the created transport from a listener or connection
- Prefer short private names when clear locally, and descriptive names for fields and helpers with broader scope

## SIP Protocol Code

- Follow SIP RFC specifications strictly
- Use appropriate SIP terminology
- Handle protocol edge cases
- Maintain protocol compatibility
- Preserve SIP header names, parameter names, JSON tags, and serialized formats during naming-only refactors

## Network Code

- Handle network errors gracefully
- Use appropriate timeouts
- Implement proper connection management
- Consider security implications

## Testing Infrastructure

- Follow the general [Go testing guide](go_test_guide.md), including its flexible naming patterns
- Use `<source>_test.go` for black box tests in the external `<package>_test` package
- Use `<source>_internal_test.go` for white box tests in the package under test
- These filename conventions do not change Go visibility rules; the package declaration controls access
- Maintain reusable test utilities in separate packages under `internal/testutil`, including `dnstest` and `siptest`
- Keep tests fast and reliable
- Use table-driven tests for multiple scenarios

### Structural Comparisons

- Use `cmp.Diff` from `github.com/google/go-cmp/cmp` for complex structural comparisons
- Prefer public behavior in black box tests; use `cmp.AllowUnexported` only where white box access is appropriate
- Use `errors.Is` or `errors.As` for focused error assertions; `cmpopts.EquateErrors()` is available
  when error matching is part of a larger structural comparison
- Label diffs according to argument order

```go
if diff := cmp.Diff(want, got); diff != "" {
    t.Errorf("result mismatch (-want +got):\n%s", diff)
}
```

## Error Handling

### Package and Sentinels

- Use `github.com/ghettovoice/gosip/internal/errors` for error construction, inspection, and trace wrapping,
  rather than importing standard `errors` or `braces.dev/errtrace` directly in application code
- This package supplies replacements for standard error operations and gosip-specific helpers
- Error adapters and classification helpers may use the underlying standard-library APIs they implement or inspect
- Unlike the generic standard-library examples, declare sentinels as constants of type `errors.Error`,
  or use a local alias
- Keep sentinel messages concise and lowercase

```go
import "github.com/ghettovoice/gosip/internal/errors"

type Error = errors.Error

const (
    ErrInvalidMessage Error = "invalid message"
    ErrConnLost Error = "connection lost"
)
```

### Return Traces and Construction

- Wrap returned errors with `errors.Wrap`, `Wrap2`, or `Wrap3`, or a helper ending in `Wrap`
- Wrap errors when passing them to handlers or sending them through channels
- Use `errors.ErrorWrap`, `errors.ErrorfWrap`, or `errors.PrefixWrap` when constructing and returning an error
- Prefer the condition's classified constructor over directly prefixing its sentinel when such a constructor exists
- `errors.Prefix` adds a sentinel-based prefix without adding a trace frame;
  use `errors.PrefixWrap` when returning that constructed error
- Error constructor functions are the exception to adding another return wrapper:
  use `errors.GetCaller()` and its construction methods to control the recorded caller
- Return traces supply propagation context; do not repeat an action prefix at every layer
- When an action prefix is useful, keep it brief, such as `resolve IP` or `parse header`

The following examples are written for package `sip`:

```go
func validatePayload(data []byte) error {
    if len(data) == 0 {
        return errors.Wrap(NewInvalidMessageError("empty input"))
    }
    return nil
}

func processRequest(req *Request) (any, error) {
    data, err := parseData(req)
    if err != nil {
        return nil, errors.Wrap(err)
    }
    return errors.Wrap2(processData(data))
}

func NewEmptyMessageError() error {
    return errclass.Classify(
        errors.GetCaller().Prefix(ErrInvalidMessage, "empty input"),
        ErrClassMessage,
    )
}
```

### Multiple Errors

- Collect independent errors instead of discarding all but the last one
- `errors.Join` and `errors.JoinPrefix` preserve their inputs but do not add a return trace
- Use `errors.JoinWrap` or `errors.JoinPrefixWrap` when returning a combined error
- A `Wrap`-suffixed join helper already adds the trace; do not wrap its result again

```go
return errors.JoinPrefixWrap("validation failed:", errs...)
```

### Error Inspection

- Use `errors.Is`, `errors.As`, or `errors.AsType` to inspect wrapped errors
- Use `github.com/ghettovoice/gosip/pkg/errclass` for behavioral or class-based checks:
  `IsTimeout`, `IsTemporary`, `IsGrammar`, `IsNetwork`, `IsClosed`, `IsCanceled`,
  `IsDeadline`, `IsNotFound`, and `IsConflict`
- These classification helpers belong to `errclass`, not to `internal/errors`
- Do not compare message strings or bypass the error chain with a direct type assertion

### Error Classification

- Use classification when callers need to distinguish broad error categories in addition to a specific sentinel
- Prefer existing `New...Error` constructors that already supply the condition's sentinel and applicable classes
- For a new error condition, use `errclass.Classify(base, class, otherClasses...)` where the classification is known
- Classes are independent `errclass.Class` values, not bit flags; pass multiple classes as arguments, not with `|`
- Keep the underlying cause: classification preserves it through `Unwrap` and does not replace its identity
- Classification does not add a return trace; keep the trace wrapping rules when returning classified errors
- Do not reclassify an error at every propagation point, or infer its classes from message text

Use the common classes according to their actual meaning:

| Class | Meaning |
| --- | --- |
| `errclass.ClassClosed` | A closed resource or component |
| `errclass.ClassTimeout` | An operation timed out |
| `errclass.ClassTemporary` | A failure known to be temporary; this is not implied by every timeout |
| `errclass.ClassCanceled` | An operation was canceled |
| `errclass.ClassNotFound` | A requested object or resource was not found |
| `errclass.ClassConflict` | A conflict such as duplicate registration |

Add the appropriate SIP domain class where applicable: `sip.ErrClassMessage`, `sip.ErrClassTransport`,
`sip.ErrClassTransaction`, `sip.ErrClassElement`, `sip.ErrClassParser`, or `proxy.ErrClass`
(the proxy domain class lives in package `github.com/ghettovoice/gosip/sip/proxy`).
Domain classes and common classes describe different aspects of the same error.

For example, inside package `sip`, a newly implemented not-found boundary can construct:

```go
return errors.Wrap(errclass.Classify(
    errors.Prefix(ErrTransactionNotFound, "key %q", key),
    ErrClassTransaction,
    errclass.ClassNotFound,
))
```

For this existing condition, prefer its constructor instead:

```go
return errors.Wrap(NewTransactionNotFoundError("key %q", key))
```

Callers can distinguish the exact condition, the domain, and the common category independently.
In this example all three predicates match:

```go
err := errors.Wrap(sip.NewTransactionNotFoundError("key %q", "tx-1"))

if errors.Is(err, sip.ErrTransactionNotFound) {
    // Handle this specific missing-transaction condition.
}
if errors.Is(err, sip.ErrClassTransaction) {
    // Handle the broader transaction error category.
}
if errclass.IsNotFound(err) {
    // Handle missing resources regardless of their domain.
}
```

- Use domain predicates such as `sip.IsTransactionError` or `sip.IsTransportError` when their semantics fit
- Behavioral helpers also recognize underlying causes, not just explicit class tags:
  for example, `errclass.IsCanceled` recognizes `context.Canceled`, and `IsDeadline` recognizes
  `context.DeadlineExceeded` and `os.ErrDeadlineExceeded`
- A timeout class alone does not imply a deadline sentinel or temporary failure
- `IsGrammar` checks the `Grammar() bool` behavior; a parser-domain error is not necessarily a grammar error
- `IsNetwork` recognizes an underlying `*net.OpError`; it is not a synonym for the transport domain class
- These inspections work through wrapped and joined errors; avoid relying only on the outermost error type
- When adding or changing an error constructor, test the specific sentinel, domain, and applicable common classes,
  including inspection after wrapping or joining

## Project Tools

Run tools from the repository root. `Taskfile.yml` is the source of truth for task commands and defaults;
use the Task entry points or their direct equivalents.

| Command | Purpose |
| --- | --- |
| `task test` | All tests with race detection, `-vet=all`, a default `1m` timeout per package, and atomic coverage in `cover.out` |
| `task lint` | Code style, static analysis, and configured formatter checks through golangci-lint |
| `task vuln` | Vulnerability analysis through govulncheck |
| `task check` | Run `test`, `lint`, and `vuln` in sequence |

Direct equivalents with the current defaults:

```bash
go test -race -vet=all -timeout=1m -covermode=atomic -coverprofile=cover.out ./...
golangci-lint run ./...
govulncheck ./...
```

- `PACKAGE` selects a package relative to the module root and defaults to `...`
- The test task also accepts `TIMEOUT` and `COVERAGE_FILE`
- The `test`, `lint`, and `vuln` tasks forward arguments after `--` through `CLI_ARGS`;
  choose flags supported by the selected tool

```bash
task test PACKAGE=sip TIMEOUT=2m COVERAGE_FILE=sip-cover.out -- -run TestTransaction
task lint PACKAGE=sip/transport
```

Tasks use source-based up-to-date checks. When a fresh verification is required, especially after changing
lint configuration without Go source changes, use `task --force lint` or run the tool directly;
do not count a skipped task as a fresh successful check.

## Formatting and Verification

- Use `task lint` or `golangci-lint run ./...` to verify code style
- `.golangci.yml` is authoritative for lint and formatting rules, including formatter options and exclusions
- Check formatting in the context of golangci-lint's configured `gofumpt` and `gci` formatters;
  a bare `gofmt` run or editor auto-formatting alone is not sufficient evidence of compliance
- `gci` groups imports as standard library, third-party, and local module imports, in that order
- Apply formatting with `golangci-lint fmt`, which uses the configured formatters and their options,
  then rerun the lint check
- Standalone `gofumpt` or `gci` runs must match the repository configuration; do not assume their defaults are equivalent
- Use the Go version declared in `go.mod` and test against the minimum supported toolchain where feasible
- Run relevant tests for the changed behavior and update documentation with code changes
- After changing Go code, tests, lint configuration, or documentation code examples, run:

```bash
task lint
# Or directly:
golangci-lint run ./...
```

- Fix every lint issue in added or modified code and avoid introducing new issues
- Rerun the linter after fixes; require a successful exit with zero reported issues
- Apply these checks to new implementations, modified functions, tests, configuration changes,
  and code examples as applicable
- For documentation changes, also check formatting, references, and example consistency
- Do not change lint configuration merely to accommodate a code change
- Suppress a rule only when necessary, after considering alternatives;
  use a specific `//nolint:<linter>` with a clear justification
- Review any `go fix` changes and avoid unrelated automatic rewrites

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
