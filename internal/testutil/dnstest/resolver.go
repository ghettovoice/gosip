// Package dnstest provides reusable test doubles for DNS resolution.
package dnstest

import (
	"context"
	"net"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/pkg/dns"
)

// StubResolver implements DNS lookup methods with optional hooks. A nil hook
// returns (nil, nil), representing an empty DNS answer.
type StubResolver struct {
	// LookupIPFunc handles IP address lookups.
	LookupIPFunc func(context.Context, string, string) ([]net.IP, error)
	// LookupSRVFunc handles SRV record lookups.
	LookupSRVFunc func(context.Context, string, string, string) ([]*dns.SRV, error)
	// LookupNAPTRFunc handles NAPTR record lookups.
	LookupNAPTRFunc func(context.Context, string) ([]*dns.NAPTR, error)
}

// LookupIP forwards to LookupIPFunc or returns an empty DNS answer.
func (r StubResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	if r.LookupIPFunc == nil {
		return nil, nil
	}
	return errors.Wrap2(r.LookupIPFunc(ctx, network, host))
}

// LookupSRV forwards to LookupSRVFunc or returns an empty DNS answer.
func (r StubResolver) LookupSRV(ctx context.Context, service, proto, host string) ([]*dns.SRV, error) {
	if r.LookupSRVFunc == nil {
		return nil, nil
	}
	return errors.Wrap2(r.LookupSRVFunc(ctx, service, proto, host))
}

// LookupNAPTR forwards to LookupNAPTRFunc or returns an empty DNS answer.
func (r StubResolver) LookupNAPTR(ctx context.Context, host string) ([]*dns.NAPTR, error) {
	if r.LookupNAPTRFunc == nil {
		return nil, nil
	}
	return errors.Wrap2(r.LookupNAPTRFunc(ctx, host))
}
