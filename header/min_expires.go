package header

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

// MinExpires represents the Min-Expires header field.
// The Min-Expires header field conveys the minimum refresh interval supported for soft-state elements.
type MinExpires Expires

// CanonicName returns the canonical name of the header.
func (*MinExpires) CanonicName() Name { return "Min-Expires" }

// CompactName returns the compact name of the header (Min-Expires has no compact form).
func (*MinExpires) CompactName() Name { return "Min-Expires" }

// RenderTo writes the header to the provided writer.
func (hdr *MinExpires) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.NewCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call((*Expires)(hdr).renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

// Render returns the string representation of the header.
func (hdr *MinExpires) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr *MinExpires) RenderValue() string {
	return (*Expires)(hdr).RenderValue()
}

func (hdr *MinExpires) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr *MinExpires) Format(f fmt.State, verb rune) {
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
		type hideMethods MinExpires
		type MinExpires hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*MinExpires)(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr *MinExpires) Clone() Header {
	hdr2, ok := (*Expires)(hdr).Clone().(*Expires)
	if !ok {
		return nil
	}
	return (*MinExpires)(hdr2)
}

// Equal compares this header with another for equality.
func (hdr *MinExpires) Equal(val any) bool {
	var other *MinExpires
	switch v := val.(type) {
	case MinExpires:
		other = &v
	case *MinExpires:
		other = v
	default:
		return false
	}
	return (*Expires)(hdr).Equal((*Expires)(other))
}

// IsValid checks whether the header is syntactically valid.
func (hdr *MinExpires) IsValid() bool {
	return (*Expires)(hdr).IsValid()
}

func (hdr *MinExpires) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

var zeroMinExpires MinExpires

func (hdr *MinExpires) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = zeroMinExpires
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(*MinExpires)
	if !ok {
		*hdr = zeroMinExpires
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, hdr))
	}

	*hdr = *h
	return nil
}

func buildFromMinExpiresNode(node *abnf.Node) *MinExpires {
	sec, _ := strconv.ParseUint(grammar.MustGetNode(node, "delta-seconds").String(), 10, 64)
	return &MinExpires{time.Duration(sec) * time.Second}
}
