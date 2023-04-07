package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

type UserAgent string

func (hdr UserAgent) HeaderName() string { return "User-Agent" }

func (hdr UserAgent) RenderHeaderTo(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.HeaderName(), ": ", string(hdr))
	return err
}

func (hdr UserAgent) RenderHeader() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr UserAgent) Clone() Header { return hdr }

func (hdr UserAgent) Equal(val any) bool {
	var other UserAgent
	switch v := val.(type) {
	case UserAgent:
		other = v
	case *UserAgent:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return hdr == other
}

func (hdr UserAgent) IsValid() bool { return hdr != "" }

func buildFromUserAgentNode(node *abnf.Node) UserAgent {
	return UserAgent(buildFromServerNode(node))
}
