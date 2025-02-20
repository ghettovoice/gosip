package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
)

type From EntityAddr

func (*From) CanonicName() Name { return "From" }

func (hdr *From) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	_, err := fmt.Fprint(w, hdr.CanonicName(), ": ", EntityAddr(*hdr))
	return err
}

func (hdr *From) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr *From) String() string {
	if hdr == nil {
		return nilTag
	}
	return EntityAddr(*hdr).String()
}

func (hdr *From) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := From(EntityAddr(*hdr).Clone())
	return &hdr2
}

func (hdr *From) Equal(val any) bool {
	var other *From
	switch v := val.(type) {
	case From:
		other = &v
	case *From:
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

func (hdr *From) IsValid() bool { return hdr != nil && EntityAddr(*hdr).IsValid() }

func buildFromFromNode(node *abnf.Node) *From {
	hdr := From(buildFromHeaderAddrNode(abnfutils.MustGetNode(node, "from-spec"), "from-param"))
	return &hdr
}
