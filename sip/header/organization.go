package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
)

type Organization string

func (Organization) CanonicName() Name { return "Organization" }

func (hdr Organization) RenderTo(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.CanonicName(), ": ", string(hdr))
	return err
}

func (hdr Organization) Render() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr Organization) Clone() Header { return hdr }

func (hdr Organization) Equal(val any) bool {
	var other Organization
	switch v := val.(type) {
	case Organization:
		other = v
	case *Organization:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return hdr == other
}

func (Organization) IsValid() bool { return true }

func buildFromOrganizationNode(node *abnf.Node) Organization {
	return Organization(node.Children[2].Value)
}
