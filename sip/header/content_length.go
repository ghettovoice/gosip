package header

import (
	"fmt"
	"io"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

type ContentLength uint

func (hdr ContentLength) HeaderName() string { return "Content-Length" }

func (hdr ContentLength) RenderHeaderTo(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.HeaderName(), ": ", uint(hdr))
	return err
}

func (hdr ContentLength) RenderHeader() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr ContentLength) Clone() Header { return hdr }

func (hdr ContentLength) Equal(val any) bool {
	var other ContentLength
	switch v := val.(type) {
	case ContentLength:
		other = v
	case *ContentLength:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return hdr == other
}

func (hdr ContentLength) IsValid() bool { return true }

func buildFromContentLengthNode(node *abnf.Node) ContentLength {
	l, _ := strconv.ParseUint(node.Children[2].String(), 10, 64)
	return ContentLength(l)
}
