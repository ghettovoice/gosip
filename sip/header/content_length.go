package header

import (
	"fmt"
	"io"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// ContentLength represents the Content-Length header field.
// The Content-Length header field indicates the size of the message body in decimal number of octets.
type ContentLength uint

// CanonicName returns the canonical name of the header.
func (ContentLength) CanonicName() Name { return "Content-Length" }

// CompactName returns the compact name of the header.
func (ContentLength) CompactName() Name { return "l" }

// RenderTo writes the header to the provided writer.
func (hdr ContentLength) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	return errors.Wrap2(fmt.Fprint(w, hdr.name(opts...), ": ", hdr.RenderValue()))
}

func (hdr ContentLength) name(opts ...RenderOptions) Name {
	if util.LastSliceElemOr(opts, RenderOptions{}).Compact {
		return hdr.CompactName()
	}
	return hdr.CanonicName()
}

// Render returns the string representation of the header.
func (hdr ContentLength) Render(opts ...RenderOptions) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	_, _ = hdr.RenderTo(sb, opts...)
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr ContentLength) RenderValue() string { return strconv.FormatUint(uint64(hdr), 10) }

func (hdr ContentLength) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr ContentLength) Format(f fmt.State, verb rune) {
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
			hideMethods   ContentLength
			ContentLength hideMethods
		)
		fmt.Fprintf(f, fmt.FormatString(f, verb), ContentLength(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr ContentLength) Clone() Header { return hdr }

// Equal compares this header with another for equality.
func (hdr ContentLength) Equal(val any) bool {
	var other ContentLength
	switch v := val.(type) {
	case ContentLength:
		other = v
	case *ContentLength:
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
func (ContentLength) IsValid() bool { return true }

func (hdr ContentLength) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ContentLength) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		return errors.Wrap(err)
	}

	if gh == nil {
		*hdr = 0
		return nil
	}

	h, ok := gh.(ContentLength)
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

func buildFromContentLengthNode(node *abnf.Node) ContentLength {
	l, _ := strconv.ParseUint(node.Children[2].String(), 10, 64)
	return ContentLength(l)
}
