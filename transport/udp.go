package transport

import (
	"fmt"
	"net"
	"strings"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
)

// UDP protocol implementation
type udpProtocol struct {
	protocol
	connections ConnectionPool
}

func NewUdpProtocol(
	output chan<- sip.Message,
	errs chan<- error,
	cancel <-chan struct{},
	msgMapper sip.MessageMapper,
	logger log.Logger,
) Protocol {
	p := new(udpProtocol)
	p.network = "udp"
	p.reliable = false
	p.streamed = false
	p.log = logger.
		WithPrefix("transport.Protocol").
		WithFields(log.Fields{
			"protocol_ptr": fmt.Sprintf("%p", p),
		})
	// TODO: add separate errs chan to listen errors from pool for reconnection?
	p.connections = NewConnectionPool(output, errs, cancel, msgMapper, p.Log())

	return p
}

func (p *udpProtocol) Done() <-chan struct{} {
	return p.connections.Done()
}

func (p *udpProtocol) Listen(target *Target, options ...ListenOption) error {
	// fill empty target props with default values
	target = FillTargetHostAndPort(p.Network(), target)
	// resolve local UDP endpoint
	laddr, err := net.ResolveUDPAddr(p.network, target.Addr())
	if err != nil {
		return &ProtocolError{
			err,
			fmt.Sprintf("resolve target address %s %s", p.Network(), target.Addr()),
			fmt.Sprintf("%p", p),
		}
	}
	// create UDP connection
	udpConn, err := net.ListenUDP(p.network, laddr)
	if err != nil {
		return &ProtocolError{
			err,
			fmt.Sprintf("listen on %s %s address", p.Network(), laddr),
			fmt.Sprintf("%p", p),
		}
	}

	p.Log().Debugf("begin listening on %s %s", p.Network(), laddr)

	// register new connection
	// index by local address, TTL=0 - unlimited expiry time
	key := ConnectionKey(fmt.Sprintf("%s:0.0.0.0:%d", p.network, laddr.Port))
	conn := NewConnection(udpConn, key, p.network, p.Log())
	err = p.connections.Put(conn, 0)
	if err != nil {
		err = &ProtocolError{
			Err:      err,
			Op:       fmt.Sprintf("put %s connection to the pool", conn.Key()),
			ProtoPtr: fmt.Sprintf("%p", p),
		}
	}

	return err // should be nil here
}

func (p *udpProtocol) Send(target *Target, msg sip.Message) error {
	target = FillTargetHostAndPort(p.Network(), target)

	// validate remote address
	if target.Host == "" {
		return &ProtocolError{
			fmt.Errorf("empty remote target host"),
			fmt.Sprintf("send SIP message to %s %s", p.Network(), target.Addr()),
			fmt.Sprintf("%p", p),
		}
	}

	// resolve remote address
	raddr, err := net.ResolveUDPAddr(p.network, target.Addr())
	if err != nil {
		return &ProtocolError{
			err,
			fmt.Sprintf("resolve target address %s %s", p.Network(), target.Addr()),
			fmt.Sprintf("%p", p),
		}
	}

	_, port, err := SafeSplitHostPort(msg.Source())
	if err != nil {
		return &ProtocolError{
			Err:      err,
			Op:       "resolve source port",
			ProtoPtr: fmt.Sprintf("%p", p),
		}
	}

	for _, conn := range p.connections.All() {
		parts := strings.Split(string(conn.Key()), ":")
		if parts[2] == port {
			logger := log.AddFieldsFrom(p.Log(), conn, msg)
			logger.Tracef("writing SIP message to %s %s", p.Network(), raddr)

			if _, err = conn.WriteTo([]byte(msg.String()), raddr); err != nil {
				return &ProtocolError{
					Err:      err,
					Op:       fmt.Sprintf("write SIP message to the %s connection", conn.Key()),
					ProtoPtr: fmt.Sprintf("%p", p),
				}
			}

			return nil
		}
	}

	return &ProtocolError{
		fmt.Errorf("connection on port %s not found", port),
		"search connection",
		fmt.Sprintf("%p", p),
	}
}

// SafeSplitHostPort attempts to parse the host and port from various non-standard address formats,
// including raw IPv6 addresses without brackets and formats starting with '::' or ':::'.
func SafeSplitHostPort(source string) (host, port string, err error) {
	// 1. Try standard library parsing (Works for IPv4:port, [IPv6]:port, or valid domain:port)
	host, port, err = net.SplitHostPort(source)
	if err == nil {
		return host, port, nil
	}
	// 2. Special handling for malformed IPv6 wildcard formats (e.g., :::8088 or ::8088)
	if strings.HasPrefix(source, "::") {
		lastColonIndex := strings.LastIndex(source, ":")
		if lastColonIndex != -1 && lastColonIndex < len(source)-1 {
			portStr := source[lastColonIndex+1:]
			ipv6AddrCandidate := source[:lastColonIndex]
			if strings.HasSuffix(ipv6AddrCandidate, ":") {
				ipv6AddrCandidate = ipv6AddrCandidate[:len(ipv6AddrCandidate)-1]
			}
			if ipv6AddrCandidate == "::" {
				hostportWithBrackets := fmt.Sprintf("[%s]:%s", ipv6AddrCandidate, portStr)
				return net.SplitHostPort(hostportWithBrackets)
			}
		}
	}

	// 3. Handle raw IPv6 addresses without mandatory brackets (e.g., 240e:..:f50b:8088)
	lastColonIndex := strings.LastIndex(source, ":")
	if strings.Count(source, ":") > 1 && !strings.Contains(source, "]") {
		if lastColonIndex != -1 && lastColonIndex < len(source)-1 {
			ipv6Addr := source[:lastColonIndex]
			portStr := source[lastColonIndex+1:]
			hostPortWithBrackets := fmt.Sprintf("[%s]:%s", ipv6Addr, portStr)
			return net.SplitHostPort(hostPortWithBrackets)
		}
	}

	// If all attempts fail, return the original error from net.SplitHostPort.
	return "", "", err
}
