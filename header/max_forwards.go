package header

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/util"
)

// MaxForwards represents the Max-Forwards header field.
// The Max-Forwards header field limits the number of proxies or gateways that can forward the request.
type MaxForwards uint

// CanonicName returns the canonical name of the header.
func (MaxForwards) CanonicName() Name { return "Max-Forwards" }

// CompactName returns the compact name of the header (Max-Forwards has no compact form).
func (MaxForwards) CompactName() Name { return "Max-Forwards" }

// RenderTo writes the header to the provided writer.
func (hdr MaxForwards) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	return errtrace.Wrap2(fmt.Fprint(w, hdr.CanonicName(), ": ", hdr.RenderValue()))
}

// Render returns the string representation of the header.
func (hdr MaxForwards) Render(opts *RenderOptions) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr MaxForwards) RenderValue() string { return strconv.FormatUint(uint64(hdr), 10) }

func (hdr MaxForwards) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr MaxForwards) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			hdr.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, hdr.String())
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(hdr.Render(nil)))
			return
		}
		fmt.Fprint(f, strconv.Quote(hdr.String()))
		return
	default:
		type hideMethods MaxForwards
		type MaxForwards hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), MaxForwards(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr MaxForwards) Clone() Header { return hdr }

// Equal compares this header with another for equality.
func (hdr MaxForwards) Equal(val any) bool {
	var other MaxForwards
	switch v := val.(type) {
	case MaxForwards:
		other = v
	case *MaxForwards:
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
func (MaxForwards) IsValid() bool { return true }

func (hdr MaxForwards) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *MaxForwards) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = 0
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(MaxForwards)
	if !ok {
		*hdr = 0
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromMaxForwardsNode(node *abnf.Node) MaxForwards {
	v, _ := strconv.ParseUint(node.Children[2].String(), 10, 8)
	return MaxForwards(v)
}
