package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
)

type CallID string

func (CallID) CanonicName() Name { return "Call-ID" }

func (hdr CallID) RenderTo(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.CanonicName(), ": ", string(hdr))
	return err
}

func (hdr CallID) Render() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
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
	return CallID(abnfutils.MustGetNode(node, "callid").Value)
}
