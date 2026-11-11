# Design

## Context

See [proposal.md](proposal.md) for motivation and the two delta specs for the behavioral contract.

Observed implementation:

- `Route` in `sip/proxy/config.go` exposes ordinary function fields for target resolution and other application hooks.
- `selectFinalRes` in `response.go` combines candidate ranking, lone-503 rewriting, and authentication aggregation. Its ranking helpers already implement the desired default preferences.
- `Request.finishLocked` and `chooseOutcomeLocked` in `stateful.go` currently choose and commit synchronously under the coordinator mutex. Effects returned by transitions execute outside that mutex in `drain`.
- `applyClnResLocked` stores final responses in arrival order, including address-retry 503s, excluding non-INVITE 408. Eligible late INVITE successes remain forwardable even after an attempt settles.
- Version-1 `RequestSnapshot` stores candidates and `FinalStatus`, but not the selected candidate. Both `validateRestoreFinalStatus` and installation call `selectFinalRes` again. Status alone cannot distinguish two candidates with different tags or headers.
- `failLocked` provides the existing abandoned-request path and `rejectUpstream` sends through the original upstream transaction, never a replacement.

The user selected an ordinary function field, an exported default implementation, and a returned candidate index. The remaining decisions below are this proposal's concrete design, not previously implemented behavior.

## Goals / Non-Goals

**Goals:**

- Separate application preference from proxy-owned SIP processing while preserving the nil-hook wire behavior.
- Offer a small reusable API that cannot return a fabricated response envelope.
- Make callback execution, result commitment, and checkpoint reconstruction distinct boundaries.
- Preserve exact response identity through JSON recovery without serializing policy.

**Non-Goals:**

- An interface, named function type, strategy registry, comparator API, or global proxy option.
- Arbitrary response rewriting, application-generated responses, or configurable general-unavailability handling for 503.
- Changes to stateless forwarding, target scheduling rules, cancellation semantics, transaction lifetime, or storage ownership.
- A new callback execution pool, timeout setting, or exactly-once callback guarantee across crashes.

## Decisions

### 1. Ordinary route function with a small selection value

Add the following public input type in `config.go`:

```go
type FinalResponseSelection struct {
    Request    *sip.RequestEnvelope
    Candidates []*sip.ResponseEnvelope
}
```

Add this field to `Route` beside its other hook fields:

```go
ResolveFinalResponse func(context.Context, FinalResponseSelection) (int, error)
```

Export this function from `response.go`:

```go
func DefaultFinalResponseResolver(
    ctx context.Context,
    selection FinalResponseSelection,
) (int, error)
```

`Request` is a detached copy of the admitted original request, not a downstream branch request. Candidates retain arrival order and existing response-stage metadata; the index addresses the originally offered slice. The proxy does not expose a mutable `Request` coordinator or add parallel target/attempt fields already available through metadata.

For custom callbacks, clone the input envelopes, candidate slice, and metadata containers using the existing envelope copy helpers. Opaque metadata values retain their existing shared-value semantics; do not claim arbitrary application objects are deep-copied. Inputs are contractually read-only even though protocol-owned fields are defensively detached.

An index avoids pointer-membership checks against detached envelopes, preserves the selected original's identity, and cannot manufacture a replacement response. Returning an envelope or an independently constructed response was rejected for those reasons. A named function type or interface adds no needed behavior compared with the neighboring hooks.

### 2. Default ranking is reusable; response processing stays internal

`DefaultFinalResponseResolver` is deterministic and has no protocol side effects. It ignores nil/non-final entries and returns the winning original index using the current `resOutranks` and `preferredFailure` ordering:

1. Success before every negative class.
2. 6xx before the other negative classes.
3. Otherwise the lowest class.
4. Within 4xx, prefer 401, 407, 415, 420, and 484.
5. Preserve the earlier candidate on ties.

Return `-1` and an error if no final exists. The function does not mutate inputs, merge headers, map 503, or send anything. Its context parameter makes it directly assignable to the route field; the built-in ranking needs no external work.

Extract a private finalization helper taking the chosen original index and the original full response context. It preserves current behavior:

- A lone final 503 becomes an owned 500 copy, retaining the downstream dialog tag.
- A selected 401/407 produces an owned copy with the existing authentication aggregation.
- Otherwise return the selected original envelope, retaining pointer identity for send deduplication.

Do not change the current behavior for multiple 503 candidates or introduce a general-availability exception in this change. Keep the existing all-default `selectFinalRes` behavior available as a private composition for existing internal consumers, without duplicating ranking. Snapshot restoration uses saved selection identity rather than this ranking composition.

### 3. Custom preference cannot replace mandatory protocol decisions

Invoke custom resolution only when all logical targets have settled, there is at least one eligible negative final, and no successful candidate is authoritative. Preserve the existing collection of retryable 503 candidates and exclusion of non-INVITE 408.

If any success exists, the core selects the existing first-success candidate without invoking custom policy. INVITE provisional/success forwarding, additional successes, and no-candidate timeout/cancellation/error classification remain core operations. Routes that remain stateless never invoke this hook; pre-send promotion retains the route and therefore its callback.

Validate a successful callback result against the original candidate list:

- It is within bounds and names a selectable negative final.
- If a 6xx exists, it names a 6xx.
- It does not override a superseding success, abandonment, or shutdown.

Do not enforce lowest-class or preferred-4xx recommendations on custom policy. They remain the default, and applications are responsible for understanding deliberate departures from RFC recommendation-level behavior.

Filtering the offered list to the default winning class was rejected: it would prevent legitimate custom policy and obscure the relationship to stored candidates. Letting the callback choose success handling or cancellation was rejected because these are protocol decisions, not negative-response preferences.

### 4. Resolve outside the lock, commit on the coordinator

Keep the nil-hook path synchronous and pure under the existing guard. For a custom callback:

1. After settlement, mark resolution pending, capture its decision token and original candidate mapping, and return an effect. Do not mark the request completed or emit completion yet.
2. Run the callback outside the guard with the request's processing element in context.
3. Enqueue a dedicated result event carrying the token, selected index, and error.
4. Apply the result on the coordinator, validate against original state, finalize the selected response, record its index, and commit the ordinary final-send/completion work.

Pending resolution prevents repeated `finishLocked` or scheduling events from issuing another callback. Snapshot capture while pending returns the existing unstable-state error, as pending transaction admission already does; snapshot publication does not publish a partial decision.

Handle the specific new race created by this boundary: an INVITE success on a retained settled attempt can arrive while the callback is running. The late-success path must retain that response as the authoritative decision evidence while resolution is pending, forward it under the existing transaction rules, and invalidate the pending negative selection. Do not merely rely on the old early-return path, which forwards late successes without adding them to the response context. A queued stale callback result, including an error, must not overwrite the success or emit another completion.

Shutdown/abandonment also invalidates pending result tokens. The callback need not finish before retirement can be recorded, but an eventual return cannot restart work. No new goroutine-per-callback scheme is introduced; execution follows the existing coordinator effect model.

### 5. Invalid selection is a policy failure, not fallback

Use `internal/errors` and the proxy error class for result-validation failures and preserve returned callback causes. Do not add a policy fallback that silently selects another response; applications wanting fallback explicitly call the exported default.

An applicable returned error or invalid result enters `failLocked`, committing `OutcomeAbandoned` once. Normalize the failure to a local 500 rejection before the existing upstream failure effect, so a callback-returned `sip.RequestRejectedError` cannot become an undocumented way to choose an arbitrary SIP status. Preserve the underlying error chain. If the upstream transaction is gone, retain the failure without manufacturing a send or transaction.

Use the existing error-reporting and completion boundaries rather than logging the same failure at every helper. Distinguish validation failure from absence of candidates: the latter never invokes custom policy and retains existing classification.

### 6. Extend the current snapshot format in place

Add the following public snapshot field and a matching private committed index:

```go
FinalCandidateIndex *int `json:"final_candidate_index,omitempty"`
```

The pointer distinguishes an absent decision from candidate zero. It indexes the complete stored `Candidates` slice, not a filtered view. Allocate independent pointer storage during snapshot capture and `Clone`.

Keep the current snapshot version at 1 and extend its layout in place. Require an index exactly when `Completed` and `OutcomeResponse` are true, including core-selected successful outcomes; otherwise the field is absent. Intermediate resolution execution is not serializable.

Restore validation checks index presence, bounds, final eligibility, method-specific suppression, mandatory success/6xx precedence, and agreement between `FinalStatus` and proxy finalization of the indexed candidate. It does not impose default class/4xx preferences. Complete validation before installing bindings or subscriptions.

Installation reconstructs `rq.final` from the saved original index and deterministic proxy-owned finalization. It must not call the route resolver or rerank with the default. This also preserves same-status selections whose tags or headers differ. Completed restored requests remain inspect-only on activation, with no replay of sends or historical observations.

Backward compatibility with the previous layout is explicitly not required. A completed response checkpoint without the required index is invalid, even if its `Version` is 1 and its status could be reproduced by the default resolver. Do not infer a missing index, invoke policy to recover it, or add a legacy reconstruction branch. Keep rejecting snapshot versions other than the current version.

An unfinished restored request has no committed index; after activation it can invoke its current bound route's resolver when its preserved work reaches a new selection decision.

The user explicitly chose an in-place format change without compatibility constraints. A version bump, dual-layout reader, and deployment/rollback compatibility machinery are unnecessary for this change.

## Risks / Trade-offs

- [The current snapshot layout changes] -> Validate the required saved index strictly; compatibility with previously captured response decisions is deliberately outside scope.
- [Callback latency holds coordinator effects] -> Keep the existing synchronous hook model, document prompt callbacks, and test with channels rather than sleeps. This change does not promise a response-time bound for a blocking application callback.
- [Resolution introduces a success race] -> Retain authoritative late-success evidence while pending, invalidate stale results, and test both callback return ordering and shutdown ordering.
- [Opaque metadata values can alias] -> State the existing envelope copy contract explicitly; detach protocol state and metadata containers without introducing serialization or arbitrary deep-copy requirements.
- [Custom policy departs from recommended preferences] -> Keep standards-oriented defaults and document application responsibility without weakening mandatory core constraints.
- [An index depends on stable order] -> Preserve stored arrival order, retain the original mapping across the callback, and test selection of the second same-status candidate.
- [Public struct additions affect unkeyed literals] -> Document the additive API migration and prefer keyed literals in examples.

## Delivery Plan

1. Implement the ordinary route field, default function, coordinator boundary, and current snapshot layout changes together with their regression tests.
2. Leave existing route fields nil unless customization is wanted; optionally assign `DefaultFinalResponseResolver` explicitly or call it from a custom callback.
3. Keep the snapshot version at 1. Do not implement old-layout conversion, reader/writer rollout coordination, or rollback compatibility. Persistence remains application-owned.

The proposal has no unresolved behavioral decisions. Implementation begins only after a separate apply request.
