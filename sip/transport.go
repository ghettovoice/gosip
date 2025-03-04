package sip

import (
	"context"
	"math"
	"net/netip"
	"time"

	"github.com/ghettovoice/gosip/sip/internal/shared"
)

var (
	MTU        = 1500 // Maximum Transport Unit.
	MaxMsgSize = math.MaxUint16
)

type TransportProto = shared.TransportProto

// Transport represents a SIP transport.
// It provides methods for both sides (server and client).
// RFC 3261 Section 18.
type Transport interface {
	Proto() TransportProto
	ListenAndServe(ctx context.Context, addr netip.AddrPort, opts ...any) error
	GetOrDial(ctx context.Context, addr netip.AddrPort, opts ...any) (RequestWriter, error)
	Shutdown() error
	Stats() TransportReport

	OnInboundRequest(fn func(context.Context, *Request, ResponseWriter) error)
	OnInboundResponse(fn func(context.Context, *Response) error)
	OnOutboundRequest(fn func(context.Context, *Request) error)
	OnOutboundResponse(fn func(context.Context, *Response) error)
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

// TransportMetadata describes a transport.
// It is used to register a transports in the library registry.
type TransportMetadata struct {
	Proto       TransportProto
	Network     string
	DefaultPort uint16
	IsReliable  bool
	IsSecured   bool
	Factory     TransportFactory
}

var transportRegistry = make(map[TransportProto]TransportMetadata)

// RegisterTransport registers a transport in the library registry.
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
