package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

type Unsupported Require

func (hdr Unsupported) HeaderName() string { return "Unsupported" }

func (hdr Unsupported) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return Require(hdr).renderValue(w)
}

func (hdr Unsupported) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr Unsupported) String() string { return Require(hdr).String() }

func (hdr Unsupported) Clone() Header {
	return Unsupported(Require(hdr).Clone().(Require))
}

func (hdr Unsupported) Equal(val any) bool {
	var other Unsupported
	switch v := val.(type) {
	case Unsupported:
		other = v
	default:
		return false
	}
	return Require(hdr).Equal(Require(other))
}

func (hdr Unsupported) IsValid() bool { return Require(hdr).IsValid() }

func buildFromUnsupportedNode(node *abnf.Node) Unsupported {
	return Unsupported(buildFromRequireNode(node))
}
