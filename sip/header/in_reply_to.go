package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// InReplyTo represents the In-Reply-To header field.
// The In-Reply-To header field enumerates the Call-IDs that this call references or returns.
type InReplyTo []CallID

// CanonicName returns the canonical name of the header.
func (InReplyTo) CanonicName() Name { return "In-Reply-To" }

// CompactName returns the compact name of the header (In-Reply-To has no compact form).
func (InReplyTo) CompactName() Name { return "In-Reply-To" }

// RenderTo writes the header to the provided writer.
func (hdr InReplyTo) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr InReplyTo) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrTokensTo(w, hdr))
}

// Render returns the string representation of the header.
func (hdr InReplyTo) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// String returns the string representation of the header value.
func (hdr InReplyTo) String() string {
	return hdr.RenderValue()
}

// RenderValue returns the header value without the name prefix.
func (hdr InReplyTo) RenderValue() string {
	return renderHdrVal(hdr)
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr InReplyTo) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods InReplyTo
		InReplyTo   hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), InReplyTo(hdr))
}

// Clone returns a copy of the header.
func (hdr InReplyTo) Clone() Header {
	return slices.Clone(hdr)
}

// Equal compares this header with another for equality.
func (hdr InReplyTo) Equal(val any) bool {
	return util.EqualValue(val, func(other InReplyTo) bool {
		return slices.EqualFunc(hdr, other, func(id1, id2 CallID) bool {
			return id1.Equal(id2)
		})
	})
}

// IsValid checks whether the header is syntactically valid.
func (hdr InReplyTo) IsValid() bool {
	return len(hdr) > 0 &&
		!slices.ContainsFunc(hdr, func(id CallID) bool {
			return !id.IsValid()
		})
}

func (hdr InReplyTo) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *InReplyTo) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromInReplyToNode(node *abnf.Node) InReplyTo {
	idNodes := node.GetNodes("callid")
	h := make(InReplyTo, len(idNodes))
	for i, idNode := range idNodes {
		h[i] = CallID(idNode.Value)
	}
	return h
}
