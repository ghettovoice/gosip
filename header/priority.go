package header

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

// Priority represents the Priority header field.
// The Priority header field indicates the urgency of the request as perceived by the client.
type Priority string

// CanonicName returns the canonical name of the header.
func (Priority) CanonicName() Name { return "Priority" }

// CompactName returns the compact name of the header (Priority has no compact form).
func (Priority) CompactName() Name { return "Priority" }

// RenderTo writes the header to the provided writer.
func (hdr Priority) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	return errtrace.Wrap2(fmt.Fprint(w, hdr.CanonicName(), ": ", hdr.RenderValue()))
}

// Render returns the string representation of the header.
func (hdr Priority) Render(opts *RenderOptions) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr Priority) RenderValue() string { return string(hdr) }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Priority) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			hdr.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, string(hdr))
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(hdr.Render(nil)))
			return
		}
		fmt.Fprint(f, strconv.Quote(string(hdr)))
		return
	default:
		type hideMethods Priority
		type Priority hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), Priority(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr Priority) Clone() Header { return hdr }

// Equal compares this header with another for equality.
func (hdr Priority) Equal(val any) bool {
	var other Priority
	switch v := val.(type) {
	case Priority:
		other = v
	case *Priority:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return util.EqFold(hdr, other)
}

// IsValid checks whether the header is syntactically valid.
func (hdr Priority) IsValid() bool { return grammar.IsToken(string(hdr)) }

func (hdr Priority) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *Priority) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = ""
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(Priority)
	if !ok {
		*hdr = ""
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromPriorityNode(node *abnf.Node) Priority {
	return Priority(grammar.MustGetNode(node, "priority-value").Value)
}
