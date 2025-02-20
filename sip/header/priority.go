package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type Priority string

func (Priority) CanonicName() Name { return "Priority" }

func (hdr Priority) RenderTo(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.CanonicName(), ": ", string(hdr))
	return err
}

func (hdr Priority) Render() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr Priority) Clone() Header { return hdr }

func (hdr Priority) Equal(val any) bool {
	var other Priority
	switch v := val.(type) {
	case Priority:
		other = v
	case *Priority:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return stringutils.LCase(hdr) == stringutils.LCase(other)
}

func (hdr Priority) IsValid() bool { return grammar.IsToken(string(hdr)) }

func buildFromPriorityNode(node *abnf.Node) Priority {
	return Priority(abnfutils.MustGetNode(node, "priority-value").Value)
}
