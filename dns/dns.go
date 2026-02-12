package dns

//go:generate errtrace -w .

import (
	"cmp"
	"context"
	"net"
	"slices"
	"time"

	"braces.dev/errtrace"
	"github.com/miekg/dns"
)

// Resolver wraps net.Resolver with additional DNS lookup capabilities.
type Resolver struct {
	net.Resolver

	// NameServer specifies the DNS server address (e.g., "8.8.8.8:53").
	// If empty, the system's default resolver configuration is used.
	NameServer string
	// Timeout specifies the timeout for DNS queries.
	// If zero, defaults to 5 seconds.
	Timeout time.Duration
}

func (r *Resolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	ips, err := r.Resolver.LookupIP(ctx, network, host)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	for i, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			ips[i] = ip4
		}
	}
	return ips, nil
}

type SRV = net.SRV

func (r *Resolver) LookupSRV(ctx context.Context, service, proto, host string) ([]*SRV, error) {
	_, srvs, err := r.Resolver.LookupSRV(ctx, service, proto, host)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	return srvs, nil
}

// NAPTR represents a NAPTR DNS record as defined in RFC 3403.
// NAPTR records are used for URI resolution, particularly in SIP (RFC 3263)
// for discovering transport protocols and services.
type NAPTR struct {
	// Order specifies the order in which NAPTR records must be processed.
	// Lower values are processed first.
	Order uint16
	// Preference specifies the preference for records with equal Order values.
	// Lower values are preferred.
	Preference uint16
	// Flags control aspects of the rewriting and interpretation of fields.
	// Common flags: "s" (SRV lookup), "a" (A/AAAA lookup), "u" (terminal URI).
	Flags string
	// Service specifies the service and protocol available.
	// For SIP: "SIP+D2U" (UDP), "SIP+D2T" (TCP), "SIP+D2S" (SCTP), "SIPS+D2T" (TLS).
	Service string
	// Regexp is a substitution expression applied to the original string.
	// Usually empty when Replacement is used.
	Regexp string
	// Replacement is the next domain name to query.
	// Usually points to an SRV record when Flags is "s".
	Replacement string
}

// LookupNAPTR queries NAPTR records for the given host.
// Returns records sorted by Order (ascending), then by Preference (ascending).
func (r *Resolver) LookupNAPTR(ctx context.Context, host string) ([]*NAPTR, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(host), dns.TypeNAPTR)
	m.RecursionDesired = true

	nameserver, err := r.nameserver()
	if err != nil {
		return nil, errtrace.Wrap(err)
	}

	client := &dns.Client{Timeout: r.timeout()}
	resp, _, err := client.ExchangeContext(ctx, m, nameserver)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}

	if resp.Rcode != dns.RcodeSuccess {
		return nil, errtrace.Wrap(&net.DNSError{
			Err:        dns.RcodeToString[resp.Rcode],
			Name:       host,
			IsNotFound: resp.Rcode == dns.RcodeNameError,
		})
	}

	recs := make([]*NAPTR, 0, len(resp.Answer))
	for _, ans := range resp.Answer {
		if rr, ok := ans.(*dns.NAPTR); ok {
			recs = append(recs, &NAPTR{
				Order:       rr.Order,
				Preference:  rr.Preference,
				Flags:       rr.Flags,
				Service:     rr.Service,
				Regexp:      rr.Regexp,
				Replacement: rr.Replacement,
			})
		}
	}

	// Sort by Order, then by Preference (RFC 3403)
	slices.SortFunc(recs, func(a, b *NAPTR) int {
		if c := cmp.Compare(a.Order, b.Order); c != 0 {
			return c
		}
		return cmp.Compare(a.Preference, b.Preference)
	})

	return recs, nil
}

func (r *Resolver) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 5 * time.Second
}

func (r *Resolver) nameserver() (string, error) {
	if r.NameServer != "" {
		if _, _, err := net.SplitHostPort(r.NameServer); err != nil {
			return net.JoinHostPort(r.NameServer, "53"), nil //nolint:nilerr
		}
		return r.NameServer, nil
	}

	conf, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		return "", errtrace.Wrap(err)
	}
	if len(conf.Servers) == 0 {
		return "", errtrace.Wrap(&net.DNSError{
			Err:  "no DNS servers configured",
			Name: "resolv.conf",
		})
	}

	return net.JoinHostPort(conf.Servers[0], conf.Port), nil
}

var defResolver = &Resolver{}

func DefaultResolver() *Resolver { return defResolver }

func LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	return errtrace.Wrap2(defResolver.LookupIP(ctx, "ip", host))
}

func LookupSRV(ctx context.Context, service, proto, host string) ([]*SRV, error) {
	return errtrace.Wrap2(defResolver.LookupSRV(ctx, service, proto, host))
}

func LookupNAPTR(ctx context.Context, host string) ([]*NAPTR, error) {
	return errtrace.Wrap2(defResolver.LookupNAPTR(ctx, host))
}
