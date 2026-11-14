# gosip Architecture

This document describes the **current** architecture of the gosip SIP stack.
The target architecture is covered in a separate section and is not decided yet.

Audience: maintainers, contributors, and coding agents working on this
repository. Every section points to the packages and files it describes, so it
can be used as a navigation map into the codebase.

## 1. What gosip is

gosip is a Go implementation of the SIP protocol stack per RFC 3261 and related
RFCs (3263, 3581, 3966, 5954, 8217, 8898). It provides the **protocol-level
mechanics** — message parsing/rendering, transports, transactions, and a
composition root (`Element`) — shared by user agents, proxies, and future
higher-level components (dialogs, registrars). It deliberately does **not**
own authentication or business response policy.

Layers that do not exist yet (as of this writing): dialogs, UA/registrar
logic, and a public root-package facade (`gosip.go` holds only `Version`).

## 2. Design principles

These recur throughout the code and explain most API shapes:

- **Composition root with private managers.** `sip.Element` owns a
  `TransportManager` and a `TransactionManager`; they are unexported fields and
  all operations go through element methods or component hooks.
- **Components over inheritance.** Extensions attach via
  `Element.UseComponent` and implement optional interceptor/handler
  interfaces; the core never depends on extensions (`sip/proxy` and
  `sip/stats` depend on `sip`, not the other way around).
- **Explicit lifecycle with admission control.** `Start`/`Shutdown`/`Close`
  are one-shot; operations and callbacks are admitted through WaitGroups so
  closing never races in-flight work.
- **Context propagation over parameters.** Element, transport, and
  transaction travel in `context.Context` and are retrievable from any
  callback.
- **Envelope model.** SIP messages are immutable-ish values wrapped in a
  concurrency-safe envelope that carries transport metadata, addresses, and
  serializable provenance.
- **Fail-loud errors with traces.** `internal/errors` is a drop-in stdlib
  replacement whose wrapping helpers record file/line segments
  (braces.dev/errtrace); `pkg/errclass` adds coarse classification
  (`closed`, `timeout`, `temporary`, ...).
- **Structured logging everywhere** via `log/slog` (`pkg/log` provides the
  default handler chain).

## 3. Repository layout

```
github.com/ghettovoice/gosip
├── doc.go, gosip.go          # root package: TODO doc, Version const only
├── uri/                      # SIP/SIPS/TEL/generic URIs (RFC 3261 §19, RFC 3966)
├── sip/                      # CORE: messages, transactions, transports, Element
│   ├── header/               # typed SIP header implementations
│   ├── transport/            # transport implementations (conn-oriented / conn-less)
│   ├── proxy/                # proxy component (route-based forwarding)
│   └── stats/                # statistics recorder component
├── pkg/                      # public support packages (importable by users)
│   ├── dns/                  # RFC 3263 resolver (SRV/NAPTR/A/AAAA) + opt-in cache
│   ├── errclass/             # error classification helpers
│   └── log/                  # default slog handlers
├── internal/                 # private support code
│   ├── errors/               # stdlib-errors replacement with errtrace wrapping
│   ├── grammar/              # ABNF grammars: internal/grammar/rfc3261, rfc3966
│   ├── shared/               # cross-package types: Addr, Values, TransportProto,
│   │                         #   RequestMethod, CallbackManager, mixin interfaces
│   ├── syncutil/             # OrderedList, ShardMap, RWMap
│   ├── netutil/              # conn decorators (logging/deadline/keepalive), HostIP
│   ├── ioutil/ util/ testutil/
├── openspec/                 # spec-driven workflow: specs/ + changes/archive/
├── docs/                     # proxy.md, this file, superpowers plans
└── Taskfile.yml              # task runner (test, lint, cover, ...)
```

Dependency direction (arrows mean "imports"):

```
uri ──► internal/{shared, grammar, ...}

sip ──► uri, sip/header, pkg/{dns, errclass, log}, internal/*
sip/transport ──► sip, pkg/dns, internal/*
sip/proxy   ──► sip, pkg/log, internal/*
sip/stats   ──► sip, sip/proxy

pkg/dns     ──► internal/* (standalone otherwise)
```

The core `sip` package imports neither `sip/proxy` nor `sip/stats`.

## 4. Layered model and message flow

Conceptual layers, bottom-up:

```
┌───────────────────────────────────────────────────────────────┐
│ application / extension components (sip/proxy, sip/stats, ...) │
├───────────────────────────────────────────────────────────────┤
│ Element (sip/element.go) — composition root, send/recv paths,  │
│   capabilities, component wiring, lifecycle                    │
├───────────────────────────────────────────────────────────────┤
│ Transactions (sip/transaction_*.go) — RFC 3261 §17 FSMs,       │
│   keys, retransmissions, snapshots/restore, TransactionManager │
├───────────────────────────────────────────────────────────────┤
│ Transports (sip/transport.go, sip/transport/) — listeners,     │
│   connections, framing, TransportManager, RFC 3263 resolution  │
├───────────────────────────────────────────────────────────────┤
│ Messages (sip/message*.go, sip/header/, uri/, internal/grammar)│
└───────────────────────────────────────────────────────────────┘
```

### 4.1 Interceptor chains

Every transport routes messages through four interceptor chains owned by
`TransportManager` (`sip/transport_manager.go`):
inbound-request, inbound-response, outbound-request, outbound-response.

- **Inbound chains run in FIFO order**: the first registered interceptor is
  the outermost and runs first (`sip.InterceptInboundRequest`,
  `sip/message_recv.go`).
- **Outbound chains run in LIFO order**: the last registered interceptor runs
  first (`sip.InterceptOutboundRequest`).

During `Element.init` the manager registers, in order:
the `TransactionManager` (message matcher) first, the `Element` itself second,
then every user component in `UseComponent` order. So the effective
pipelines are:

```
inbound request:   txm ─► element ─► component1 ─► component2 ─► ... ─► final receiver
outbound request:   ... ─► component2 ─► component1 ─► element ─► txm ─► transport write
```

A component that "claims" a message (e.g. proxy forwarding) stops calling
`next`; a component that only observes calls `next` and inspects the result.

### 4.2 Inbound request flow

```
socket (net.PacketConn | net.Conn)
  │  sip/transport: read, frame by Content-Length, parse via sip.Parser,
  │  fill RequestEnvelope {transport, local addr, remote addr}
  ▼
TransportManager inbound-request interceptors
  ├─ TransactionManager.InterceptInboundRequest      (sip/transaction_manager.go)
  │    • matches top Via (branch+sent-by+method) against registered server
  │      transactions → retransmission is absorbed by the matched FSM
  │    • if a downstream receiver produced a RequestRejectedError and a
  │      server transaction was created, responds statefully through it
  ├─ Element.InterceptInboundRequest                  (context injection)
  ├─ user components (proxy claims matching routes, stats counts, ...)
  ▼
final receiver: unhandled request → 503 Service Unavailable (transport level)
```

Invalid messages (parse or validation failures) are rejected with
400 Bad Request before the interceptor chain runs.

A stateful application (or proxy) creates a server transaction via
`Element.NewServerTransaction` inside its interceptor, registers it, and
responds through it; `SendResponseStateful`/`RespondStateful` resolve the
destination per RFC 3261 §18.2.2 / RFC 3263 §5 (symmetric response routing,
`rport`).

### 4.3 Outbound request flow (stateful)

```
application
  │ Element.SendRequestStateful                       (sip/element.go)
  │   1. ResolveRequestTarget (request-URI → target URI)
  │   2. ProduceRequestAttempts: RemoteElementLocator.LookupRequestAddrs
  │      (RFC 3263: NAPTR → SRV → A/AAAA, transport priority order)
  │   3. per attempt: pick transport by proto, build client transaction,
  │      register in TransactionManager, start, send
  │   4. confirmClnTx: wait for accepted/completed final response;
  │      503/transport failure/timeout before any response → next candidate
  ▼
TransportManager outbound-request interceptors (LIFO: components first)
  ├─ user components
  ├─ Element.InterceptOutboundRequest
  │    • PrepareVia (top Via: transport, sent-by, branch)
  │    • EnsureFromTag, Max-Forwards, Content-Length
  │    • unreliable transport: compact-render / MTU-200 check
  ├─ TransactionManager.InterceptOutboundRequest
  ▼
transport write (connection pool for stream transports, per-packet for UDP)
```

`Element.SendRequest` sends only the first candidate;
`SendRequestStateless` iterates candidates with failover;
`SendRequestStateful` adds a client transaction and candidate confirmation.
RFC 2543 fallback and unreliable-transport fallback attempts are opt-in.

Responses to requests sent statefully are matched by
`TransactionManager.InterceptInboundResponse` to the client transaction
(Via branch), which absorbs retransmissions and exposes them through
`ClientTransaction.BindResponseHandler`.

## 5. Core `sip` package

### 5.1 Message model (`sip/message*.go`, `sip/header/`)

- `Message` interface — composition of mixin interfaces from
  `internal/shared`: `Renderer`, `Cloneable[Message]`, `Validatable`,
  `ValidFlag`, `Equalable`.
- Concrete messages: `Request` (`Method`, `URI`, `Proto`, `Headers`, `Body`)
  and `Response` (`Status`, `Reason`, `Proto`, `Headers`, `Body`) — plain
  value structs, not concurrency-safe by themselves.
- `Headers` — `map[Name][]Header` keyed by canonical header names
  (`sip/header.go`); iteration and rendering apply a canonical sort order.
  Typed header implementations live in `sip/header/` and are exposed through
  typed accessors (`CallID()`, `FirstVia()`, ...).
- `MessageEnvelope[T]` (`sip/message.go`) — the unit that flows through the
  stack: guards the message with `RWMutex`, caches the rendered form, and
  atomically stores transport metadata (`TransportMetadata`), local/remote
  `netip.AddrPort`, and a serializable `*MessageMetadata`. Typed wrappers:
  `RequestEnvelope`, `ResponseEnvelope` (e.g. `NewResponse`,
  `UpdateFrom`, provenance marks).
- `FlowToken` (`sip/flow.go`) — HMAC-signed RFC 5626-style flow identifier
  (transport + local + remote address). Standalone utility type; not yet
  wired into connection management.

### 5.2 Parsing and rendering (`sip/parser.go`, `internal/grammar/`)

- `Parser` interface: `ParsePacket([]byte)` for datagrams and
  `ParseStream(io.Reader)` returning a `StreamParser` iterator that frames
  stream messages by Content-Length. `StdParser` (obtained via
  `sip.DefaultParser()`) is the ABNF-based implementation.
- Grammar rules live in `internal/grammar/rfc3261` and `.../rfc3966` on top
  of `github.com/ghettovoice/abnf` with an enabled node cache.
- Rendering honors `RenderOptions` (compact form, etc.) and is cached on the
  envelope until the message mutates.

### 5.3 Transport layer (`sip/transport.go`, `sip/transport_manager.go`, `sip/transport/`)

- `Transport` interface: request/response senders, `Responder`, the four
  interceptor-chain hooks, `Metadata()`, `Listen()`, `MatchSentBy()`,
  `Close()`. Narrow roles `ClientTransport` / `ServerTransport` are used by
  transactions.
- `TransportMetadata` (`sip/transport_meta.go`): proto, network, default
  port, flags (`Reliable`/`Secured`/`Streamed`), NAPTR service, priority,
  MTU. Standard constructors: `UDPMetadata`, `TCPMetadata`, `TLSMetadata`,
  `SCTPMetadata`, `TLSSCTPMetadata`, `WSMetadata`, `WSSMetadata`.
- `TransportManager`: protocol→transport registry (`syncutil.RWMap`) plus
  the four interceptor chains; resolves outbound targets via
  `RemoteElementLocator` / `RemoteClientLocator` (RFC 3263).
- Implementations (`sip/transport/`) are **generic over the listener type**:
  `ConnLessTransport` wraps `net.PacketConn` (UDP), `ConnOrientedTransport`
  wraps `net.Listener` (TCP, TLS, WS, WSS — distinguished by metadata and
  configured `ListenConfig`/`ConnDialer`).
- `TransportOptions` (`sip/transport/transport.go`): public address template
  for Via sent-by (with send-time resolution rules), parser, client locator,
  DNS resolver, listen/dial config, message size limits, read/write/idle
  timeouts, custom-message handler.
- Connection management: pooling and reuse for stream transports, idle
  timeout reaping, keep-alive/deadline/logging decorators from
  `internal/netutil`.

### 5.4 Transaction layer (`sip/transaction*.go`)

- `Transaction` interface: type, state, message matching, `Start` (exactly
  once after registration), `Terminate`, bindable start/state/error handlers.
- Four RFC 3261 §17 FSMs built on `github.com/qmuntal/stateless`:
  client/server × INVITE/non-INVITE, with the standard states (`Trying`,
  `Calling`, `Proceeding`, `Accepted`, `Completed`, `Confirmed`,
  `Terminated`) and timers from `TimingConfig` (`T1`, `T2`, `T4`, `Timer D`,
  `Timer 100`; constants in `sip/timing.go`).
- Retransmissions over unreliable transports per RFC 3261 §17.1.1.2/§17.2.1;
  matched server transactions absorb request retransmissions.
- `ClientTransaction` / `ServerTransaction` add keys, initial/last messages,
  narrow transport roles, and `Respond`/response-handler binding.
- `TransactionManager` (`sip/transaction_manager.go`): shard-mapped
  registries keyed by `ClientTransactionKey`/`ServerTransactionKey` (top Via
  branch, sent-by, method); implements the inbound interceptor that routes
  responses/requests to transactions; watchdogs terminate stale
  (`StaleTransactionTimeout`, default 1h) and never-started
  (`TransactionStartTimeout`, default 1m) transactions; factories
  (`ClientTransactionFactory`, `ServerTransactionFactory`) are injectable
  through `Element` config.
- **Snapshots and restore**: every built-in transaction publishes stable,
  cloned, JSON-serializable snapshots (`ClientTransactionSnapshot`,
  `ServerTransactionSnapshot`) through optional `*Snapshotter` interfaces;
  `Restore*Transaction` functions rebuild a transaction from a snapshot.
  This is the basis for durable-state recovery (used by `sip/proxy`).
- On element close, proceeding INVITE client transactions receive a
  best-effort CANCEL before transports are torn down
  (`Element.close`, `sip/element.go`).

### 5.5 Element (`sip/element.go`)

`Element` is the composition root. The zero value is usable; configuration
fields (factories, `DNSResolver`, `URIConverter`, `BaseLogger`,
`StaleTransactionTimeout`, `TransactionStartTimeout`, `BaseCapabilities`)
must be set before first use.

Responsibilities:

- **Transport tracking**: `TrackTransport`/`UntrackTransport`,
  `TransportByProto`, metadata provider for resolution.
- **Listeners**: `Listen` (caller serves) and `ListenManaged` (retained for
  `Start`); `Start` is a one-shot managed runtime that starts dormant
  transactions and serves managed listeners under a detached context.
- **Send paths**: `SendRequest` / `SendRequestStateless` /
  `SendRequestStateful` (attempts, DNS failover, transaction confirmation),
  `SendResponse` / `SendResponseStateful`, `Respond` / `RespondStateful`,
  `LookupRequestAddrs`, `ProduceRequestAttempts`.
- **Transaction administration**: `NewClientTransaction`,
  `NewCancelClientTransaction`, `NewServerTransaction`, `Register*`,
  `Load*`, `All*`.
- **Errors**: unexpected runtime failures go to handlers bound via
  `BindErrorHandler` (e.g. `*ListenerServeError`).

Lifecycle (`sip/element.go`): states `ready → running → closing → closed`.
Operations are admitted via `opsWg`; element-wired component callbacks via
`cbWg` (sealed only during close, so final transaction termination can still
reach components). `Shutdown(ctx)` runs component `Shutdown` hooks in
registration order, then `Close()`. `Close()` sends best-effort CANCELs,
cancels the runtime context, closes the transaction and transport managers,
drains operations and callbacks, then unbinds and closes components in
registration order. An explicit **draining phase does not exist yet** — see
`TODO.md`.

Capabilities (`sip/element_capabilities.go`): `ElementCapabilities`
(methods, option tags, MIME types) merged from `BaseCapabilities` and every
component implementing `Capabilities()`; recompute is
revision-guarded against concurrent registration.

### 5.6 Component model (`sip/element_component.go`)

`ElementComponent` is `any`; `UseComponent` type-switches the optional
interfaces a component may implement:

| Interface | Effect |
| --- | --- |
| `MessageInterceptor` (or any of the four `Inbound/Outbound{Request,Response}Interceptor`) | inserted into the transport interceptor chains |
| `TransactionHandler` (or `Client/ServerTransactionHandler`) | notified of transaction termination |
| `Capabilities() ElementCapabilities` | merged into element capabilities |
| `Shutdown(ctx) error` | invoked by `Element.Shutdown` |
| `io.Closer` | invoked by `Element.Close` |

Element adapters wrap every component callback with callback admission and
the element in the context. Registration order is significant (see §4.1).
Unbind is idempotent; after closing began it is a no-op because shutdown
retains ownership.

### 5.7 Context propagation (`sip/context.go`)

Three context keys carry the current `Element`, `Transport`, and
`Transaction`. `ContextWithTransaction` also installs the transaction's
transport. Passing `nil` masks an inherited value (used by transports before
intercepting a new inbound message). Typed getters:
`ElementFromContext`, `TransportFromContext`,
`TransactionFromContext`, `ClientTransactionFromContext`,
`ServerTransactionFromContext`.

### 5.8 Provenance and metadata (`sip/message_provenance.go`)

The envelope carries private metadata marks that switch helpers between
**endpoint** and **transit** behavior:

- `MarkForwarded` — the message is a forwarded (proxied) copy;
  `PrepareVia` then only updates the designated local top Via hop (matched
  by recorded branch) instead of adding a new one; `EnsureFromTag` /
  `EnsureToTag` become no-ops preserving dialog identity.
- `SetForwardedViaBranch` / `SetPreserveToTag` — finer-grained designations.

All marks live in `MessageMetadata`: serializable, survive envelope cloning,
and never appear on the wire. `sip/proxy` stamps its own observation metadata
the same way.

## 6. Extension packages

### 6.1 `sip/proxy` (`sip/proxy/`, user guide `docs/proxy.md`)

An inbound component claiming requests that match configured routes and
forwarding them in **stateful** (default) or **stateless** mode.

- `New(routes, opts)` is pure: validates and copies config, binds nothing;
  registration is an explicit `elm.UseComponent(prx)`; one instance may
  serve several elements.
- Routing: ordered `Route` list; per-route targets, branch selection,
  request snapshots and observation `Event`s (`BindEventHandler`,
  `BindSnapshotHandler`).
- Route-level final response resolution (latest rework): pluggable policy
  for when a branch set ends with a final response.
- Recovery: `RestoreRequest` validates a captured `RequestSnapshot`
  against `RestoreBindings` and installs the coordinator dormant until
  `Request.Start`.
- Lifecycle: `Proxy.Shutdown` seals admission and retires owned work across
  all elements; unbind detaches one element only.

### 6.2 `sip/stats` (`sip/stats/`)

`Recorder` implements the element component and transaction-handler
interfaces of the core plus the proxy event-handler contract; `Report()`
returns a JSON-serializable report with Transports, Transactions, and Proxy
counter sections.

### 6.3 `sip/auth` (`sip/auth/`)

Optional basic authentication mechanisms (Digest, Bearer) built on the
consumer-owned `sip.ClientAuthenticator` and `sip.ServerAuthenticator[P]`
contracts declared in the core package. Authentication mechanics are opt-in:
no component is registered automatically, credentials/tokens/account stores
stay with the application, a server authenticator returns a typed principal
instead of inferring identity, and the package never sends, retries, or owns
SIP exchanges. See `docs/authentication.md` for the contract and ownership
model.

## 7. Support packages

- **`uri/`** — `SIP`/`SIPS` (RFC 3261 §19.1 with §19.1.4 equality), `Tel`
  (RFC 3966, `ToSIP` conversion), `Any` (generic, `net/url.URL`-based);
  unified `URI` interface; text/JSON serialization.
- **`pkg/dns`** — SIP-oriented resolver (SRV, NAPTR, A/AAAA with RFC 2782 /
  RFC 3263 ordering) on `codeberg.org/miekg/dns`; `NewCachedResolver` wraps
  any lookup implementation with an opt-in TTL cache (fixed application
  lifetime, not record TTL).
- **`pkg/errclass`** — error classification: `Classify`, `Classes`
  (`closed`, `timeout`, `temporary`, `canceled`, `not found`, `conflict`),
  `Is*` helpers.
- **`pkg/log`** — default `slog` handler chain for dev/console output.

## 8. Internal packages (`internal/`)

- **`errors`** — stdlib replacement: `errors.New`-like constructors plus
  wrapping variants (`Wrap`, `ErrorfWrap`, `JoinWrap`, `JoinPrefixWrap`)
  that record errtrace segments; used repository-wide.
- **`grammar`** — ABNF rules for RFC 3261 / RFC 3966, escaping helpers,
  node cache setup.
- **`shared`** — types shared across packages: `Addr`, `Values`,
  `TransportProto`, `RequestMethod`, `ResponseStatus`, mixin interfaces
  (`Renderer`, `Cloneable`, `Validatable`, `ValidFlag`, `Equalable`),
  `CallbackManager[T]` (ordered bind/unbind callback registry).
- **`syncutil`** — `OrderedList[T]`, `ShardMap[K,V]`, `RWMap[K,V]`.
- **`netutil`** — connection decorators (logging, deadlines, keep-alive,
  auto-close), `HostIP`, address conversion helpers.
- **`ioutil`, `util`, `testutil`** — small IO, misc, and test helpers.

## 9. Development workflow

- **Specs**: `openspec/specs/` — `sip-message-provenance`,
  `sip-non-invite-transactions`, `sip-proxy`, `sip-proxy-recovery`;
  completed changes are archived under `openspec/changes/archive/`.
- **Plans**: `docs/superpowers/plans/` — implementation plans for larger
  reworks.
- **Conventions**: `AGENTS.md` points to `.agents/rules/` (Go base/testing/
  error guides, project conventions, commit-message rules).
- **Tasks**: `Taskfile.yml` (test, lint with `.golangci.yml`, coverage).
- **CI**: `.github/workflows/` — tests, CodeQL.

## 10. Known gaps and deferred work

- **Element draining** — designed as an explicit phase for
  `Element.Shutdown` (admission rules for completion traffic, component registration
  freeze, drain criteria); open questions listed in `TODO.md`.
- **Public facade** — root `doc.go` is a TODO; README installation/usage/
  features sections are TODO; no runnable examples yet (`examples/` holds
  only TLS certs).
- **Higher layers** — dialogs and UA/registrar are not implemented;
  `Element` is explicitly scoped below them. Authentication contracts in
  `sip` and optional `sip/auth` mechanics are landing incrementally but no
  automatic authentication is attached.
- **Flow tokens** — `FlowToken` exists but is not used by connection
  management (outbound interface setting, RFC 5626, would be its consumer).
