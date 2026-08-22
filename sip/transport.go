package sip

import (
	"context"
	"iter"
	"net/netip"

	"github.com/ghettovoice/gosip/internal/errors"
)

// IsTransportError reports whether err belongs to the transport error class.
func IsTransportError(err error) bool {
	return errors.IsNetError(err) || errors.Is(err, ErrClassTransport)
}

// Transport represents a combination of client and server transport functions.
type Transport interface {
	RequestSender
	ResponseSender
	Responder
	MessageInterceptorChain
	// Metadata returns transport metadata.
	Metadata() TransportMetadata
	// Close closes the transport and all listeners and connections currently tracked by it.
	Close(ctx context.Context) error
	// Listen binds a new listener to the given local address and registers it as a
	// transport-owned resource.
	// It does not start serving the listener.
	Listen(ctx context.Context, addr string) (TransportListener, error)
	// MatchSentBy checks whether the send-by address matches one of the transport's
	// public addresses.
	MatchSentBy(sentBy Addr) bool
}

// TransportListener represents a transport listener.
type TransportListener interface {
	// Metadata returns the transport metadata of the listener.
	Metadata() TransportMetadata
	// LocalAddr returns the local address of the listener.
	LocalAddr() netip.AddrPort
	// Serve starts serving the listener.
	//
	// For a listener created by [Transport.Listen], cancelling ctx closes the
	// transport-owned listener and unblocks Serve.
	//
	// It blocks until the context is cancelled, a non-temporary error occurs, or the
	// listener is closed. The last breaking error is returned.
	Serve(ctx context.Context) error
	// Close closes the underlying listener.
	Close(ctx context.Context) error
}

// TransportConnection represents a transport peer-to-peer connection.
type TransportConnection interface {
	// Metadata returns the transport metadata of the connection.
	Metadata() TransportMetadata
	// LocalAddr returns the local address of the connection.
	LocalAddr() netip.AddrPort
	// RemoteAddr returns the remote address of the connection.
	RemoteAddr() netip.AddrPort
	// Close closes the connection.
	Close(ctx context.Context) error
	// Messages returns iterator of messages and possible read/parse errors received
	// on the connection.
	//
	// Context can be used to yield last error and stop the iterator.
	Messages(ctx context.Context) iter.Seq2[Message, error]
	// WriteMessage writes a message to the connection.
	WriteMessage(
		ctx context.Context,
		msg Message,
		raddr netip.AddrPort,
		opts ...RenderOptions,
	) error
}
