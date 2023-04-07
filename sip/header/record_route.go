package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

type RecordRoute Route

func (hdr RecordRoute) HeaderName() string { return "Record-Route" }

func (hdr RecordRoute) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return Route(hdr).renderValue(w)
}

func (hdr RecordRoute) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr RecordRoute) String() string { return Route(hdr).String() }

func (hdr RecordRoute) Clone() Header {
	return RecordRoute(Route(hdr).Clone().(Route))
}

func (hdr RecordRoute) Equal(val any) bool {
	var other RecordRoute
	switch v := val.(type) {
	case RecordRoute:
		other = v
	default:
		return false
	}
	return Route(hdr).Equal(Route(other))
}

func (hdr RecordRoute) IsValid() bool { return Route(hdr).IsValid() }

func buildFromRecordRouteNode(node *abnf.Node) RecordRoute {
	addrNodes := node.GetNodes("rec-route")
	hdr := make(RecordRoute, 0, len(addrNodes))
	for i := range addrNodes {
		hdr = append(hdr, buildFromHeaderAddrNode(addrNodes[i], "generic-param"))
	}
	return hdr
}
