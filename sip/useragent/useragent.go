package useragent

import (
	"context"
	"sync"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
)

// UserAgent is a SIP user agent component for an [sip.Element].
//
// The zero value is not usable: build a user agent with [New] and bind it
// to an element with [sip.Element.UseComponent].
type UserAgent struct {
	// opts is the normalized user-agent-owned copy of the construction
	// options.
	opts Options

	// mu serializes lifecycle transitions and the operation and inbound
	// registries: admission and the workers count commit inside the same
	// critical section as the seal, so a workers.Add can never race the
	// drain's Wait. closed commits the lifecycle end; ops tracks every
	// admitted operation's cancel and inbounds every admitted inbound
	// handle, whose release the seal snapshots for cleanup; drained is
	// the shared channel the first seal creates together with the single
	// waiter goroutine that closes it once the admitted workers finish.
	mu       sync.Mutex
	closed   bool
	ops      map[*operation]context.CancelFunc
	inbounds map[*IncomingMessage]struct{}
	drained  chan struct{}
	workers  sync.WaitGroup
}

// isClosed reports whether the user agent's lifecycle is already sealed.
func (ua *UserAgent) isClosed() bool {
	ua.mu.Lock()
	defer ua.mu.Unlock()
	return ua.closed
}

// Capabilities advertises the methods the user agent is configured to
// handle: MESSAGE with an [Options.OnMessage] handler, and OPTIONS with
// an [Options.OnOptions] handler or the enabled auto answer. Option tags
// and MIME types are not claimed.
//
// The advertisement follows the immutable construction options, not the
// lifecycle state: a sealed user agent still reports its configuration,
// while an instance that never passed [New] advertises nothing. Each call
// builds a fresh capability value, so mutating a returned slice never
// leaks into the next call.
func (ua *UserAgent) Capabilities() sip.ElementCapabilities {
	// opts is immutable after construction, so the never-initialized
	// guard is a plain read, not a lock-sensitive check.
	if ua.opts.AOR == nil {
		return sip.ElementCapabilities{}
	}

	var methods []sip.RequestMethod
	if ua.opts.OnMessage != nil {
		methods = append(methods, sip.RequestMethodMessage)
	}
	if ua.opts.OnOptions != nil || !ua.opts.DisableOptionsAutoAnswer {
		methods = append(methods, sip.RequestMethodOptions)
	}
	return sip.ElementCapabilities{Methods: methods}
}

// startOp admits an operation worker and registers it for the lifecycle
// drain. Admission, registration, and the workers count commit inside the
// seal's critical section, so a sealed — or never-initialized — user agent
// reports a classified [ErrClosed] instead of starting work. The releaser
// launches only after the lock is released and follows worker exit — after
// the completion callback finished — never the outcome publication or a
// waiter, so the drain also covers in-flight callbacks.
func (ua *UserAgent) startOp(
	ctx context.Context,
	opts OperationOptions,
	run func(context.Context) (*sip.ResponseEnvelope, error),
) (*operation, error) {
	ua.mu.Lock()
	if ua.closed || ua.opts.AOR == nil {
		ua.mu.Unlock()
		return nil, errors.Wrap(NewClosedError())
	}
	if ua.ops == nil {
		ua.ops = make(map[*operation]context.CancelFunc)
	}
	op := newOperation(ctx, opts, run)
	ua.ops[op] = op.cancel
	ua.workers.Add(1)
	ua.mu.Unlock()

	go func() {
		<-op.exited
		ua.mu.Lock()
		delete(ua.ops, op)
		ua.mu.Unlock()
		ua.workers.Done()
	}()

	return op, nil
}

// trackInbound admits an inbound handler invocation and registers in for
// the lifecycle drain inside the same seal critical section as the
// operation registry, so admission can never race the drain's Wait. The
// returned release is idempotent: it unregisters the handle, unbinds its
// transaction-termination observation, and cancels the inbound scope. The
// workers slot belongs to the active handler call and is done by the
// caller when it returns — a retained handle never holds a worker.
func (ua *UserAgent) trackInbound(in *IncomingMessage) (release func(), admitted bool) {
	ua.mu.Lock()
	defer ua.mu.Unlock()

	if ua.closed || ua.opts.AOR == nil {
		return nil, false
	}
	if ua.inbounds == nil {
		ua.inbounds = make(map[*IncomingMessage]struct{})
	}
	ua.inbounds[in] = struct{}{}
	ua.workers.Add(1)
	return in.release, true
}

// seal commits the lifecycle end under mu and returns the shared drained
// channel together with the stop snapshot the caller runs outside the
// lock. The first seal — Shutdown's or Close's — creates drained and its
// single waiter goroutine; later seals reuse them, so repeated Shutdown
// calls never spawn a waiter per call. The snapshot is the drain point of
// every worker class: operation cancels join it here, and inbound-handle
// releases join it from the inbound registry, so a retained responder's
// registration and transaction unbind are torn down even after its
// handler already returned.
func (ua *UserAgent) seal() (chan struct{}, []func()) {
	ua.mu.Lock()
	defer ua.mu.Unlock()

	ua.closed = true
	if ua.drained == nil {
		ua.drained = make(chan struct{})
		go func() {
			ua.workers.Wait()
			close(ua.drained)
		}()
	}
	stops := make([]func(), 0, len(ua.ops)+len(ua.inbounds))
	for _, cancel := range ua.ops {
		stops = append(stops, cancel)
	}
	for in := range ua.inbounds {
		stops = append(stops, in.release)
	}
	return ua.drained, stops
}

// Shutdown gracefully shuts the user agent down.
//
// It is the optional element component shutdown hook: the owning element
// invokes it before component and transport closure, and the lifecycle is
// instance-wide — a user agent registered on several elements has one
// shutdown that seals admission on every element. Shutdown seals the
// lifecycle first, cancels every admitted operation, releases inbound
// responders and their transaction observers, then drains the in-flight
// handler and operation workers — completion callbacks included — until
// they finish or ctx is done. Repeated and concurrent calls share
// the first seal's drained channel. The component registration itself
// belongs to the caller: it is detached by the unbind returned from
// [sip.Element.UseComponent], never by Shutdown, and the borrowed elements
// are never closed.
//
// No callback runs under the lifecycle lock. Shutdown must not be called
// synchronously from an inbound handler or an operation callback it is
// draining: it would wait for itself.
func (ua *UserAgent) Shutdown(ctx context.Context) error {
	drained, stops := ua.seal()
	for _, stop := range stops {
		stop()
	}

	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return errors.Wrap(ctx.Err())
	}
}

// Close seals the user agent's lifecycle. It is the component close hook
// of the owning element and idempotent: repeated and concurrent calls
// share the first call's effect. Unlike [UserAgent.Shutdown], Close is
// the non-graceful seal: it cancels every admitted operation and releases
// inbound responders but does not wait for the workers to exit. Transaction
// lifetimes belong to the elements; Close never closes those elements or
// calls the registration unbind the application holds.
func (ua *UserAgent) Close() error {
	_, stops := ua.seal()
	for _, stop := range stops {
		stop()
	}
	return nil
}
