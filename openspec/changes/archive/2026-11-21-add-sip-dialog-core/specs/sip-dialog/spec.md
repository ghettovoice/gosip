# SIP Dialog Spec Delta

## Purpose

Provide reusable endpoint behavior for creating and managing RFC 3261
INVITE dialogs independently of the UserAgent operation and session layers.

## ADDED Requirements

### Requirement: Role-relative dialog matching

A dialog SHALL be identified by Call-ID, local tag, and remote tag. For an
incoming request, the To tag SHALL be local and the From tag remote. For a
response to the local initial INVITE, the From tag SHALL be local and the To
tag remote. Matching SHALL NOT mutate dialog state or claim a message.

#### Scenario: Roles reverse the tag orientation

- **WHEN** the caller and callee establish a dialog from the same INVITE and tagged 2xx
- **THEN** they have the same Call-ID and opposite local and remote tag assignments

#### Scenario: Responses from separate forks

- **WHEN** two responses to one INVITE carry different remote tags
- **THEN** they identify different dialogs even though the Call-ID, local tag, and INVITE CSeq are the same

#### Scenario: Incoming request has an unrelated tag

- **WHEN** an inbound request has the right Call-ID but a tag that does not match the dialog
- **THEN** it does not match and cannot change that dialog

### Requirement: Establishment and confirmation

A tagged 101-199 response to the initial INVITE SHALL establish an early
dialog; a valid tagged 2xx SHALL establish or confirm its matching dialog.
The dialog core SHALL NOT create a dialog for 100, an untagged provisional
response, or an unsuccessful final response. Malformed or mismatched
establishing messages SHALL NOT commit state. A late provisional response
or retransmitted 2xx for a confirmed dialog SHALL NOT mutate it or cause
another state transition.

#### Scenario: Confirmation without provisional dialog

- **WHEN** the first dialog-establishing response is a valid 2xx
- **THEN** the dialog is created in the confirmed state

#### Scenario: Same fork confirms after a provisional response

- **WHEN** a valid 2xx for the initial INVITE has the same identity as an early dialog
- **THEN** it confirms that dialog once without resetting its sequence state

#### Scenario: Late provisional response for a confirmed dialog

- **WHEN** a tagged provisional response for the initial INVITE arrives after its matching dialog is confirmed
- **THEN** the dialog stays confirmed and its routing and sequence state are unchanged

#### Scenario: Non-establishing provisional responses

- **WHEN** an INVITE receives 100 or an untagged 101-199
- **THEN** neither response creates a dialog

#### Scenario: Invalid establishing input

- **WHEN** a purported 2xx for the initial INVITE lacks a To tag or usable Contact, or has a different INVITE CSeq
- **THEN** the core reports an inspectable error without creating or updating a dialog

### Requirement: Dialog route set and remote target

The UAC SHALL take its route set from the response Record-Route in reverse
order and its initial remote target from the response Contact. The UAS
SHALL take both from the request, without reversing Record-Route. Route
parameters SHALL be preserved. On UAC confirmation of an early dialog,
only the route set SHALL be recomputed from the 2xx, even when empty.

#### Scenario: Record-Route in both directions

- **WHEN** the INVITE and response contain the same two Record-Route hops in wire order
- **THEN** the UAS stores them in wire order and the UAC stores them in reverse order, preserving URI parameters

#### Scenario: Final response removes the early route set

- **WHEN** a valid 2xx confirms an early UAC dialog with a non-empty route set but has no Record-Route and carries a Contact different from the one in the early response
- **THEN** the resulting route set is empty and the existing remote target and sequence state are unchanged

### Requirement: Outbound dialog routing

An in-dialog request SHALL preserve the dialog's Call-ID, From and To
identities and tags, and SHALL route using its route set and remote target.
The route set and message headers SHALL NOT be derived from the network
source address of the establishing response or request.

#### Scenario: No route or a loose first hop

- **WHEN** a request is sent in a dialog with no route set or with a loose first hop
- **THEN** the remote target is the Request-URI, and the next hop is the target or the first Route respectively

#### Scenario: Strict first hop

- **WHEN** a request is sent in a dialog whose first Route is strict
- **THEN** the first hop becomes the Request-URI, the remote target is appended after the remaining Routes, and the transformation occurs only once

### Requirement: Local CSeq allocation

Local CSeq SHALL start at the initial INVITE CSeq for the UAC and remain
unset for the UAS until it originates a request. After initialization, each
successful allocation for a new ordinary in-dialog request SHALL increment
by one without wrapping. ACK and CANCEL SHALL NOT use this allocator.

#### Scenario: Concurrent allocation on one dialog

- **WHEN** two new ordinary requests are constructed concurrently for one initialized dialog
- **THEN** they receive distinct consecutive local CSeq values with their own request methods

#### Scenario: Counter is exhausted

- **WHEN** the next local CSeq is outside the valid range
- **THEN** construction fails with an inspectable error, without wrapping or modifying the stored sequence

### Requirement: Remote CSeq validation

Remote CSeq SHALL start at the initial INVITE CSeq for the UAS and remain
unset for the UAC until its first ordinary inbound dialog request. A
matching ordinary request with a greater number SHALL advance remote
CSeq, including across gaps; a lower number SHALL yield an inspectable
out-of-order result without modifying state. ACK SHALL NOT advance it.

#### Scenario: Gap followed by stale request

- **WHEN** an ordinary request with a higher CSeq is accepted and an ordinary request with a lower CSeq then arrives
- **THEN** the second request is classified as out of order and neither CSeq counter is changed by it

#### Scenario: Request repeats an equal remote sequence

- **WHEN** validation sees an ordinary request with the same number as the current remote CSeq
- **THEN** the core does not advance the sequence or classify it as lower; transaction deduplication remains separate

### Requirement: End-to-end ACK for INVITE 2xx

The dialog core SHALL provide an ACK for the matching 2xx using the same
remote target and Route handling as an ordinary in-dialog request. It
SHALL use the acknowledged INVITE CSeq number with method ACK and copy
that INVITE's credentials, if present. It SHALL NOT create a client
transaction, invoke an authenticator, or allocate an ordinary CSeq for
the ACK. A caller SHALL be able to retain and resend the ACK for a
repeated 2xx independently of the live dialog.

#### Scenario: Two answers and one retransmission

- **WHEN** one INVITE transaction delivers a valid 2xx from each of two distinct remote tags and then repeats one of those 2xx responses
- **THEN** the consumer can send three ACKs using the appropriate dialog routes; the repeated 2xx does not create, reconfirm, or mutate a dialog

#### Scenario: ACK uses the original INVITE number

- **WHEN** an ordinary request has already advanced local CSeq since the initial INVITE
- **THEN** ACK still uses the initial INVITE's number and leaves both counters untouched

#### Scenario: Prepared ACK outlives a terminated dialog

- **WHEN** the consumer has retained the prepared ACK for a confirmed dialog that subsequently terminates
- **THEN** the consumer can retransmit that ACK without reopening or mutating the dialog

#### Scenario: UAS validates a matching ACK separately

- **WHEN** an ACK matches a confirmed UAS dialog's tags and initial INVITE CSeq
- **THEN** it is recognizable without advancing remote CSeq or sending a response

### Requirement: Explicit ownership and termination

A dialog SHALL expose detached state and explicit, concurrency-safe
actions without registering callbacks, owning transport or transaction
lifetimes, or sending on termination. Explicit termination SHALL be
idempotent and SHALL prevent new ordinary requests and state updates for
that object. The consumer SHALL own lookup scope and retention.

#### Scenario: Caller modifies data it owns

- **WHEN** the caller changes establishment messages or a returned view after a dialog has recorded its state
- **THEN** the dialog's identity, routing, and sequences do not change

#### Scenario: Terminating one leg

- **WHEN** the consumer terminates a dialog more than once while another dialog from the same INVITE remains active
- **THEN** the ended leg stays terminated, no wire message is sent by termination, and the other leg is unaffected

### Requirement: Public APIs are sufficient for a complete dialog exchange

An endpoint consumer SHALL be able to use only public dialog and SIP APIs
to establish both sides, send an end-to-end 2xx ACK, and send and receive
a stateful BYE. The dialog package SHALL NOT require installing a
UserAgent or taking ownership of an Element.

#### Scenario: Two endpoints exchange INVITE through BYE

- **WHEN** two Elements exchange an initial INVITE, 2xx, ACK, and then BYE with its final response using an independent consumer
- **THEN** both dialog identities and wire sequence numbers are correct and the consumer can explicitly terminate both dialog objects
