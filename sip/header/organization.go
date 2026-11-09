package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// Organization represents the Organization header field.
// The Organization header field conveys the name of the organization to which the SIP element issuing the request or response belongs.
type Organization string

// CanonicName returns the canonical name of the header.
func (Organization) CanonicName() Name { return "Organization" }

// CompactName returns the compact name of the header (Organization has no compact form).
func (Organization) CompactName() Name { return "Organization" }

// RenderTo writes the header to the provided writer.
func (hdr Organization) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr Organization) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(io.WriteString(w, string(hdr)))
}

// Render returns the string representation of the header.
func (hdr Organization) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr Organization) RenderValue() string {
	return string(hdr)
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Organization) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods  Organization
		Organization hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), Organization(hdr))
}

// Clone returns a copy of the header.
func (hdr Organization) Clone() Header {
	return hdr
}

// Equal compares this header with another for equality.
func (hdr Organization) Equal(val any) bool {
	return util.EqualValue(val, func(other Organization) bool {
		return hdr == other
	})
}

// IsValid checks whether the header is syntactically valid.
func (Organization) IsValid() bool {
	return true
}

func (hdr Organization) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Organization) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromOrganizationNode(node *abnf.Node) Organization {
	return Organization(node.Children[2].Value)
}
