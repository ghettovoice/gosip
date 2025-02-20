package shared

import (
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

// Addr is a container for host and option port.
type Addr struct {
	host    string
	ip      net.IP
	port    uint16
	hasPort bool
}

// Host returns an [Addr] containing the provided host and no port.
func Host(host string) Addr {
	host = strings.Trim(host, "[]")
	return Addr{
		host: host,
		ip:   net.ParseIP(host),
	}
}

// HostPort returns an [Addr] containing the provided host and port.
func HostPort(host string, port uint16) Addr {
	host = strings.Trim(host, "[]")
	return Addr{
		host:    host,
		ip:      net.ParseIP(host),
		port:    port,
		hasPort: true,
	}
}

func (addr Addr) Host() string { return addr.host }

func (addr Addr) IP() net.IP { return addr.ip }

// Port returns the port, in case it is set, and bool flag indicating whether it is set.
func (addr Addr) Port() (uint16, bool) { return addr.port, addr.hasPort }

func (addr Addr) String() string {
	var host string
	if addr.ip == nil {
		host = addr.host
	} else {
		host = addr.ip.String()
	}
	if !addr.hasPort {
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(int(addr.port)))
}

func (addr Addr) Clone() Addr {
	addr.ip = slices.Clone(addr.ip)
	return addr
}

func (addr Addr) Equal(val any) bool {
	var other Addr
	switch v := val.(type) {
	case Addr:
		other = v
	case *Addr:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}

	var hostMatch bool
	switch {
	case addr.ip == nil && other.ip == nil:
		hostMatch = stringutils.LCase(addr.host) == stringutils.LCase(other.host)
	case addr.ip != nil && other.ip != nil:
		hostMatch = addr.ip.Equal(other.ip)
	default:
		return false
	}

	return hostMatch && addr.port == other.port && addr.hasPort == other.hasPort
}

func (addr Addr) IsValid() bool { return grammar.IsHost(addr.host) }

func (addr Addr) IsZero() bool { return addr.host == "" && addr.ip == nil && !addr.hasPort }
