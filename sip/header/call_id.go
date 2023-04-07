package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
)

type CallID string

func (hdr CallID) HeaderName() string { return "Call-ID" }

func (hdr CallID) RenderHeaderTo(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.HeaderName(), ": ", string(hdr))
	return err
}

func (hdr CallID) RenderHeader() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr CallID) Clone() Header { return hdr }

func (hdr CallID) Equal(val any) bool {
	var other CallID
	switch v := val.(type) {
	case CallID:
		other = v
	case *CallID:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return hdr == other
}

func (hdr CallID) IsValid() bool { return hdr != "" }

func buildFromCallIDNode(node *abnf.Node) CallID {
	return CallID(utils.MustGetNode(node, "callid").Value)
}
