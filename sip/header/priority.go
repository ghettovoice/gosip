package header

import (
	"fmt"
	"io"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
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
func (hdr Priority) RenderTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(fmt.Fprint(w, hdr.CanonicName(), ": ", hdr.RenderValue()))
}

// Render returns the string representation of the header.
func (hdr Priority) Render(opts ...RenderOptions) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	_, _ = hdr.RenderTo(sb, opts...)
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr Priority) RenderValue() string { return string(hdr) }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Priority) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			_, _ = hdr.RenderTo(f)
			return
		}
		fmt.Fprint(f, string(hdr))
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(hdr.Render()))
			return
		}
		fmt.Fprint(f, strconv.Quote(string(hdr)))
		return
	default:
		type (
			hideMethods Priority
			Priority    hideMethods
		)
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
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Priority) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		return errors.Wrap(err)
	}

	if gh == nil {
		*hdr = ""
		return nil
	}

	h, ok := gh.(Priority)
	if !ok {
		ah, ok := gh.(*Any)
		if ok && ah.CanonicName().Equal(hdr.CanonicName()) && len(ah.Value) == 0 {
			return nil
		}
		return errors.Wrap(newUnexpectHdrTypeErr(gh))
	}

	*hdr = h
	return nil
}

func buildFromPriorityNode(node *abnf.Node) Priority {
	return Priority(grammar.MustGetNode(node, "priority-value").Value)
}
