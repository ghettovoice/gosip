package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
)

type InReplyTo []CallID

func (InReplyTo) CanonicName() Name { return "In-Reply-To" }

func (hdr InReplyTo) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr InReplyTo) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr InReplyTo) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr InReplyTo) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	sb.WriteByte('[')
	_ = hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr InReplyTo) Clone() Header { return slices.Clone(hdr) }

func (hdr InReplyTo) Equal(val any) bool {
	var other InReplyTo
	switch v := val.(type) {
	case InReplyTo:
		other = v
	case *InReplyTo:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(id1, id2 CallID) bool { return id1.Equal(id2) })
}

func (hdr InReplyTo) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(id CallID) bool { return !id.IsValid() })
}

func buildFromInReplyToNode(node *abnf.Node) InReplyTo {
	idNodes := node.GetNodes("callid")
	h := make(InReplyTo, len(idNodes))
	for i, idNode := range idNodes {
		h[i] = CallID(idNode.Value)
	}
	return h
}
