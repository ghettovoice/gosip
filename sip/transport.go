package sip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"math"
	"net/netip"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/internal/types"
)

// Transport global configuration variables.
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

// TransportMetadata represents transport metadata.
type TransportMetadata struct {
	// Proto is the transport protocol.
	Proto TransportProto
	// Network is the network type.
	Network string
	// Reliable is the flag that indicates whether the transport is reliable.
	Reliable bool
	// Secured is the flag that indicates whether the transport is secured.
	Secured bool
	// Streamed is the flag that indicates whether the transport is streamed.
	Streamed bool
	// DefaultPort is the default port for the transport.
	DefaultPort uint16
}

// Transport represents a combination of client and server transport functions.
type Transport interface {
	RequestSender
	ResponseSender
	// UseInboundRequestInterceptor adds interceptor for inbound requests.
	// The interceptor can be removed by calling the returned unbind function.
	UseInboundRequestInterceptor(interceptor InboundRequestInterceptor) (unbind func())
	// UseInboundResponseInterceptor adds interceptor for inbound responses.
	// The interceptor can be removed by calling the returned unbind function.
	UseInboundResponseInterceptor(interceptor InboundResponseInterceptor) (unbind func())
	// UseOutboundRequestInterceptor adds interceptor for outbound requests.
	// The interceptor can be removed by calling the returned unbind function.
	UseOutboundRequestInterceptor(interceptor OutboundRequestInterceptor) (unbind func())
	// UseOutboundResponseInterceptor adds interceptor for outbound responses.
	// The interceptor can be removed by calling the returned unbind function.
	UseOutboundResponseInterceptor(interceptor OutboundResponseInterceptor) (unbind func())
	// UseInterceptor adds all non-nil interceptors from the provided object.
	// The interceptor can be removed by calling the returned unbind function.
	UseInterceptor(interceptor MessageInterceptor) (unbind func())
	// Serve starts the transport read loop and blocks until the transport is closed.
	// Serve always returns a non-nil error: [ErrTransportClosed] in case of transport close
	// or last error from read/accept loop.
	Serve(ctx context.Context) error
	// Close closes the transport and releases underlying resources.
	Close(ctx context.Context) error
}

const transpCtxKey types.ContextKey = "transport"

func ContextWithTransport(ctx context.Context, tp Transport) context.Context {
	return context.WithValue(ctx, transpCtxKey, tp)
}

func TransportFromContext(ctx context.Context) (Transport, bool) {
	v, ok := ctx.Value(transpCtxKey).(Transport)
	if !ok {
		return nil, false
	}
	return v, true
}

func GetTransportProto(tp Transport) (TransportProto, bool) {
	if v, ok := tp.(interface{ Proto() TransportProto }); ok {
		return v.Proto(), true
	}
	return "", false
}

func GetTransportNetwork(tp Transport) (string, bool) {
	if v, ok := tp.(interface{ Network() string }); ok {
		return v.Network(), true
	}
	return "", false
}

func GetTransportLocalAddr(tp Transport) (netip.AddrPort, bool) {
	if v, ok := tp.(interface{ LocalAddr() netip.AddrPort }); ok {
		return v.LocalAddr(), true
	}
	return zeroAddrPort, false
}

func IsReliableTransport(tp Transport) bool {
	if v, ok := tp.(interface{ Reliable() bool }); ok {
		return v.Reliable()
	}
	return false
}

func IsSecuredTransport(tp Transport) bool {
	if v, ok := tp.(interface{ Secured() bool }); ok {
		return v.Secured()
	}
	return false
}

func IsStreamedTransport(tp Transport) bool {
	if v, ok := tp.(interface{ Streamed() bool }); ok {
		return v.Streamed()
	}
	return false
}

func GetTransportDefaultPort(tp Transport) (uint16, bool) {
	if v, ok := tp.(interface{ DefaultPort() uint16 }); ok {
		return v.DefaultPort(), true
	}
	return 0, false
}

type rejectRequestError struct {
	err error
	sts ResponseStatus
	lvl slog.Level
}

func NewRejectRequestError(err error, sts ResponseStatus, lvl slog.Level) error {
	if sts == 0 {
		sts = ResponseStatusServerInternalError
	}
	return &rejectRequestError{err, sts, lvl} //errtrace:skip
}

func (e *rejectRequestError) Error() string {
	if e == nil || e.err == nil {
		return sNilTag
	}
	return e.err.Error()
}

func (e *rejectRequestError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

type rejectResponseError struct {
	err error
	lvl slog.Level
}

func NewRejectResponseError(err error, lvl slog.Level) error {
	return &rejectResponseError{err, lvl} //errtrace:skip
}

func (e *rejectResponseError) Error() string {
	if e == nil || e.err == nil {
		return sNilTag
	}
	return e.err.Error()
}

func (e *rejectResponseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func SendRequestStateless(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	opts *SendRequestOptions,
) error {
	// TODO: implement, resolve DNS procedure
	return nil
}

// RespondOptions are options for respond helper functions.
type RespondOptions struct {
	ResponseOptions *ResponseOptions
	SendOptions     *SendResponseOptions
}

func (o *RespondOptions) resOpts() *ResponseOptions {
	if o == nil {
		return nil
	}
	return o.ResponseOptions
}

func (o *RespondOptions) sendOpts() *SendResponseOptions {
	if o == nil {
		return nil
	}
	return o.SendOptions
}

// RespondStateless responds to a request statelessly using the given transport.
// If [ResponseOptions.LocalTag] is not specified,
// then stable tag is generated based on inbound request details.
func RespondStateless(
	ctx context.Context,
	sndr ResponseSender,
	req *InboundRequestEnvelope,
	sts ResponseStatus,
	opts *RespondOptions,
) error {
	resOpts := opts.resOpts()
	if resOpts == nil {
		resOpts = &ResponseOptions{}
	}
	if resOpts.LocalTag == "" {
		resOpts.LocalTag = genStableResTag(req)
	}
	res, err := req.NewResponse(sts, resOpts)
	if err != nil {
		return errtrace.Wrap(err)
	}
	return errtrace.Wrap(sndr.SendResponse(ctx, res, opts.sendOpts()))
}

func genStableResTag(req *InboundRequestEnvelope) string {
	if req == nil {
		return ""
	}

	hdrs := req.Headers()
	if hdrs == nil {
		return ""
	}

	callID, _ := hdrs.CallID()

	var fromTag string
	if from, ok := hdrs.From(); ok && from != nil {
		if t, ok := from.Tag(); ok {
			fromTag = t
		}
	}

	key := make([]byte, 0, 96)
	key = append(key, callID...)
	key = append(key, fromTag...)
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:8])
}
