// Package netutil provides common network utilities and types.
// It includes wrappers for automatic connection closing, logging, deadline management,
// and other connection-related functionality.
package netutil

import (
	"iter"
	"net"
	"net/netip"
	"strings"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// AddrPortToNetAddr converts a netip.AddrPort to a net.Addr.
func AddrPortToNetAddr(network string, addrPort netip.AddrPort) net.Addr {
	switch strings.ToLower(network) {
	case "udp", "udp4", "udp6":
		return &net.UDPAddr{
			IP:   addrPort.Addr().AsSlice(),
			Port: int(addrPort.Port()),
			Zone: addrPort.Addr().Zone(),
		}
	case "tcp", "tcp4", "tcp6":
		return &net.TCPAddr{
			IP:   addrPort.Addr().AsSlice(),
			Port: int(addrPort.Port()),
			Zone: addrPort.Addr().Zone(),
		}
	case "ip", "ip4", "ip6":
		return &net.IPAddr{
			IP:   addrPort.Addr().AsSlice(),
			Zone: addrPort.Addr().Zone(),
		}
	case "unix", "unixgram", "unixpacket":
		// For Unix domain sockets, we can't meaningfully convert from AddrPort
		panic(errors.ErrorfWrap("unexpected network %q", network))
	default:
		// For unknown networks, return a basic implementation
		return &netAddr{
			network: network,
			addr:    addrPort.String(),
		}
	}
}

type netAddr struct {
	network string
	addr    string
}

func (a *netAddr) Network() string { return a.network }

func (a *netAddr) String() string { return a.addr }

func UnmapAddrPort(addr netip.AddrPort) netip.AddrPort {
	if addr.Addr().Is4In6() {
		return netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	}
	return addr
}

// AsListener recursively unwraps a net.Listener to find an underlying listener of type T.
// It returns the found listener and true if successful, or the zero value and false otherwise.
func AsListener[T any](l net.Listener) (T, bool) {
	var zero T

	for l != nil {
		if v, ok := any(l).(T); ok {
			return v, true
		}

		u, ok := any(l).(interface{ Unwrap() net.Listener })
		if !ok {
			break
		}

		l = u.Unwrap()
	}

	return zero, false
}

// AsConn recursively unwraps a net.Conn to find an underlying connection of type T.
// It returns the found connection and true if successful, or the zero value and false otherwise.
func AsConn[T any](c net.Conn) (T, bool) {
	var zero T

	for c != nil {
		if v, ok := any(c).(T); ok {
			return v, true
		}

		u, ok := any(c).(interface{ Unwrap() net.Conn })
		if !ok {
			break
		}

		c = u.Unwrap()
	}

	return zero, false
}

// AsPacketConn recursively unwraps a net.PacketConn to find an underlying connection of type T.
// It returns the found connection and true if successful, or the zero value and false otherwise.
func AsPacketConn[T any](c net.PacketConn) (T, bool) {
	var zero T

	for c != nil {
		if v, ok := any(c).(T); ok {
			return v, true
		}

		u, ok := any(c).(interface{ Unwrap() net.PacketConn })
		if !ok {
			break
		}

		c = u.Unwrap()
	}

	return zero, false
}

// HostIP returns the preferred IP address of the current host.
// It attempts to find a non-loopback, non-link-local IPv4 address first,
// then falls back to IPv6 if no IPv4 address is available.
// If no suitable address is found, it returns an error.
func HostIP() (net.IP, error) {
	ip, ok := util.SeqFirst(AllHostIPs())
	if !ok {
		return nil, errors.ErrorWrap("no IP resolved")
	}
	return ip, nil
}

//nolint:gocognit
func AllHostIPs() iter.Seq[net.IP] {
	return func(yield func(net.IP) bool) {
		interfaces, err := net.Interfaces()
		if err != nil {
			return
		}

		// Yield all IPv4 addresses.
		for _, iface := range interfaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}

			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}

			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				default:
					continue
				}

				if ip.To4() != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
					if !yield(ip) {
						return
					}
				}
			}
		}

		// Yield all IPv6 addresses.
		for _, iface := range interfaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}

			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}

			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				default:
					continue
				}

				if ip.To4() == nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
					if !yield(ip) {
						return
					}
				}
			}
		}
	}
}

// IsNetworkCompatible checks if two network names are compatible.
//
//   - "udp" is compatible with "udp", "udp4", "udp6"
//   - "udp4" is compatible with "udp", "udp4" (but not "udp6")
//   - "udp6" is compatible with "udp", "udp6" (but not "udp4")
func IsNetworkCompatible(net1, net2 string) bool {
	net1 = strings.ToLower(net1)
	net2 = strings.ToLower(net2)

	// Exact match
	if net1 == net2 {
		return true
	}

	// Generic networks are compatible with specific variants
	switch net1 {
	case "udp", "tcp", "ip":
		// udp is compatible with udp4, udp6
		// tcp is compatible with tcp4, tcp6
		// ip is compatible with ip4, ip6
		return strings.HasPrefix(net2, net1)
	case "udp4", "tcp4", "ip4":
		// udp4 is compatible with udp, udp4 (but not udp6)
		return net2 == net1[:len(net1)-1] || net2 == net1
	case "udp6", "tcp6", "ip6":
		// udp6 is compatible with udp, udp6 (but not udp4)
		return net2 == net1[:len(net1)-1] || net2 == net1
	}

	return false
}

func ListenAddr(ls any) (net.Addr, bool) {
	switch l := ls.(type) {
	case net.Listener:
		return l.Addr(), true
	case net.PacketConn:
		return l.LocalAddr(), true
	default:
		return nil, false
	}
}
