package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type Allow []string

func (Allow) CanonicName() Name { return "Allow" }

func (hdr Allow) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr Allow) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr Allow) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr Allow) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	sb.WriteByte('[')
	_ = hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr Allow) Clone() Header { return slices.Clone(hdr) }

func (hdr Allow) Equal(val any) bool {
	var other Allow
	switch v := val.(type) {
	case Allow:
		other = v
	case *Allow:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(mtd1, mtd2 string) bool { return stringutils.UCase(mtd1) == stringutils.UCase(mtd2) })
}

func (hdr Allow) IsValid() bool {
	return hdr != nil && !slices.ContainsFunc(hdr, func(mtd string) bool { return !grammar.IsToken(mtd) })
}

func buildFromAllowNode(node *abnf.Node) Allow {
	mthNodes := node.GetNodes("Method")
	hdr := make(Allow, len(mthNodes))
	for i, mthNode := range mthNodes {
		hdr[i] = mthNode.String()
	}
	return hdr
}
