package dns

import (
	"cmp"
	"context"
	"net"
	"slices"
	"sync"
	"time"

	"codeberg.org/miekg/dns"
	"codeberg.org/miekg/dns/dnsconf"

	"github.com/ghettovoice/gosip/internal/errors"
)

var defResolver = &Resolver{}

func DefaultResolver() *Resolver { return defResolver }

// LookupIP looks up IP addresses for the given network and host.
func LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	return errors.Wrap2(defResolver.LookupIP(ctx, network, host))
}

// LookupSRV looks up SRV records for the given service, protocol, and host.
func LookupSRV(ctx context.Context, service, proto, host string) ([]*SRV, error) {
	return errors.Wrap2(defResolver.LookupSRV(ctx, service, proto, host))
}

// LookupNAPTR looks up NAPTR records for the given host.
func LookupNAPTR(ctx context.Context, host string) ([]*NAPTR, error) {
	return errors.Wrap2(defResolver.LookupNAPTR(ctx, host))
}

// Resolver wraps net.Resolver with additional DNS lookup capabilities.
type Resolver struct {
	// NameServer specifies the DNS server address (e.g., "8.8.8.8:53").
	// If empty, the system's default resolver configuration is used.
	NameServer string
	// Dialer is used used to set local address and timeouts.
	// If nil, a [net.Dialer] with timeout 5s and keep-alive 3s is used.
	Dialer *net.Dialer
	// ReadTimeout is the maximum duration for reading a response.
	// If zero, a default timeout of 2s is used.
	ReadTimeout time.Duration
	// WriteTimeout is the maximum duration for writing a request.
	// If zero, a default timeout of 2s is used.
	WriteTimeout time.Duration

	nameSrv  string // resolved name server
	netRslvr *net.Resolver
	dnsCln   *dns.Client
	initOnce sync.Once
	initErr  error
}

func (r *Resolver) init() error {
	r.nameSrv = r.NameServer
	if r.nameSrv == "" {
		cfg, err := dnsconf.FromFile("/etc/resolv.conf")
		if err != nil {
			return errors.ErrorfWrap("load system DNS config: %w", err)
		}

		if len(cfg.Servers) == 0 {
			return errors.ErrorWrap("no system DNS servers configured")
		}

		r.nameSrv = net.JoinHostPort(cfg.Servers[0], cfg.Port)
	} else if _, _, err := net.SplitHostPort(r.NameServer); err != nil {
		r.nameSrv = net.JoinHostPort(r.NameServer, "53")
	}

	dlr := r.Dialer
	if dlr == nil {
		dlr = &net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 3 * time.Second,
		}
	}

	r.netRslvr = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return errors.Wrap2(dlr.DialContext(ctx, network, r.nameSrv))
		},
	}

	rdrTimeout := r.ReadTimeout
	if rdrTimeout == 0 {
		rdrTimeout = 2 * time.Second
	}

	wrtTimeout := r.WriteTimeout
	if wrtTimeout == 0 {
		wrtTimeout = 2 * time.Second
	}

	r.dnsCln = &dns.Client{
		Transport: &dns.Transport{
			Dialer:       dlr,
			ReadTimeout:  rdrTimeout,
			WriteTimeout: wrtTimeout,
		},
	}

	return nil
}

func (r *Resolver) initIfNeeded() error {
	r.initOnce.Do(func() { r.initErr = r.init() })
	return errors.Wrap(r.initErr)
}

// LookupIP looks up IP addresses for the given network and host.
func (r *Resolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	if err := r.initIfNeeded(); err != nil {
		return nil, errors.Wrap(err)
	}
	return errors.Wrap2(r.netRslvr.LookupIP(ctx, network, host))
}

type SRV = net.SRV

// LookupSRV looks up SRV records for the given service, protocol, and host.
func (r *Resolver) LookupSRV(ctx context.Context, service, proto, host string) ([]*SRV, error) {
	if err := r.initIfNeeded(); err != nil {
		return nil, errors.Wrap(err)
	}

	_, recs, err := r.netRslvr.LookupSRV(ctx, service, proto, host)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return recs, nil
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
	if err := r.initIfNeeded(); err != nil {
		return nil, errors.Wrap(err)
	}

	resp, _, err := r.dnsCln.Exchange(ctx, dns.NewMsg(host, dns.TypeNAPTR), "udp", r.nameSrv)
	if err != nil {
		return nil, errors.ErrorfWrap("request NAPTR: %w", err)
	}

	if resp.Rcode != dns.RcodeSuccess {
		return nil, errors.Wrap(&net.DNSError{
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
