package sip

import (
	"context"
	"net"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

// RenderOptions represents options for rendering SIP messages.
// See [types.RenderOptions].
type RenderOptions = types.RenderOptions

// ProtoInfo represents SIP protocol information.
// See [types.ProtoInfo].
type ProtoInfo = types.ProtoInfo

var protoVer20 = ProtoInfo{Name: "SIP", Version: "2.0"}

// ProtoVer20 returns the SIP 2.0 protocol information.
func ProtoVer20() ProtoInfo { return protoVer20 }

// Addr represents a network address.
// See [types.Addr].
type Addr = types.Addr

// AddrFromHost returns an [Addr] containing the provided host and no port.
func AddrFromHost(host string) Addr { return types.AddrFromHost(host) }

// AddrFromHostPort returns an [Addr] containing the provided host and port.
func AddrFromHostPort(host string, port uint16) Addr { return types.AddrFromHostPort(host, port) }

func AddrFromIP(ip net.IP) Addr { return types.AddrFromIP(ip) }

func AddrFromIPPort(ip net.IP, port uint16) Addr { return types.AddrFromIPPort(ip, port) }

// ParseAddr parses a "host[:port]" string into an [Addr].
func ParseAddr(s string) (Addr, error) { return errors.Wrap2(types.ParseAddr(s)) }

// Values represents a map of string keys to string values.
// See [types.Values].
type Values = types.Values

type ErrorHandler interface {
	HandleError(ctx context.Context, err error)
}

type ErrorHandlerFunc func(ctx context.Context, err error)

func (f ErrorHandlerFunc) HandleError(ctx context.Context, err error) { f(ctx, errors.Wrap(err)) }
