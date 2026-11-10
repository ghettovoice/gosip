// Package proxy provides a SIP proxy element middleware: route-based
// request forwarding in stateful and stateless modes with observation
// events, request snapshots, and restore bindings for durable forwarding
// state.
//
// [New] is pure: it validates and copies routes and options without
// binding an element. An explicit [sip.Element.UseMiddleware] call registers the
// proxy on an element's processing chain — construction installs no
// middleware — and one instance may serve several elements at once. The
// lifecycle stays instance-wide: [Proxy.Shutdown], invoked as the
// element's optional middleware hook or called directly, seals admission
// and retires owned work on every element. The registration's unbind
// detaches one element only and is never a shutdown path; callers retire
// active proxy work before unbinding because the outbound response
// interceptor is required to settle in-flight sends. Request lookup is
// element-qualified through [Proxy.LookupRequest], and observers bind
// through [Proxy.BindEventHandler] and [Proxy.BindSnapshotHandler].
//
// Recovery keeps the same explicit-binding rule: [Proxy.RestoreRequest]
// validates a captured [RequestSnapshot] against [RestoreBindings] and
// installs the coordinator dormant — no sends, callbacks, snapshot
// revisions, or timers run until the once-only [Request.Start]
// activation. [RestoreBindings.Element] is required and authoritative: it
// overrides the element carried by the caller's context while the
// context's other values survive, a nil element fails with
// [ErrMissingElement] before anything is staged, and ownership is never
// inferred from the context or a transaction key. Neither a snapshot nor
// an event serializes the element: observers read the request's element
// from the callback context through [sip.ElementFromContext].
//
// Processed envelopes carry one serializable [Metadata] stamp under
// [MetadataKey] in their [sip.MessageMetadata]. A stamp records the
// processing boundary that produced it — claim, forward, response, or
// upstream — with correlation fields populated only when that boundary
// actually knows them: a stateless response without recoverable
// provenance keeps mode and stage alone. Stamps replace rather than
// merge, so a message traversing the proxy again carries only the fresh
// traversal's record. The stamp is local, non-authenticated observation
// state: it never reaches the wire and is no wire identifier,
// authentication, or routing authority; events, errors, and outcomes
// join to it through [Event.RequestID].
//
// The package depends on the core [github.com/ghettovoice/gosip/sip]
// package; the core does not depend on this package.
package proxy
