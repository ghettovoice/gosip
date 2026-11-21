// Package dialog provides RFC 3261 INVITE-created SIP dialog state for
// SIP endpoints.
//
// A [Dialog] records one dialog leg: its role-relative identity, parties,
// sequence baselines, and lifecycle state. The package only inspects and
// transforms [sip] messages; it owns no transport, transaction, or timer,
// sends nothing on the wire, and registers no callbacks. Consumers keep
// their own lookup of dialogs by [ID] and drive all I/O themselves.
//
// Both endpoint roles are supported through role-specific constructors:
// [NewUAC] establishes a caller-side dialog from the sent initial INVITE
// and a dialog-establishing response, and [NewUAS] establishes the
// callee-side dialog from the received INVITE and the endpoint's tagged
// response. [Dialog.Confirm] promotes an early dialog to confirmed once,
// and [Dialog.Terminate] ends it explicitly without wire effects.
//
// Dialog support is limited to tagged RFC 3261 establishment; tagless
// RFC 2543 dialogs are out of scope.
//
// Establishing input is validated before any state is committed. A
// malformed message reports [ErrInvalidMessage]. A well-formed exchange
// that cannot create a tagged dialog — a 100 Trying, an untagged
// provisional, or an unsuccessful final response — reports
// [ErrNotEstablishing] and is a normal no-dialog outcome, not an input
// error. A well-formed response belonging to another fork or INVITE
// attempt reports [ErrMismatch]. Lookup scope, response generation,
// retention, and teardown stay with the caller; the package installs
// nothing and sends nothing.
//
// [Dialog.NewRequest] builds owned ordinary in-dialog request envelopes
// with fresh endpoint headers and no inherited transport, address,
// forwarding, or resolution state. Each successful build consumes a
// local CSeq number that is not rolled back when the caller drops the
// envelope, so callers serialize dispatch to keep wire order. Route-set
// resolution through [sip.Element.ResolveRequestTarget] happens only at
// send time.
//
// [Dialog.ValidateRequest] accounts for the remote endpoint's sequence
// numbers on inbound in-dialog requests: a first or greater number
// advances the baseline, an equal retransmission is accepted unchanged,
// and a stale number reports [ErrCSeqOutOfOrder] without mutating
// state. The caller answers a stale request (500 per RFC 3261 Section
// 12.2.2) and keeps transaction-level deduplication separate.
//
// [Dialog.NewACK] builds the stateless ACK for a confirming 2xx on a
// UAC dialog, reusing the INVITE's CSeq number and credentials; the
// caller sends it through [sip.Element.SendRequest] and retains it per
// fork/attempt until response observation ends, never logging the
// copied credential values. [Dialog.MatchACK] recognizes a confirmed
// UAS dialog's inbound ACK without touching sequence state.
package dialog
