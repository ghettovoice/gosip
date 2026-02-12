package sip

import (
	"context"
	"iter"
	"net"
	"net/netip"
	"slices"
	"strings"

	"github.com/ghettovoice/gosip/dns"
	"github.com/ghettovoice/gosip/header"
)

// DNSResolver is used to resolve the message destination address.
type DNSResolver interface {
	// LookupIP looks up the IP address for the given host.
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
	// LookupSRV looks up the SRV record for the given service and protocol.
	LookupSRV(ctx context.Context, service, proto, host string) ([]*dns.SRV, error)
	// LookupNAPTR looks up the NAPTR record for the given host.
	LookupNAPTR(ctx context.Context, host string) ([]*dns.NAPTR, error)
}

// func RequestAddrs(
// 	ctx context.Context,
// 	uri URI,
// 	tpsMeta map[TransportProto]TransportMetadata,
// 	dns DNSResolver,
// ) iter.Seq2[TransportProto, netip.AddrPort] {
// 	return func(yield func(TransportProto, netip.AddrPort) bool) {
// 		// TODO: implement
// 	}
// }

// ResponseAddrs returns the list of addresses to which the response should be sent.
// It implements the logic defined in RFC 3261 Section 18.2.2. and RFC 3263 Section 5.
// The response must contain a "Via" header field and the transport protocol must match
// the transport protocol in the topmost "Via" header field.
//
//nolint:gocognit
func ResponseAddrs(
	ctx context.Context,
	via header.ViaHop,
	tpMeta TransportMetadata,
	dnsRslvr DNSResolver,
) iter.Seq2[TransportProto, netip.AddrPort] {
	return func(yield func(TransportProto, netip.AddrPort) bool) {
		if !via.IsValid() || !via.Transport.Equal(tpMeta.Proto) {
			return
		}

		if !tpMeta.Reliable {
			// RFC 3261 Section 18.2.2, bullet 2.
			if maddr, ok := via.MAddr(); ok {
				// maddr can be host name or IP address, need to lookup IP addresses
				if ips, err := dnsRslvr.LookupIP(ctx, "ip", maddr); err == nil {
					for _, ip := range ips {
						if addr, ok := netip.AddrFromSlice(ip); ok {
							addr = addr.Unmap()

							var port uint16
							if p, ok := via.Addr.Port(); ok {
								port = p
							} else {
								port = tpMeta.DefaultPort
							}

							if addrPort := netip.AddrPortFrom(addr, port); addrPort.IsValid() && !yield(via.Transport, addrPort) {
								return
							}
						}
					}
				}
				// no fallback to RFC 3263 Section 5 is defined for "maddr" case,
				// so we stop here.
				return
			}
		}

		// RFC 3261 Section 18.2.2, bullet 1 and 3.
		if addr, ok := via.Received(); ok {
			var port uint16
			if !tpMeta.Reliable {
				// RFC 3581 Section 4.
				if p, ok := via.RPort(); ok {
					port = p
				}
			}
			if port == 0 {
				if p, ok := via.Addr.Port(); ok {
					port = p
				} else {
					port = tpMeta.DefaultPort
				}
			}

			if addrPort := netip.AddrPortFrom(addr, port); addrPort.IsValid() && !yield(via.Transport, addrPort) {
				return
			}
		}

		// RFC 3261 Section 18.2.2, bullet 4, i.e. fallback to RFC 3263 Section 5.
		if via.Addr.IP() != nil {
			if addr, ok := netip.AddrFromSlice(via.Addr.IP()); ok {
				addr = addr.Unmap()

				var port uint16
				if p, ok := via.Addr.Port(); ok {
					port = p
				} else {
					port = tpMeta.DefaultPort
				}

				if addrPort := netip.AddrPortFrom(addr, port); addrPort.IsValid() && !yield(via.Transport, addrPort) {
					return
				}
			}
			return
		}

		if port, ok := via.Addr.Port(); ok {
			if ips, err := dnsRslvr.LookupIP(ctx, "ip", via.Addr.Host()); err == nil {
				for _, ip := range ips {
					if addr, ok := netip.AddrFromSlice(ip); ok {
						addr = addr.Unmap()

						if addrPort := netip.AddrPortFrom(addr, port); addrPort.IsValid() && !yield(via.Transport, addrPort) {
							return
						}
					}
				}
			}
			return
		}

		// RFC 3263 Section 5.
		serv := "sip"
		if tpMeta.Secured {
			serv = "sips"
		}

		if srvs, err := dnsRslvr.LookupSRV(ctx, serv, tpMeta.Network, via.Addr.Host()); err == nil {
			srvs = slices.SortedFunc(slices.Values(srvs), func(e1, e2 *dns.SRV) int {
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
				if ips, err := dnsRslvr.LookupIP(ctx, "ip", srv.Target); err == nil {
					for _, ip := range ips {
						if addr, ok := netip.AddrFromSlice(ip); ok {
							addr = addr.Unmap()

							if addrPort := netip.AddrPortFrom(addr, srv.Port); addrPort.IsValid() && !yield(via.Transport, addrPort) {
								return
							}
						}
					}
				}
			}
		}
	}
}
