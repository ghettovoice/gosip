package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

// CallID represents the Call-ID header field.
// The Call-ID header field uniquely identifies a particular invitation or all registrations of a particular client.
type CallID string

// CanonicName returns the canonical name of the header.
func (CallID) CanonicName() Name { return "Call-ID" }

// CompactName returns the compact name of the header.
func (CallID) CompactName() Name { return "i" }

// RenderTo writes the header to the provided writer.
func (hdr CallID) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr CallID) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(io.WriteString(w, string(hdr)))
}

// Render returns the string representation of the header.
func (hdr CallID) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr CallID) RenderValue() string {
	return string(hdr)
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr CallID) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods CallID
		CallID      hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), CallID(hdr))
}

// Clone returns a copy of the header.
func (hdr CallID) Clone() Header {
	return hdr
}

// Equal compares this header with another for equality.
func (hdr CallID) Equal(val any) bool {
	return util.EqualValue(val, func(other CallID) bool {
		return hdr == other
	})
}

// IsValid checks whether the header is syntactically valid.
func (hdr CallID) IsValid() bool {
	return hdr != ""
}

func (hdr CallID) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *CallID) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromCallIDNode(node *abnf.Node) CallID {
	return CallID(grammar.MustGetNode(node, "callid").Value)
}
