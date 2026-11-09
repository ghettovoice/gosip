package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// Subject represents the Subject header field.
// The Subject header field provides a summary or indicates the nature of the call.
type Subject string

// CanonicName returns the canonical name of the header.
func (Subject) CanonicName() Name { return "Subject" }

// CompactName returns the compact name of the header.
func (Subject) CompactName() Name { return "s" }

// RenderTo writes the header to the provided writer.
func (hdr Subject) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr Subject) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(io.WriteString(w, string(hdr)))
}

// Render returns the string representation of the header.
func (hdr Subject) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr Subject) RenderValue() string {
	return string(hdr)
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Subject) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Subject
		Subject     hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), Subject(hdr))
}

// Clone returns a copy of the header.
func (hdr Subject) Clone() Header {
	return hdr
}

// Equal compares this header with another for equality.
func (hdr Subject) Equal(val any) bool {
	return util.EqualValue(val, func(other Subject) bool {
		return hdr == other
	})
}

// IsValid checks whether the header is syntactically valid.
func (Subject) IsValid() bool {
	return true
}

func (hdr Subject) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Subject) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromSubjectNode(node *abnf.Node) Subject {
	return Subject(node.Children[2].Value)
}
