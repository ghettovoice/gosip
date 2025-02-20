package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
)

type Server string

func (Server) CanonicName() Name { return "Server" }

func (hdr Server) RenderTo(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.CanonicName(), ": ", string(hdr))
	return err
}

func (hdr Server) Render() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr Server) Clone() Header { return hdr }

func (hdr Server) Equal(val any) bool {
	var other Server
	switch v := val.(type) {
	case Server:
		other = v
	case *Server:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return hdr == other
}

func (hdr Server) IsValid() bool { return hdr != "" }

func buildFromServerNode(node *abnf.Node) Server {
	var s []byte
	for _, n := range node.Children[2:] {
		if n.IsEmpty() {
			continue
		}
		s = append(s, n.Value...)
	}
	return Server(s)
}
