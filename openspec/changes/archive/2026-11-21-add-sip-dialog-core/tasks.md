# Tasks

## 1. Dialog identity and state

- [x] 1.1 Add `sip/dialog` with role-relative identity, detached views,
  and explicit early, confirmed, and terminated transitions. In external
  package tests, verify reversed tags across roles, fork isolation, direct
  2xx establishment, confirmation exactly once, no state change on a late
  provisional or repeated 2xx for a confirmed dialog, clone isolation,
  and idempotent termination without affecting sibling dialogs.
- [x] 1.2 Validate the initial INVITE and response pair before mutation.
  Test that 100, untagged provisionals and non-2xx finals do not establish
  dialogs, and invalid Contact, tags, Call-ID, or CSeq do not commit state.
  Document the tagged RFC 3261 scope and caller-owned lookup and teardown.

## 2. Routing and ordinary requests

- [x] 2.1 Derive the remote target and role-specific route set from the
  establishing exchange. Test preservation of URI parameters and scheme,
  UAC reversal, UAS wire order, and early-to-confirmed route replacement
  (including an empty final set), preserving both CSeq values and the
  remote target even when the final Contact differs.
- [x] 2.2 Build owned ordinary request envelopes and document that callers
  serialize dispatch. Use `Element.ResolveRequestTarget` in a public-API
  test to verify empty, loose, and strict routes, the selected next hop,
  and no inherited transport destination or double rewrite.
- [x] 2.3 Add local allocation and remote sequence validation with separate
  unset baselines for the two roles. Verify concurrent consecutive
  allocation, no wrap or commit on build failure, initial baselines,
  accepted gaps, equal and stale remote values, and an inspectable stale
  result without state mutation. Document the caller's response ownership.

## 3. End-to-end INVITE ACK

- [x] 3.1 Add construction of a stateless ACK using the actual INVITE and
  confirmed dialog, plus recognition of a matching inbound UAS ACK.
  Verify the original CSeq and credentials, dialog routing through Element
  including a strict first hop, optional opaque body, no ordinary sequence
  allocation, no response to ACK, and rejection of mismatched tags or CSeq.
  Document replay ownership and credential handling.
- [x] 3.2 Bind a public response handler before starting a built-in INVITE
  client transaction in a black-box fixture. Deliver two 2xx responses
  with different tags and one retransmission. Assert two distinct dialogs
  and three ACKs, including replay after local termination of the repeated
  leg; no extra dialog transition or client transaction for ACK.

## 4. Component boundary verification

- [x] 4.1 Use `siptest.NewLoopbackUDPElement` for a two-endpoint exchange:
  INVITE, 2xx, stateless ACK, then stateful BYE and final response. Drive
  dialog creation, matching, and termination through public SIP and dialog
  APIs, without `UserAgent.Invite`. Verify identities, wire CSeq values,
  and both dialogs ending after the consumer's explicit teardown.
- [x] 4.2 Run `task --force lint`, `task --force test`, and
  `openspec validate add-sip-dialog-core --strict`. Confirm no changes to
  MESSAGE/OPTIONS, proxy, or existing transaction behavior.
