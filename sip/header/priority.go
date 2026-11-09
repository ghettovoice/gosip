package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

// Priority represents the Priority header field.
// The Priority header field indicates the urgency of the request as perceived by the client.
type Priority string

// CanonicName returns the canonical name of the header.
func (Priority) CanonicName() Name { return "Priority" }

// CompactName returns the compact name of the header (Priority has no compact form).
func (Priority) CompactName() Name { return "Priority" }

// RenderTo writes the header to the provided writer.
func (hdr Priority) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr Priority) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(io.WriteString(w, string(hdr)))
}

// Render returns the string representation of the header.
func (hdr Priority) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr Priority) RenderValue() string {
	return string(hdr)
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Priority) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Priority
		Priority    hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), Priority(hdr))
}

// Clone returns a copy of the header.
func (hdr Priority) Clone() Header {
	return hdr
}

// Equal compares this header with another for equality.
func (hdr Priority) Equal(val any) bool {
	return util.EqualValue(val, func(other Priority) bool {
		return util.EqualFold(hdr, other)
	})
}

// IsValid checks whether the header is syntactically valid.
func (hdr Priority) IsValid() bool {
	return grammar.IsToken(string(hdr))
}

func (hdr Priority) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Priority) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromPriorityNode(node *abnf.Node) Priority {
	return Priority(grammar.MustGetNode(node, "priority-value").Value)
}
