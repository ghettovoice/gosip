// Package useragent provides a SIP user agent component for an
// [sip.Element].
//
// A [UserAgent] adds the endpoint roles to an element: outgoing request
// operations (the UAC side) and incoming request handling (the UAS side).
// Construction and binding stay explicit: [New] validates and copies its
// [Options] and attaches nothing, while an explicit
// [sip.Element.UseComponent] registration binds the component to an
// element's inbound chain and capabilities. One instance may serve several
// elements: the processing element is carried in each request's context
// instead of a fixed field, so the component never binds itself to a
// single element. Close semantics follow the shared instance: closing the
// user agent ends it for every element it is registered on, independent of
// the elements' own lifecycle, and closing an attached element closes the
// component instance and all its operations through the same seal, while
// the other elements sharing the instance stay open. [UserAgent.Shutdown]
// seals the lifecycle, cancels the admitted operations, and drains the
// handler and operation workers before the element proceeds to close,
// while [UserAgent.Close] is the non-graceful seal that cancels in-flight
// operations and returns without the drain. Neither touches the borrowed
// elements — they stay open and keep admitting components — nor the
// registration unbind the application holds from
// [sip.Element.UseComponent]. Operation cancellation is local: a canceled
// operation context ends the exchange with the canceled outcome, and for
// non-INVITE methods it never turns into a wire CANCEL — there is none to
// send.
//
// Outgoing operations optionally follow redirect responses under a
// [RedirectPolicy]. The chain is bounded by a maximum hop depth (default
// 5), a shared count of logical sends across the initial request,
// authentication retries, and redirect hops (default 10), and a single
// operation-wide deadline created once at operation entry (default 32 s).
// Every candidate target is gated by the policy's AllowTarget check —
// before any send to it and before any authentication computation for it —
// and a visited-target set detects loops. Only SIP and SIPS contacts are
// followed, cleaned to request URIs, and a secured operation never follows
// a downgrade from SIPS to plain SIP.
//
// This slice covers the operation skeleton only: MESSAGE and OPTIONS
// operations, inbound handler dispatch, authentication retry, and redirect
// following. INVITE, dialogs, sessions, and registration are explicit
// non-goals here and arrive in later slices of the upstream design.
package useragent
