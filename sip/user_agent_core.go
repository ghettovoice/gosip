package sip

import (
	"context"
	"log/slog"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/log"
)

// UserAgentCoreOptions are the options for the [UserAgentCore].
type UserAgentCoreOptions struct {
	// ServerTransactionFactory is the server transaction factory.
	// If nil, a [DefaultServerTransactionFactory] is used.
	ServerTransactionFactory ServerTransactionFactory
	// ServerTransactionStore is the server transaction store.
	// If nil, a [NewMemoryServerTransactionStore] is used.
	ServerTransactionStore ServerTransactionStore
	// ClientTransactionFactory is the client transaction factory.
	// If nil, a [DefaultClientTransactionFactory] is used.
	ClientTransactionFactory ClientTransactionFactory
	// ClientTransactionStore is the client transaction store.
	// If nil, a [NewMemoryClientTransactionStore] is used.
	ClientTransactionStore ClientTransactionStore
	// Log is the logger.
	// If nil, the [log.Default] is used.
	Log *slog.Logger
}

func (o *UserAgentCoreOptions) srvTxFactory() ServerTransactionFactory {
	if o == nil {
		return nil
	}
	return o.ServerTransactionFactory
}

func (o *UserAgentCoreOptions) srvTxStore() ServerTransactionStore {
	if o == nil {
		return nil
	}
	return o.ServerTransactionStore
}

func (o *UserAgentCoreOptions) clnTxFactory() ClientTransactionFactory {
	if o == nil {
		return nil
	}
	return o.ClientTransactionFactory
}

func (o *UserAgentCoreOptions) clnTxStore() ClientTransactionStore {
	if o == nil {
		return nil
	}
	return o.ClientTransactionStore
}

func (o *UserAgentCoreOptions) log() *slog.Logger {
	if o == nil || o.Log == nil {
		return log.Default()
	}
	return o.Log
}

// UserAgentCore is the User Agent Core.
type UserAgentCore struct {
	txl *TransactionLayer
	tpl *TransportLayer
	log *slog.Logger

	cancOnReq,
	cancOnRes func()
}

// NewUserAgentCore creates a new UserAgentCore.
// The transport is required and is added to the transport layer as default.
// The options are optional and can be nil.
func NewUserAgentCore(tp Transport, opts *UserAgentCoreOptions) (*UserAgentCore, error) {
	tpl := &TransportLayer{}
	if err := tpl.TrackTransport(tp, true); err != nil {
		return nil, errtrace.Wrap(err)
	}

	txl := NewTransactionLayer(&TransactionLayerOptions{
		ServerTransactionFactory: opts.srvTxFactory(),
		ServerTransactionStore:   opts.srvTxStore(),
		ClientTransactionFactory: opts.clnTxFactory(),
		ClientTransactionStore:   opts.clnTxStore(),
		Log:                      opts.log(),
	})

	ua := &UserAgentCore{
		tpl: tpl,
		txl: txl,
		log: opts.log(),
	}
	ua.cancOnReq = tpl.OnRequest(txl.RequestMiddleware(ua.recvReq))
	ua.cancOnRes = tpl.OnResponse(txl.ResponseMiddleware(nil))
	return ua, nil
}

func (ua *UserAgentCore) recvReq(ctx context.Context, tp ServerTransport, req *InboundRequest) {
	// TODO: process new request
	// - create server transaction or better some high level request handler UAS
	// - RFC 3261 Section 8.2
	// - inside/outside of dialog
	// - pass to upper layers

	if !IsKnownRequestMethod(req.Method()) {
		log.LoggerFromValues(ctx, tp).LogAttrs(ctx, slog.LevelDebug,
			"discarding inbound request due to unknown method",
			slog.Any("request", req),
		)
		respondStateless(ctx, tp, req, ResponseStatusNotImplemented)
		return
	}

	// go next steps
}

func (ua *UserAgentCore) Run(ctx context.Context) error {
	go ua.tpl.Serve()
	return nil
}

func (ua *UserAgentCore) Stop(ctx context.Context) error {
	ua.tpl.Close()
	return nil
}

// AddTransport adds a transport to the transport layer.
// It can be used to add additional non-default transports after the UserAgentCore has been created.
func (ua *UserAgentCore) AddTransport(tp Transport) error {
	return errtrace.Wrap(ua.tpl.TrackTransport(tp, false))
}

// RemoveTransport removes a transport from the transport layer.
func (ua *UserAgentCore) RemoveTransport(tp Transport) error {
	return errtrace.Wrap(ua.tpl.UntrackTransport(tp))
}

func (ua *UserAgentCore) OnRequest() {
}

func (ua *UserAgentCore) SendRequest() {
	// TODO: RFC 3261 Section 8.1
}
