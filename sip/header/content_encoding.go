package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type ContentEncoding []string

func (ContentEncoding) CanonicName() Name { return "Content-Encoding" }

func (hdr ContentEncoding) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr ContentEncoding) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr ContentEncoding) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr ContentEncoding) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	sb.WriteByte('[')
	_ = hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr ContentEncoding) Clone() Header { return slices.Clone(hdr) }

func (hdr ContentEncoding) Equal(val any) bool {
	var other ContentEncoding
	switch v := val.(type) {
	case ContentEncoding:
		other = v
	case *ContentEncoding:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(enc1, enc2 string) bool { return stringutils.LCase(enc1) == stringutils.LCase(enc2) })
}

func (hdr ContentEncoding) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(enc string) bool { return !grammar.IsToken(enc) })
}

func buildFromContentEncodingNode(node *abnf.Node) ContentEncoding {
	encNodes := node.GetNodes("token")
	h := make(ContentEncoding, len(encNodes))
	for i, encNode := range encNodes {
		h[i] = encNode.String()
	}
	return h
}
