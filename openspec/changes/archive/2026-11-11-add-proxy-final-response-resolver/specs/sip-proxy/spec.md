# Spec Delta

## MODIFIED Requirements

### Requirement: Best negative response and authentication aggregation

When final response selection is required, the proxy SHALL enforce mandatory RFC 3261 Section 16.7 constraints and authentication-header aggregation. The default selection SHALL preserve current response preferences. The proxy SHALL preserve forwarded dialog tags, retain the existing lone-503-to-500 transformation, and use 408 only where the method permits it.

#### Scenario: Combined authentication challenges

- **WHEN** a selected 401 or 407 follows other 401 and 407 responses in the same response context
- **THEN** its WWW-Authenticate and Proxy-Authenticate values include the challenges from those responses without rewriting them.

#### Scenario: Only service unavailable

- **WHEN** the only usable downstream final result is 503 and no policy establishes general proxy unavailability
- **THEN** the proxy returns 500 rather than forwarding 503 upstream.

#### Scenario: INVITE attempts produce no final response

- **WHEN** all INVITE attempts finish without a forwardable final response
- **THEN** the proxy produces 408 through the existing upstream transaction.

#### Scenario: Custom selection retains authentication processing

- **WHEN** an application selects a 407 from a response context containing both 401 and 407 challenges
- **THEN** the proxy aggregates the authentication challenges before forwarding without requiring the callback to modify responses.

#### Scenario: Custom selection does not bypass lone-503 handling

- **WHEN** a custom callback selects the only candidate and that response is 503
- **THEN** the proxy applies the same 500 transformation as the default path without changing the stored 503 candidate.

## ADDED Requirements

### Requirement: Route-specific final response resolution

Applications SHALL be able to configure an ordinary function field `Route.ResolveFinalResponse` without a named callback type or interface. It SHALL receive `context.Context` and `FinalResponseSelection`, containing the request and arrival-ordered candidates, and return an integer candidate index and an error. A nil function SHALL use the exported `DefaultFinalResponseResolver` with the same signature.

#### Scenario: Select a different negative response

- **WHEN** a route receives 480 followed by 486 and its callback returns index 1 without an error
- **THEN** the proxy selects the 486 candidate instead of the default first-arrival choice.

#### Scenario: Explicitly delegate to the default

- **WHEN** a custom callback delegates an unchanged selection to `DefaultFinalResponseResolver`
- **THEN** the resulting candidate and upstream response match the nil-callback path.

#### Scenario: Preserve response attribution for policy inspection

- **WHEN** a callback inspects a candidate that has known proxy target and attempt metadata
- **THEN** the selection input retains that attribution without inventing identity for an unknown source.

### Requirement: Reusable default response selection

The exported default resolver SHALL return an index into its original input without modifying it. It SHALL ignore nil and provisional entries, prefer success over 6xx, then 6xx over other classes, otherwise the lowest class, prefer 401/407/415/420/484 within 4xx, and retain input order on ties. With no selectable final response, it SHALL return index -1 and an error.

#### Scenario: Default preferences and ties

- **WHEN** default resolution receives 486 followed by 401
- **THEN** it returns index 1; for 480 followed by 486 it returns index 0.

#### Scenario: Index refers to the original slice

- **WHEN** the exported default resolver receives a nil entry, a 180, and a 404 in that order
- **THEN** it returns index 2 rather than an index into a filtered copy.

#### Scenario: Successful candidate remains strongest

- **WHEN** the exported default resolver receives 600 followed by 200
- **THEN** it returns index 1 without performing sends or response rewriting.

#### Scenario: No usable candidate

- **WHEN** the exported default resolver receives an empty input or only nil and provisional entries
- **THEN** it returns index -1 and a non-nil error without panicking.

### Requirement: Resolution does not replace protocol decisions

Custom resolution SHALL apply only to a settled stateful response context with negative final candidates and no successful candidate. It SHALL NOT control immediate response forwarding, target scheduling, cancellation, or local no-response outcomes. If any candidate is 6xx, a successful selection SHALL name a 6xx candidate. Other class and 4xx preferences SHALL be application policy.

#### Scenario: Deliberate departure from default preferences

- **WHEN** a custom callback selects 404 over 401, or 486 over 302, without a successful or 6xx candidate
- **THEN** the proxy accepts that selection as application policy rather than treating a recommendation-level preference as mandatory.

#### Scenario: Mandatory global-failure precedence

- **WHEN** a callback selects 486 from a context also containing 600
- **THEN** the proxy rejects the selection rather than forwarding 486.

#### Scenario: Success bypasses custom resolution

- **WHEN** a response context contains a successful response
- **THEN** the proxy preserves its existing success path without calling the custom resolver or replacing success with a negative candidate.

#### Scenario: No response and method-specific suppression

- **WHEN** attempts finish without a usable candidate, including a non-INVITE context whose only received final is 408
- **THEN** no custom resolver is called and existing timeout, cancellation, send-failure, and method-specific suppression rules remain effective.

#### Scenario: Stateless forwarding and stateful promotion

- **WHEN** a request remains stateless
- **THEN** its route callback is not called; if the request is promoted before sending, the resulting stateful request uses the same route's callback when selection is required.

### Requirement: Resolution input isolation and coordinator safety

The proxy SHALL call custom resolution outside internal locks, at most once for an eligible live decision, with the owning processing element in context. Input protocol messages, slices, and metadata containers SHALL be detached from live state; opaque application metadata values SHALL retain the existing envelope copy semantics. Callback results SHALL NOT overwrite a superseding success or terminal decision.

#### Scenario: Reentrant inspection

- **WHEN** a callback looks up its request or attempts to capture a snapshot
- **THEN** it does not deadlock; capture during an uncommitted resolution reports an unstable snapshot rather than a partial terminal decision.

#### Scenario: Callback changes its detached inputs

- **WHEN** a callback changes a candidate's SIP status or headers, the request's protocol fields, or metadata container entries
- **THEN** those changes do not alter the proxy-owned request, candidates, or published snapshots, and the returned index still refers to the originally offered order.

#### Scenario: Late success during resolution

- **WHEN** an eligible INVITE 2xx arrives while negative response resolution is outstanding
- **THEN** the proxy forwards that success through its existing owned transactions and prevents the stale callback result from committing or sending a conflicting negative outcome.

#### Scenario: Shutdown during resolution

- **WHEN** the request is retired while its callback is outstanding
- **THEN** the callback result produces no new final send, scheduling, or duplicate completion.

### Requirement: Resolution failures are explicit

A callback error, an out-of-range index, or a mandatory-precedence violation SHALL cause an inspectable resolution failure, not an implicit default choice or timeout. Absent a superseding decision, it SHALL abandon the request once and use the existing upstream failure path with status 500 if the original upstream transaction is usable. Returned error causes SHALL be preserved.

#### Scenario: Invalid candidate index

- **WHEN** a callback returns a negative index or an index equal to the number of offered candidates without an error
- **THEN** the proxy records an invalid selection and does not forward a default candidate or panic.

#### Scenario: Callback error is not a SIP rejection policy

- **WHEN** a callback returns an error, including an error carrying another SIP rejection status
- **THEN** the request follows the resolution-failure path with status 500 rather than using the error to select a different SIP result, and the underlying cause remains inspectable.

#### Scenario: Original upstream transaction is unavailable

- **WHEN** resolution fails after the original upstream transaction becomes unavailable
- **THEN** the request records the failure without creating a replacement transaction or sending a fabricated response.
