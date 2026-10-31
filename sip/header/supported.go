package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

// Supported represents the Supported header field.
// The Supported header field enumerates all the extensions supported by the UAC or UAS.
type Supported []OptionTag

// CanonicName returns the canonical name of the header.
func (Supported) CanonicName() Name { return "Supported" }

// CompactName returns the compact name of the header.
func (Supported) CompactName() Name { return "k" }

// RenderTo writes the header to the provided writer.
func (hdr Supported) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, renderHdrName(hdr, opts...), func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrEntries(w, hdr))
	}))
}

// Render returns the string representation of the header.
func (hdr Supported) Render(opts ...RenderOptions) string {
	return types.Render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr Supported) RenderValue() string {
	return renderHdrEntriesString(hdr)
}

// String returns the string representation of the header value.
func (hdr Supported) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Supported) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Supported
		Supported   hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), Supported(hdr))
}

// Clone returns a copy of the header.
func (hdr Supported) Clone() Header {
	return slices.Clone(hdr)
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

	return slices.EqualFunc(hdr, other, func(tag1, tag2 OptionTag) bool { return tag1.Equal(tag2) })
}

// IsValid checks whether the header is syntactically valid.
func (hdr Supported) IsValid() bool {
	return hdr != nil && validOptionTags(hdr)
}

func (hdr Supported) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Supported) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), nil))
}

func buildFromSupportedNode(node *abnf.Node) Supported {
	return Supported(buildOptionTagsFromNode(node))
}
