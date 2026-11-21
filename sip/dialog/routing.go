package dialog

import (
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// recRouteSet extracts the message's Record-Route header set as a flat
// list of owned route hops in wire order, reversing the whole list when
// reverse is set (the UAC direction, RFC 3261 section 12.1.2). Every
// Record-Route field must be a nonempty header.RecordRoute and every hop
// must carry a usable SIP or SIPS URI and valid parameters; a malformed
// entry fails the whole set so no partial routes can be installed.
func recRouteSet(hdrs sip.Headers, reverse bool) ([]header.RouteHop, error) {
	for _, h := range hdrs.Get("Record-Route") {
		rr, ok := h.(header.RecordRoute)
		if !ok {
			return nil, newInvalidMsgErr("invalid Record-Route header type %T", h)
		}
		if len(rr) == 0 {
			return nil, newInvalidMsgErr("empty Record-Route header")
		}
	}

	var hops []header.RouteHop
	for hop := range hdrs.RecordRoutes() {
		u, ok := hop.URI.(*sip.URI)
		if !ok || u == nil || !u.IsValid() || !hop.IsValid() {
			return nil, newInvalidMsgErr("unusable Record-Route hop")
		}
		hops = append(hops, hop.Clone())
	}
	if !reverse {
		return hops, nil
	}
	for i, j := 0, len(hops)-1; i < j; i, j = i+1, j-1 {
		hops[i], hops[j] = hops[j], hops[i]
	}
	return hops, nil
}

// cloneRouteSet returns a detached copy of the route set, or nil when the
// set is empty.
func cloneRouteSet(routes []header.RouteHop) []header.RouteHop {
	if len(routes) == 0 {
		return nil
	}
	cloned := make([]header.RouteHop, len(routes))
	for i := range routes {
		cloned[i] = routes[i].Clone()
	}
	return cloned
}
