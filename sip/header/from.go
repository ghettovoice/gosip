package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
)

type From EntityAddr

func (hdr *From) HeaderName() string { return "From" }

func (hdr *From) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	_, err := fmt.Fprint(w, hdr.HeaderName(), ": ", EntityAddr(*hdr))
	return err
}

func (hdr *From) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr *From) String() string {
	if hdr == nil {
		return "<nil>"
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
	hdr := From(buildFromHeaderAddrNode(utils.MustGetNode(node, "from-spec"), "from-param"))
	return &hdr
}
