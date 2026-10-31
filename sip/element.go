package sip

import (
	"context"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/pkg/log"
	"github.com/ghettovoice/gosip/sip/header"
)

type ElementID = uuid.UUID

func NextElementID() ElementID { return uuid.New() }

// Element is the composition root for SIP transport, routing, and transaction processing.
//
// It provides the protocol-level mechanics shared by user agents, proxies, dialogs,
// and other higher-level components; it does not own their authentication or business response policy.
//
// The transport and transaction managers owned by the element are private:
// all operations go through the element's own methods, and extensions attach
// via [Element.UseMiddleware] or the configured factories.
// Element methods and middleware callbacks carry the element in their context;
// transactions called directly outside the element do not acquire it automatically.
//
// The zero value is a usable element with default configuration and no transports.
// Configuration fields must be assigned before the first use of the element and must
// not be changed afterwards; an Element must not be copied after first use.
type Element struct {
	// InstanceID is the unique identifier of the element instance.
	// If zero, a new ID is generated on first use.
	InstanceID ElementID
	// BaseCapabilities is the initial capabilities of the element.
	// Empty by default.
	BaseCapabilities ElementCapabilities
	// ServerLocator is used to locate remote servers for request routing.
	// If nil, a new [RemoteElementLocator] is created with [Element.DNSResolver]
	// and current transport metadata.
	ServerLocator RemoteServerLocator
	// DNSResolver is used only to create new [RemoteElementLocator] when [Element.ServerLocator] is nil.
	// If nil, [dns.DefaultResolver] is used.
	DNSResolver DNSResolver
	// URIConverter is used to convert URIs to SIP URIs.
	// If nil, [DefaultURIConverter] is used.
	URIConverter URIConverter
	// BaseLogger is the base logger used by the element.
	// If nil, the [log.Default] is used.
	BaseLogger *slog.Logger
	// ServerTransactionFactory is the server transaction factory.
	// If nil, a [NewServerTransaction] is used.
	ServerTransactionFactory ServerTransactionFactory
	// ClientTransactionFactory is the client transaction factory.
	// If nil, a [NewClientTransaction] is used.
	ClientTransactionFactory ClientTransactionFactory
	// StaleTransactionTimeout is the emergency transaction cleanup timeout.
	// See [TransactionManager.StaleTransactionTimeout] for its semantics.
	// If zero, 1 hour is used. If negative, automatic stale cleanup is disabled.
	StaleTransactionTimeout time.Duration
	// TransactionStartTimeout limits how long a registered transaction may remain unstarted.
	// If 0, 1 minute is used. If negative, unstarted transactions are never terminated
	// automatically.
	TransactionStartTimeout time.Duration

	id      ElementID
	tpm     *TransportManager
	txm     *TransactionManager
	srvLctr RemoteServerLocator
	uriConv URIConverter
	log     *slog.Logger

	initOnce sync.Once

	closeOnce sync.Once
	closeErr  error

	shutdownOnce sync.Once
	shutdownErr  error

	// lcMu serializes lifecycle admission, middleware registration and
	// capabilities revision updates. It is never held while calling factories,
	// managers, transports or user-provided middleware code.
	lcMu  sync.Mutex
	state atomic.Uint32
	// opsWg counts operations admitted before closing starts; Close waits for
	// them before shutting down owned middleware.
	opsWg sync.WaitGroup
	// cbWg counts in-flight Element-wired middleware callbacks; Close waits for
	// them after sealing callback admission and before closing middleware.
	cbWg     sync.WaitGroup
	cbSealed bool

	// listeners are the transport-owned listeners prepared through
	// [Element.ListenManaged]; ownership stays with the transport, and managed
	// startup serves them. [Element.UntrackTransport] prunes the records of
	// the untracked protocol while the element is still ready.
	listeners []elemListener
	// listenerRev is a lazily allocated per-protocol record that
	// [Element.UntrackTransport] updates under lcMu; a listener bind that
	// races a detachment is rejected or discarded at commit time.
	listenerRev map[TransportProto]elemListenerRevision
	// cancelRun cancels the detached runtime context shared by the
	// background transaction starts and listener serves launched by
	// [Element.Start].
	cancelRun context.CancelFunc
	// runWg counts the background Start/Serve goroutines launched by
	// [Element.Start]. Entries are added only while an operation lease is
	// held, and Close waits opsWG first, so Add never races Wait.
	runWg sync.WaitGroup
	// errHdlrs holds the element error handlers bound through
	// [Element.BindErrorHandler].
	errHdlrs types.CallbackManager[ErrorHandler]

	mws syncutil.OrderedList[*elemMiddlewareEntry]

	// cpbRev is the middleware/capabilities revision; every accepted
	// registration or removal advances it under lcMu.
	cpbRev  uint64
	baseCpb ElementCapabilities
	curCpb  atomic.Value // ElementCapabilities
}

const (
	elmStateReady uint32 = iota
	elmStateRunning
	elmStateClosing
	elmStateClosed
)

// elemListener records a listener prepared through [Element.ListenManaged] for
// managed startup. The canonical protocol is cached so
// [Element.UntrackTransport] can prune records under lcMu without calling
// back into transport code.
type elemListener struct {
	proto    TransportProto
	listener TransportListener
}

// elemListenerRevision tracks a protocol's untrack generation and the number
// of untrack calls still in flight for it, so a racing bind can neither
// start nor commit during a detachment.
type elemListenerRevision struct {
	generation uint64
	untracking uint32
}

// ensureInit lazily initializes the element runtime state from the public
// configuration fields exactly once.
func (elm *Element) ensureInit() {
	elm.initOnce.Do(elm.init)
}

func (elm *Element) init() {
	elm.id = elm.InstanceID
	if elm.id == uuid.Nil() {
		elm.id = NextElementID()
	}

	elm.uriConv = elm.URIConverter
	if elm.uriConv == nil {
		elm.uriConv = defURIConverter
	}

	baseLog := elm.BaseLogger
	if baseLog == nil {
		baseLog = log.Default()
	}
	// The element attribute must use the prebuilt log value instead of the
	// element itself: resolving elm as a slog.LogValuer would re-enter initOnce.
	elm.log = baseLog.With(slog.Any("element", elm.logValue()))

	elm.tpm = &TransportManager{Logger: elm.log}

	elm.txm = &TransactionManager{
		ServerTransactionFactory: elm.ServerTransactionFactory,
		ClientTransactionFactory: elm.ClientTransactionFactory,
		StaleTransactionTimeout:  elm.StaleTransactionTimeout,
		TransactionStartTimeout:  elm.TransactionStartTimeout,
		Logger:                   elm.log,
	}

	elm.srvLctr = elm.ServerLocator
	if elm.srvLctr == nil {
		elm.srvLctr = &RemoteElementLocator{DNSResolver: elm.DNSResolver, MetadataProvider: elm.tpm}
	}

	elm.baseCpb = ElementCapabilities{
		Methods: slices.Clone(elm.BaseCapabilities.Methods),
		Options: slices.Clone(elm.BaseCapabilities.Options),
		MIMETypes: util.CloneSliceFunc(
			elm.BaseCapabilities.MIMETypes,
			func(m header.MIMERange) header.MIMERange { return m.Clone() },
		),
	}
	elm.curCpb.Store(elm.baseCpb)

	elm.tpm.UseMessageInterceptor(elm.txm)
	elm.tpm.UseMessageInterceptor(elm)
}

func (elm *Element) ID() ElementID {
	elm.ensureInit()
	return elm.id
}

func (elm *Element) Logger() *slog.Logger {
	elm.ensureInit()
	return elm.log
}

func (elm *Element) LogValue() slog.Value {
	if elm == nil {
		return slog.Value{}
	}
	elm.ensureInit()
	return elm.logValue()
}

// logValue returns the log representation of the element.
// It only reads the already initialized runtime fields, so it can be used
// while lazy initialization is in progress.
func (elm *Element) logValue() slog.Value {
	return slog.GroupValue(
		slog.String("ptr", fmt.Sprintf("%p", elm)),
		slog.Any("id", elm.id),
	)
}

func (elm *Element) InterceptOutboundRequest(
	ctx context.Context,
	next RequestSender,
	req *RequestEnvelope,
	opts ...SendRequestOptions,
) error {
	ctx = ContextWithElement(ctx, elm)

	req.WithMessage(func(r *Request) {
		EnsureRequestVia(r, req.Transport().Proto, Addr{})
		EnsureRequestFromTag(r)
		EnsureRequestMaxForwards(r)
		EnsureMessageContentLength(r)
	})

	if tp := req.Transport(); !tp.Reliable() && tp.MTU > 0 {
		sendOpts := util.LastSliceElemOr(opts, SendRequestOptions{})
		isLarge := uint(len(req.Render(sendOpts.RenderOptions))) > tp.MTU-200

		if isLarge && !sendOpts.RenderOptions.Compact {
			sendOpts.RenderOptions.Compact = true
			isLarge = uint(len(req.Render(sendOpts.RenderOptions))) > tp.MTU-200
			if !isLarge {
				opts = append(opts, sendOpts)
			}
		}

		if isLarge && !req.Metadata().Has(forceUnrelMetaKey) {
			return errors.Wrap(NewMessageTooLargeError(tp.MTU - 200))
		}
	}

	return errors.Wrap(next.SendRequest(ctx, req, opts...))
}

func (elm *Element) InterceptOutboundResponse(
	ctx context.Context,
	next ResponseSender,
	res *ResponseEnvelope,
	opts ...SendResponseOptions,
) error {
	elm.ensureInit()
	ctx = ContextWithElement(ctx, elm)

	res.WithMessage(func(r *Response) {
		EnsureResponseToTag(r, GenerateStableToTag(r, elm.id[:]))
		EnsureMessageContentLength(r)
	})

	return errors.Wrap(next.SendResponse(ctx, res, opts...))
}

func (elm *Element) InterceptInboundRequest(ctx context.Context, next RequestReceiver, req *RequestEnvelope) error {
	return errors.Wrap(next.RecvRequest(ContextWithElement(ctx, elm), req))
}

func (elm *Element) InterceptInboundResponse(ctx context.Context, next ResponseReceiver, res *ResponseEnvelope) error {
	return errors.Wrap(next.RecvResponse(ContextWithElement(ctx, elm), res))
}

// admitOp records an in-flight element operation while the element is running.
// Admission is serialized with Close under lcMu, so a racing operation
// either completes before middleware shutdown or is rejected. The returned
// release must be deferred by the caller.
func (elm *Element) admitOp() (release func(), ok bool) {
	elm.lcMu.Lock()
	defer elm.lcMu.Unlock()

	if elm.state.Load() >= elmStateClosing {
		return nil, false
	}

	elm.opsWg.Add(1)
	return elm.opsWg.Done, true
}

// admitCallback records an in-flight Element-wired middleware callback.
// Callback admission stays open while the element is closing so final
// transaction termination and transport activity can still reach middleware;
// it is sealed by Close after managers close and admitted operations drain.
func (elm *Element) admitCallback() (release func(), ok bool) {
	elm.lcMu.Lock()
	defer elm.lcMu.Unlock()

	if elm.cbSealed {
		return nil, false
	}

	elm.cbWg.Add(1)
	return elm.cbWg.Done, true
}

// Start asynchronously starts the element's managed runtime.
//
// Every server and client transaction registered at call time and still
// dormant is started, and every listener prepared through
// [Element.ListenManaged] begins serving, each in its own goroutine; Start
// returns without waiting for any of them, in particular without waiting
// for transaction activation or serving readiness. Transactions already
// started are skipped, and transactions or listeners registered after Start
// are not covered: all preparation must complete before Start, and there is
// no barrier against outbound traffic in between.
//
// The ctx argument only gates startup admission and the commitment itself:
// a done ctx rejects the call without consuming the one-shot start. The
// managed work instead runs under a detached runtime context that keeps the
// ctx values but ignores its cancellation and deadline, so canceling ctx
// after a committed Start never stops or suppresses the managed goroutines.
// The detached context is canceled by [Element.Close] (and by
// [Element.Shutdown] through it). There is no rollback or restart of
// partially launched work.
//
// Unexpected failures are reported to the handlers bound through
// [Element.BindErrorHandler]; a serve failure is delivered as a
// [*ListenerServeError], and one failure does not stop the other managed
// units.
//
// Start is a one-shot operation: concurrent and repeated calls return
// [ErrElementStarted], a call after closing began returns [ErrElementClosed],
// and a pre-canceled ctx returns its error without consuming the start.
// Calling Start is optional: a fully granular application may instead start
// transactions and serve listeners itself, see [Element.Listen].
func (elm *Element) Start(ctx context.Context) error {
	elm.ensureInit()

	// A caller context that is already done rejects the call without
	// consuming the one-shot start.
	if err := ctx.Err(); err != nil {
		return errors.Wrap(err)
	}

	elm.lcMu.Lock()
	switch st := elm.state.Load(); {
	case st == elmStateRunning:
		elm.lcMu.Unlock()
		return errors.Wrap(NewElementStartedError())
	case st >= elmStateClosing:
		elm.lcMu.Unlock()
		return errors.Wrap(NewElementClosedError())
	}

	// The caller context may have been canceled while waiting for the lock;
	// reject it here so it never consumes the one-shot start either.
	if err := ctx.Err(); err != nil {
		elm.lcMu.Unlock()
		return errors.Wrap(err)
	}

	// The operation lease is taken in the same critical section as the
	// lifecycle check and the one-shot commitment: Close drains admitted
	// operations before it waits for the runtime group launched below.
	elm.opsWg.Add(1)
	defer elm.opsWg.Done()

	elm.state.Store(elmStateRunning)

	// The runtime context inherits ctx values but detaches cancellation and
	// deadline: only Close may stop the managed work from now on.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ContextWithElement(ctx, elm)))
	elm.cancelRun = cancel

	listeners := slices.Clone(elm.listeners)
	elm.lcMu.Unlock()

	// Snapshot the registered transactions outside lcMu and before launching
	// any startup goroutine: registrations made afterwards are intentionally
	// not covered, and manager iteration must never run under lcMu.
	clnTxs := slices.Collect(elm.AllClientTransactions())
	srvTxs := slices.Collect(elm.AllServerTransactions())

	startTx := func(tx Transaction) {
		// Group entries are added while the operation lease above is held, so
		// Close drains admitted operations before it waits on the group.
		elm.runWg.Go(func() {
			if tx.Started() {
				return
			}
			txCtx := ContextWithTransaction(runCtx, tx)
			if err := tx.Start(txCtx); err != nil {
				elm.reportRunErr(txCtx, errors.ErrorfWrap("start transaction %s: %w", tx, err))
			}
		})
	}

	for _, tx := range srvTxs {
		startTx(tx)
	}
	for _, tx := range clnTxs {
		startTx(tx)
	}

	for _, entry := range listeners {
		lis := entry.listener
		elm.runWg.Go(func() {
			if err := lis.Serve(runCtx); err != nil {
				elm.reportRunErr(runCtx, errors.Wrap(NewListenerServeError(
					lis.LocalAddr(),
					lis.Metadata(),
					err,
				)))
			}
		})
	}

	return nil
}

// BindErrorHandler binds a callback invoked for unexpected background
// failures observed by the element, currently transaction Start and listener
// Serve errors reported by the goroutines launched from [Element.Start].
//
// A nil handler or a binding made once closing began returns a no-op unbind.
// Handlers are invoked outside element locks and may run concurrently.
// The returned unbind is idempotent and only excludes the handler from
// callback snapshots taken afterwards: a notification already captured for
// delivery (whether in flight or still pending behind another handler)
// still runs to completion after unbind returns. The handler must
// not call [Element.Close] or [Element.Shutdown] synchronously, since those
// wait for in-flight callbacks, but may initiate them from another
// goroutine.
func (elm *Element) BindErrorHandler(hndlr ErrorHandler) (unbind func()) {
	elm.ensureInit()
	if util.IsNil(hndlr) {
		return noop
	}

	release, ok := elm.admitOp()
	if !ok {
		return noop
	}
	defer release()

	return elm.errHdlrs.Add(hndlr)
}

// reportRunErr forwards an unexpected failure from a managed Start/Serve
// goroutine to the bound element error handlers. Nil errors and the expected
// completions (runtime context cancellation or element closing) are
// filtered out, and the callback lease lets Close drain in-flight handlers
// before middleware shutdown.
func (elm *Element) reportRunErr(ctx context.Context, err error) {
	if err == nil || ctx.Err() != nil || elm.state.Load() >= elmStateClosing {
		return
	}

	release, ok := elm.admitCallback()
	if !ok {
		return
	}
	defer release()

	ctx = ContextWithElement(ctx, elm)

	// elm.log.DebugContext(ctx, "element runtime failure", slog.Any("error", err))

	for hndlr := range elm.errHdlrs.All() {
		hndlr.HandleError(ctx, errors.Wrap(err))
	}
}

// Close closes the element, transport manager and transaction manager.
//
// New work is rejected with [ErrElementClosed] once Close begins, while
// operations already admitted are allowed to finish. Close sends the
// best-effort transaction cancels first and only then cancels the detached
// runtime context created by [Element.Start], so outbound sends still see
// live transports; it then waits for the
// admitted operations and the managed transaction-start/listener-serve
// goroutines, and then for in-flight
// middleware callbacks that Element itself wired before closing middleware,
// so middleware never observes an Element-wired notification after
// its own Close. Callbacks or asynchronous work created directly by middleware
// remain that middleware's lifecycle responsibility.
// Close must not be invoked synchronously from one of those operations or
// callbacks, since it would then wait for itself.
func (elm *Element) Close() error {
	elm.ensureInit()
	elm.closeOnce.Do(func() {
		elm.lcMu.Lock()
		elm.state.Store(elmStateClosing)
		cncRun := elm.cancelRun
		elm.lcMu.Unlock()

		elm.closeErr = elm.close(cncRun)

		elm.lcMu.Lock()
		elm.state.Store(elmStateClosed)
		elm.lcMu.Unlock()

		elm.log.Debug("element closed")
	})
	return errors.Wrap(elm.closeErr)
}

func (elm *Element) close(cncRun context.CancelFunc) error {
	// Cancel proceeding INVITE client transactions best-effort while transports
	// and middleware are still alive. The private send path bypasses the
	// admission lease, which is already closed for new work.
	for tx := range elm.AllClientTransactions() {
		if tx.Type() == TransactionTypeClientInvite && tx.State() == TransactionStateProceeding {
			if cncReq, err := NewCancelRequestEnvelope(tx.Request()); err == nil {
				func() {
					ctx, cnc := context.WithTimeout(context.Background(), 5*time.Second)
					defer cnc()

					if err = elm.sendRequest(ctx, cncReq, true); err != nil {
						elm.log.Warn(
							"failed to cancel transaction",
							slog.Any("error", err),
							slog.Any("transaction", tx),
						)
					}
				}()
			}
		}
	}

	// Release transaction starts and listener serves still blocked on the
	// detached runtime context only after the best-effort CANCELs went out:
	// cancellation may also tear down transport sockets the sends still need.
	if cncRun != nil {
		cncRun()
	}

	txmErr := elm.txm.Close()
	tpmErr := elm.tpm.Close()

	// Admitted operations may still interact with middleware; wait for them to
	// finish, then drain the managed Start/Serve goroutines: runtime group
	// entries are added only under an operation lease, so this Wait cannot
	// race an Add. Afterwards seal callback admission so no new Element-wired
	// callback can begin, and drain the ones already in flight.
	elm.opsWg.Wait()
	elm.runWg.Wait()

	elm.lcMu.Lock()
	elm.cbSealed = true
	elm.lcMu.Unlock()

	elm.cbWg.Wait()

	// Detach the bindings Element installed for each middleware, then close the
	// middleware in registration order.
	for entry := range elm.mws.All() {
		for _, unbind := range entry.unbinds {
			unbind()
		}
		if c, ok := entry.mw.(io.Closer); ok {
			if closeErr := c.Close(); closeErr != nil {
				elm.log.Warn(
					"failed to close middleware",
					slog.Any("error", closeErr),
					slog.Any("middleware", entry.mw),
				)
			}
		}
	}

	return errors.JoinPrefix("element close errors:", txmErr, tpmErr)
}

// Shutdown gracefully shuts down the element.
//
// Shutdown runs the optional hook
//
//	Shutdown(ctx context.Context) error
//
// of every registered middleware that implements it, sequentially in
// registration order over the middleware snapshot taken when Shutdown begins,
// and then closes the element with [Element.Close]. A middleware is assumed
// to have completed its own work when its hook returns; Close then terminates
// the remaining transactions and transports and closes all middleware. Close
// always follows the returned hooks, even when a hook fails or ctx is
// canceled, and the collected hook errors are joined with the Close result.
//
// The context is cooperative for the hooks: they inherit its cancellation,
// deadline and values along with the element reference, but it does not bound
// the final Close. Only the first call's context is used; concurrent and repeated calls share one
// shutdown, run the hooks at most once and return the same result. A call
// after the element already began closing skips the hooks and returns the
// Close result.
//
// Ordinary element operations remain usable while the hooks run unless a
// concurrent direct [Element.Close] begins; such a close is a hard override
// that starts transport/transaction shutdown without waiting for the hooks,
// while still waiting for them before closing middleware. The caller must
// stop new application work
// and middleware mutation before invoking Shutdown, which deliberately does
// not seal operation admission before running the hooks. Like Close, Shutdown
// must not be invoked synchronously from an admitted element operation, an
// Element-wired middleware callback or a shutdown hook, since it would then
// wait for itself.
func (elm *Element) Shutdown(ctx context.Context) error {
	elm.ensureInit()
	ctx = ContextWithElement(ctx, elm)
	elm.shutdownOnce.Do(func() {
		mwErr := elm.shutdownMiddleware(ctx)
		// The hooks released their operation lease before returning, so the
		// final Close cannot wait for this call.
		closeErr := elm.Close()
		elm.shutdownErr = errors.JoinPrefix("element shutdown errors:", mwErr, closeErr)
	})
	return errors.Wrap(elm.shutdownErr)
}

func (elm *Element) TrackTransport(tp Transport) error {
	elm.ensureInit()

	release, ok := elm.admitOp()
	if !ok {
		return errors.Wrap(NewElementClosedError())
	}
	defer release()

	return errors.Wrap(elm.tpm.TrackTransport(tp))
}

// UntrackTransport unregisters a transport while the element is still being
// prepared.
//
// Untracking is only allowed before managed startup committed, i.e. before
// [Element.Start]: a call while the element is started returns
// [ErrElementStarted], and a call once closing began returns
// [ErrElementClosed]. Granular usage without [Element.Start] keeps
// untracking as before. Listeners the element prepared through
// [Element.ListenManaged] on the untracked protocol are removed from the
// managed launch set but are neither served nor closed: ownership stays
// with the caller and the transport. A bind that is still in flight when
// the transport is untracked discards its fresh listener and fails with
// [ErrNoTransport], even if the same protocol is tracked again before the
// bind returns.
func (elm *Element) UntrackTransport(tp Transport) error {
	elm.ensureInit()

	release, ok := elm.admitOp()
	if !ok {
		return errors.Wrap(NewElementClosedError())
	}
	defer release()

	if util.IsNil(tp) {
		return errors.ErrorWrap("nil transport")
	}
	proto := tp.Metadata().Proto.Canonic()

	elm.lcMu.Lock()
	switch st := elm.state.Load(); {
	case st == elmStateRunning:
		elm.lcMu.Unlock()
		return errors.Wrap(NewElementStartedError())
	case st >= elmStateClosing:
		elm.lcMu.Unlock()
		return errors.Wrap(NewElementClosedError())
	}
	if elm.listenerRev == nil {
		elm.listenerRev = make(map[TransportProto]elemListenerRevision)
	}
	rev := elm.listenerRev[proto]
	rev.generation++
	rev.untracking++
	elm.listenerRev[proto] = rev
	// Prune the protocol's prepared listeners before releasing the lock, so
	// a racing Start can never launch them.
	elm.listeners = slices.DeleteFunc(elm.listeners, func(entry elemListener) bool {
		return entry.proto == proto
	})
	elm.lcMu.Unlock()
	defer func() {
		// The in-flight mark covers the whole manager call; re-read the
		// current record because a concurrent untrack of the same protocol
		// may have replaced it and must keep its own count.
		elm.lcMu.Lock()
		if cur := elm.listenerRev[proto]; cur.untracking > 0 {
			cur.untracking--
			elm.listenerRev[proto] = cur
		}
		elm.lcMu.Unlock()
	}()

	return errors.Wrap(elm.tpm.UntrackTransport(tp))
}

func (elm *Element) TransportByProto(proto TransportProto) (Transport, bool) {
	elm.ensureInit()
	return elm.tpm.TransportByProto(proto)
}

func (elm *Element) TransportFromRequest(req *RequestEnvelope) (Transport, bool) {
	elm.ensureInit()
	return elm.tpm.TransportFromRequest(req)
}

func (elm *Element) TransportFromResponse(res *ResponseEnvelope) (Transport, bool) {
	elm.ensureInit()
	return elm.tpm.TransportFromResponse(res)
}

func (elm *Element) AllTransports() iter.Seq[Transport] {
	elm.ensureInit()
	return elm.tpm.AllTransports()
}

func (elm *Element) TransportMetadataByProto(proto TransportProto) (TransportMetadata, bool) {
	elm.ensureInit()
	return elm.tpm.TransportMetadataByProto(proto)
}

func (elm *Element) TransportMetadataByNAPTRService(service string) (TransportMetadata, bool) {
	elm.ensureInit()
	return elm.tpm.TransportMetadataByNAPTRService(service)
}

func (elm *Element) AllTransportMetadata() iter.Seq[TransportMetadata] {
	elm.ensureInit()
	return elm.tpm.AllTransportMetadata()
}

// Listen binds a new listener on the given transport protocol and local
// address without registering it for managed startup.
//
// The returned listener is never served by [Element.Start]: the caller owns
// the Serve call, its context, the returned errors and the goroutine wait.
// The ctx argument controls the bind only. The listener stays
// transport-owned, so [Element.Close] still closes it through the tracked
// transport but does not wait for the caller's Serve goroutine.
// Listen is allowed before and after managed Start and is rejected
// with [ErrElementClosed] once closing began. For listeners managed by
// [Element.Start], see [Element.ListenManaged].
func (elm *Element) Listen(ctx context.Context, proto TransportProto, addr string) (TransportListener, error) {
	elm.ensureInit()
	ctx = ContextWithElement(ctx, elm)

	release, ok := elm.admitOp()
	if !ok {
		return nil, errors.Wrap(NewElementClosedError())
	}
	defer release()

	return errors.Wrap2(elm.tpm.Listen(ctx, proto, addr))
}

// ListenManaged binds a new listener on the given transport protocol and local
// address and retains it for managed startup. For a caller-served granular
// listener that [Element.Start] does not serve, see [Element.Listen].
//
// The listener stays transport-owned and does not serve yet: serving begins
// on [Element.Start], so listener preparation must finish before it.
// A call after Start returns [ErrElementStarted] and a call once closing
// began returns [ErrElementClosed]. If Start or Close wins the race while
// the bind is in progress, the fresh listener is closed again instead of
// being retained, and the matching lifecycle error is returned. If the
// protocol's transport was untracked while the bind was in progress, the
// fresh listener is discarded the same way and [ErrNoTransport] is
// returned, even when the protocol was tracked again in the meantime.
func (elm *Element) ListenManaged(ctx context.Context, proto TransportProto, addr string) error {
	elm.ensureInit()
	ctx = ContextWithElement(ctx, elm)

	elm.lcMu.Lock()
	switch st := elm.state.Load(); {
	case st == elmStateRunning:
		elm.lcMu.Unlock()
		return errors.Wrap(NewElementStartedError())
	case st >= elmStateClosing:
		elm.lcMu.Unlock()
		return errors.Wrap(NewElementClosedError())
	}

	proto = proto.Canonic()
	rev := elm.listenerRev[proto]
	// A detachment is in progress for the whole manager call, so a new bind
	// on this protocol is rejected before any transport work: the manager
	// lookup could still resolve the transport it is about to drop.
	if rev.untracking != 0 {
		elm.lcMu.Unlock()
		return errors.Wrap(NewNoTransportError("transport untracked during bind"))
	}

	elm.opsWg.Add(1)
	defer elm.opsWg.Done()

	gen := rev.generation
	elm.lcMu.Unlock()

	lis, err := elm.tpm.Listen(ctx, proto, addr)
	if err != nil {
		return errors.Wrap(err)
	}

	// The bind may have raced with Start, Close or UntrackTransport, none of
	// which waits for the lease: re-check the lifecycle state first, then the
	// cached untrack snapshot. A loser is closed again outside the lock
	// rather than being forgotten or left unserved.
	elm.lcMu.Lock()
	rev = elm.listenerRev[proto]
	var lcErr error
	switch st := elm.state.Load(); {
	case st == elmStateRunning:
		lcErr = NewElementStartedError()
	case st >= elmStateClosing:
		lcErr = NewElementClosedError()
	case rev.generation != gen || rev.untracking != 0:
		lcErr = NewNoTransportError("transport untracked during bind")
	default:
		elm.listeners = append(elm.listeners, elemListener{proto: proto, listener: lis})
	}
	elm.lcMu.Unlock()

	if lcErr != nil {
		return errors.JoinWrap(lcErr, errors.Wrap(lis.Close()))
	}
	return nil
}

func (elm *Element) RegisterClientTransaction(ctx context.Context, tx ClientTransaction) error {
	elm.ensureInit()
	ctx = ContextWithElement(ctx, elm)

	release, ok := elm.admitOp()
	if !ok {
		return errors.Wrap(NewElementClosedError())
	}
	defer release()

	return errors.Wrap(elm.txm.RegisterClientTransaction(ctx, tx))
}

func (elm *Element) NewClientTransaction(
	ctx context.Context,
	req *RequestEnvelope,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (ClientTransaction, error) {
	elm.ensureInit()
	ctx = ContextWithElement(ctx, elm)

	release, ok := elm.admitOp()
	if !ok {
		return nil, errors.Wrap(NewElementClosedError())
	}
	defer release()

	txOpts := util.LastSliceElemOr(opts, ClientTransactionOptions{})
	if txOpts.Logger == nil {
		txOpts.Logger = elm.log
	} else {
		txOpts.Logger = txOpts.Logger.With(slog.Any("element", elm))
	}

	return errors.Wrap2(elm.txm.NewClientTransaction(ctx, req, tp, txOpts))
}

func (elm *Element) LoadClientTransaction(key ClientTransactionKey) (ClientTransaction, bool) {
	elm.ensureInit()
	return elm.txm.LoadClientTransaction(key)
}

func (elm *Element) AllClientTransactions() iter.Seq[ClientTransaction] {
	elm.ensureInit()
	return elm.txm.AllClientTransactions()
}

func (elm *Element) RegisterServerTransaction(ctx context.Context, tx ServerTransaction) error {
	elm.ensureInit()
	ctx = ContextWithElement(ctx, elm)

	release, ok := elm.admitOp()
	if !ok {
		return errors.Wrap(NewElementClosedError())
	}
	defer release()

	return errors.Wrap(elm.txm.RegisterServerTransaction(ctx, tx))
}

func (elm *Element) NewServerTransaction(
	ctx context.Context,
	req *RequestEnvelope,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (ServerTransaction, error) {
	elm.ensureInit()
	ctx = ContextWithElement(ctx, elm)

	release, ok := elm.admitOp()
	if !ok {
		return nil, errors.Wrap(NewElementClosedError())
	}
	defer release()

	txOpts := util.LastSliceElemOr(opts, ServerTransactionOptions{})
	if txOpts.Logger == nil {
		txOpts.Logger = elm.log
	} else {
		txOpts.Logger = txOpts.Logger.With(slog.Any("element", elm))
	}

	return errors.Wrap2(elm.txm.NewServerTransaction(ctx, req, tp, txOpts))
}

func (elm *Element) LoadServerTransaction(key ServerTransactionKey) (ServerTransaction, bool) {
	elm.ensureInit()
	return elm.txm.LoadServerTransaction(key)
}

func (elm *Element) AllServerTransactions() iter.Seq[ServerTransaction] {
	elm.ensureInit()
	return elm.txm.AllServerTransactions()
}

// RequestAttempt binds one cloned request to one resolved next-hop candidate.
//
// Attempts are produced by [Element.ProduceRequestAttempts] and are executed
// by [RequestAttempt.DoStateless] or [RequestAttempt.DoStateful].
// The exported request and address let a higher level adjust an attempt before sending;
// the [Element] owns only the private state used to coordinate branch generation
// and size fallbacks.
type RequestAttempt struct {
	// Addr is the concrete transport and network destination for this attempt.
	Addr ResolvedAddr
	// Request is the request copy prepared for this attempt.
	Request *RequestEnvelope

	elm         *Element
	sharedState *reqAttemptsSharedState
	forceUnrel  bool
}

// IsRFC2543Fallback reports whether this attempt is the compatibility retry
// that deliberately sends an oversized request over an unreliable transport.
func (ra *RequestAttempt) IsRFC2543Fallback() bool { return ra.forceUnrel }

const forceUnrelMetaKey = "sip.force_unreliable"

// prepareReq applies the selected transport and destination to the attempt.
//
// A normal DNS/transaction retry receives a fresh branch.
// The only branch reuse is the local UDP-to-reliable size switch, which happens
// before the request has been successfully delivered and is still the same logical send.
func (ra *RequestAttempt) prepareReq(tpMeta TransportMetadata, reuseBranch bool) *RequestAttempt {
	if ra.forceUnrel && !tpMeta.Reliable() {
		ra.Request.Metadata().Set(forceUnrelMetaKey, true)
	} else {
		ra.Request.Metadata().Delete(forceUnrelMetaKey)
	}

	ra.Request.
		SetTransport(tpMeta).
		SetRemoteAddr(ra.Addr.Addr).
		WithMessage(func(r *Request) {
			// ensure that each attempt sends request with unique branch
			EnsureRequestVia(r, tpMeta.Proto, Addr{})
			via, _ := r.Headers.FirstVia()
			currBranch, _ := via.Branch()
			if !reuseBranch && currBranch == ra.sharedState.prevBranch {
				currBranch = GenerateBranch(0)
				via.Params.Set("branch", currBranch)
			}
			ra.sharedState.prevBranch = currBranch
		})

	return ra
}

// DoStateless sends this attempt without creating a SIP transaction.
//
// It may switch a direct, non-service-record UDP candidate to a matching reliable
// transport when the message is too large. Service-record candidates are deferred
// so the next RFC 3263 candidate is tried first.
func (ra *RequestAttempt) DoStateless(ctx context.Context, opts ...SendRequestOptions) error {
	ctx = ContextWithElement(ctx, ra.elm)

	release, ok := ra.elm.admitOp()
	if !ok {
		return errors.Wrap(NewElementClosedError())
	}
	defer release()

	return errors.Wrap(ra.doStateless(ctx, opts...))
}

// doStateless is the lease-free core of [RequestAttempt.DoStateless]; internal
// shutdown sends call it directly after public admission has been sealed.
func (ra *RequestAttempt) doStateless(ctx context.Context, opts ...SendRequestOptions) error {
	tp, ok := ra.elm.TransportByProto(ra.Addr.Transport)
	if !ok {
		return errors.Wrap(NewNoTransportError())
	}

	sendOpts := util.LastSliceElemOr(opts, SendRequestOptions{})

	err := ra.prepareReq(tp.Metadata(), false).sendStateless(ctx, tp, sendOpts)
	if err != nil && !ra.forceUnrel && !tp.Metadata().Reliable() &&
		(errors.Is(err, ErrMessageBodyTooLarge) || errors.Is(err, ErrMessageTooLarge)) {
		if ra.Addr.FromDNS {
			// defer to fallback, re-try will be on next reliable attempt
			ra.sharedState.unrelFallback = append(ra.sharedState.unrelFallback, ra.Addr)
		} else {
			// re-try with switch to reliable transport
			for newTp := range ra.elm.AllTransports() {
				if newTp.Metadata().Reliable() && newTp.Metadata().Secured() == tp.Metadata().Secured() {
					newAddr := ra.Addr
					newAddr.Transport = newTp.Metadata().Proto

					err = ra.prepareReq(newTp.Metadata(), true).sendStateless(ctx, newTp, sendOpts)
					if err == nil {
						ra.Addr = newAddr
					} else if IsTransportError(err) {
						ra.sharedState.unrelFallback = append(ra.sharedState.unrelFallback, ra.Addr)
					}
					break
				}
			}
		}
	}

	return errors.Wrap(err)
}

func (ra *RequestAttempt) sendStateless(ctx context.Context, tp ClientTransport, opts SendRequestOptions) error {
	return errors.Wrap(tp.SendRequest(ctx, ra.Request, opts))
}

// DoStateful creates and starts a client transaction for this attempt.
//
// A successful return means the initial request was accepted by the selected
// transport; response policy and the remainder of the transaction lifecycle
// belong to the caller of the sending API.
func (ra *RequestAttempt) DoStateful(ctx context.Context, opts ...ClientTransactionOptions) (ClientTransaction, error) {
	ctx = ContextWithElement(ctx, ra.elm)

	release, ok := ra.elm.admitOp()
	if !ok {
		return nil, errors.Wrap(NewElementClosedError())
	}
	defer release()

	tx, err := ra.doStateful(ctx, opts...)
	return tx, errors.Wrap(err)
}

// doStateful is the lease-free core of [RequestAttempt.DoStateful].
func (ra *RequestAttempt) doStateful(ctx context.Context, opts ...ClientTransactionOptions) (ClientTransaction, error) {
	tp, ok := ra.elm.TransportByProto(ra.Addr.Transport)
	if !ok {
		return nil, errors.Wrap(NewNoTransportError())
	}

	txOpts := util.LastSliceElemOr(opts, ClientTransactionOptions{})

	tx, err := ra.prepareReq(tp.Metadata(), false).sendStateful(ctx, tp, txOpts)
	if err != nil && !ra.forceUnrel && !tp.Metadata().Reliable() &&
		(errors.Is(err, ErrMessageBodyTooLarge) || errors.Is(err, ErrMessageTooLarge)) {
		if ra.Addr.FromDNS {
			// defer to fallback, re-try will be on next reliable attempt
			ra.sharedState.unrelFallback = append(ra.sharedState.unrelFallback, ra.Addr)
		} else {
			// re-try with switch to reliable transport
			for newTp := range ra.elm.AllTransports() {
				if newTp.Metadata().Reliable() && newTp.Metadata().Secured() == tp.Metadata().Secured() {
					newAddr := ra.Addr
					newAddr.Transport = newTp.Metadata().Proto

					tx, err = ra.prepareReq(newTp.Metadata(), true).sendStateful(ctx, newTp, txOpts)
					if err == nil {
						ra.Addr = newAddr
					} else if IsTransportError(err) {
						ra.sharedState.unrelFallback = append(ra.sharedState.unrelFallback, ra.Addr)
					}
					break
				}
			}
		}
	}

	return tx, errors.Wrap(err)
}

func (ra *RequestAttempt) sendStateful(ctx context.Context, tp Transport, opts ClientTransactionOptions) (ClientTransaction, error) {
	tx, err := ra.elm.NewClientTransaction(ctx, ra.Request.Clone().(*RequestEnvelope), tp, opts) //nolint:forcetypeassert
	if err != nil {
		return nil, errors.Wrap(err)
	}

	if err := tx.Start(ContextWithTransaction(ctx, tx)); err != nil {
		return nil, errors.Wrap(err)
	}

	ra.Request.UpdateFrom(tx.Request())
	return tx, nil
}

// reqAttemptsSharedState is shared by the lazy attempt sequence.
//
// A primary attempt can append an unreliable size fallback while it is being executed;
// the fallback is emitted only after all primary candidates have been tried.
type reqAttemptsSharedState struct {
	unrelFallback []ResolvedAddr
	prevBranch    string
}

// RequestAttemptError describes a failed attempt together with the request and
// transaction state available to the caller.
type RequestAttemptError struct {
	// Cause is the transport, transaction, timeout, or response-classification error.
	Cause error
	// Addr is the candidate used by the failed attempt.
	Addr ResolvedAddr
	// Request is the exact request copy used by the attempt.
	Request *RequestEnvelope
	// Transaction is the client transaction, when one was started.
	Transaction ClientTransaction
	// CancelTransaction is the transaction created while cancelling the request.
	CancelTransaction ClientTransaction
	// Terminal prevents the request runner from trying another candidate.
	Terminal bool
}

func (e *RequestAttemptError) Error() string {
	if e == nil || e.Cause == nil {
		return sNilTag
	}
	return fmt.Sprintf("send request to %q: %v", e.Addr, e.Cause)
}

func (e *RequestAttemptError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

const lookupTargetMetaKey = "sip.lookup_target"

// ResolveRequestTarget resolves the next-hop target following RFC 3261
// Section 8.1.2.
//
// If the request has no pre-resolved remote address, it applies strict/loose
// Route processing before DNS resolution and may mutate the request to perform
// the RFC 2543 strict-router transformation.
//
// A valid remote address is an explicit contract that the caller has already
// performed all required route-set transformations and selected the next hop.
// The resolved target is cached in request metadata because all attempts for
// one send operation use the same routing decision.
func (elm *Element) ResolveRequestTarget(ctx context.Context, req *RequestEnvelope) (*URI, error) {
	elm.ensureInit()
	ctx = ContextWithElement(ctx, elm)

	if v, ok := req.Metadata().Get(lookupTargetMetaKey); ok {
		if trgt, ok := v.(*URI); ok {
			return trgt, nil
		}
	}

	if addr := req.RemoteAddr(); addr.IsValid() {
		trgt := &URI{
			Addr: MakeIPPortAddr(addr.Addr().AsSlice(), addr.Port()),
		}
		if tp := req.Transport(); tp.IsValid() {
			trgt.Params = make(Values).Set("transport", string(tp.Proto))
		}
		req.WithMessage(func(r *Request) {
			if u, ok := r.URI.(*URI); ok {
				trgt.Secured = u.Secured
			}
		})
		req.Metadata().Set(lookupTargetMetaKey, trgt)
		return trgt, nil
	}

	var (
		trgt *URI
		err  error
	)
	req.WithMessage(func(r *Request) {
		var secured bool

		reqURI := r.URI
		if u, ok := reqURI.(*URI); ok {
			trgt = u.Clone().(*URI) //nolint:forcetypeassert
			secured = u.Secured
		}

		route, ok := r.Headers.FirstRoute()
		if !ok || !route.IsValid() {
			if trgt == nil {
				trgt, err = elm.uriConv.ConvertURI(ctx, reqURI.Clone())
				if err != nil {
					err = errors.Wrap(err)
					return
				}
			}
			return
		}

		var routeURI *URI
		routeURI, err = elm.uriConv.ConvertURI(ctx, route.URI)
		if err != nil {
			err = errors.Wrap(err)
			return
		}

		trgt = routeURI.Clone().(*URI) //nolint:forcetypeassert
		if secured {
			trgt.Secured = secured
		}

		if !routeURI.LR() {
			r.Headers.
				AppendRoute(header.RouteHop{URI: reqURI.Clone()}).
				PopFirstRoute()
			r.URI = cleanReqURI(routeURI)
		}
	})
	if err != nil {
		return nil, errors.Wrap(err)
	}

	req.Metadata().Set(lookupTargetMetaKey, trgt)
	return trgt, nil
}

// ProduceRequestAttempts lazily produces ordered request attempts for the
// resolved next-hop candidates.
//
// Each attempt owns a clone of req, so routing, transport metadata, and branch
// changes do not leak between candidates.
//
// The sequence first emits DNS candidates.
// If an executed attempt discovers a size-related unreliable-transport fallback,
// that candidate is appended only after all primary candidates have been consumed.
// Consequently callers should execute attempts as they iterate them rather than
// collect the sequence before sending.
func (elm *Element) ProduceRequestAttempts(
	ctx context.Context,
	req *RequestEnvelope,
	opts ...LookupMessageAddrsOptions,
) (iter.Seq[*RequestAttempt], error) {
	elm.ensureInit()
	ctx = ContextWithElement(ctx, elm)

	trgt, err := elm.ResolveRequestTarget(ctx, req)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	// Produce attempts based on DNS lookup procedure.
	// This is the common path for most outbound requests.
	return func(yield func(*RequestAttempt) bool) {
		lookupOpts := util.LastSliceElemOr(opts, LookupMessageAddrsOptions{})
		sharedState := &reqAttemptsSharedState{}

		for addr := range elm.srvLctr.LookupRequestAddrs(ctx, trgt, lookupOpts) {
			req := req.Clone().(*RequestEnvelope) //nolint:forcetypeassert
			req.SetRemoteAddr(addr.Addr)
			if tp, ok := elm.TransportMetadataByProto(addr.Transport); ok {
				req.SetTransport(tp)
			}

			if !yield(&RequestAttempt{
				Request:     req,
				Addr:        addr,
				elm:         elm,
				sharedState: sharedState,
			}) {
				return
			}
		}

		for _, addr := range sharedState.unrelFallback {
			req := req.Clone().(*RequestEnvelope) //nolint:forcetypeassert
			req.SetRemoteAddr(addr.Addr)
			if tp, ok := elm.TransportMetadataByProto(addr.Transport); ok {
				req.SetTransport(tp)
			}

			if !yield(&RequestAttempt{
				Request:     req,
				Addr:        addr,
				elm:         elm,
				sharedState: sharedState,
				forceUnrel:  true,
			}) {
				return
			}
		}
	}, nil
}

// runReqAttempts is the common execution loop for stateless and stateful
// request sending.
//
// It owns candidate iteration and retry classification, but delegates the actual
// send and attempt-specific policy to its callbacks.
func (elm *Element) runReqAttempts(
	ctx context.Context,
	req *RequestEnvelope,
	lookupOpts LookupMessageAddrsOptions,
	beforeSend func(context.Context, *RequestAttempt) bool,
	send func(context.Context, *RequestAttempt) error,
	onError func(context.Context, *RequestAttempt, error) bool,
	useFallback bool,
) error {
	attempts, err := elm.ProduceRequestAttempts(ctx, req, lookupOpts)
	if err != nil {
		return errors.Wrap(err)
	}

	var errs []error
	for attempt := range attempts {
		if !useFallback && attempt.forceUnrel {
			continue
		}

		if beforeSend != nil && !beforeSend(ctx, attempt) {
			continue
		}

		err := send(ctx, attempt)
		if err == nil {
			return nil
		}

		errs = append(errs, err)

		if errors.Is(err, ctx.Err()) ||
			errors.Is(err, ErrElementClosed) ||
			errors.Is(err, ErrTransportManagerClosed) ||
			errors.Is(err, ErrTransactionManagerClosed) ||
			(IsMessageError(err) &&
				!errors.Is(err, ErrMessageBodyTooLarge) &&
				!errors.Is(err, ErrMessageTooLarge)) {
			break
		}

		if err, ok := errors.AsType[*RequestAttemptError](err); ok && err.Terminal {
			break
		}

		if onError != nil && !onError(ctx, attempt, errors.Wrap(err)) {
			break
		}
	}

	if len(errs) == 0 {
		return errors.Wrap(NewNoAddressError())
	}
	return errors.JoinPrefixWrap("send request errors:", errs...)
}

// SendRequest implements the [RequestSender] contract for [Element].
//
// It resolves the request target but sends only through the first candidate;
// callers that need DNS candidate failover should use [Element.SendRequestStateless].
// The request is updated with the successfully prepared attempt before returning.
func (elm *Element) SendRequest(ctx context.Context, req *RequestEnvelope, opts ...SendRequestOptions) error {
	elm.ensureInit()
	if elm.state.Load() >= elmStateClosing {
		return errors.Wrap(NewElementClosedError())
	}
	ctx = ContextWithElement(ctx, elm)
	return errors.Wrap(elm.sendRequest(ctx, req, false, opts...))
}

// sendRequest is the lease-free core of [Element.SendRequest]. Target
// resolution and DNS lookup are not covered by the operation lease: they may
// block arbitrarily long and must never delay shutdown. Only the actual
// attempt send is admitted; pass allowClosing when calling after admission
// has already been sealed, e.g. for the best-effort transaction cancellation
// performed while shutting down.
func (elm *Element) sendRequest(
	ctx context.Context,
	req *RequestEnvelope,
	allowClosing bool,
	opts ...SendRequestOptions,
) error {
	ctx = ContextWithElement(ctx, elm)

	sendOpts := util.LastSliceElemOr(opts, SendRequestOptions{})
	sendOpts.LookupOptions.StableDNSRecordsOrder = true

	attempts, err := elm.ProduceRequestAttempts(ctx, req, sendOpts.LookupOptions)
	if err != nil {
		return errors.Wrap(err)
	}

	attempt, ok := util.SeqFirst(attempts)
	if !ok {
		return errors.Wrap(NewNoAddressError())
	}

	if allowClosing {
		err = attempt.doStateless(ctx, sendOpts)
	} else {
		err = attempt.DoStateless(ctx, sendOpts)
	}
	if err != nil {
		return errors.Wrap(err)
	}

	req.UpdateFrom(attempt.Request)

	return nil
}

// SendResponse sends a SIP response statelessly using the element's transport manager.
func (elm *Element) SendResponse(ctx context.Context, res *ResponseEnvelope, opts ...SendResponseOptions) error {
	elm.ensureInit()
	ctx = ContextWithElement(ctx, elm)

	release, ok := elm.admitOp()
	if !ok {
		return errors.Wrap(NewElementClosedError())
	}
	defer release()

	sendOpts := util.LastSliceElemOr(opts, SendResponseOptions{})
	sendOpts.LookupOptions.StableDNSRecordsOrder = true

	return errors.Wrap(elm.tpm.SendResponse(ctx, res, sendOpts))
}

// Respond sends a SIP response statelessly using the element's transport manager.
func (elm *Element) Respond(
	ctx context.Context,
	req *RequestEnvelope,
	sts ResponseStatus,
	opts ...RespondOptions,
) error {
	elm.ensureInit()
	if elm.state.Load() >= elmStateClosing {
		return errors.Wrap(NewElementClosedError())
	}
	ctx = ContextWithElement(ctx, elm)

	resOpts := util.LastSliceElemOr(opts, RespondOptions{})
	if resOpts.ResponseOptions.LocalTag == "" {
		req.WithMessage(func(r *Request) {
			resOpts.ResponseOptions.LocalTag = GenerateStableToTag(r, elm.id[:])
		})
	}

	return errors.Wrap(Respond(ctx, req, sts, elm, resOpts))
}

type SendRequestStatelessOptions struct {
	// SendOptions are applied to every candidate attempt.
	SendOptions SendRequestOptions
	// BeforeAttempt can modify or reject a candidate before it is sent.
	// A false result skips that candidate without treating it as a failure.
	BeforeAttempt func(context.Context, *RequestAttempt) bool
	// OnAttemptError observes a failed candidate. Returning false stops the
	// DNS failover loop; nil means that retry classification remains automatic.
	OnAttemptError func(context.Context, *RequestAttempt, error) bool
	// UseRFC2543Fallback permits retrying an oversized request over an
	// unreliable transport after reliable candidates have been exhausted.
	UseRFC2543Fallback bool
}

// SendRequestStateless sends a request without a transaction while trying the
// ordered DNS candidates.
//
// Stateless sending can retry local transport/write failures, but it cannot detect
// SIP responses or transaction timeouts; those require [Element.SendRequestStateful]
// and a [ClientTransaction].
func (elm *Element) SendRequestStateless(
	ctx context.Context,
	req *RequestEnvelope,
	opts ...SendRequestStatelessOptions,
) error {
	elm.ensureInit()
	if elm.state.Load() >= elmStateClosing {
		return errors.Wrap(NewElementClosedError())
	}
	ctx = ContextWithElement(ctx, elm)

	sendOpts := util.LastSliceElemOr(opts, SendRequestStatelessOptions{})
	sendOpts.SendOptions.LookupOptions.StableDNSRecordsOrder = true

	var successAttempt *RequestAttempt
	err := elm.runReqAttempts(
		ctx, req,
		sendOpts.SendOptions.LookupOptions,
		sendOpts.BeforeAttempt,
		func(ctx context.Context, attempt *RequestAttempt) error {
			if err := attempt.DoStateless(ctx, sendOpts.SendOptions); err != nil {
				return errors.Wrap(&RequestAttemptError{
					Cause:   err,
					Addr:    attempt.Addr,
					Request: attempt.Request.Clone().(*RequestEnvelope), //nolint:forcetypeassert
				})
			}

			successAttempt = attempt

			return nil
		},
		sendOpts.OnAttemptError,
		sendOpts.UseRFC2543Fallback,
	)
	if err != nil {
		return errors.Wrap(err)
	}

	req.UpdateFrom(successAttempt.Request)

	return nil
}

type SendRequestStatefulOptions struct {
	// SendOptions are applied to every transaction attempt.
	SendOptions SendRequestOptions
	// Timing configures the client transaction.
	// If zero, [DefaultTimings] is used.
	Timing TimingConfig
	// Logger configures the client transaction logger.
	// If nil, the [Element] logger is inherited.
	Logger *slog.Logger
	// BeforeAttempt can modify or reject a candidate before its transaction is created.
	// A false result skips that candidate.
	BeforeAttempt func(context.Context, *RequestAttempt) bool
	// OnAttemptProgress observes provisional 101-199 responses while [Element]
	// confirms the current direction.
	// The callback is informational; it does not transfer transaction ownership
	// before confirmation completes.
	OnAttemptProgress func(context.Context, *RequestAttempt, *ResponseEnvelope)
	// OnAttemptError observes a failed candidate.
	// Returning false stops DNS failover; nil leaves the built-in RFC 3263
	// classification in control.
	OnAttemptError func(context.Context, *RequestAttempt, error) bool
	// UseRFC2543Fallback permits retrying an oversized request over an
	// unreliable transport after reliable candidates have been exhausted.
	UseRFC2543Fallback bool
}

// SendRequestStateful resolves and tries request candidates using client transactions.
//
// [Element] keeps an attempt alive until the current direction is confirmed
// according to RFC 3263 policy: a successful final transaction state
// ends confirmation, while 503, transport failure, or a timeout before any
// response may advance to the next candidate.
// Once this method returns, the caller owns the transaction's application-level
// response policy.
func (elm *Element) SendRequestStateful(
	ctx context.Context,
	req *RequestEnvelope,
	opts ...SendRequestStatefulOptions,
) (ClientTransaction, error) {
	elm.ensureInit()
	if elm.state.Load() >= elmStateClosing {
		return nil, errors.Wrap(NewElementClosedError())
	}
	ctx = ContextWithElement(ctx, elm)

	sendOpts := util.LastSliceElemOr(opts, SendRequestStatefulOptions{})

	var successTx ClientTransaction
	err := elm.runReqAttempts(
		ctx, req, sendOpts.SendOptions.LookupOptions,
		sendOpts.BeforeAttempt,
		func(ctx context.Context, attempt *RequestAttempt) error {
			tx, err := attempt.DoStateful(ctx, ClientTransactionOptions{
				SendOptions: sendOpts.SendOptions,
				Timing:      sendOpts.Timing,
				Logger:      sendOpts.Logger,
			})
			if err != nil {
				return errors.Wrap(&RequestAttemptError{
					Cause:   err,
					Addr:    attempt.Addr,
					Request: attempt.Request.Clone().(*RequestEnvelope), //nolint:forcetypeassert
				})
			}

			if err := elm.confirmClientTx(ctx, attempt, tx, sendOpts.OnAttemptProgress); err != nil {
				return errors.Wrap(err)
			}

			successTx = tx

			return nil
		},
		sendOpts.OnAttemptError,
		sendOpts.UseRFC2543Fallback,
	)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return successTx, nil
}

const errServiceUnavail errors.Error = "service unavailable"

// confirmClientTx decides whether the current candidate is usable before the
// request runner commits to it.
//
// The method intentionally waits for the transaction's accepted/completed state
// instead of returning on a provisional response: RFC 3263 treats a 503 as an
// attempt failure, and the caller must give the current server a chance to
// produce that final result before trying another DNS candidate.
//
// The confirmation handler is removed before returning.
// The transaction itself remains alive and continues its normal SIP FSM; only
// application-specific response handling is left to the caller.
func (elm *Element) confirmClientTx(
	ctx context.Context,
	attempt *RequestAttempt,
	tx ClientTransaction,
	onProgress func(context.Context, *RequestAttempt, *ResponseEnvelope),
) error {
	unbindResHndlr := tx.BindResponseHandler(InboundResponseHandlerFunc(
		func(ctx context.Context, res *ResponseEnvelope) {
			if onProgress != nil {
				if sts := res.Status(); ResponseStatusTrying < sts && sts < types.ResponseStatusOK {
					onProgress(ContextWithElement(ctx, elm), attempt, res)
				}
			}
		},
	))
	defer unbindResHndlr()

	// RFC 3263 Section 4.3: keep the attempt until a final transaction
	// outcome or a transport/timeout error is available for classification.
	// A provisional response is reported through OnAttemptProgress but does not
	// complete confirmation because a later 503 still fails this attempt.
	// firstOf must be fully assigned before binding any handler,
	// since tx.Bind*Handler may invoke the handler synchronously
	// (e.g. to deliver an already pending error or state transition).
	firstOf := syncutil.NewFirstOf[error]()
	firstOf.
		AddCancel(tx.BindErrorHandler(ErrorHandlerFunc(
			func(_ context.Context, err error) {
				unbindResHndlr()
				firstOf.Resolve(errors.Wrap(err))
			},
		))).
		AddCancel(tx.BindStateHandler(TransactionStateHandlerFunc(
			func(_ context.Context, _, to TransactionState) {
				//nolint:exhaustive
				switch to {
				case TransactionStateAccepted, TransactionStateCompleted:
					unbindResHndlr()
					firstOf.Resolve(nil)
				}
			},
		)))

	select {
	case err := <-firstOf.Chan():
		if err != nil {
			return errors.Wrap(&RequestAttemptError{
				Cause:       err,
				Addr:        attempt.Addr,
				Request:     tx.Request(),
				Transaction: tx,
				Terminal: !errors.Is(err, ErrTransactionTimedOut) && !IsTransportError(err) ||
					errors.Is(err, ErrTransactionTimedOut) && tx.State() >= TransactionStateProceeding,
			})
		}
	case <-ctx.Done():
		unbindResHndlr()
		firstOf.Resolve(errors.Wrap(ctx.Err()))

		if err := <-firstOf.Chan(); err != nil {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()

			cncTx, cncErr := elm.txm.NewCancelClientTransaction(ctx, tx)
			switch {
			case cncErr == nil:
				cncErr = cncTx.Start(ContextWithTransaction(ctx, cncTx))
			case errors.Is(cncErr, ErrTransactionDuplicate):
				if cncTx = elm.cancelTxOf(tx); cncTx != nil {
					cncErr = nil
				}
			}
			if cncErr != nil && !errors.Is(cncErr, ErrTransactionActionNotAllowed) && !errors.Is(cncErr, ErrTransactionManagerClosed) {
				elm.log.WarnContext(
					ctx, "failed to cancel transaction",
					slog.Any("transaction", tx),
					slog.Any("error", cncErr),
				)
			}

			return errors.Wrap(&RequestAttemptError{
				Cause:             err,
				Addr:              attempt.Addr,
				Request:           tx.Request(),
				Transaction:       tx,
				CancelTransaction: cncTx,
				Terminal:          true,
			})
		}
	}

	switch res := tx.LastResponse(); res.Status() {
	case ResponseStatusServiceUnavailable:
		return errors.Wrap(&RequestAttemptError{
			Cause:       errServiceUnavail,
			Addr:        attempt.Addr,
			Request:     tx.Request(),
			Transaction: tx,
		})
	default:
		return nil
	}
}

func (elm *Element) cancelTxOf(invTx ClientTransaction) ClientTransaction {
	cncReq, err := NewCancelRequestEnvelope(invTx.Request())
	if err != nil {
		return nil
	}
	key, err := MakeClientTransactionKey(cncReq)
	if err != nil {
		return nil
	}
	tx, _ := elm.txm.LoadClientTransaction(key)
	return tx
}

type SendResponseStatefulOptions struct {
	// SendOptions are options for sending the response.
	SendOptions SendResponseOptions
	// Timing is the SIP timing config that will be used with the transaction.
	// If zero, [DefaultTimings] will be used.
	Timing TimingConfig
	// Logger is the logger that will be used with the transaction.
	// If nil, the [log.Default] will be used.
	Logger *slog.Logger
}

// SendResponseStateful sends a SIP response statefully using the element's
// transport manager and transaction manager.
func (elm *Element) SendResponseStateful(
	ctx context.Context,
	req *RequestEnvelope,
	res *ResponseEnvelope,
	opts ...SendResponseStatefulOptions,
) (ServerTransaction, error) {
	elm.ensureInit()
	ctx = ContextWithElement(ctx, elm)

	release, ok := elm.admitOp()
	if !ok {
		return nil, errors.Wrap(NewElementClosedError())
	}
	defer release()

	txKey, err := MakeServerTransactionKey(req)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	sendOpts := util.LastSliceElemOr(opts, SendResponseStatefulOptions{})

	tx, ok := elm.LoadServerTransaction(txKey)
	if !ok {
		tp, ok := elm.TransportFromRequest(req)
		if !ok {
			return nil, errors.Wrap(NewNoTransportError())
		}

		newTx, err := elm.NewServerTransaction(ctx, req, tp, ServerTransactionOptions{
			Timing: sendOpts.Timing,
			Logger: sendOpts.Logger,
		})
		switch {
		case err == nil:
			if err := newTx.Start(ContextWithTransaction(ctx, newTx)); err != nil {
				return nil, errors.Wrap(err)
			}
			tx = newTx
		case errors.Is(err, ErrTransactionDuplicate):
			tx, ok = elm.LoadServerTransaction(txKey)
			if !ok {
				return nil, errors.Wrap(err)
			}
		default:
			return nil, errors.Wrap(err)
		}
	}

	ctx = ContextWithTransaction(ctx, tx)

	if err := tx.SendResponse(ctx, res, sendOpts.SendOptions); err != nil {
		if !errors.Is(err, ErrTransactionActionNotAllowed) {
			if err := tx.Terminate(ctx, errors.Wrap(err)); err != nil {
				elm.log.WarnContext(
					ctx, "failed to terminate transaction",
					slog.Any("transaction", tx),
					slog.Any("error", err),
				)
			}
		}
		return nil, errors.Wrap(err)
	}

	return tx, nil
}

type RespondStatefulOptions struct {
	ResponseOptions ResponseOptions
	SendOptions     SendResponseStatefulOptions
}

// RespondStateful sends a SIP response statefully using the element's
// transport manager and transaction manager.
func (elm *Element) RespondStateful(
	ctx context.Context,
	req *RequestEnvelope,
	sts ResponseStatus,
	opts ...RespondStatefulOptions,
) (ServerTransaction, error) {
	elm.ensureInit()
	if elm.state.Load() >= elmStateClosing {
		return nil, errors.Wrap(NewElementClosedError())
	}
	ctx = ContextWithElement(ctx, elm)

	resOpts := util.LastSliceElemOr(opts, RespondStatefulOptions{})

	res, err := req.NewResponse(sts, resOpts.ResponseOptions)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return errors.Wrap2(elm.SendResponseStateful(ctx, req, res, resOpts.SendOptions))
}
