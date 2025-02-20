package header

import (
	"fmt"
	"io"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
)

type ContentLength uint

func (ContentLength) CanonicName() Name { return "Content-Length" }

func (hdr ContentLength) RenderTo(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.CanonicName(), ": ", uint(hdr))
	return err
}

func (hdr ContentLength) Render() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
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

func (ContentLength) IsValid() bool { return true }

func buildFromContentLengthNode(node *abnf.Node) ContentLength {
	l, _ := strconv.ParseUint(node.Children[2].String(), 10, 64)
	return ContentLength(l)
}
