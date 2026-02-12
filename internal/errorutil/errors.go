package errorutil

//go:generate errtrace -w .

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ghettovoice/gosip/internal/util"
)

// Error is a string type that implements the error interface.
type Error string

func (s Error) Error() string { return string(s) }

func Errorf(format string, args ...any) error {
	return Error(fmt.Sprintf(format, args...)) //errtrace:skip
}

// NewWrapperError creates or wraps an error with a sentinel error.
// It supports multiple argument patterns:
//   - No args: returns sentinel
//   - error arg: wraps with sentinel (unless already wrapped)
//   - string arg: formats as message with sentinel
//   - string + args: formats with Sprintf then wraps with sentinel
func NewWrapperError(sentinel error, args ...any) error {
	if len(args) == 0 {
		return sentinel //errtrace:skip
	}
	switch v := args[0].(type) {
	case error:
		if errors.Is(v, sentinel) {
			return v //errtrace:skip
		}
		return fmt.Errorf("%w: %w", sentinel, v) //errtrace:skip
	case string:
		if len(args) == 1 {
			return fmt.Errorf("%w: %s", sentinel, v) //errtrace:skip
		}
		return fmt.Errorf("%w: %s", sentinel, fmt.Sprintf(v, args[1:]...)) //errtrace:skip
	default:
		return sentinel //errtrace:skip
	}
}

// ErrInvalidArgument is an error returned when an invalid argument is provided.
const ErrInvalidArgument Error = "invalid argument"

// NewInvalidArgumentError creates a new error with [ErrInvalidArgument] or
// wraps provided error with [ErrInvalidArgument].
func NewInvalidArgumentError(args ...any) error {
	return NewWrapperError(ErrInvalidArgument, args...) //errtrace:skip
}

func Join(errs ...error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0] //errtrace:skip
	}
	return &multiError{errs: errs} //errtrace:skip
}

func JoinPrefix(prefix string, errs ...error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return fmt.Errorf("%s: %w", strings.TrimRight(prefix, ":"), errs[0]) //errtrace:skip
	}
	return &multiError{prefix: prefix, errs: errs} //errtrace:skip
}

type multiError struct {
	prefix string
	errs   []error
}

func (e *multiError) Error() string {
	if len(e.errs) == 0 {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	sb.WriteString(e.prefix)
	e.writeErrors(sb, "")

	return sb.String()
}

func (e *multiError) writeErrors(sb *strings.Builder, indent string) {
	for _, err := range e.errs {
		if err == nil {
			continue
		}

		sb.WriteString("\n")
		sb.WriteString(indent)
		sb.WriteString("  - ")

		if nested, ok := err.(*multiError); ok { //nolint:errorlint
			label := nested.prefix
			if label == "" {
				label = "multiple errors"
			}
			sb.WriteString(label)
			nested.writeErrors(sb, indent+"  ")
			continue
		}

		msg := err.Error()
		if strings.Contains(msg, "\n") {
			msg = indentMultiline(msg, indent+"    ")
		}
		sb.WriteString(msg)
	}
}

func indentMultiline(text, indent string) string {
	if text == "" {
		return text
	}
	return strings.ReplaceAll(text, "\n", "\n"+indent)
}

func (e *multiError) Unwrap() []error { return e.errs }
