package header

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
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
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	if addr.DisplayName != "" {
		_, _ = fmt.Fprint(sb, grammar.Quote(addr.DisplayName), " ")
	}
	_, _ = fmt.Fprint(sb, "<")
	if addr.URI != nil {
		_ = stringutils.RenderTo(sb, addr.URI)
	}
	_, _ = fmt.Fprint(sb, ">")
	_ = renderHeaderParams(sb, addr.Params, false)
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
		URI:    uri.FromABNF(abnfutils.MustGetNode(node, "addr-spec").Children[0]),
		Params: buildFromHeaderParamNodes(node.GetNodes(psNodeKey), nil),
	}

	// https://datatracker.ietf.org/doc/rfc8217/
	if !node.Contains("name-addr") && strings.ContainsAny(node.String(), ",;?") {
		switch v := addr.URI.(type) {
		case *uri.SIP:
			addr.Params = v.Params
			v.Params = nil
		case *uri.Tel:
			addr.Params = v.Params
			v.Params = nil
		case *uri.Any:
			p, _ := url.ParseQuery(v.RawQuery)
			v.RawQuery = ""
			addr.Params = Values(p)
		}
	}
	if dnameNode := node.GetNode("display-name"); dnameNode != nil {
		addr.DisplayName = grammar.Unquote(strings.TrimSpace(dnameNode.String()))
	}
	return addr
}
