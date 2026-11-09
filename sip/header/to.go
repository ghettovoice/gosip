package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// To represents the To header field.
// The To header field specifies the logical recipient of the request.
type To NameAddr

// CanonicName returns the canonical name of the header.
func (*To) CanonicName() Name { return "To" }

// CompactName returns the compact name of the header.
func (*To) CompactName() Name { return "t" }

// RenderTo writes the header to the provided writer.
func (hdr *To) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr *To) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(NameAddr(*hdr).renderTo(w))
}

// Render returns the string representation of the header.
func (hdr *To) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr *To) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderHdrVal(hdr)
}

// String returns the string representation of the header value.
func (hdr *To) String() string {
	return hdr.RenderValue()
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr *To) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods To
		To          hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*To)(hdr))
}

// Clone returns a copy of the header.
func (hdr *To) Clone() Header {
	if hdr == nil {
		return nil
	}

	clone := To(NameAddr(*hdr).Clone())
	return &clone
}

// Equal compares this header with another for equality.
func (hdr *To) Equal(val any) bool {
	return util.EqualPointer(hdr, val, func(other *To) bool {
		return NameAddr(*hdr).Equal(NameAddr(*other))
	})
}

// IsValid checks whether the header is syntactically valid.
func (hdr *To) IsValid() bool {
	return hdr != nil && NameAddr(*hdr).IsValid()
}

func (hdr *To) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *To) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyNameAddrVal))
}

func (hdr *To) Tag() (string, bool) {
	if hdr == nil {
		return "", false
	}
	return NameAddr(*hdr).Tag()
}

func buildFromToNode(node *abnf.Node) *To {
	hdr := To(buildFromNameAddrNode(node, "to-param"))
	return &hdr
}
