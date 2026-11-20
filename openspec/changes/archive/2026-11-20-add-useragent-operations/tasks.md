# Tasks

## 1. Component shell

- [x] 1.1 Create `sip/useragent` with the `UserAgent` type, its `Options`
  (address of record, contact, authenticator, redirect policy, OnMessage,
  OnOptions, OPTIONS auto-answer) and the `New` constructor; verify with
  table-driven construction tests covering validation failures, option
  copying, and that nothing is bound before registration
- [x] 1.2 Implement component wiring: inbound request interceptor,
  `Capabilities()`, `Shutdown(ctx)`, and `io.Closer` through
  `Element.UseComponent`; verify with element-level tests that advertised
  methods follow handler and auto-answer configuration
- [x] 1.3 Add the `useragent` error taxonomy (domain class plus canceled,
  authentication-limited, redirect-loop sentinels); verify with tests that
  `errors.Is`/`errclass` predicates match each sentinel through wrapping

## 2. Operation engine

- [x] 2.1 Implement the operation state machine (send, classify, retry or
  resolve) on its own goroutine with single-resolution outcome storage,
  `Wait`, `Cancel`, and context cancellation; verify with tests that
  repeated `Wait` returns the same outcome, cancellation reports the
  canceled class, and no response surfaces after cancellation
- [x] 2.2 Implement the operation registry with shutdown draining and
  closed-component rejection; verify with tests that shutdown cancels
  in-flight operations, releases retained inbound responders, completes
  without remote peers, and rejects later operations, with the race detector clean

## 3. Incoming handling

- [x] 3.1 Implement inbound claim rules (handler registered for the method;
  OPTIONS auto-answer when enabled and no handler) with unchanged
  pass-through otherwise; verify with table-driven interceptor tests over
  the claim combinations
- [x] 3.2 Implement server transaction creation on claim, the
  `IncomingMessage` responder surface, and OnMessage dispatch; verify with
  tests that a handler response is sent statefully and request
  retransmission is absorbed without re-invoking the handler
- [x] 3.3 Implement OPTIONS auto-answer from advertised capabilities and the
  OnOptions override; verify with tests for the 200 capabilities answer and
  the handler-controlled response

## 4. Outgoing operations

- [x] 4.1 Implement endpoint request building (address of record, From tag,
  contact, Max-Forwards, body and content type) and the Message operation
  through `Element.SendRequestStateful` with final-response classification;
  verify with a two-element end-to-end test that `Wait` resolves the final
  response; verify that ordinary failures surface as responses and that
  exhausted element failover after 503 surfaces as a send error
- [x] 4.2 Implement the Options operation on the same engine; verify with a
  two-element end-to-end test against the peer's OPTIONS auto-answer

## 5. Authentication retry

- [x] 5.1 Implement the challenge retry chain: clone the request, increment
  CSeq, apply `ClientAuthenticator.AuthenticateRequest`, resend, bound by
  the attempt limit; verify with tests asserting the wire shape of retries
  (CSeq incremented, stable Call-ID and From tag, fresh Via branch), the
  challenge-then-success chain, the no-authenticator path, and the
  exhausted-attempts error
- [x] 5.2 Add the two-element end-to-end MESSAGE flow with a Digest
  challenge and retry; verify the full chain resolves with the final
  response to the credentialed retry

## 6. Redirect following

- [x] 6.1 Implement the redirect policy (depth, attempt caps, visited-target
  loop detection, shared deadline, allowed targets) and 3xx classification;
  verify with table-driven tests for follow-one-redirect, loop detection,
  disallowed or exhausted targets, and final resolution for unsupported 3xx
  or when no redirect policy is configured
- [x] 6.2 Enforce the trust gate before credential recomputation; verify
  with a test that the authenticator is invoked for a retried request only
  after the new target passes the policy

## 7. Integration and documentation

- [x] 7.1 Add the phase exit-criteria suite: two elements exchanging MESSAGE
  and OPTIONS with authentication retry and redirect following under
  deterministic transaction timing; verify it passes under `task test` with
  race detection and vet
- [x] 7.2 Write `docs/useragent.md` (construction, operations, handlers,
  policies) and link it from `docs/README.md`; update the package layout in
  `docs/architecture.md`; verify the documented examples run as written
- [x] 7.3 Run the full verification gates; verify `task lint` and
  `task test` pass with zero reported issues

## Workflow follow-up

- Archive the change after the project's review requirements are satisfied.
- Verify the archived result.
