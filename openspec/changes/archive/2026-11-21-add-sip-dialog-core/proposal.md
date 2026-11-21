# Proposal

## Why

The MESSAGE and OPTIONS operations establish the UserAgent component but do
not track SIP dialogs. Before the INVITE operation can handle several
answers to one request, gosip needs a reusable dialog layer that preserves
the identity, routing, and sequence state of each leg.

## What Changes

- Add the standalone `sip/dialog` package for RFC 3261 INVITE-created
  dialog identity and matching, early/confirmed/terminated state,
  route sets, remote targets, and independent local and remote CSeq.
- Build ordinary in-dialog requests and end-to-end ACKs for INVITE 2xx
  responses over the existing `sip.Element` send paths. Preserve the
  transaction layer's ownership of ACKs for non-2xx responses and of
  CANCEL.
- Exercise the package through public APIs in a two-element
  INVITE/2xx/ACK/BYE exchange and an INVITE transaction delivering two
  distinct 2xx responses plus a retransmission.
- Leave public `UserAgent.Invite`, session management, fork selection,
  CANCEL/late-2xx policy, UAS 2xx retransmission and BYE orchestration to
  the INVITE vertical. Leave target refresh, offer/answer, registration,
  persistence, and SIP extensions outside this change.

## Capabilities

### New Capabilities

- `sip-dialog`: Establishing, matching, sequencing, routing, and
  terminating INVITE-created SIP dialogs, including the dialog-side
  primitives for 2xx ACK.

### Modified Capabilities

None. Existing UserAgent operations, transaction behavior, and
authentication contracts are unchanged.

## Impact

- New public package `sip/dialog`, using the core `sip` message, envelope,
  and send APIs. No new external dependencies or `Element` component.
- No production changes planned in `sip`, `sip/useragent`, `sip/proxy`,
  or `sip/auth`.
- Black-box tests and package documentation define the public boundary for
  the future UA and for callers outside gosip.
