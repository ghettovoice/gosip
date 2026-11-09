package header

import (
	"fmt"
	"io"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// MaxForwards represents the Max-Forwards header field.
// The Max-Forwards header field limits the number of proxies or gateways that can forward the request.
type MaxForwards uint

// CanonicName returns the canonical name of the header.
func (MaxForwards) CanonicName() Name { return "Max-Forwards" }

// CompactName returns the compact name of the header (Max-Forwards has no compact form).
func (MaxForwards) CompactName() Name { return "Max-Forwards" }

// RenderTo writes the header to the provided writer.
func (hdr MaxForwards) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr MaxForwards) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(io.WriteString(w, strconv.FormatUint(uint64(hdr), 10)))
}

// Render returns the string representation of the header.
func (hdr MaxForwards) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr MaxForwards) RenderValue() string {
	return strconv.FormatUint(uint64(hdr), 10)
}

func (hdr MaxForwards) String() string {
	return hdr.RenderValue()
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr MaxForwards) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods MaxForwards
		MaxForwards hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), MaxForwards(hdr))
}

// Clone returns a copy of the header.
func (hdr MaxForwards) Clone() Header {
	return hdr
}

// Equal compares this header with another for equality.
func (hdr MaxForwards) Equal(val any) bool {
	return util.EqualValue(val, func(other MaxForwards) bool {
		return hdr == other
	})
}

// IsValid checks whether the header is syntactically valid.
func (MaxForwards) IsValid() bool {
	return true
}

func (hdr MaxForwards) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *MaxForwards) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromMaxForwardsNode(node *abnf.Node) MaxForwards {
	v, _ := strconv.ParseUint(node.Children[2].String(), 10, 8)
	return MaxForwards(v)
}
