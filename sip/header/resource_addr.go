package header

import (
	"fmt"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/uri"
)

// ResourceAddr represents a single element in Alert-Info, Call-Info, Error-Info headers.
type ResourceAddr struct {
	URI    uri.URI
	Params Values
}

func (addr ResourceAddr) String() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	fmt.Fprint(sb, "<")
	if addr.URI != nil {
		utils.RenderTo(sb, addr.URI)
	}
	fmt.Fprint(sb, ">")
	renderHeaderParams(sb, addr.Params, false)
	return sb.String()
}

func (addr ResourceAddr) Equal(val any) bool {
	var other ResourceAddr
	switch v := val.(type) {
	case ResourceAddr:
		other = v
	case *ResourceAddr:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}

	return utils.IsEqual(addr.URI, other.URI) && compareHeaderParams(addr.Params, other.Params, map[string]bool{"purpose": true})
}

func (addr ResourceAddr) IsValid() bool {
	return utils.IsValid(addr.URI) && validateHeaderParams(addr.Params)
}

func (addr ResourceAddr) IsZero() bool { return addr.URI == nil && len(addr.Params) == 0 }

func (addr ResourceAddr) Clone() ResourceAddr {
	addr.URI = utils.Clone[uri.URI](addr.URI)
	addr.Params = addr.Params.Clone()
	return addr
}

func buildFromInfoHeaderElemNode(node *abnf.Node) ResourceAddr {
	psKey := "generic-param"
	if node.Key == "info" {
		psKey = "info-param"
	}
	return ResourceAddr{
		URI:    uri.FromABNF(utils.MustGetNode(node, "absoluteURI")),
		Params: buildFromHeaderParamNodes(node.GetNodes(psKey), nil),
	}
}
