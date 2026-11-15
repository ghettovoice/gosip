# sip-redirector Specification

## Purpose

Provide a small, independent SIP redirect server component that claims matched inbound requests and answers them with 3xx redirection responses, leaving transaction correctness to the existing transaction layer.

## Requirements

### Requirement: Route claiming

The redirector SHALL claim an inbound request only when a configured route predicate matches, the component is open, and the request method is not ACK. Claimed requests SHALL NOT reach the next receiver. Unmatched requests, ACK requests, and requests observed after the component is closed SHALL continue to the next receiver unchanged.

#### Scenario: Matched request is claimed

- **WHEN** an inbound request with a non-ACK method matches a route predicate
- **THEN** the request does not reach the next receiver.

#### Scenario: Unmatched request passes through

- **WHEN** no route predicate matches an inbound request
- **THEN** the request reaches the next receiver with unchanged processing.

#### Scenario: ACK is never claimed

- **WHEN** an ACK request matches a route predicate
- **THEN** the ACK continues to the next receiver and the redirector sends no response for it.

#### Scenario: Closed component claims nothing

- **WHEN** the component has been closed and a previously matched request arrives again
- **THEN** the request continues to the next receiver unchanged.

### Requirement: Contact resolution callback

Each route SHALL resolve redirection contacts through an application callback that receives the request context and envelope. The resolved contacts, including any `q` and `expires` parameters, SHALL be carried into the Contact header of the redirect response without loss or reordering.

#### Scenario: Contacts with parameters preserved

- **WHEN** the resolver returns two contacts carrying `q` and `expires` parameters
- **THEN** the redirect response Contact header contains both contacts with those parameters in the resolved order.

#### Scenario: Resolution runs per request

- **WHEN** two different requests match the same route
- **THEN** the resolver callback is invoked once per request and may return different contacts.

### Requirement: Redirect status selection

A redirect response status SHALL be 300, 301, or 302. A route SHALL select the status through an optional callback receiving the request and the resolved contacts; without a callback the route's configured constant status SHALL be used, and an unset constant SHALL mean 302. A selected status outside the supported set SHALL produce a 500 response without a Contact header.

#### Scenario: Default status

- **WHEN** a route configures neither a status callback nor a constant status
- **THEN** the redirect response is a 302.

#### Scenario: Per-request selection from resolved contacts

- **WHEN** the status callback returns 300 for a request resolved with several contacts and 302 for a request resolved with one contact
- **THEN** each response carries its respectively selected status.

#### Scenario: Unsupported selected status

- **WHEN** the status callback returns a status outside 300, 301, and 302
- **THEN** the component responds 500 without a Contact header.

### Requirement: Authorization gate

A route MAY configure an authorization callback that runs after a match and before contact resolution. Authorization failure SHALL produce a 403 response without invoking the contact resolver. Successful authorization SHALL continue to contact resolution.

#### Scenario: Authorization failure

- **WHEN** the authorization callback returns an error for a matched request
- **THEN** the component responds 403 and the contact resolver is not invoked.

#### Scenario: Authorized request proceeds

- **WHEN** the authorization callback returns no error
- **THEN** contact resolution and redirect response generation proceed.

### Requirement: Resolution failure mapping

When the resolver returns no contacts for a claimed request, the component SHALL respond 404. When the resolver returns an operational error, the component SHALL respond 500. Neither response SHALL carry a Contact header.

#### Scenario: No resolved contacts

- **WHEN** the resolver returns an empty contact set for a matched request
- **THEN** the component responds 404 without a Contact header.

#### Scenario: Operational resolver failure

- **WHEN** the resolver returns an operational error such as a backend or cancellation failure
- **THEN** the component responds 500 without a Contact header.

### Requirement: CANCEL answered with 481

The redirector SHALL claim a CANCEL that matches a route and respond 481.

#### Scenario: CANCEL after redirect response

- **WHEN** a CANCEL matching a route arrives after its original request was answered with a final response
- **THEN** the CANCEL does not reach the next receiver and the component responds 481.

### Requirement: Responses use server transactions

Redirect, authorization, and failure responses SHALL be sent statefully through a server transaction created or loaded for the request being answered. A retransmission matched to a live server transaction SHALL be absorbed by the transaction layer without re-invoking route callbacks.

#### Scenario: Retransmission absorbed

- **WHEN** a request is retransmitted after its redirect response and the server transaction is still live
- **THEN** the stored response is resent by the transaction layer and no route callback runs again.

#### Scenario: Failure responses share the transaction path

- **WHEN** a claimed request produces a 403, 404, or 500 response
- **THEN** the response is sent through a server transaction on the request, not statelessly.

### Requirement: One instance serves several elements

The component SHALL keep no per-request or per-element state. The processing element for a claimed request SHALL be resolved from the interception context, and one instance SHALL serve multiple concurrently running elements without cross-element effects.

#### Scenario: Two elements, one instance

- **WHEN** one component instance is registered on two elements and each receives a matching request
- **THEN** each request is answered through the element that received it.

### Requirement: Explicit construction and configuration ownership

Construction SHALL validate routes and reject a route without a contact resolver or with a constant status outside 300, 301, and 302. Construction SHALL copy the route configuration and SHALL NOT register the component anywhere. Mutation of the application's source configuration after construction SHALL NOT change the component's behavior.

#### Scenario: Missing resolver rejected

- **WHEN** a route without a contact resolver is passed to construction
- **THEN** construction fails before the component can be registered.

#### Scenario: Invalid constant status rejected

- **WHEN** a route is constructed with a constant status outside 300, 301, and 302
- **THEN** construction fails.

#### Scenario: Configuration is copied

- **WHEN** the application mutates its route slice or a callback field after construction
- **THEN** the component's configured behavior is unchanged.
