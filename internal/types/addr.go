package types

import (
	"bytes"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

// Addr is a container for host and option port.
type Addr struct {
	host    string
	ip      net.IP
	port    uint16
	hasPort bool
}

// AddrFromHost returns an [Addr] containing the provided host and no port.
func AddrFromHost(host string) Addr {
	host = strings.Trim(host, "[]")

	ip := net.ParseIP(host)
	if v := ip.To4(); v != nil {
		ip = v
	}

	return Addr{
		host: host,
		ip:   ip,
	}
}

// AddrFromHostPort returns an [Addr] containing the provided host and port.
func AddrFromHostPort(host string, port uint16) Addr {
	addr := AddrFromHost(host)
	addr.port = port
	addr.hasPort = true
	return addr
}

func AddrFromIP(ip net.IP) Addr {
	if v := ip.To4(); v != nil {
		ip = v
	}

	return Addr{
		host: ip.String(),
		ip:   ip,
	}
}

func AddrFromIPPort(ip net.IP, port uint16) Addr {
	addr := AddrFromIP(ip)
	addr.port = port
	addr.hasPort = true
	return addr
}

// ParseAddr parses a "host:port" string into an [Addr].
func ParseAddr[T ~string | ~[]byte](s T) (addr Addr, err error) {
	defer func() {
		if rv := recover(); rv != nil {
			addr = Addr{}

			if e, ok := rv.(error); ok {
				err = errors.Wrap(e)
			} else {
				err = errors.ErrorfWrap("%v", rv)
			}
		}
	}()

	node, err := grammar.ParseHostport(s)
	if err != nil {
		return Addr{}, errors.Wrap(err)
	}

	host := grammar.MustGetNode(node, "host").String()
	if portNode, ok := node.GetNode("port"); ok {
		port, _ := strconv.Atoi(portNode.String())
		return AddrFromHostPort(host, uint16(port)), nil
	}

	return AddrFromHost(host), nil
}

// Host returns the hostname portion of the address as provided during construction or parsing.
func (addr Addr) Host() string { return addr.host }

// IP returns the parsed IP representation when the host is an IP literal, otherwise nil.
func (addr Addr) IP() net.IP { return addr.ip }

// Port returns the port, in case it is set, and bool flag indicating whether it is set.
func (addr Addr) Port() (uint16, bool) { return addr.port, addr.hasPort }

// String formats the address as host[:port], adding brackets for IPv6 literals when required.
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

// Format implements fmt.Formatter to support custom formatting verbs for Addr values.
func (addr Addr) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		fmt.Fprint(f, addr.String())
		return
	case 'q':
		fmt.Fprint(f, strconv.Quote(addr.String()))
		return
	default:
		if !f.Flag('+') && !f.Flag('#') {
			fmt.Fprint(f, addr.String())
			return
		}

		type (
			hideMethods Addr
			Addr        hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), Addr(addr))

		return
	}
}

// Clone returns a deep copy of the address including the underlying IP slice.
func (addr Addr) Clone() Addr {
	addr.ip = slices.Clone(addr.ip)
	return addr
}

// Equal reports whether the address equals the provided value, accepting Addr and *Addr.
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
		hostMatch = util.EqFold(addr.host, other.host)
	case addr.ip != nil && other.ip != nil:
		hostMatch = addr.ip.Equal(other.ip)
	default:
		return false
	}

	return hostMatch && addr.port == other.port && addr.hasPort == other.hasPort
}

// IsValid reports whether the address contains a syntactically valid host component.
func (addr Addr) IsValid() bool { return grammar.IsHost(addr.host) && (!addr.hasPort || addr.port > 0) }

// IsZero reports whether the address has zero host, IP and port information.
func (addr Addr) IsZero() bool { return addr.host == "" && addr.ip == nil && !addr.hasPort }

// MarshalText encodes the address into its textual representation suitable for JSON/Text marshalling.
func (addr Addr) MarshalText() (text []byte, err error) {
	return []byte(addr.String()), nil
}

func (addr Addr) AppendText(b []byte) ([]byte, error) {
	return append(b, addr.String()...), nil
}

// UnmarshalText parses a textual representation of an address into the receiver.
func (addr *Addr) UnmarshalText(text []byte) error {
	if addr == nil {
		return errors.NewInvalidArgumentErrorWrap("nil address")
	}

	if len(text) == 0 {
		*addr = Addr{}
		return nil
	}

	if bytes.Equal(text, []byte(":0")) {
		*addr = Addr{hasPort: true, port: 0}
		return nil
	}

	parsed, err := ParseAddr(text)
	if err != nil {
		*addr = Addr{}
		return errors.Wrap(err)
	}

	*addr = parsed

	return nil
}

func (addr Addr) Canonic() Addr {
	addr.host = util.LCase(addr.host)

	addr.ip = slices.Clone(addr.ip)
	if ipv4 := addr.ip.To4(); ipv4 != nil {
		addr.ip = ipv4
	}

	return addr
}
