package sip

import (
	"context"
	"iter"
	"net"
	"net/netip"
	"regexp"
	"strings"

	"github.com/ghettovoice/gosip/dns"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
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

func lookupIP(ctx context.Context, dnsResolver DNSResolver, host string) ([]net.IP, error) {
	return dnsResolver.LookupIP(ctx, "ip", host)
}

func lookupSRV(
	ctx context.Context,
	dnsRslvr DNSResolver,
	network, name string,
	secured bool,
) ([]*dns.SRV, error) {
	service := "sip"
	if secured {
		service = "sips"
	}

	return dnsRslvr.LookupSRV(ctx, service, network, name)
}

var transpNAPTRRegex = regexp.MustCompile(`^(SIPS?)\+D2([A-Z]{1})$`)

func lookupNAPTR(
	ctx context.Context,
	dnsRslvr DNSResolver,
	metaPrvdr TransportMetadataProvider,
	host string,
	secured bool,
) ([]*dns.NAPTR, error) {
	recs, err := dnsRslvr.LookupNAPTR(ctx, host)
	if err != nil {
		return nil, err
	}

	relevant := make([]*dns.NAPTR, 0, len(recs))
	for _, rec := range recs {
		match := transpNAPTRRegex.FindStringSubmatch(strings.ToUpper(rec.Service))
		if len(match) == 0 || secured && match[1] != "SIPS" {
			continue
		}

		if !metaPrvdr.MetadataByNAPTRService(rec.Service).IsValid() {
			continue
		}

		relevant = append(relevant, rec)
	}

	return relevant, nil
}

// applyNAPTRRegexp applies the NAPTR substitution expression to input as defined in RFC 3403 Section 4.
// The expression format is: <delim><ere><delim><replacement><delim><flags>
// where <delim> is the first character of the expression.
// Returns the result of the substitution, or replacement if expr is empty.
func applyNAPTRRegexp(expr, replacement, input string) string {
	if expr == "" {
		return replacement
	}

	if len(expr) < 2 {
		return replacement
	}

	delim := string(expr[0])
	parts := strings.SplitN(expr[1:], delim, 3)

	if len(parts) < 2 {
		return replacement
	}

	ere, repl := parts[0], parts[1]

	re, err := regexp.Compile(ere)
	if err != nil {
		return replacement
	}

	return re.ReplaceAllString(input, repl)
}

func selectTranspMeta(metaPrvd TransportMetadataProvider, proto TransportProto, secured bool) TransportMetadata {
	if proto.IsValid() {
		return metaPrvd.MetadataByProto(proto)
	}

	for m := range metaPrvd.AllMetadata() {
		if m.Secured() == secured {
			return m
		}
	}

	return TransportMetadata{}
}

func yieldAddrPort(
	yield func(TransportProto, netip.AddrPort) bool,
	proto TransportProto,
	addr netip.Addr,
	port uint16,
) bool {
	addrPort := netip.AddrPortFrom(addr, port)
	if !addrPort.IsValid() {
		return true
	}

	return yield(proto, addrPort)
}

func yieldFromIPs(
	ctx context.Context,
	yield func(TransportProto, netip.AddrPort) bool,
	dnsRslvr DNSResolver,
	proto TransportProto,
	host string,
	port uint16,
) bool {
	ips, err := lookupIP(ctx, dnsRslvr, host)
	if err != nil {
		return true
	}

	for _, ip := range ips {
		if addr, ok := netip.AddrFromSlice(ip); ok {
			if !yieldAddrPort(yield, proto, addr, port) {
				return false
			}
		}
	}

	return true
}

func yieldFromSRVs(
	ctx context.Context,
	yield func(TransportProto, netip.AddrPort) bool,
	dnsRslvr DNSResolver,
	meta TransportMetadata,
	host string,
) bool {
	srvRecs, err := lookupSRV(ctx, dnsRslvr, meta.Network, host, meta.Secured())
	if err != nil {
		return true
	}

	for _, srv := range srvRecs {
		if !yieldFromIPs(ctx, yield, dnsRslvr, meta.Proto, srv.Target, srv.Port) {
			return false
		}
	}

	return true
}

func yieldFromNAPTRs(
	ctx context.Context,
	yield func(TransportProto, netip.AddrPort) bool,
	dnsRslvr DNSResolver,
	metaPrvdr TransportMetadataProvider,
	reqURI *uri.SIP,
) bool {
	naptrRecs, err := lookupNAPTR(ctx, dnsRslvr, metaPrvdr, reqURI.Addr.Host(), reqURI.Secured)
	if err != nil || len(naptrRecs) == 0 {
		return true
	}

	for _, naptr := range naptrRecs {
		meta := metaPrvdr.MetadataByNAPTRService(naptr.Service)
		if !meta.IsValid() {
			continue
		}

		host := applyNAPTRRegexp(naptr.Regexp, naptr.Replacement, reqURI.Addr.Host())
		if !yieldFromSRVs(ctx, yield, dnsRslvr, meta, host) {
			return false
		}
	}

	return true
}

func yieldAllTranspSRVs(
	ctx context.Context,
	yield func(TransportProto, netip.AddrPort) bool,
	dnsRslvr DNSResolver,
	metaPrvd TransportMetadataProvider,
	reqURI *uri.SIP,
) {
	for meta := range metaPrvd.AllMetadata() {
		if meta.Secured() != reqURI.Secured {
			continue
		}

		if !yieldFromSRVs(ctx, yield, dnsRslvr, meta, reqURI.Addr.Host()) {
			return
		}
	}
}

func yieldFromReceived(
	yield func(TransportProto, netip.AddrPort) bool,
	resVia header.ViaHop,
	meta TransportMetadata,
) (stop bool) {
	addr, ok := resVia.Received()
	if !ok {
		return false // received not present, continue to next case
	}

	port := resolveResPort(resVia, meta)

	return !yieldAddrPort(yield, resVia.Transport, addr, port) // received present, return indicates whether to continue
}

func yieldFromMAddr(
	ctx context.Context,
	yield func(TransportProto, netip.AddrPort) bool,
	dnsRslvr DNSResolver,
	resVia header.ViaHop,
	meta TransportMetadata,
) (stop bool) {
	maddr, ok := resVia.MAddr()
	if !ok {
		return false // maddr not present, continue to next case
	}

	ips, err := lookupIP(ctx, dnsRslvr, maddr)
	if err != nil {
		return true // maddr present but lookup failed, no fallback per RFC
	}

	port := resolveResPort(resVia, meta)

	for _, ip := range ips {
		if addr, ok := netip.AddrFromSlice(ip); ok {
			if !yieldAddrPort(yield, resVia.Transport, addr, port) {
				return true // maddr present, iteration stopped by yield
			}
		}
	}

	return true // maddr present and processed, no fallback
}

func resolveTranspPort(reqURI *uri.SIP, meta TransportMetadata) uint16 {
	if p, ok := reqURI.Addr.Port(); ok {
		return p
	}
	return meta.DefaultPort
}

func resolveResPort(resVia header.ViaHop, meta TransportMetadata) uint16 {
	// For unreliable transports, check rport first (RFC 3581 Section 4)
	if !meta.Reliable() {
		if p, ok := resVia.RPort(); ok {
			return p
		}
	}

	if p, ok := resVia.Addr.Port(); ok {
		return p
	}

	return meta.DefaultPort
}

// RemoteElementLocator used to lookup remote element addresses from SIP messages.
type RemoteElementLocator struct {
	DNSResolver DNSResolver
}

func (lctr *RemoteElementLocator) dnsRslvr() DNSResolver {
	if lctr.DNSResolver == nil {
		return dns.DefaultResolver()
	}
	return lctr.DNSResolver
}

// LookupRequestAddrs returns the list of addresses that should be tried to send the request.
// It implements the logic defined in RFC 3263 Section 4.
func (lctr *RemoteElementLocator) LookupRequestAddrs(
	ctx context.Context,
	reqURI *uri.SIP,
	metaPrvdr TransportMetadataProvider,
) iter.Seq2[TransportProto, netip.AddrPort] {
	return func(yield func(TransportProto, netip.AddrPort) bool) {
		if lctr == nil || reqURI == nil || !reqURI.IsValid() {
			return
		}

		proto, _ := reqURI.Transport()

		// Case 1: IP address is directly available in URI
		if reqURI.Addr.IP() != nil {
			meta := selectTranspMeta(metaPrvdr, proto, reqURI.Secured)
			if !meta.IsValid() {
				return
			}

			port := resolveTranspPort(reqURI, meta)
			if addr, ok := netip.AddrFromSlice(reqURI.Addr.IP()); ok {
				yieldAddrPort(yield, meta.Proto, addr, port)
			}

			return
		}

		// Case 2: Port is specified, need to lookup IP
		if port, ok := reqURI.Addr.Port(); ok {
			meta := selectTranspMeta(metaPrvdr, proto, reqURI.Secured)
			if !meta.IsValid() {
				return
			}

			yieldFromIPs(ctx, yield, lctr.dnsRslvr(), meta.Proto, reqURI.Addr.Host(), port)

			return
		}

		// Case 3: Transport protocol is specified
		if proto.IsValid() {
			meta := metaPrvdr.MetadataByProto(proto)
			if !meta.IsValid() {
				return
			}

			if !yieldFromSRVs(ctx, yield, lctr.dnsRslvr(), meta, reqURI.Addr.Host()) {
				return
			}

			yieldFromIPs(ctx, yield, lctr.dnsRslvr(), meta.Proto, reqURI.Addr.Host(), meta.DefaultPort)

			return
		}

		// Case 4: Try NAPTR lookup
		if !yieldFromNAPTRs(ctx, yield, lctr.dnsRslvr(), metaPrvdr, reqURI) {
			return
		}

		// Case 5: Try SRV for all matching transports
		yieldAllTranspSRVs(ctx, yield, lctr.dnsRslvr(), metaPrvdr, reqURI)

		// Case 6: Fallback to default transport with A/AAAA lookup
		meta := selectTranspMeta(metaPrvdr, "", reqURI.Secured)
		if !meta.IsValid() {
			return
		}

		yieldFromIPs(ctx, yield, lctr.dnsRslvr(), meta.Proto, reqURI.Addr.Host(), meta.DefaultPort)
	}
}

// LookupResponseAddrs returns the list of addresses that should be tried to send the response.
// It implements the logic defined in RFC 3261 Section 18.2.2. and RFC 3263 Section 5.
func (lctr *RemoteElementLocator) LookupResponseAddrs(
	ctx context.Context,
	resVia header.ViaHop,
	metaPrvdr TransportMetadataProvider,
) iter.Seq2[TransportProto, netip.AddrPort] {
	return func(yield func(TransportProto, netip.AddrPort) bool) {
		if lctr == nil || !resVia.IsValid() {
			return
		}

		meta := metaPrvdr.MetadataByProto(resVia.Transport)
		if !meta.IsValid() {
			return
		}

		// RFC 3261 Section 18.2.2, bullet 2: maddr parameter
		if yieldFromMAddr(ctx, yield, lctr.dnsRslvr(), resVia, meta) {
			return // no fallback to RFC 3263 Section 5 for "maddr" case
		}

		// RFC 3261 Section 18.2.2, bullet 1 and 3: received parameter
		if yieldFromReceived(yield, resVia, meta) {
			return
		}

		// RFC 3261 Section 18.2.2, bullet 4: IP in sent-by, i.e. fallback to RFC 3263 Section 5
		if resVia.Addr.IP() != nil {
			if addr, ok := netip.AddrFromSlice(resVia.Addr.IP()); ok {
				port := resolveResPort(resVia, meta)
				yieldAddrPort(yield, resVia.Transport, addr, port)
			}

			return
		}

		// Port specified, need to lookup IP
		if port, ok := resVia.Addr.Port(); ok {
			yieldFromIPs(ctx, yield, lctr.dnsRslvr(), resVia.Transport, resVia.Addr.Host(), port)
			return
		}

		// RFC 3263 Section 5: SRV lookup fallback
		yieldFromSRVs(ctx, yield, lctr.dnsRslvr(), meta, resVia.Addr.Host())
	}
}

var defRmtElemLocator = &RemoteElementLocator{}

func DefaultRemoteElementLocator() *RemoteElementLocator { return defRmtElemLocator }

func LookupRequestAddrs(
	ctx context.Context,
	reqURI *uri.SIP,
	metaPrvdr TransportMetadataProvider,
) iter.Seq2[TransportProto, netip.AddrPort] {
	return defRmtElemLocator.LookupRequestAddrs(ctx, reqURI, metaPrvdr)
}

func LookupResponseAddrs(
	ctx context.Context,
	resVia header.ViaHop,
	metaPrvdr TransportMetadataProvider,
) iter.Seq2[TransportProto, netip.AddrPort] {
	return defRmtElemLocator.LookupResponseAddrs(ctx, resVia, metaPrvdr)
}

type RemoteServerLocator interface {
	LookupRequestAddrs(
		ctx context.Context,
		reqURI *uri.SIP,
		metaPrvdr TransportMetadataProvider,
	) iter.Seq2[TransportProto, netip.AddrPort]
}

type RemoteClientLocator interface {
	LookupResponseAddrs(
		ctx context.Context,
		resVia header.ViaHop,
		metaPrvdr TransportMetadataProvider,
	) iter.Seq2[TransportProto, netip.AddrPort]
}
