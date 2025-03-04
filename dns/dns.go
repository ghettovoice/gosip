package dns

//go:generate go tool errtrace -w .

import (
	"context"
	"net"

	"braces.dev/errtrace"
)

type Resolver struct {
	net.Resolver
}

func (*Resolver) LookupNAPTR(ctx context.Context, host string) ([]any, error) {
	// TODO: implement me
	panic("not implemented")
}

var defResolver = &Resolver{}

func DefaultResolver() *Resolver { return defResolver }

func LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	return errtrace.Wrap2(defResolver.LookupIP(ctx, "ip", host))
}

func LookupSRV(ctx context.Context, service, proto, host string) (string, []*net.SRV, error) {
	return errtrace.Wrap3(defResolver.LookupSRV(ctx, service, proto, host))
}

func LookupNAPTR(ctx context.Context, host string) ([]any, error) {
	return errtrace.Wrap2(defResolver.LookupNAPTR(ctx, host))
}
