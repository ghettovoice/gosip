package sip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"iter"
	"log/slog"
	"math"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/dns"
	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/log"
)

// Transport configuration variables.
var (
	// Maximum Transport Unit.
	// It is used to limit the size of the message that can be sent over the unreliable transport.
	MTU uint = 1500
	// Maximum network message size.
	// It is used to limit read buffer size for the streamed transport.
	MaxMsgSize uint = math.MaxUint16
)

// TransportProto is a transport protocol.
type TransportProto = types.TransportProto

// ClientTransport represents a client transport.
// It is used to send requests and receive responses.
type ClientTransport interface {
	// SendRequest sends a request to the remote address.
	SendRequest(ctx context.Context, req *OutboundRequest, opts *SendRequestOptions) error
	// OnResponse registers a response callback.
	OnResponse(fn TransportResponseHandler) (cancel func())
}

// SendRequestOptions are options for sending a request.
type SendRequestOptions struct {
	// Timeout is the timeout for the request sending process.
	// If zero, the default timeout 1m is used.
	Timeout time.Duration `json:"timeout,omitempty"`
	// RenderCompact is the flag that indicates whether the message should be rendered in compact form.
	// See [RenderOptions] for more details.
	RenderCompact bool `json:"render_compact,omitempty"`
	// TODO: options for multicast
}

func (o *SendRequestOptions) timeout() time.Duration {
	if o == nil || o.Timeout == 0 {
		return msgSendTimeout
	}
	return o.Timeout
}

func (o *SendRequestOptions) rendOpts() *RenderOptions {
	if o == nil {
		return nil
	}
	return &RenderOptions{
		Compact: o.RenderCompact,
	}
}

func cloneSendReqOpts(opts *SendRequestOptions) *SendRequestOptions {
	if opts == nil {
		return nil
	}
	newOpts := *opts
	return &newOpts
}

type TransportResponseHandler = func(ctx context.Context, tp ClientTransport, res *InboundResponse)

const clnTranspCtxKey types.ContextKey = "client_transport"

// ClientTransportFromContext returns the [ClientTransport] from the given context.
func ClientTransportFromContext(ctx context.Context) (ClientTransport, bool) {
	tp, ok := ctx.Value(clnTranspCtxKey).(ClientTransport)
	return tp, ok
}

// ServerTransport represents a server transport.
// It is used to receive requests and send responses.
type ServerTransport interface {
	// SendResponse sends a response to a remote address resolved with steps
	// defined in RFC 3261 Section 18.2.2. and RFC 3263 Section 5.
	SendResponse(ctx context.Context, res *OutboundResponse, opts *SendResponseOptions) error
	// OnRequest registers a request callback.
	OnRequest(fn TransportRequestHandler) (cancel func())
}

// SendResponseOptions are options for sending a response.
type SendResponseOptions struct {
	// Timeout is the timeout for the response sending process.
	// If zero, the default timeout 1m is used.
	Timeout time.Duration `json:"timeout,omitempty"`
	// RenderCompact is the flag that indicates whether the message should be rendered in compact form.
	// See [RenderOptions] for more details.
	RenderCompact bool `json:"render_compact,omitempty"`
}

func (o *SendResponseOptions) timeout() time.Duration {
	if o == nil || o.Timeout == 0 {
		return msgSendTimeout
	}
	return o.Timeout
}

func (o *SendResponseOptions) rendOpts() *RenderOptions {
	if o == nil {
		return nil
	}
	return &RenderOptions{
		Compact: o.RenderCompact,
	}
}

func cloneSendResOpts(opts *SendResponseOptions) *SendResponseOptions {
	if opts == nil {
		return nil
	}
	newOpts := *opts
	return &newOpts
}

type TransportRequestHandler = func(ctx context.Context, tp ServerTransport, req *InboundRequest)

const srvTranspCtxKey types.ContextKey = "server_transport"

// ServerTransportFromContext returns the [ServerTransport] from the given context.
func ServerTransportFromContext(ctx context.Context) (ServerTransport, bool) {
	tp, ok := ctx.Value(srvTranspCtxKey).(ServerTransport)
	return tp, ok
}

// ErrTransportClosed is returned when attempting to use a closed transport.
const ErrTransportClosed Error = "transport closed"

// Transport represents a combination of client and server transports.
type Transport interface {
	ClientTransport
	ServerTransport
	// Serve starts the transport read loop and blocks until the transport is closed.
	Serve() error
	// Close closes the transport.
	Close() error
}

// ConnDialer is used to dial connections for reliable transports.
type ConnDialer interface {
	// DialConn dials a connection to the remote address.
	DialConn(ctx context.Context, network string, raddr netip.AddrPort) (net.Conn, error)
}

// ConnDialerFunc is a [ConnDialer] implementation based on a function.
type ConnDialerFunc func(ctx context.Context, network string, raddr netip.AddrPort) (net.Conn, error)

func (f ConnDialerFunc) DialConn(ctx context.Context, network string, raddr netip.AddrPort) (net.Conn, error) {
	return errtrace.Wrap2(f(ctx, network, raddr))
}

// DNSResolver is used to resolve the message destination address.
type DNSResolver interface {
	// LookupIP looks up the IP address for the given host.
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
	// LookupSRV looks up the SRV record for the given service and protocol.
	LookupSRV(ctx context.Context, service, proto, host string) ([]*dns.SRV, error)
	// LookupNAPTR looks up the NAPTR record for the given host.
	LookupNAPTR(ctx context.Context, host string) ([]*dns.NAPTR, error)
}

func GetTransportProto(tp any) (TransportProto, bool) {
	if v, ok := tp.(interface{ Proto() TransportProto }); ok {
		return v.Proto(), true
	}
	return "", false
}

func GetTransportNetwork(tp any) (string, bool) {
	if v, ok := tp.(interface{ Network() string }); ok {
		return v.Network(), true
	}
	return "", false
}

func GetTransportLocalAddr(tp any) (netip.AddrPort, bool) {
	if v, ok := tp.(interface{ LocalAddr() netip.AddrPort }); ok {
		return v.LocalAddr(), true
	}
	return zeroAddrPort, false
}

func IsReliableTransport(tp any) bool {
	if v, ok := tp.(interface{ Reliable() bool }); ok {
		return v.Reliable()
	}
	return false
}

func IsSecuredTransport(tp any) bool {
	if v, ok := tp.(interface{ Secured() bool }); ok {
		return v.Secured()
	}
	return false
}

func IsStreamedTransport(tp any) bool {
	if v, ok := tp.(interface{ Streamed() bool }); ok {
		return v.Streamed()
	}
	return false
}

func GetTransportDefaultPort(tp any) (uint16, bool) {
	if v, ok := tp.(interface{ DefaultPort() uint16 }); ok {
		return v.DefaultPort(), true
	}
	return 0, false
}

type TransportMetadata struct {
	Proto       TransportProto
	Network     string
	Reliable    bool
	Secured     bool
	Streamed    bool
	DefaultPort uint16
}

// ResponseAddrs returns the list of addresses to which the response should be sent.
// It implements the logic defined in RFC 3261 Section 18.2.2. and RFC 3263 Section 5.
// The response must contain a "Via" header field and the transport protocol must match
// the transport protocol in the topmost "Via" header field.
//
//nolint:gocognit
func ResponseAddrs(
	ctx context.Context,
	via header.ViaHop,
	tpMeta TransportMetadata,
	dnsRslvr DNSResolver,
) iter.Seq2[TransportProto, netip.AddrPort] {
	return func(yield func(TransportProto, netip.AddrPort) bool) {
		if !via.IsValid() || !via.Transport.Equal(tpMeta.Proto) {
			return
		}

		if !tpMeta.Reliable {
			// RFC 3261 Section 18.2.2, bullet 2.
			if maddr, ok := via.MAddr(); ok {
				// maddr can be host name or IP address, need to lookup IP addresses
				if ips, err := dnsRslvr.LookupIP(ctx, "ip", maddr); err == nil {
					for _, ip := range ips {
						if addr, ok := netip.AddrFromSlice(ip); ok {
							addr = addr.Unmap()

							var port uint16
							if p, ok := via.Addr.Port(); ok {
								port = p
							} else {
								port = tpMeta.DefaultPort
							}

							if addrPort := netip.AddrPortFrom(addr, port); addrPort.IsValid() && !yield(via.Transport, addrPort) {
								return
							}
						}
					}
				}
				// no fallback to RFC 3263 Section 5 is defined for "maddr" case,
				// so we stop here.
				return
			}
		}

		// RFC 3261 Section 18.2.2, bullet 1 and 3.
		if addr, ok := via.Received(); ok {
			var port uint16
			if !tpMeta.Reliable {
				// RFC 3581 Section 4.
				if p, ok := via.RPort(); ok {
					port = p
				}
			}
			if port == 0 {
				if p, ok := via.Addr.Port(); ok {
					port = p
				} else {
					port = tpMeta.DefaultPort
				}
			}

			if addrPort := netip.AddrPortFrom(addr, port); addrPort.IsValid() && !yield(via.Transport, addrPort) {
				return
			}
		}

		// RFC 3261 Section 18.2.2, bullet 4, i.e. fallback to RFC 3263 Section 5.
		if via.Addr.IP() != nil {
			if addr, ok := netip.AddrFromSlice(via.Addr.IP()); ok {
				addr = addr.Unmap()

				var port uint16
				if p, ok := via.Addr.Port(); ok {
					port = p
				} else {
					port = tpMeta.DefaultPort
				}

				if addrPort := netip.AddrPortFrom(addr, port); addrPort.IsValid() && !yield(via.Transport, addrPort) {
					return
				}
			}
			return
		}

		if port, ok := via.Addr.Port(); ok {
			if ips, err := dnsRslvr.LookupIP(ctx, "ip", via.Addr.Host()); err == nil {
				for _, ip := range ips {
					if addr, ok := netip.AddrFromSlice(ip); ok {
						addr = addr.Unmap()

						if addrPort := netip.AddrPortFrom(addr, port); addrPort.IsValid() && !yield(via.Transport, addrPort) {
							return
						}
					}
				}
			}
			return
		}

		// RFC 3263 Section 5.
		serv := "sip"
		if tpMeta.Secured {
			serv = "sips"
		}

		if srvs, err := dnsRslvr.LookupSRV(ctx, serv, tpMeta.Network, via.Addr.Host()); err == nil {
			srvs = slices.SortedFunc(slices.Values(srvs), func(e1, e2 *dns.SRV) int {
				switch {
				case e1.Priority < e2.Priority:
					return -1
				case e1.Priority > e2.Priority:
					return 1
				case e1.Weight > e2.Weight:
					return -1
				case e1.Weight < e2.Weight:
					return 1
				default:
					return strings.Compare(e1.Target, e2.Target)
				}
			})

			for _, srv := range srvs {
				if ips, err := dnsRslvr.LookupIP(ctx, "ip", srv.Target); err == nil {
					for _, ip := range ips {
						if addr, ok := netip.AddrFromSlice(ip); ok {
							addr = addr.Unmap()

							if addrPort := netip.AddrPortFrom(addr, srv.Port); addrPort.IsValid() && !yield(via.Transport, addrPort) {
								return
							}
						}
					}
				}
			}
		}
	}
}

// func RequestAddrs(
// 	ctx context.Context,
// 	uri URI,
// 	tpsMeta map[TransportProto]TransportMetadata,
// 	dns DNSResolver,
// ) iter.Seq2[TransportProto, netip.AddrPort] {
// 	return func(yield func(TransportProto, netip.AddrPort) bool) {
// 		// TODO: implement
// 	}
// }

func respondStateless(ctx context.Context, tp ServerTransport, req *InboundRequest, sts ResponseStatus) {
	logger := log.LoggerFromValues(ctx, tp)
	if tp == nil {
		logger.LogAttrs(ctx, slog.LevelError, "silently discard inbound request due to missing transport",
			slog.Any("request", req),
		)
		return
	}
	if req.Method().Equal(RequestMethodAck) {
		logger.LogAttrs(ctx, slog.LevelDebug, "silently discard inbound ACK request", slog.Any("request", req))
		return
	}

	var hdrs Headers
	if sts == ResponseStatusServerInternalError || sts == ResponseStatusServiceUnavailable {
		hdrs = make(Headers).Append(&header.RetryAfter{Delay: time.Minute})
	}
	res, err := req.NewResponse(sts, &ResponseOptions{
		Headers:  hdrs,
		LocalTag: stableStatelessToTag(req),
	})
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "failed to build response on inbound request",
			slog.Any("request", req),
			slog.Any("error", err),
		)
		return
	}

	if err := tp.SendResponse(ctx, res, nil); err != nil {
		if errors.Is(err, ErrInvalidMessage) {
			logger.LogAttrs(ctx, slog.LevelDebug, "silently discard inbound request due to invalid response",
				slog.Any("request", req),
				slog.Any("response", res),
				slog.Any("error", err),
			)
			return
		}

		logger.LogAttrs(ctx, slog.LevelError, "failed to respond on inbound request",
			slog.Any("request", req),
			slog.Any("response", res),
			slog.Any("error", err),
		)
		return
	}
}

func stableStatelessToTag(req *InboundRequest) string {
	if req == nil {
		return ""
	}

	hdrs := req.Headers()
	if hdrs == nil {
		return ""
	}

	var reqURI string
	if uri := req.URI(); uri != nil {
		reqURI = util.LCase(uri.Render(nil))
	}

	var topVia string
	if via, ok := hdrs.FirstVia(); ok && via != nil {
		topVia = util.LCase(via.String())
	}

	callID, _ := hdrs.CallID()

	var fromTag string
	if from, ok := hdrs.From(); ok && from != nil {
		if t, ok := from.Tag(); ok {
			fromTag = t
		}
	}

	var cseqNum uint
	var cseqMethod RequestMethod
	if cseq, ok := hdrs.CSeq(); ok && cseq != nil {
		cseqNum = cseq.SeqNum
		cseqMethod = util.UCase(cseq.Method)
	}

	key := make([]byte, 0, 96)
	key = append(key, "uri="...)
	key = append(key, reqURI...)
	key = append(key, "|via="...)
	key = append(key, topVia...)
	key = append(key, "|callid="...)
	key = append(key, callID...)
	key = append(key, "|fromtag="...)
	key = append(key, fromTag...)
	key = append(key, "|cseq="...)
	key = strconv.AppendUint(key, uint64(cseqNum), 10)
	key = append(key, "|cseqm="...)
	key = append(key, cseqMethod...)

	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:8])
}
