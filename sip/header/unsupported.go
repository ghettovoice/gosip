package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

// Unsupported represents the Unsupported header field.
// The Unsupported header field lists the features not supported by the UAS.
type Unsupported []OptionTag

// CanonicName returns the canonical name of the header.
func (Unsupported) CanonicName() Name { return "Unsupported" }

// CompactName returns the compact name of the header (Unsupported has no compact form).
func (Unsupported) CompactName() Name { return "Unsupported" }

// RenderTo writes the header to the provided writer.
func (hdr Unsupported) RenderTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr.CanonicName(), func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrEntries(w, hdr))
	}))
}

// Render returns the string representation of the header.
func (hdr Unsupported) Render(opts ...RenderOptions) string {
	return types.Render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr Unsupported) RenderValue() string {
	return renderHdrEntriesString(hdr)
}

// String returns the string representation of the header value.
func (hdr Unsupported) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Unsupported) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Unsupported
		Unsupported hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), Unsupported(hdr))
}

// Clone returns a copy of the header.
func (hdr Unsupported) Clone() Header {
	return slices.Clone(hdr)
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

	return slices.EqualFunc(hdr, other, func(tag1, tag2 OptionTag) bool { return tag1.Equal(tag2) })
}

// IsValid checks whether the header is syntactically valid.
func (hdr Unsupported) IsValid() bool { return len(hdr) > 0 && validOptionTags(hdr) }

func (hdr Unsupported) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Unsupported) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), nil))
}

func buildFromUnsupportedNode(node *abnf.Node) Unsupported {
	return Unsupported(buildOptionTagsFromNode(node))
}
