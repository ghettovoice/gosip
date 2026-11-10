package sip

// PrepareVia prepares an endpoint Via or updates only the designated local
// top Via of a forwarded request.
//
// On an unmarked envelope it behaves like the raw [EnsureRequestVia] helper:
// it ensures a top Via hop exists, fills its transport, sent-by, and an
// RFC 3261 branch. This is endpoint behavior: the sender originates a new
// hop of its own.
//
// On an envelope marked with [MessageEnvelope.MarkForwarded] it instead
// performs transit behavior: only the top Via hop is updated, and only when
// its branch equals the branch recorded with
// [RequestEnvelope.SetForwardedViaBranch]. The branch itself is preserved —
// it is the identity of the local hop placed before forwarding — and all
// earlier (upstream) Via entries are left untouched. A top Via whose branch
// does not match the designation is treated as an upstream hop and is never
// rewritten, and no new endpoint Via is invented for a forwarded request.
// The designation is an exact branch match; no proxy-specific branch
// namespace is recognized.
func (req *RequestEnvelope) PrepareVia(tp TransportProto, addr Addr) {
	forwarded := req.Forwarded()
	branch := req.ForwardedViaBranch()
	req.WithMessage(func(r *Request) {
		if !forwarded {
			EnsureRequestVia(r, tp, addr)
			return
		}
		via, ok := r.Headers.FirstVia()
		if !ok || branch == "" {
			return
		}
		if topBranch, ok := via.Branch(); !ok || topBranch != branch {
			return
		}
		if tp.IsValid() {
			via.Transport = tp
		}
		if addr.IsValid() {
			via.Addr = addr
		}
	})
}

// EnsureFromTag fills an absent endpoint From tag, leaving transit identity
// intact.
//
// Unlike the raw [EnsureRequestFromTag] helper, it is a no-op on envelopes
// marked with [MessageEnvelope.MarkForwarded]: a forwarded request keeps the
// downstream dialog identity verbatim, including an absent From tag.
func (req *RequestEnvelope) EnsureFromTag() {
	if req.Forwarded() {
		return
	}
	req.WithMessage(EnsureRequestFromTag)
}

// EnsureToTag fills an absent endpoint To tag with tag (or a generated tag
// when tag is empty), unless forwarding provenance requires preserving the
// downstream identity.
//
// Unlike the raw [EnsureResponseToTag] helper, it is a no-op when the
// envelope is marked with [MessageEnvelope.MarkForwarded] or
// [ResponseEnvelope.SetPreserveToTag]: a forwarded response keeps the To
// header of the downstream leg verbatim, including an absent tag.
func (res *ResponseEnvelope) EnsureToTag(tag string) {
	forwarded := res.Forwarded()
	preserve := res.PreservesToTag()
	if forwarded || preserve {
		return
	}
	res.WithMessage(func(r *Response) { EnsureResponseToTag(r, tag) })
}

// Private metadata keys carrying forwarding provenance on message envelopes.
// They are stored through [MessageMetadata] so provenance survives envelope
// cloning and JSON serialization, while never appearing in the rendered SIP
// message:
//
//   - "sip.forwarded" (bool): the message is a forwarded (proxied) copy
//     whose Via, From, and To identity must be preserved by transit-aware
//     preparation.
//   - "sip.preserve_to_tag" (bool): a response To tag must be preserved
//     verbatim even when the forwarded marker is absent.
//   - "sip.forwarded_via_branch" (string): the Via branch of the local hop a
//     request was forwarded with; it designates which top Via
//     [RequestEnvelope.PrepareVia] may update.
//
// These values are local claims made by the process that marked the
// envelope. They are not authenticated, signed, or otherwise bound to the
// SIP message on the wire: a marker received inside deserialized metadata
// asserts only what the producing application recorded.
const (
	forwardedMetaKey          = "sip.forwarded"
	preserveToTagMetaKey      = "sip.preserve_to_tag"
	forwardedViaBranchMetaKey = "sip.forwarded_via_branch"
)

// MarkForwarded marks the envelope's message as a forwarded (proxied) copy
// by storing true under the "sip.forwarded" metadata key.
//
// Forwarding middleware calls it before handing an incoming message to a
// send path so that transit-aware preparation —
// [RequestEnvelope.PrepareVia], [RequestEnvelope.EnsureFromTag], and
// [ResponseEnvelope.EnsureToTag] — preserves the transit message identity
// instead of applying endpoint defaults.
//
// The marker is local, non-authenticated envelope state: it travels in
// [MessageMetadata], survives cloning and JSON serialization, and never
// appears in the rendered SIP message.
func (m *MessageEnvelope[T]) MarkForwarded() {
	m.Metadata().Set(forwardedMetaKey, true)
}

// Forwarded reports whether the envelope's message was marked as forwarded
// with [MessageEnvelope.MarkForwarded], i.e. whether "sip.forwarded" holds
// a boolean true. Absent, false, or non-boolean stored values all read
// false.
func (m *MessageEnvelope[T]) Forwarded() bool {
	val, ok := m.Metadata().Get(forwardedMetaKey)
	if !ok {
		return false
	}
	forwarded, ok := val.(bool)
	return ok && forwarded
}

// SetPreserveToTag records under the "sip.preserve_to_tag" metadata key
// whether the response's To header must be forwarded verbatim by a proxy.
//
// A true value makes [ResponseEnvelope.EnsureToTag] preserve the downstream
// To identity, including an absent tag. Like every provenance marker it is
// local, non-authenticated envelope state that survives cloning and JSON
// serialization without appearing in the rendered SIP message.
func (r *ResponseEnvelope) SetPreserveToTag(preserve bool) {
	r.Metadata().Set(preserveToTagMetaKey, preserve)
}

// PreservesToTag reports whether the response was marked with
// [ResponseEnvelope.SetPreserveToTag], i.e. whether "sip.preserve_to_tag"
// holds a boolean true. Absent, false, or non-boolean stored values all
// read false.
func (r *ResponseEnvelope) PreservesToTag() bool {
	val, ok := r.Metadata().Get(preserveToTagMetaKey)
	if !ok {
		return false
	}
	preserve, ok := val.(bool)
	return ok && preserve
}

// SetForwardedViaBranch records under the "sip.forwarded_via_branch"
// metadata key the Via branch the request was forwarded with.
//
// The branch designates the local hop: [RequestEnvelope.PrepareVia] updates
// the top Via of a forwarded request only while its branch equals this
// value, preserving the branch itself and all upstream hops. Like every
// provenance marker it is local, non-authenticated envelope state that
// survives cloning and JSON serialization without appearing in the rendered
// SIP message.
func (r *RequestEnvelope) SetForwardedViaBranch(branch string) {
	r.Metadata().Set(forwardedViaBranchMetaKey, branch)
}

// ForwardedViaBranch returns the Via branch recorded with
// [RequestEnvelope.SetForwardedViaBranch], i.e. the string stored under
// "sip.forwarded_via_branch". Absent or non-string stored values read
// empty.
func (r *RequestEnvelope) ForwardedViaBranch() string {
	val, ok := r.Metadata().Get(forwardedViaBranchMetaKey)
	if !ok {
		return ""
	}
	branch, ok := val.(string)
	if !ok {
		return ""
	}
	return branch
}
