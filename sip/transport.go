package sip

import (
	"context"
	"errors"
	"iter"
	"math"
	"net/netip"
	"time"

	"github.com/ghettovoice/gosip/sip/internal/shared"
)

var (
	MTU        = 1500 // Maximum Transport Unit.
	MaxMsgSize = math.MaxUint16

	ErrTransportClosed = errors.New("transport closed")
)

type TransportProto = shared.TransportProto

// Transport represents a SIP transport.
// It provides methods for both sides (server and client).
// RFC 3261 Section 18.
type Transport interface {
	Proto() TransportProto
	// ListenAndServe starts listener on the address addr.
	// The listener is closed on context done or Shutdown call.
	// If context closes the listener, the error will be context.Err().
	// If Shutdown closes the listener, the error will be ErrTransportClosed.
	// Inbound messages, if they are valid, are passed to the OnInboundRequest and OnInboundResponse handlers.
	ListenAndServe(ctx context.Context, addr netip.AddrPort, opts ...any) error
	// SendRequest sends the request req to the remote address raddr.
	// The transport user must prepend the top header.ViaHop by itself.
	// The transport will fill Transport and Addr fields automatically before sending.
	SendRequest(ctx context.Context, req *Request, raddr netip.AddrPort, opts ...any) error
	// SendResponse sends the response res from the local address laddr.
	// The destination address is resolved according to RFC 3261 Section 18.2.2, RFC 3263 Section 5.
	SendResponse(ctx context.Context, res *Response, laddr netip.AddrPort, opts ...any) error
	// Shutdown gracefully stops all listeners and connections.
	// Every running call to ListenAndServe will return ErrTransportClosed immediately.
	Shutdown() error
	Stats() TransportReport

	OnInboundRequest(hdlr RequestHandler)
	OnInboundResponse(hdlr ResponseHandler)
	OnOutboundRequest(hdlr RequestHandler)
	OnOutboundResponse(hdlr ResponseHandler)
}

// TransportReport provides statistics about the transport.
type TransportReport struct {
	Proto       TransportProto `json:"proto"       yaml:"proto"`
	Listeners   uint32         `json:"listeners"   yaml:"listeners"`
	Connections uint32         `json:"connections" yaml:"connections"`

	InboundRequests          uint64 `json:"inbound_requests"           yaml:"inbound_requests"`
	InboundRequestsRejected  uint64 `json:"inbound_requests_rejected"  yaml:"inbound_requests_rejected"`
	InboundResponses         uint64 `json:"inbound_responses"          yaml:"inbound_responses"`
	InboundResponsesRejected uint64 `json:"inbound_responses_rejected" yaml:"inbound_responses_rejected"`

	OutboundRequests          uint64 `json:"outbound_requests"           yaml:"outbound_requests"`
	OutboundRequestsRejected  uint64 `json:"outbound_requests_rejected"  yaml:"outbound_requests_rejected"`
	OutboundResponses         uint64 `json:"outbound_responses"          yaml:"outbound_responses"`
	OutboundResponsesRejected uint64 `json:"outbound_responses_rejected" yaml:"outbound_responses_rejected"`

	MessageRTT             time.Duration `json:"message_rtt"              yaml:"message_rtt"`
	MessageRTTMeasurements uint64        `json:"message_rtt_measurements" yaml:"message_rtt_measurements"`
}

// TransportFactory creates a new transport.
type TransportFactory interface {
	NewTransport() (Transport, error)
}

// TransportMetadata describes transport.
// It is used to register transport in the library registry.
type TransportMetadata struct {
	Proto       TransportProto
	Network     string
	DefaultPort uint16
	IsReliable  bool
	IsSecured   bool
	Factory     TransportFactory
}

var transportRegistry = make(map[TransportProto]TransportMetadata)

// RegisterTransport registers transport in the library registry.
func RegisterTransport(md TransportMetadata) {
	transportRegistry[md.Proto.ToUpper()] = md
}

func IsReliableTransport(proto TransportProto) bool {
	if md, ok := transportRegistry[proto.ToUpper()]; ok {
		return md.IsReliable
	}
	return false
}

func IsSecuredTransport(proto TransportProto) bool {
	if md, ok := transportRegistry[proto.ToUpper()]; ok {
		return md.IsSecured
	}
	return false
}

func TransportDefaultPort(proto TransportProto) uint16 {
	if md, ok := transportRegistry[proto.ToUpper()]; ok {
		return md.DefaultPort
	}
	return 0
}

func TransportNetwork(proto TransportProto) string {
	if md, ok := transportRegistry[proto.ToUpper()]; ok {
		return md.Network
	}
	return ""
}

// RequestAddrResolver resolves request addresses and transports.
// RFC 3261 Section 8.1.2., RFC 3263 Section 4.
type RequestAddrResolver interface {
	RequestAddrs(req *Request) iter.Seq2[TransportProto, netip.AddrPort]
}

// ResponseAddrResolver resolves response addresses.
// RFC 3261 Section 18.2.2., RFC 3263 Section 5.
type ResponseAddrResolver interface {
	ResponseAddrs(res *Response) iter.Seq[netip.AddrPort]
}
