package sip

import (
	"context"
	"net"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
)

// RenderOptions represents options for rendering SIP messages.
// See [shared.RenderOptions].
type RenderOptions = shared.RenderOptions

// ProtoInfo represents SIP protocol information.
// See [shared.ProtoInfo].
type ProtoInfo = shared.ProtoInfo

var protoVer20 = ProtoInfo{Name: "SIP", Version: "2.0"}

// ProtoVer20 returns the SIP 2.0 protocol information.
func ProtoVer20() ProtoInfo { return protoVer20 }

// Addr represents a network address.
// See [shared.Addr].
type Addr = shared.Addr

// MakeHostAddr returns an [Addr] containing the provided host and no port.
func MakeHostAddr(host string) Addr { return shared.MakeHostAddr(host) }

// MakeHostPortAddr returns an [Addr] containing the provided host and port.
func MakeHostPortAddr(host string, port uint16) Addr { return shared.MakeHostPortAddr(host, port) }

func MakeIPAddr(ip net.IP) Addr { return shared.MakeIPAddr(ip) }

func MakeIPPortAddr(ip net.IP, port uint16) Addr { return shared.MakeIPPortAddr(ip, port) }

// ParseAddr parses a "host[:port]" string into an [Addr].
func ParseAddr(s string) (Addr, error) { return errors.Wrap2(shared.ParseAddr(s)) }

// Values represents a map of string keys to string values.
// See [shared.Values].
type Values = shared.Values

type ErrorHandler interface {
	HandleError(ctx context.Context, err error)
}

type ErrorHandlerFunc func(ctx context.Context, err error)

func (f ErrorHandlerFunc) HandleError(ctx context.Context, err error) { f(ctx, errors.Wrap(err)) }
