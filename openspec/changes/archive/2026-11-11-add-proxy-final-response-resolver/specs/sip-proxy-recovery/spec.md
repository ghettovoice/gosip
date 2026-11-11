# Spec Delta

## ADDED Requirements

### Requirement: Checkpoints preserve selected response identity

Request checkpoints SHALL retain the exact selected candidate for a committed response outcome, independently of its status code. Capture, cloning, and JSON round trips SHALL preserve that identity and candidate order. A selection SHALL be absent for unfinished requests and non-response outcomes. Callbacks and in-flight callback execution SHALL NOT be serialized.

#### Scenario: Restore a custom choice with an equal status

- **WHEN** a custom resolver chooses the second of two 486 responses with different To tags or headers and the completed request is captured, serialized, decoded, and restored
- **THEN** the restored decision uses the second response's content rather than the default first-arrival candidate.

#### Scenario: Restore a transformed or aggregated response

- **WHEN** a committed response was selected as the only 503 or as a 401/407 with aggregated challenges
- **THEN** restoration retains the source candidate identity and reconstructs the same proxy-processed status, tags, and authentication challenges without changing stored candidates.

#### Scenario: Snapshot clone owns its selection identity

- **WHEN** an application changes selection identity in a cloned checkpoint
- **THEN** the original checkpoint and live request remain unchanged.

### Requirement: Restore does not rerun committed response policy

Restoration SHALL validate and reconstruct committed response decisions from saved selection identity and proxy-owned response processing, not by invoking route callbacks or reranking with the default policy. Activation SHALL NOT replay a committed response send or completion observation. Unfinished restored requests SHALL use their bound route's policy only when a new selection decision becomes eligible after activation.

#### Scenario: Runtime policy changed after the checkpoint

- **WHEN** a completed custom decision is restored with a route whose callback would now select another candidate or return an error
- **THEN** neither restoration nor activation calls that callback or changes the saved decision.

#### Scenario: Continue an unfinished request

- **WHEN** an unfinished request with saved negative candidates is restored and its remaining targets settle after activation
- **THEN** final selection uses the current bound route callback with the preserved candidate order and request context.

#### Scenario: Selection is still outstanding at capture

- **WHEN** an application requests a checkpoint while a final-response callback has not yet committed its result
- **THEN** capture reports unstable state rather than saving an incomplete response decision.

### Requirement: Saved selection validation precedes effects

Restoration SHALL reject missing, extraneous, or invalid selection identity and any inconsistency with the saved outcome, processed final status, or mandatory response precedence. Validation SHALL complete before claiming transaction bindings or installing state. Invalid checkpoints SHALL NOT invoke application policy or silently substitute a default choice.

#### Scenario: Missing or out-of-range selection

- **WHEN** a completed response checkpoint omits its selection or names an index outside its candidates
- **THEN** restoration fails with an inspectable invalid-snapshot error without sending traffic or claiming bindings.

#### Scenario: Selection cannot exist before a response decision

- **WHEN** a checkpoint records a selection for an unfinished request or a completed non-response outcome
- **THEN** restoration rejects the inconsistent checkpoint before producing effects.

#### Scenario: Non-final or mandatory-precedence violation

- **WHEN** a saved index identifies a nil or provisional response, a negative response despite a successful candidate, or a non-6xx response despite 6xx candidates and no success
- **THEN** restoration rejects the checkpoint rather than accepting a contradictory committed decision.

#### Scenario: Application preferences are not snapshot validity rules

- **WHEN** a saved custom selection chooses 486 over a saved 302 without a successful or 6xx candidate
- **THEN** restoration accepts the choice if the processed final status and other checkpoint relationships are valid, without imposing default preferences.

### Requirement: Current checkpoint format is updated in place

The snapshot format SHALL remain version 1 and incorporate selected-candidate identity in place. Restoration SHALL apply the updated format requirements without a legacy fallback or inferred selection. Compatibility with the previous layout is not required. Snapshot versions other than 1 SHALL continue to fail explicitly.

#### Scenario: Capture uses the current version

- **WHEN** a completed response decision is captured
- **THEN** its checkpoint uses version 1 and contains the explicit selected-candidate index.

#### Scenario: Old layout does not trigger reconstruction

- **WHEN** a version-1 completed response checkpoint lacks its selected-candidate index, even though the default resolver could reproduce its saved status
- **THEN** restoration rejects the checkpoint without inferring a selection or invoking the route callback.

#### Scenario: Unsupported checkpoint version

- **WHEN** a checkpoint version is not 1
- **THEN** restoration rejects it before claiming bindings, invoking callbacks, or producing network effects.
