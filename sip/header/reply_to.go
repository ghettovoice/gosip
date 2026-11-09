package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// ReplyTo represents the Reply-To header field.
// The Reply-To header field contains a logical return URI that may be different from the From header field.
type ReplyTo NameAddr

// CanonicName returns the canonical name of the header.
func (*ReplyTo) CanonicName() Name { return "Reply-To" }

// CompactName returns the compact name of the header (Reply-To has no compact form).
func (*ReplyTo) CompactName() Name { return "Reply-To" }

// RenderTo writes the header to the provided writer.
func (hdr *ReplyTo) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr *ReplyTo) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(NameAddr(*hdr).renderTo(w))
}

// Render returns the string representation of the header.
func (hdr *ReplyTo) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr *ReplyTo) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderHdrVal(hdr)
}

// String returns the string representation of the header value.
func (hdr *ReplyTo) String() string {
	return hdr.RenderValue()
}

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

	clone := ReplyTo(NameAddr(*hdr).Clone())
	return &clone
}

// Equal compares this header with another for equality.
func (hdr *ReplyTo) Equal(val any) bool {
	return util.EqualPointer(hdr, val, func(other *ReplyTo) bool {
		return NameAddr(*hdr).Equal(NameAddr(*other))
	})
}

// IsValid checks whether the header is syntactically valid.
func (hdr *ReplyTo) IsValid() bool {
	return hdr != nil && NameAddr(*hdr).IsValid()
}

func (hdr *ReplyTo) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ReplyTo) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyNameAddrVal))
}

func buildFromReplyToNode(node *abnf.Node) *ReplyTo {
	hdr := ReplyTo(buildFromNameAddrNode(node, "generic-param"))
	return &hdr
}
