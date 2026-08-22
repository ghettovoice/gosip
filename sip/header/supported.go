package header

import (
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

// Supported represents the Supported header field.
// The Supported header field enumerates all the extensions supported by the UAC or UAS.
type Supported Require

// CanonicName returns the canonical name of the header.
func (Supported) CanonicName() Name { return "Supported" }

// CompactName returns the compact name of the header.
func (Supported) CompactName() Name { return "k" }

// RenderTo writes the header to the provided writer.
func (hdr Supported) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(hdr.name(opts...), ": ")
	cw.Call(Require(hdr).renderValueTo)
	return errors.Wrap2(cw.Result())
}

func (hdr Supported) name(opts ...RenderOptions) Name {
	if util.LastSliceElemOr(opts, RenderOptions{}).Compact {
		return hdr.CompactName()
	}
	return hdr.CanonicName()
}

// Render returns the string representation of the header.
func (hdr Supported) Render(opts ...RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	_, _ = hdr.RenderTo(sb, opts...)
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr Supported) RenderValue() string {
	return Require(hdr).RenderValue()
}

// String returns the string representation of the header value.
func (hdr Supported) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Supported) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			_, _ = hdr.RenderTo(f)
			return
		}
		fmt.Fprint(f, hdr.String())
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(hdr.Render()))
			return
		}
		fmt.Fprint(f, strconv.Quote(hdr.String()))
		return
	default:
		type (
			hideMethods Supported
			Supported   hideMethods
		)
		fmt.Fprintf(f, fmt.FormatString(f, verb), Supported(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr Supported) Clone() Header {
	hdr2, ok := Require(hdr).Clone().(Require)
	if !ok {
		return nil
	}
	return Supported(hdr2)
}

// Equal compares this header with another for equality.
func (hdr Supported) Equal(val any) bool {
	var other Supported
	switch v := val.(type) {
	case Supported:
		other = v
	case *Supported:
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
func (hdr Supported) IsValid() bool {
	return hdr != nil && !slices.ContainsFunc(hdr, func(o OptionTag) bool { return !o.IsValid() })
}

func (hdr Supported) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Supported) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		return errors.Wrap(err)
	}

	if gh == nil {
		*hdr = nil
		return nil
	}

	h, ok := gh.(Supported)
	if !ok {
		return errors.Wrap(newUnexpectHdrTypeErr(gh))
	}

	*hdr = h
	return nil
}

func buildFromSupportedNode(node *abnf.Node) Supported {
	return Supported(buildFromRequireNode(node))
}
