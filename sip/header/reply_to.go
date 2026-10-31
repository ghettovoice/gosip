package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

// ReplyTo represents the Reply-To header field.
// The Reply-To header field contains a logical return URI that may be different from the From header field.
type ReplyTo NameAddr

// CanonicName returns the canonical name of the header.
func (*ReplyTo) CanonicName() Name { return "Reply-To" }

// CompactName returns the compact name of the header (Reply-To has no compact form).
func (*ReplyTo) CompactName() Name { return "Reply-To" }

// RenderTo writes the header to the provided writer.
func (hdr *ReplyTo) RenderTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr.CanonicName(), func(w io.Writer) (int, error) {
		return errors.Wrap2(NameAddr(*hdr).renderTo(w))
	}))
}

// Render returns the string representation of the header.
func (hdr *ReplyTo) Render(opts ...RenderOptions) string {
	return types.Render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr *ReplyTo) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return NameAddr(*hdr).String()
}

// String returns the string representation of the header value.
func (hdr *ReplyTo) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr *ReplyTo) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods ReplyTo
		ReplyTo     hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*ReplyTo)(hdr))
}

// Clone returns a copy of the header.
func (hdr *ReplyTo) Clone() Header {
	if hdr == nil {
		return nil
	}

	hdr2 := ReplyTo(NameAddr(*hdr).Clone())
	return &hdr2
}

// Equal compares this header with another for equality.
func (hdr *ReplyTo) Equal(val any) bool {
	var other *ReplyTo
	switch v := val.(type) {
	case ReplyTo:
		other = &v
	case *ReplyTo:
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
func (hdr *ReplyTo) IsValid() bool { return hdr != nil && NameAddr(*hdr).IsValid() }

func (hdr *ReplyTo) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ReplyTo) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), emptyNameAddrValue))
}

func buildFromReplyToNode(node *abnf.Node) *ReplyTo {
	hdr := ReplyTo(buildFromNameAddrNode(node, "generic-param"))
	return &hdr
}
