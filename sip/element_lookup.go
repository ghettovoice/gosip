package sip

import (
	"cmp"
	"context"
	"fmt"
	"iter"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/ghettovoice/gosip/dns"
	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip/header"
)

// lookupIP resolves host to all A and AAAA addresses returned by the resolver.
//
// The caller decides whether an IP lookup is a terminal resolution step or a
// fallback after service-record processing, so this function does not apply
// any RFC 3263 ordering policy itself.
func lookupIP(ctx context.Context, dnsResolver DNSResolver, host string) ([]net.IP, error) {
	return errors.Wrap2(dnsResolver.LookupIP(ctx, "ip", host))
}

// lookupSRV queries the SIP or SIPS service for the requested network.
//
// The service name is selected from the URI security scheme, while the network
// is supplied by the transport metadata (for example, "udp" or "tcp").
func lookupSRV(ctx context.Context, dnsRslvr DNSResolver, network, name string, secured bool) ([]*dns.SRV, error) {
	service := "sip"
	if secured {
		service = "sips"
	}
	return errors.Wrap2(dnsRslvr.LookupSRV(ctx, service, network, name))
}

// sortSRVStable orders SRV records deterministically for stateless forwarding.
//
// RFC 3263 Section 4.4 requires a stateless proxy to use a deterministic
// order, because it cannot keep transaction state that identifies the server
// selected for retransmissions.
// The order is Priority ascending, Weight descending, Target ascending, and
// Port ascending.
// Stateful callers retain the resolver's normal RFC 2782 ordering unless
// this option is enabled.
func sortSRVStable(recs []*dns.SRV) {
	slices.SortStableFunc(recs, func(a, b *dns.SRV) int {
		if c := cmp.Compare(a.Priority, b.Priority); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Weight, a.Weight); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Target, b.Target); c != 0 {
			return c
		}
		return cmp.Compare(a.Port, b.Port)
	})
}

var tpNAPTRRegex = regexp.MustCompile(`^(SIPS?)\+D2([A-Z]{1})$`)

// lookupNAPTR keeps only NAPTR services that describe a transport known to
// the metadata provider and that are compatible with the URI security scheme.
//
// The DNS resolver is responsible for returning NAPTR records in RFC 3403
// order; this function only filters records that this element cannot use.
func lookupNAPTR(
	ctx context.Context,
	dnsRslvr DNSResolver,
	metaPrvdr TransportMetadataProvider,
	host string,
	secured bool,
) ([]*dns.NAPTR, error) {
	recs, err := dnsRslvr.LookupNAPTR(ctx, host)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	relevant := make([]*dns.NAPTR, 0, len(recs))
	for _, rec := range recs {
		match := tpNAPTRRegex.FindStringSubmatch(strings.ToUpper(rec.Service))
		if len(match) == 0 || secured && match[1] != "SIPS" {
			continue
		}
		if m, ok := metaPrvdr.TransportMetadataByNAPTRService(rec.Service); !ok || !m.IsValid() {
			continue
		}
		relevant = append(relevant, rec)
	}
	return relevant, nil
}

// applyNAPTRRegexp applies the NAPTR substitution expression to input as defined in
// RFC 3403 Section 4.
//
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

// selectTranspMeta selects transport metadata for a URI's security scheme.
//
// An explicitly requested transport is used only when it exists and has the
// required security properties.
// Otherwise, the first registered transport matching the scheme is selected.
// In particular, a SIPS target must not be silently downgraded to an unsecured transport.
func selectTranspMeta(metaPrvd TransportMetadataProvider, tp TransportProto, secured bool) TransportMetadata {
	if tp.IsValid() {
		if m, ok := metaPrvd.TransportMetadataByProto(tp); ok && m.IsValid() && m.Secured() == secured {
			return m
		}
	}

	for m := range metaPrvd.AllTransportMetadata() {
		if m.IsValid() && m.Secured() == secured {
			return m
		}
	}

	return TransportMetadata{}
}

// yieldAddrPort converts an IP/port pair into a resolution candidate.
//
// Returning false from yield stops the current resolution branch and is
// propagated to the caller as the stopped result.
func yieldAddrPort(
	yield func(ResolvedAddr) bool,
	tp TransportProto,
	addr netip.Addr,
	port uint16,
	fromDNS bool,
) bool {
	addrPort := netip.AddrPortFrom(addr, port)
	if !addrPort.IsValid() {
		return true
	}

	return yield(ResolvedAddr{
		Transport: tp,
		Addr:      addrPort,
		FromDNS:   fromDNS,
	})
}

// yieldFromIPs emits one candidate for each A/AAAA result.
//
// The fromDNS argument describes the resolution source used for transport policy:
// it is true for IPs reached through an SRV/NAPTR service record and false for a
// direct URI or an A/AAAA fallback.
func yieldFromIPs(
	ctx context.Context,
	yield func(ResolvedAddr) bool,
	dnsRslvr DNSResolver,
	tp TransportProto,
	host string,
	port uint16,
	fromDNS bool,
) bool {
	ips, err := lookupIP(ctx, dnsRslvr, host)
	if err != nil {
		return true
	}

	for _, ip := range ips {
		if addr, ok := netip.AddrFromSlice(ip); ok {
			if !yieldAddrPort(yield, tp, addr, port, fromDNS) {
				return false
			}
		}
	}
	return true
}

// yieldFromSRVs emits candidates for one SRV lookup.
//
// found reports that at least one SRV record was present. stopped reports
// that the consumer stopped the iterator; it is distinct from found because
// an empty lookup must allow the caller to apply the next RFC 3263 fallback.
// A successful SRV lookup takes precedence over A/AAAA lookup of the original
// host, even when resolving an SRV target produces no usable IP address.
func yieldFromSRVs(
	ctx context.Context,
	yield func(ResolvedAddr) bool,
	dnsRslvr DNSResolver,
	tp TransportMetadata,
	host string,
	opts LookupMessageAddrsOptions,
) (found, stopped bool) {
	srvs, err := lookupSRV(ctx, dnsRslvr, tp.Network, host, tp.Secured())
	if err != nil {
		return false, false
	}

	if opts.StableDNSRecordsOrder {
		sortSRVStable(srvs)
	}

	found = len(srvs) > 0
	for _, srv := range srvs {
		if !yieldFromIPs(ctx, yield, dnsRslvr, tp.Proto, srv.Target, srv.Port, true) {
			return found, true
		}
	}
	return found, false
}

// yieldFromNAPTRs follows relevant NAPTR records to their SRV targets.
//
// found means that at least one NAPTR-selected SRV branch contained SR records.
// stopped means that the consumer stopped while those candidates were being emitted.
// A found NAPTR/SRV branch prevents the locator from querying unrelated transports
// for the original host.
func yieldFromNAPTRs(
	ctx context.Context,
	yield func(ResolvedAddr) bool,
	dnsRslvr DNSResolver,
	metaPrvdr TransportMetadataProvider,
	uri *URI,
	trgt string,
	opts LookupMessageAddrsOptions,
) (found, stopped bool) {
	naptrs, err := lookupNAPTR(ctx, dnsRslvr, metaPrvdr, trgt, uri.Secured)
	if err != nil {
		return false, false
	}

	for _, naptr := range naptrs {
		tp, ok := metaPrvdr.TransportMetadataByNAPTRService(naptr.Service)
		if !ok || !tp.IsValid() {
			continue
		}

		host := applyNAPTRRegexp(naptr.Regexp, naptr.Replacement, trgt)
		srvFound, srvStopped := yieldFromSRVs(ctx, yield, dnsRslvr, tp, host, opts)
		found = found || srvFound
		if srvStopped {
			return found, true
		}
	}
	return found, false
}

// yieldAllTranspSRVs performs the RFC 3263 fallback SRV lookups when NAPTR
// did not select a service.
//
// Only transports with the same security level as the URI are considered.
// found prevents the later default A/AAAA fallback; stopped preserves early
// termination requested by the consumer.
func yieldAllTranspSRVs(
	ctx context.Context,
	yield func(ResolvedAddr) bool,
	dnsRslvr DNSResolver,
	metaPrvd TransportMetadataProvider,
	uri *URI,
	trgt string,
	opts LookupMessageAddrsOptions,
) (found, stopped bool) {
	for tp := range metaPrvd.AllTransportMetadata() {
		if tp.Secured() != uri.Secured {
			continue
		}

		srvFound, srvStopped := yieldFromSRVs(ctx, yield, dnsRslvr, tp, trgt, opts)
		found = found || srvFound
		if srvStopped {
			return found, true
		}
	}
	return found, false
}

func yieldFromReceived(yield func(ResolvedAddr) bool, via header.ViaHop, tp TransportMetadata) (stop bool) {
	addr, ok := via.Received()
	if !ok {
		return false // received not present, continue to next case
	}
	// received present, return indicates whether to continue
	return !yieldAddrPort(yield, via.Transport, addr, resolveViaPort(via, tp), false)
}

func yieldFromMAddr(
	ctx context.Context,
	yield func(ResolvedAddr) bool,
	dnsRslvr DNSResolver,
	via header.ViaHop,
	tp TransportMetadata,
) (stop bool) {
	maddr, ok := via.MAddr()
	if !ok {
		return false // maddr not present, continue to next case
	}

	ips, err := lookupIP(ctx, dnsRslvr, maddr.String())
	if err != nil {
		return true // maddr present but lookup failed, no fallback per RFC
	}

	port := resolveViaPort(via, tp)

	for _, ip := range ips {
		if addr, ok := netip.AddrFromSlice(ip); ok {
			if !yieldAddrPort(yield, via.Transport, addr, port, false) {
				return true // maddr present, iteration stopped by yield
			}
		}
	}
	return true // maddr present and processed, no fallback
}

func resolveTranspPort(uri *URI, tp TransportMetadata) uint16 {
	if p, ok := uri.Addr.Port(); ok {
		return p
	}
	return tp.DefaultPort
}

func resolveViaPort(via header.ViaHop, meta TransportMetadata) uint16 {
	// For unreliable transports, check rport first (RFC 3581 Section 4)
	if !meta.Reliable() {
		if p, ok := via.RPort(); ok {
			return p
		}
	}

	if p, ok := via.Addr.Port(); ok {
		return p
	}

	return meta.DefaultPort
}

// LookupMessageAddrsOptions controls ordering of DNS candidates.
type LookupMessageAddrsOptions struct {
	// StableDNSRecordsOrder makes SRV ordering deterministic within equal
	// priority groups.
	// It is intended for stateless forwarding, where the same request
	// must produce the same candidate order across retransmissions.
	// It is disabled by default.
	StableDNSRecordsOrder bool `json:"stable_dns_records_order"`
}

// ResolvedAddr is one concrete transport destination produced by request or
// response routing.
type ResolvedAddr struct {
	// Transport is the SIP transport to use for Addr.
	Transport TransportProto
	// Addr is the resolved IP address and port.
	Addr netip.AddrPort
	// FromDNS reports that the candidate came from an SRV/NAPTR service record.
	// It is false for numeric URI targets and A/AAAA fallback candidates.
	// The request sender uses this distinction when deciding whether a large UDP
	// request may switch transport immediately or must try another candidate.
	FromDNS bool
}

func (a ResolvedAddr) IsZero() bool { return a == ResolvedAddr{} }

func (a ResolvedAddr) IsValid() bool { return a.Transport.IsValid() && a.Addr.IsValid() }

func (a ResolvedAddr) String() string { return fmt.Sprintf("%s:%s", a.Transport, a.Addr) }

// RemoteServerLocator resolves a request target into ordered next-hop candidates.
//
// The caller owns attempt execution and decides when to stop consuming the sequence.
type RemoteServerLocator interface {
	LookupRequestAddrs(ctx context.Context, uri *URI, opts ...LookupMessageAddrsOptions) iter.Seq[ResolvedAddr]
}

// RemoteClientLocator resolves the destination for a response using Via routing rules.
type RemoteClientLocator interface {
	LookupResponseAddrs(ctx context.Context, via header.ViaHop, opts ...LookupMessageAddrsOptions) iter.Seq[ResolvedAddr]
}

// DNSResolver is used to resolve SIP service and host addresses.
type DNSResolver interface {
	// LookupIP looks up the IP address for the given host.
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
	// LookupSRV looks up the SRV record for the given service and protocol.
	LookupSRV(ctx context.Context, service, proto, host string) ([]*dns.SRV, error)
	// LookupNAPTR looks up the NAPTR record for the given host.
	LookupNAPTR(ctx context.Context, host string) ([]*dns.NAPTR, error)
}

// RemoteElementLocator implements RFC 3263 request resolution and RFC 3261
// response routing using an injected DNS resolver and transport metadata.
type RemoteElementLocator struct {
	DNSResolver      DNSResolver
	MetadataProvider TransportMetadataProvider
}

var (
	_ RemoteServerLocator = (*RemoteElementLocator)(nil)
	_ RemoteClientLocator = (*RemoteElementLocator)(nil)
)

func (lctr *RemoteElementLocator) dnsRslvr() DNSResolver {
	if lctr.DNSResolver == nil {
		return dns.DefaultResolver()
	}
	return lctr.DNSResolver
}

func (lctr *RemoteElementLocator) metaPrvdr() TransportMetadataProvider {
	if lctr.MetadataProvider == nil {
		return defTranspMetaProvider
	}
	return lctr.MetadataProvider
}

// LookupRequestAddrs returns ordered next-hop candidates for a request.
//
// It implements RFC 3263 Section 4 without sending the request or creating a
// transaction.
// The caller must try candidates in the emitted order and create a new transaction
// with a new branch for each failed stateful attempt.
//
// Resolution proceeds in the RFC-defined order:
//   - use a numeric target directly;
//   - use A/AAAA when the URI has an explicit port;
//   - use SRV for an explicitly selected transport;
//   - use relevant NAPTR-selected SRV records;
//   - use SRV records for all matching transports when NAPTR yields no SRV records;
//   - use the default transport with A/AAAA as the final fallback.
//
// Candidates derived from SRV/NAPTR records have [ResolvedAddr.FromDNS] set
// so the sender can preserve the service-record-selected transport.
// Numeric targets and A/AAAA fallback candidates leave it unset and may be eligible
// for size-driven transport switching.
//
//nolint:gocognit
func (lctr *RemoteElementLocator) LookupRequestAddrs(
	ctx context.Context,
	uri *URI,
	opts ...LookupMessageAddrsOptions,
) iter.Seq[ResolvedAddr] {
	return func(yield func(ResolvedAddr) bool) {
		if !uri.IsValid() {
			return
		}

		dnsRslvr := lctr.dnsRslvr()
		metaPrvdr := lctr.metaPrvdr()

		var (
			trgtIP  net.IP
			trgtHst string
		)
		if maddr, ok := uri.MAddr(); ok && maddr.IsValid() {
			trgtIP = maddr.IP()
			trgtHst = maddr.Host()
		} else {
			trgtIP = uri.Addr.IP()
			trgtHst = uri.Addr.Host()
		}

		tpProto, _ := uri.Transport()

		// Case 1: IP address is directly available in URI
		if trgtIP != nil {
			tpMeta := selectTranspMeta(metaPrvdr, tpProto, uri.Secured)
			if !tpMeta.IsValid() {
				return
			}

			port := resolveTranspPort(uri, tpMeta)

			if addr, ok := netip.AddrFromSlice(trgtIP); ok {
				yieldAddrPort(yield, tpMeta.Proto, addr, port, false)
			}
			return
		}

		// Case 2: Port is specified, need to lookup IP
		if port, ok := uri.Addr.Port(); ok {
			tpMeta := selectTranspMeta(metaPrvdr, tpProto, uri.Secured)
			if !tpMeta.IsValid() {
				return
			}

			yieldFromIPs(ctx, yield, dnsRslvr, tpMeta.Proto, trgtHst, port, false)
			return
		}

		lookupOpts := util.LastSliceElemOr(opts, LookupMessageAddrsOptions{})

		// Case 3: Transport protocol is specified
		if tpProto.IsValid() {
			tpMeta, ok := metaPrvdr.TransportMetadataByProto(tpProto)
			if !ok || !tpMeta.IsValid() {
				return
			}

			found, stopped := yieldFromSRVs(ctx, yield, dnsRslvr, tpMeta, trgtHst, lookupOpts)
			if found || stopped {
				return
			}

			yieldFromIPs(ctx, yield, dnsRslvr, tpMeta.Proto, trgtHst, tpMeta.DefaultPort, false)
			return
		}

		// Case 4: Try NAPTR lookup
		found, stopped := yieldFromNAPTRs(ctx, yield, dnsRslvr, metaPrvdr, uri, trgtHst, lookupOpts)
		if found || stopped {
			return
		}

		// Case 5: Try SRV for all matching transports
		found, stopped = yieldAllTranspSRVs(ctx, yield, dnsRslvr, metaPrvdr, uri, trgtHst, lookupOpts)
		if found || stopped {
			return
		}

		// Case 6: Fallback to default transport with A/AAAA lookup
		tpMeta := selectTranspMeta(metaPrvdr, "", uri.Secured)
		if !tpMeta.IsValid() {
			return
		}

		yieldFromIPs(ctx, yield, dnsRslvr, tpMeta.Proto, trgtHst, tpMeta.DefaultPort, false)
	}
}

// LookupResponseAddrs returns ordered destinations for sending a response.
//
// It implements RFC 3261 Section 18.2.2: maddr, received/rport, sent-by,
// and finally RFC 3263 Section 5 SRV resolution.
// A response resolver never changes the SIP transaction or response; it only
// supplies transport targets.
func (lctr *RemoteElementLocator) LookupResponseAddrs(
	ctx context.Context,
	via header.ViaHop,
	opts ...LookupMessageAddrsOptions,
) iter.Seq[ResolvedAddr] {
	return func(yield func(ResolvedAddr) bool) {
		if !via.IsValid() {
			return
		}

		dnsRslvr := lctr.dnsRslvr()
		metaPrvdr := lctr.metaPrvdr()

		tpMeta, ok := metaPrvdr.TransportMetadataByProto(via.Transport)
		if !ok || !tpMeta.IsValid() {
			return
		}

		// RFC 3261 Section 18.2.2, bullet 2: maddr parameter
		if yieldFromMAddr(ctx, yield, dnsRslvr, via, tpMeta) {
			return // no fallback to RFC 3263 Section 5 for "maddr" case
		}

		// RFC 3261 Section 18.2.2, bullet 1 and 3: received parameter
		if yieldFromReceived(yield, via, tpMeta) {
			return
		}

		// RFC 3261 Section 18.2.2, bullet 4: IP in sent-by, i.e. fallback to RFC 3263 Section 5
		if via.Addr.IP() != nil {
			if addr, ok := netip.AddrFromSlice(via.Addr.IP()); ok {
				port := resolveViaPort(via, tpMeta)

				yieldAddrPort(yield, via.Transport, addr, port, false)
			}
			return
		}

		// Port specified, need to lookup IP
		if port, ok := via.Addr.Port(); ok {
			yieldFromIPs(ctx, yield, dnsRslvr, via.Transport, via.Addr.Host(), port, false)
			return
		}

		lookupOpts := util.LastSliceElemOr(opts, LookupMessageAddrsOptions{})

		// RFC 3263 Section 5: SRV lookup fallback
		yieldFromSRVs(ctx, yield, dnsRslvr, tpMeta, via.Addr.Host(), lookupOpts)
	}
}
