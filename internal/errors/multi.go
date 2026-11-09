package errors

import (
	"fmt"
	"strings"

	"github.com/ghettovoice/gosip/internal/util"
)

func Join(errs ...error) error {
	errs = filterNilErrs(errs)
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0] //errtrace:skip
	}
	return &multiError{errs: errs} //errtrace:skip
}

func JoinPrefix(prefix string, errs ...error) error {
	errs = filterNilErrs(errs)
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return fmt.Errorf("%s %w", prefix, errs[0]) //errtrace:skip
	}
	return &multiError{prefix: prefix, errs: errs} //errtrace:skip
}

func filterNilErrs(errs []error) []error {
	filtered := errs[:0]
	for _, err := range errs {
		if err != nil {
			filtered = append(filtered, err)
		}
	}
	return filtered
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
	e.writeErrs(sb, "")
	return sb.String()
}

func (e *multiError) writeErrs(sb *strings.Builder, indent string) {
	for _, err := range e.errs {
		if err == nil {
			continue
		}

		sb.WriteString("\n")
		sb.WriteString(indent)
		sb.WriteString("  - ")
		//nolint:errorlint // multiError is an internal type produced only by Join
		if nested, ok := err.(*multiError); ok {
			label := nested.prefix
			if label == "" {
				label = "multiple errors"
			}
			sb.WriteString(label)
			nested.writeErrs(sb, indent+"  ")
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
