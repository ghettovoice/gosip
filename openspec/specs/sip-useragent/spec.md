# sip-useragent Specification

## Purpose

Endpoint user-agent operations for gosip: component attachment to an element,
outgoing operation handles with retry policies, and MESSAGE/OPTIONS handling
on both client and server sides.

## Requirements

### Requirement: Explicit construction and element registration

A user agent SHALL be constructed explicitly from validated, copied
configuration and SHALL NOT register itself anywhere at construction.
Attachment to an element SHALL be a separate explicit call, and one instance
SHALL be attachable to several elements with unambiguous per-request element
binding. Invalid configuration SHALL fail construction before any binding.

#### Scenario: Construction binds nothing

- **WHEN** a user agent is constructed with valid configuration
- **THEN** no element is modified and no listener, interceptor, or background processing exists until an explicit registration call

#### Scenario: Invalid configuration fails fast

- **WHEN** construction receives missing required identity or contradictory options
- **THEN** it returns an inspectable error and no partially configured instance is returned

### Requirement: Inbound claim rules

The user agent SHALL claim an inbound request only when a handler is
registered for the request method, or when the method is OPTIONS and
auto-answer is enabled. Every unclaimed request SHALL be passed to the next
interceptor unchanged.

#### Scenario: Handler present claims request

- **WHEN** an inbound MESSAGE arrives and an application handler for MESSAGE is registered
- **THEN** the user agent claims the request and the application handler decides its response

#### Scenario: No handler passes through

- **WHEN** an inbound request arrives for a method with no registered handler and auto-answer does not apply
- **THEN** the user agent does not respond and the request reaches the next interceptor unchanged

### Requirement: Operation handles resolve once

Every outgoing operation SHALL return a handle whose callbacks are set at
creation, whose final outcome is observable through a waiting call, and whose
outcome is reported exactly once regardless of how many waiting calls are
made.

#### Scenario: Waiting after completion

- **WHEN** a second waiting call is made after the operation already resolved
- **THEN** it returns the same final outcome without repeating side effects

#### Scenario: Creation-time callbacks

- **WHEN** the operation emits progress or final events
- **THEN** only callbacks present in the operation options at creation are invoked

### Requirement: Operation cancellation semantics

Canceling the operation context SHALL stop waiting, release partial operation
state, and report a canceled outcome. For non-INVITE methods the cancellation
SHALL NOT emit a CANCEL request, because the protocol forbids canceling
those transactions; responses already in flight SHALL NOT surface to the
caller after cancellation.

#### Scenario: Context canceled before final response

- **WHEN** the operation context is canceled before a final response arrives
- **THEN** the operation reports a canceled outcome and no later response for it is delivered to the application

### Requirement: Authentication retry as exchange owner

When a configured authenticator exists and the operation receives a 401 or
407 challenge, it SHALL retry the request with the authenticator's
credentials, a fresh Via branch, and an incremented CSeq, preserving the
original request identity. Retries SHALL be bounded by a configurable
attempt limit, and each new protection space SHALL be challenged with
recomputed credentials. Without an authenticator, the challenge response
SHALL become the operation's final outcome.

#### Scenario: Challenge then success

- **WHEN** the operation receives a 401 challenge and the configured authenticator supplies credentials
- **THEN** the retry carries the credentials with a fresh branch and incremented CSeq and the operation completes with the final response to the retry

#### Scenario: No authenticator configured

- **WHEN** the operation receives a 401 or 407 and no authenticator is configured
- **THEN** the challenge response is the operation's final outcome and no retry is sent

#### Scenario: Challenge attempts exhausted

- **WHEN** the operation keeps receiving challenges after the configured attempt limit
- **THEN** it fails with an authentication-limited outcome that callers can distinguish from other failures

### Requirement: Redirect following is bounded

The operation SHALL follow usable contacts in 300, 301, and 302 responses
only with a configured policy, subject to depth and attempt limits, loop
detection, a shared deadline, and allowed-target checks. It SHALL recompute
credentials for a new target only after the target passes the policy.
Other 3xx responses, and any 3xx without a policy, SHALL resolve as final
without a redirect retry.

#### Scenario: Follow one redirect

- **WHEN** the operation receives a 301 with an allowed contact and the policy permits another attempt
- **THEN** the operation retries toward that contact and completes with that attempt's final response

#### Scenario: Redirect loop detected

- **WHEN** the redirect contacts repeat a previously visited target
- **THEN** the operation stops following and reports a redirect-loop outcome instead of retrying

#### Scenario: No eligible redirect candidates remain

- **WHEN** the operation receives a supported 3xx response and no redirect candidates remain after applying the allowed-target checks and policy limits
- **THEN** the operation stops following and that 3xx response becomes the final outcome

#### Scenario: Unsupported redirect status

- **WHEN** the operation receives a 3xx other than 300, 301, or 302, even with a configured policy and a usable contact
- **THEN** that response becomes the final outcome and no redirect retry is sent

### Requirement: MESSAGE operation

The user agent SHALL offer a MESSAGE operation that builds an endpoint
request carrying the configured local identity and the supplied body and
content type, sends it statefully so responses correlate to the operation's
request, and exposes the final response to the caller. The element handles
503 Service Unavailable as RFC 3263 candidate failover, not as an ordinary
final response. If that failover exhausts the candidates, the operation
SHALL expose the send error instead of a response.

#### Scenario: Delivered message

- **WHEN** the MESSAGE operation is issued toward a reachable target that answers 200
- **THEN** waiting on the operation resolves with that final response

#### Scenario: Final failure surfaces

- **WHEN** the target answers a final failure status other than 503
- **THEN** waiting resolves with that response and no retry occurs unless a policy applies to it

#### Scenario: Service unavailable exhausts failover

- **WHEN** the target answers 503 and the element has no remaining send candidates
- **THEN** waiting returns a send error and no response

### Requirement: OPTIONS operation and auto-answer

The user agent SHALL offer an OPTIONS operation issuing a stateful OPTIONS
request with the final response observable to the caller. On the server side,
an inbound OPTIONS with auto-answer enabled and no application handler
SHALL be answered 200 carrying the capabilities the user agent advertises for
the element.

#### Scenario: Capabilities auto-answer

- **WHEN** an inbound OPTIONS arrives, auto-answer is enabled, and no application handler is registered
- **THEN** the user agent responds 200 including its advertised method capabilities

#### Scenario: Handler overrides auto-answer

- **WHEN** an inbound OPTIONS arrives and an application handler is registered
- **THEN** the handler controls the response and automatic answering does not apply

### Requirement: Incoming MESSAGE handling

With an application handler registered, an inbound MESSAGE SHALL be delivered
to it together with responder control for any status. The response SHALL be
sent statefully so request retransmissions are absorbed rather than rehandled.

#### Scenario: Handler responds

- **WHEN** the application handler responds 200 to a claimed MESSAGE
- **THEN** the response is sent through the server transaction and a retransmission of the request does not invoke the handler again

### Requirement: Operation outcomes are classifiable

Operation failures SHALL be distinguishable by condition: canceled,
authentication-limited, redirect-loop, and timeout outcomes SHALL be
recognizable through the repository's error inspection helpers without
matching message text.

#### Scenario: Classify canceled operation

- **WHEN** an operation is canceled by context
- **THEN** the reported error is recognized as canceled by the error classification helpers

### Requirement: Shutdown drains operations

User agent shutdown SHALL stop accepting new operations, cancel in-flight
operations with a canceled outcome, and release its inbound responder
registrations and transaction observers. It SHALL NOT unbind the component
or close an attached element.

#### Scenario: In-flight operation on shutdown

- **WHEN** shutdown runs while an operation is awaiting a final response
- **THEN** the operation resolves with a canceled outcome and the shutdown completes without waiting for the remote peer

#### Scenario: New operation after shutdown

- **WHEN** an operation is issued after shutdown has begun
- **THEN** it fails immediately with a closed-component error

#### Scenario: Retained inbound responder on shutdown

- **WHEN** shutdown runs after an inbound handler returns without a final response
- **THEN** the retained responder's context is canceled and its registration and transaction observers are released without waiting for the transaction to terminate

### Requirement: Capability advertisement follows configuration

The methods the user agent advertises through element capabilities SHALL
follow its configuration: a method is advertised only when the user agent is
configured to handle it, and nothing is advertised as supported that is not
configured.

#### Scenario: Advertised methods reflect handlers

- **WHEN** the user agent has an application handler for MESSAGE and auto-answer enabled for OPTIONS
- **THEN** the element capabilities include MESSAGE and OPTIONS and no other user-agent method
