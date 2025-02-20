package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
)

type Unsupported Require

func (Unsupported) CanonicName() Name { return "Unsupported" }

func (hdr Unsupported) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return Require(hdr).renderValue(w)
}

func (hdr Unsupported) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr Unsupported) String() string { return Require(hdr).String() }

func (hdr Unsupported) Clone() Header {
	return Unsupported(Require(hdr).Clone().(Require)) //nolint:forcetypeassert
}

func (hdr Unsupported) Equal(val any) bool {
	var other Unsupported
	switch v := val.(type) {
	case Unsupported:
		other = v
	case *Unsupported:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return Require(hdr).Equal(Require(other))
}

func (hdr Unsupported) IsValid() bool { return Require(hdr).IsValid() }

func buildFromUnsupportedNode(node *abnf.Node) Unsupported {
	return Unsupported(buildFromRequireNode(node))
}
