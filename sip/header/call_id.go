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

// CallID represents the Call-ID header field.
// The Call-ID header field uniquely identifies a particular invitation or all registrations of a particular client.
type CallID string

// CanonicName returns the canonical name of the header.
func (CallID) CanonicName() Name { return "Call-ID" }

// CompactName returns the compact name of the header.
func (CallID) CompactName() Name { return "i" }

// RenderTo writes the header to the provided writer.
func (hdr CallID) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
	return errors.Wrap2(fmt.Fprint(w, hdr.name(opts), ": ", hdr.RenderValue()))
}

func (hdr CallID) name(opts *RenderOptions) Name {
	if opts != nil && opts.Compact {
		return hdr.CompactName()
	}
	return hdr.CanonicName()
}

// Render returns the string representation of the header.
func (hdr CallID) Render(opts *RenderOptions) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.RenderTo(sb, opts) //nolint:errcheck

	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr CallID) RenderValue() string { return string(hdr) }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr CallID) Format(f fmt.State, verb rune) {
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
		type (
			hideMethods CallID
			CallID      hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), CallID(hdr))

		return
	}
}

// Clone returns a copy of the header.
func (hdr CallID) Clone() Header { return hdr }

// Equal compares this header with another for equality.
func (hdr CallID) Equal(val any) bool {
	var other CallID
	switch v := val.(type) {
	case CallID:
		other = v
	case *CallID:
		if v == nil {
			return false
		}

		other = *v
	default:
		return false
	}

	return hdr == other
}

// IsValid checks whether the header is syntactically valid.
func (hdr CallID) IsValid() bool { return hdr != "" }

func (hdr CallID) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *CallID) UnmarshalJSON(data []byte) error {
	if hdr == nil {
		return errors.NewInvalidArgumentErrorWrap("nil header")
	}

	gh, err := FromJSON(data)
	if gh == nil {
		*hdr = ""
		return errors.Wrap(err)
	}

	h, ok := gh.(CallID)
	if !ok {
		*hdr = ""

		ah, ok := gh.(*Any)
		if ok && ah.CanonicName().Equal(hdr.CanonicName()) && len(ah.Value) == 0 {
			return nil
		}

		return newUnexpectHdrTypeErrWrap(gh)
	}

	*hdr = h

	return nil
}

func buildFromCallIDNode(node *abnf.Node) CallID {
	return CallID(grammar.MustGetNode(node, "callid").Value)
}
