package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// Allow represents the Allow header field.
// The Allow header field lists the set of methods supported by the UA generating the message.
type Allow []RequestMethod

// CanonicName returns the canonical name of the header.
func (Allow) CanonicName() Name { return "Allow" }

// CompactName returns the compact name of the header (Allow has no compact form).
func (Allow) CompactName() Name { return "Allow" }

// RenderTo writes the header to the provided writer.
func (hdr Allow) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr Allow) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrTokensTo(w, hdr))
}

// Render returns the string representation of the header.
func (hdr Allow) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// RenderValue returns the string representation of the header value.
func (hdr Allow) RenderValue() string {
	return renderHdrVal(hdr)
}

// String returns the string representation of the header value.
func (hdr Allow) String() string {
	return hdr.RenderValue()
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Allow) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Allow
		Allow       hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), Allow(hdr))
}

// Clone returns a copy of the header.
func (hdr Allow) Clone() Header {
	return slices.Clone(hdr)
}

// Equal compares this header with another for equality.
func (hdr Allow) Equal(val any) bool {
	return util.EqualValue(val, func(other Allow) bool {
		return slices.EqualFunc(hdr, other, func(mtd1, mtd2 RequestMethod) bool {
			return mtd1.Equal(mtd2)
		})
	})
}

// IsValid checks whether the header is syntactically valid.
func (hdr Allow) IsValid() bool {
	return hdr != nil &&
		!slices.ContainsFunc(hdr, func(mtd RequestMethod) bool {
			return !mtd.IsValid()
		})
}

func (hdr Allow) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Allow) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), nil))
}

func buildFromAllowNode(node *abnf.Node) Allow {
	mthNodes := node.GetNodes("Method")
	hdr := make(Allow, len(mthNodes))
	for i, mthNode := range mthNodes {
		hdr[i] = RequestMethod(mthNode.String())
	}
	return hdr
}
