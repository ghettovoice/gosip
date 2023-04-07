package header

import (
	"fmt"
	"strings"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
	"github.com/ghettovoice/gosip/sip/uri"
)

type EntityAddr struct {
	DisplayName string
	URI         uri.URI
	Params      Values
}

func (addr EntityAddr) String() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	if addr.DisplayName != "" {
		fmt.Fprint(sb, grammar.Quote(addr.DisplayName), " ")
	}
	fmt.Fprint(sb, "<")
	if addr.URI != nil {
		utils.RenderTo(sb, addr.URI)
	}
	fmt.Fprint(sb, ">")
	renderHeaderParams(sb, addr.Params, false)
	return sb.String()
}

func (addr EntityAddr) Equal(val any) bool {
	var other EntityAddr
	switch v := val.(type) {
	case EntityAddr:
		other = v
	case *EntityAddr:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}

	return utils.IsEqual(addr.URI, other.URI) && compareHeaderParams(addr.Params, other.Params, map[string]bool{
		"q":       true,
		"tag":     true,
		"expires": true,
	})
}

func (addr EntityAddr) IsValid() bool {
	return utils.IsValid(addr.URI) && validateHeaderParams(addr.Params)
}

func (addr EntityAddr) IsZero() bool {
	return addr.DisplayName == "" && addr.URI == nil && len(addr.Params) == 0
}

func (addr EntityAddr) Clone() EntityAddr {
	addr.URI = utils.Clone[uri.URI](addr.URI)
	addr.Params = addr.Params.Clone()
	return addr
}

func buildFromHeaderAddrNode(node *abnf.Node, psNodeKey string) EntityAddr {
	addr := EntityAddr{
		URI:    uri.FromABNF(utils.MustGetNode(node, "addr-spec").Children[0]),
		Params: buildFromHeaderParamNodes(node.GetNodes(psNodeKey), nil),
	}
	if dnameNode := node.GetNode("display-name"); dnameNode != nil {
		addr.DisplayName = grammar.Unquote(strings.TrimSpace(dnameNode.String()))
	}
	return addr
}
