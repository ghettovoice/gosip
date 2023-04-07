package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type Priority string

func (hdr Priority) HeaderName() string { return "Priority" }

func (hdr Priority) RenderHeaderTo(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.HeaderName(), ": ", string(hdr))
	return err
}

func (hdr Priority) RenderHeader() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr Priority) Clone() Header { return hdr }

func (hdr Priority) Equal(val any) bool {
	var other Priority
	switch v := val.(type) {
	case Priority:
		other = v
	default:
		return false
	}
	return utils.LCase(hdr) == utils.LCase(other)
}

func (hdr Priority) IsValid() bool { return grammar.IsToken(string(hdr)) }

func buildFromPriorityNode(node *abnf.Node) Priority {
	return Priority(utils.MustGetNode(node, "priority-value").Value)
}
