package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
)

type To EntityAddr

func (*To) CanonicName() Name { return "To" }

func (hdr *To) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	_, err := fmt.Fprint(w, hdr.CanonicName(), ": ", EntityAddr(*hdr))
	return err
}

func (hdr *To) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr *To) String() string {
	if hdr == nil {
		return nilTag
	}
	return EntityAddr(*hdr).String()
}

func (hdr *To) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := To(EntityAddr(*hdr).Clone())
	return &hdr2
}

func (hdr *To) Equal(val any) bool {
	var other *To
	switch v := val.(type) {
	case To:
		other = &v
	case *To:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return EntityAddr(*hdr).Equal(EntityAddr(*other))
}

func (hdr *To) IsValid() bool { return hdr != nil && EntityAddr(*hdr).IsValid() }

func buildFromToNode(node *abnf.Node) *To {
	hdr := To(buildFromHeaderAddrNode(node, "to-param"))
	return &hdr
}
