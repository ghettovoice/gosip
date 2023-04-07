package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type Require []string

func (hdr Require) HeaderName() string { return "Require" }

func (hdr Require) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr Require) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr Require) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr Require) String() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	sb.WriteByte('[')
	hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr Require) Clone() Header { return slices.Clone(hdr) }

func (hdr Require) Equal(val any) bool {
	var other Require
	switch v := val.(type) {
	case Require:
		other = v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(a, b string) bool { return utils.LCase(a) == utils.LCase(b) })
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
