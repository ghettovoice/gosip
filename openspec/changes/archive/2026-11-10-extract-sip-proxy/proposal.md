# Proposal

## Why

The proxy has become a substantial subsystem inside `sip`, but constructor-time
attachment, private message fields, and statistics dependencies obscure the
boundary between the SIP stack and proxy orchestration. Extracting that boundary
now makes proxy use explicit, keeps each request associated with its own element,
and gives applications consistent message-level correlation.

## What Changes

- **BREAKING**: Move proxy implementation, public types, errors, tests, and examples
  to `github.com/ghettovoice/gosip/sip/proxy`, removing redundant `Proxy` prefixes
  where the package name supplies the context.
- **BREAKING**: Replace `sip.NewProxy(element, routes, options)` with
  `proxy.New(routes, options)` and explicit `element.UseMiddleware(prx)`.
- Associate each processing operation and stateful coordinator with the concrete
  `*sip.Element` from its context. Scope transaction-key indexes, pending rejection
  sends, and fallback stateless keys by element, without binding the proxy instance
  to the first element that uses it.
- **BREAKING**: Require an explicit `Element` in `proxy.RestoreBindings` and an
  element argument for public request lookup. Supply the processing element to
  event and snapshot handlers through context only.
- Replace private request/response forwarding markers with serializable envelope
  metadata and typed core accessors. Expose only the core operations needed across
  the package boundary, without teaching `sip` about proxy branch formats.
- Add immutable-by-convention `proxy.Metadata` under the single `"sip.proxy"` key:
  request/related-request, target and attempt IDs, route, mode, and processing stage.
  Preserve identity through promotion and recovery; leave unknown stateless
  associations absent rather than fabricate them.
- Make `proxy.Metadata` JSON-serializable and a `slog.LogValuer`; proxy-owned logs
  use it as one structured attribute. Do not change core message `LogValue` or
  introduce slog handlers or handler helpers.
- **BREAKING**: Move statistics to `sip/stats` as `stats.Recorder`, `stats.Report`,
  `stats.Transport`, `stats.Transaction`, and `stats.Proxy`, preserving explicit
  proxy-event subscription and existing counter meanings.

## Capabilities

### New Capabilities

- `sip-message-provenance`: Public, metadata-backed forwarding provenance that
  survives cloning and JSON checkpoints without altering SIP wire messages or
  endpoint preparation defaults.

### Modified Capabilities

- `sip-proxy`: Independent proxy middleware, request-scoped element ownership,
  explicit lifecycle and observer context, serializable correlation metadata,
  structured metadata logging, and the relocated statistics API.
- `sip-proxy-recovery`: Explicit restore element bindings, element-qualified
  lookup, metadata/observation identity preservation, and context-only element
  propagation during restored work and snapshot notifications.

## Impact

- Affects `sip/proxy*.go`, `sip/stats*.go`, message preparation in `sip` and
  `sip/transport`, transaction checkpoint adapters, and narrow `Element` APIs.
- Updates imports, examples, package documentation, `docs/proxy.md`, and project
  guidance that names moved public APIs. No compatibility re-export facade is
  planned; this is an intentional source-level migration.
- Keeps existing RFC forwarding, transaction behavior defaults, observation
  counting, and snapshot version/field meanings unless an additive metadata
  contract explicitly changes their representation. No new external dependencies.
- Does not add registrar/B2BUA functionality, distributed tracing infrastructure,
  persistent stateless request history, independently owned multi-element
  middleware lifecycles, slog handlers, or implementation during this proposal.
