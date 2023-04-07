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

type ContentEncoding []string

func (hdr ContentEncoding) HeaderName() string { return "Content-Encoding" }

func (hdr ContentEncoding) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr ContentEncoding) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr ContentEncoding) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr ContentEncoding) String() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	sb.WriteByte('[')
	hdr.renderValue(sb)
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
	return slices.EqualFunc(hdr, other, func(enc1, enc2 string) bool { return utils.LCase(enc1) == utils.LCase(enc2) })
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
