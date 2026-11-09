package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

// Any implements a generic header.
// It can be used to parse/append any headers that aren't natively supported by the lib.
type Any struct {
	Name  string
	Value string
}

func (hdr *Any) CanonicName() Name { return CanonicName(hdr.Name) }

func (hdr *Any) CompactName() Name { return CanonicName(hdr.Name) }

func (hdr *Any) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr *Any) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(io.WriteString(w, hdr.Value))
}

func (hdr *Any) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr *Any) String() string {
	return hdr.RenderValue()
}

// RenderValue returns the header value without the name prefix.
func (hdr *Any) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return hdr.Value
}

func (hdr *Any) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Any
		Any         hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*Any)(hdr))
}

func (hdr *Any) Clone() Header {
	if hdr == nil {
		return nil
	}
	clone := *hdr
	return &clone
}

func (hdr *Any) Equal(val any) bool {
	return util.EqualPointer(hdr, val, func(other *Any) bool {
		return util.EqualFold(hdr.Name, other.Name) && hdr.Value == other.Value
	})
}

func (hdr *Any) IsValid() bool {
	return hdr != nil && grammar.IsToken(hdr.Name)
}

func (hdr *Any) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Any) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, "", nil))
}

func buildFromExtensionHeaderNode(node *abnf.Node) *Any {
	return &Any{
		node.Children[0].String(),
		grammar.MustGetNode(node, "header-value").String(),
	}
}
