package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// Contact represents the Contact header field.
// The Contact header field provides a SIP or SIPS URI that can be used to contact that specific instance
// of the UA for subsequent requests.
type Contact []ContactAddr

// CanonicName returns the canonical name of the header.
func (Contact) CanonicName() Name { return "Contact" }

// CompactName returns the compact name of the header.
func (Contact) CompactName() Name { return "m" }

// RenderTo writes the header to the provided writer.
func (hdr Contact) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr Contact) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	if len(hdr) == 0 {
		return errors.Wrap2(fmt.Fprint(w, "*"))
	}
	return errors.Wrap2(renderHdrValsTo(w, hdr))
}

// Render returns the string representation of the header.
func (hdr Contact) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// String returns the string representation of the header value.
func (hdr Contact) String() string {
	return hdr.RenderValue()
}

// RenderValue returns the header value without the name prefix.
func (hdr Contact) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderHdrVal(hdr)
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Contact) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Contact
		Contact     hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), Contact(hdr))
}

// Clone returns a copy of the header.
func (hdr Contact) Clone() Header {
	return cloneHdrVals(hdr)
}

// Equal compares this header with another for equality.
func (hdr Contact) Equal(val any) bool {
	return util.EqualValue(val, func(other Contact) bool {
		return slices.EqualFunc(hdr, other, func(addr1, addr2 ContactAddr) bool {
			return addr1.Equal(addr2)
		})
	})
}

// IsValid checks whether the header is syntactically valid.
func (hdr Contact) IsValid() bool {
	return hdr != nil &&
		!slices.ContainsFunc(hdr, func(addr ContactAddr) bool {
			return !addr.IsValid()
		})
}

func (hdr Contact) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Contact) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromContactNode(node *abnf.Node) Contact {
	cntNodes := node.GetNodes("contact-param")
	h := make(Contact, len(cntNodes))
	for i, cntNode := range cntNodes {
		h[i] = buildFromNameAddrNode(cntNode, "contact-params")
	}
	return h
}

type ContactAddr = NameAddr
