package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

type Route []EntityAddr

func (hdr Route) HeaderName() string { return "Route" }

func (hdr Route) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr Route) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr Route) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr Route) String() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	sb.WriteByte('[')
	hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr Route) Clone() Header { return cloneHeaderEntries(hdr) }

func (hdr Route) Equal(val any) bool {
	var other Route
	switch v := val.(type) {
	case Route:
		other = v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(addr1, addr2 EntityAddr) bool { return addr1.Equal(addr2) })
}

func (hdr Route) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(addr EntityAddr) bool { return !addr.IsValid() })
}

func buildFromRouteNode(node *abnf.Node) Route {
	addrNodes := node.GetNodes("route-param")
	hdr := make(Route, 0, len(addrNodes))
	for i := range addrNodes {
		hdr = append(hdr, buildFromHeaderAddrNode(addrNodes[i], "generic-param"))
	}
	return hdr
}
