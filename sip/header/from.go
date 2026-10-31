package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/types"
)

// From represents the From header field.
// The From header field indicates the initiator of the request.
type From NameAddr

// CanonicName returns the canonical name of the header.
func (*From) CanonicName() Name { return "From" }

// CompactName returns the compact name of the header.
func (*From) CompactName() Name { return "f" }

// RenderTo writes the header to the provided writer.
func (hdr *From) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, renderHdrName(hdr, opts...), func(w io.Writer) (int, error) {
		return errors.Wrap2(NameAddr(*hdr).renderTo(w))
	}))
}

// Render returns the string representation of the header.
func (hdr *From) Render(opts ...RenderOptions) string {
	return types.Render(hdr, opts...)
}

// String returns the string representation of the header value.
func (hdr *From) String() string { return hdr.RenderValue() }

// RenderValue returns the header value without the name prefix.
func (hdr *From) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return NameAddr(*hdr).String()
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr *From) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods From
		From        hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*From)(hdr))
}

// Clone returns a copy of the header.
func (hdr *From) Clone() Header {
	if hdr == nil {
		return nil
	}

	hdr2 := From(NameAddr(*hdr).Clone())
	return &hdr2
}

// Equal compares this header with another for equality.
func (hdr *From) Equal(val any) bool {
	var other *From
	switch v := val.(type) {
	case From:
		other = &v
	case *From:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return NameAddr(*hdr).Equal(NameAddr(*other))
}

// IsValid checks whether the header is syntactically valid.
func (hdr *From) IsValid() bool { return hdr != nil && NameAddr(*hdr).IsValid() }

func (hdr *From) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *From) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), emptyNameAddrValue))
}

func (hdr *From) Tag() (string, bool) {
	if hdr == nil {
		return "", false
	}
	return NameAddr(*hdr).Tag()
}

func buildFromFromNode(node *abnf.Node) *From {
	hdr := From(buildFromNameAddrNode(grammar.MustGetNode(node, "from-spec"), "from-param"))
	return &hdr
}
