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

// Expires represents the Expires header field.
// The Expires header field gives the relative time after which the message (or content) expires.
type Expires struct {
	time.Duration
}

// CanonicName returns the canonical name of the header.
func (*Expires) CanonicName() Name { return "Expires" }

// CompactName returns the compact name of the header (Expires has no compact form).
func (*Expires) CompactName() Name { return "Expires" }

// RenderToOptions writes the header to the provided writer.
func (hdr *Expires) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.NewCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

func (hdr *Expires) renderValueTo(w io.Writer) (num int, err error) {
	return errtrace.Wrap2(fmt.Fprint(w, int64(hdr.Duration.Seconds())))
}

// RenderOptions returns the string representation of the header.
func (hdr *Expires) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr *Expires) RenderValue() string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb) //nolint:errcheck
	return sb.String()
}

func (hdr *Expires) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr *Expires) Format(f fmt.State, verb rune) {
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
		type hideMethods Expires
		type Expires hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*Expires)(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr *Expires) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := *hdr
	return &hdr2
}

// Equal compares this header with another for equality.
func (hdr *Expires) Equal(val any) bool {
	var other *Expires
	switch v := val.(type) {
	case Expires:
		other = &v
	case *Expires:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return hdr.Duration == other.Duration
}

// IsValid checks whether the header is syntactically valid.
func (hdr *Expires) IsValid() bool { return hdr != nil }

func (hdr *Expires) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

var zeroExpires Expires

func (hdr *Expires) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = zeroExpires
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(*Expires)
	if !ok {
		*hdr = zeroExpires
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, hdr))
	}

	*hdr = *h
	return nil
}

func buildFromExpiresNode(node *abnf.Node) *Expires {
	sec, _ := strconv.ParseUint(grammar.MustGetNode(node, "delta-seconds").String(), 10, 64)
	return &Expires{time.Duration(sec) * time.Second}
}
