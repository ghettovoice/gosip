package transport

import (
	"context"
	"iter"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

var DefaultAddrResolver = &AddrResolver{}

type AddrResolver struct {
	net.Resolver
}

func (*AddrResolver) RequestAddrs(_ *sip.Request) iter.Seq2[sip.TransportProto, netip.AddrPort] {
	// TODO implement me
	panic("not implemented")
}

// ResponseAddrs returns a list of remote addresses resolving them step by step,
// according to RFC 3261 Section 18.2.2, RFC 3263 Section 5 and RFC 3581 Section 4.
//
//nolint:gocognit
func (r *AddrResolver) ResponseAddrs(res *sip.Response) iter.Seq[netip.AddrPort] {
	return func(yield func(netip.AddrPort) bool) {
		viaHop := sip.FirstHeaderElem[header.Via](res.Headers, "Via")
		if viaHop == nil || !viaHop.IsValid() {
			return
		}

		if !IsReliable(viaHop.Transport) {
			// RFC 3261 Section 18.2.2, bullet 2.
			if maddr := viaHop.Params.Last("maddr"); maddr != "" {
				if addr, err := netip.ParseAddr(maddr); err == nil {
					var port uint16
					if p, ok := viaHop.Addr.Port(); ok && p > 0 {
						port = p
					} else {
						port = DefaultPort(viaHop.Transport)
					}
					yield(netip.AddrPortFrom(addr, port))
					// no fallback to RFC 3263 Section 5 is defined for "maddr" case,
					// so we stop here.
					return
				}
			}
		}

		// RFC 3261 Section 18.2.2, bullet 1 and 3.
		if recv := viaHop.Params.Last("received"); recv != "" {
			if addr, err := netip.ParseAddr(recv); err == nil {
				var port uint16
				if !IsReliable(viaHop.Transport) {
					// RFC 3581 Section 4.
					if rport := viaHop.Params.Last("rport"); rport != "" {
						if p, err := strconv.ParseUint(rport, 10, 16); err == nil {
							port = uint16(p)
						}
					}
				}
				if port == 0 {
					if p, ok := viaHop.Addr.Port(); ok && p > 0 {
						port = p
					} else {
						port = DefaultPort(viaHop.Transport)
					}
				}
				if !yield(netip.AddrPortFrom(addr, port)) {
					return
				}
			}
		}

		// RFC 3261 Section 18.2.2, bullet 4, i.e. fallback to RFC 3263 Section 5.
		if viaHop.Addr.IP() != nil {
			if addr, ok := netip.AddrFromSlice(viaHop.Addr.IP()); ok {
				var port uint16
				if p, ok := viaHop.Addr.Port(); ok && p > 0 {
					port = p
				} else {
					port = DefaultPort(viaHop.Transport)
				}
				if !yield(netip.AddrPortFrom(addr, port)) {
					return
				}
			}
		} else {
			if port, ok := viaHop.Addr.Port(); ok && port > 0 {
				if ips, err := r.LookupIP(context.TODO(), "ip", viaHop.Addr.Host()); err == nil {
					for _, ip := range ips {
						if addr, ok := netip.AddrFromSlice(ip); ok {
							if !yield(netip.AddrPortFrom(addr, port)) {
								return
							}
						}
					}
				}
			} else {
				serv := "sip"
				if IsSecured(viaHop.Transport) {
					serv = "sips"
				}

				if _, srvs, err := r.LookupSRV(context.TODO(), serv, Network(viaHop.Transport), viaHop.Addr.Host()); err == nil {
					srvs = slices.SortedFunc(slices.Values(srvs), func(e1, e2 *net.SRV) int {
						switch {
						case e1.Priority < e2.Priority:
							return -1
						case e1.Priority > e2.Priority:
							return 1
						case e1.Weight > e2.Weight:
							return -1
						case e1.Weight < e2.Weight:
							return 1
						default:
							return strings.Compare(e1.Target, e2.Target)
						}
					})
					for _, srv := range srvs {
						if ips, err := r.LookupIP(context.TODO(), "ip", srv.Target); err == nil {
							for _, ip := range ips {
								if addr, ok := netip.AddrFromSlice(ip); ok {
									if !yield(netip.AddrPortFrom(addr, srv.Port)) {
										return
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

func RequestAddrs(req *sip.Request) iter.Seq2[sip.TransportProto, netip.AddrPort] {
	return DefaultAddrResolver.RequestAddrs(req)
}

func ResponseAddrs(res *sip.Response) iter.Seq[netip.AddrPort] {
	return DefaultAddrResolver.ResponseAddrs(res)
}
