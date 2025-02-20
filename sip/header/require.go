package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type Require []string

func (Require) CanonicName() Name { return "Require" }

func (hdr Require) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr Require) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr Require) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr Require) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	sb.WriteByte('[')
	_ = hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr Require) Clone() Header { return slices.Clone(hdr) }

func (hdr Require) Equal(val any) bool {
	var other Require
	switch v := val.(type) {
	case Require:
		other = v
	case *Require:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(a, b string) bool { return stringutils.LCase(a) == stringutils.LCase(b) })
}

func (hdr Require) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(s string) bool { return !grammar.IsToken(s) })
}

func buildFromRequireNode(node *abnf.Node) Require {
	tagNodes := node.GetNodes("token")
	h := make(Require, 0, len(tagNodes))
	for i := range tagNodes {
		if n := tagNodes[i].GetNode("token"); n != nil {
			h = append(h, n.String())
		}
	}
	return h
}
