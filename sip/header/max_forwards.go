package header

import (
	"fmt"
	"io"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

type MaxForwards uint

func (hdr MaxForwards) HeaderName() string { return "Max-Forwards" }

func (hdr MaxForwards) RenderHeaderTo(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.HeaderName(), ": ", uint(hdr))
	return err
}

func (hdr MaxForwards) RenderHeader() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr MaxForwards) Clone() Header { return hdr }

func (hdr MaxForwards) Equal(val any) bool {
	var other MaxForwards
	switch v := val.(type) {
	case MaxForwards:
		other = v
	case *MaxForwards:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return hdr == other
}

func (hdr MaxForwards) IsValid() bool { return true }

func buildFromMaxForwardsNode(node *abnf.Node) MaxForwards {
	v, _ := strconv.ParseUint(node.Children[2].String(), 10, 8)
	return MaxForwards(v)
}
