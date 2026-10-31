# Planned Work

## Element Draining

- [ ] Design an explicit draining phase for `Element.Shutdown(ctx)`.

The current implementation runs middleware shutdown hooks while the element
remains running, then calls `Close()`. Applications and middleware must stop new
business work themselves; Element does not wait for all transactions to finish
before closing.

Before adding draining, define:

- Admission rules that reject new business work but allow completion traffic,
  including new transactions for BYE, CANCEL and deregistration.
- Continued response processing and interceptor availability during draining.
- When middleware registration and removal become frozen.
- Drain completion criteria and context cancellation/deadline behavior,
  including whether forced `Close()` is automatic or caller-controlled.
- Coordination of concurrent `Shutdown()` and direct `Close()` calls.

These are deferred design questions, not guarantees of the current API.
