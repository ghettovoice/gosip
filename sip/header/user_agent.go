package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
)

type UserAgent string

func (UserAgent) CanonicName() Name { return "User-Agent" }

func (hdr UserAgent) RenderTo(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.CanonicName(), ": ", string(hdr))
	return err
}

func (hdr UserAgent) Render() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
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
