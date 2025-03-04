package header

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

// Unsupported represents the Unsupported header field.
// The Unsupported header field lists the features not supported by the UAS.
type Unsupported Require

// CanonicName returns the canonical name of the header.
func (Unsupported) CanonicName() Name { return "Unsupported" }

// CompactName returns the compact name of the header (Unsupported has no compact form).
func (Unsupported) CompactName() Name { return "Unsupported" }

// RenderTo writes the header to the provided writer.
func (hdr Unsupported) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(Require(hdr).renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

// Render returns the string representation of the header.
func (hdr Unsupported) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr Unsupported) RenderValue() string {
	return Require(hdr).RenderValue()
}

// String returns the string representation of the header value.
func (hdr Unsupported) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Unsupported) Format(f fmt.State, verb rune) {
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
		type hideMethods Unsupported
		type Unsupported hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), Unsupported(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr Unsupported) Clone() Header {
	hdr2, ok := Require(hdr).Clone().(Require)
	if !ok {
		return nil
	}
	return Unsupported(hdr2)
}

// Equal compares this header with another for equality.
func (hdr Unsupported) Equal(val any) bool {
	var other Unsupported
	switch v := val.(type) {
	case Unsupported:
		other = v
	case *Unsupported:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return Require(hdr).Equal(Require(other))
}

// IsValid checks whether the header is syntactically valid.
func (hdr Unsupported) IsValid() bool { return Require(hdr).IsValid() }

func (hdr Unsupported) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *Unsupported) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = nil
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(Unsupported)
	if !ok {
		*hdr = nil
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromUnsupportedNode(node *abnf.Node) Unsupported {
	return Unsupported(buildFromRequireNode(node))
}
