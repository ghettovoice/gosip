# sip-non-invite-transactions Specification

## Purpose

Provide optional RFC 4320 non-INVITE server transaction behavior with compatible defaults, while preserving proxy protections and the finite-lifetime guarantees informed by RFC 4321.

## Requirements

### Requirement: Explicit opt-in preserves transaction defaults
Non-INVITE server transactions SHALL retain existing behavior with zero options. RFC 4320 response restrictions and automatic delayed Trying SHALL require an explicit option. The option SHALL NOT alter INVITE or client transaction behavior, and unsupported selections SHALL fail before activation.

#### Scenario: Default behavior remains compatible
- **WHEN** a non-INVITE server transaction is created with zero options
- **THEN** it does not acquire the new automatic Trying timer or new RFC 4320 send restrictions, and existing transaction behavior remains unchanged.

#### Scenario: Explicit enabled behavior
- **WHEN** an application explicitly selects RFC 4320 behavior before activation
- **THEN** its non-INVITE server transaction enforces the response restrictions and delayed Trying rules for its lifetime.

#### Scenario: INVITE behavior is unchanged
- **WHEN** shared server options include the non-INVITE setting for an INVITE transaction
- **THEN** INVITE transaction behavior and its existing timers remain unchanged.

### Requirement: Non-INVITE provisional response restrictions
With RFC 4320 behavior enabled, a non-INVITE server transaction SHALL NOT send provisional responses other than 100. Invalid attempts SHALL return an inspectable error without transmission or transaction-state mutation.

#### Scenario: Application attempts a non-100 provisional
- **WHEN** an application tries to send 180 on an RFC 4320-enabled non-INVITE server transaction
- **THEN** the operation fails without transmission and the transaction remains able to send a valid final response.

### Requirement: Transport-aware delayed Trying
With RFC 4320 behavior enabled, unreliable transport SHALL forbid 100 before client Timer E would reach T2. If unanswered at that deadline, the transaction SHALL send 100 on either transport class. Reliable transport SHALL permit an explicit earlier 100.

#### Scenario: Default unreliable timing
- **WHEN** an RFC 4320-enabled UDP non-INVITE request remains unanswered with T1 of 500 milliseconds and T2 of 4 seconds
- **THEN** no 100 is sent before 3.5 seconds and an automatic 100 is sent at the eligible deadline if a final response has not won the race.

#### Scenario: Early explicit Trying on UDP
- **WHEN** an application requests 100 before the eligible UDP deadline on an RFC 4320-enabled transaction
- **THEN** the operation fails without transmission or suppressing the later automatic Trying response.

#### Scenario: Reliable transport behavior
- **WHEN** an application explicitly sends 100 early over a reliable transport with RFC 4320 behavior enabled
- **THEN** the send is permitted and the automatic Trying mechanism does not emit a duplicate initial response.

#### Scenario: Final response races with Trying
- **WHEN** a final response and the delayed Trying deadline become ready concurrently on an RFC 4320-enabled transaction
- **THEN** transaction serialization prevents a provisional response from being sent after the final response.

### Requirement: Trying timing derives from effective configuration
In enabled mode, the delayed Trying interval SHALL follow the effective Timer E backoff using configured T1/T2, not INVITE Trying delay or T2 alone. Receiving or sending a provisional response SHALL NOT restart the non-INVITE client Timer F.

#### Scenario: Non-default timing
- **WHEN** supported non-default T1 and T2 values are configured with RFC 4320 behavior enabled
- **THEN** Trying eligibility follows the elapsed backoff sequence reaching T2 rather than a hard-coded 3.5-second delay.

#### Scenario: Provisional response does not extend lifetime
- **WHEN** a non-INVITE client transaction receives 100 while Timer F is running
- **THEN** Timer F retains its original expiry and request retransmission follows the applicable Timer E rules.

### Requirement: Non-INVITE timeout response suppression
In RFC 4320 mode a non-INVITE server transaction SHALL reject 408 without sending. Proxy suppression of downstream non-INVITE 408 SHALL apply independently of this option. Timeouts SHALL remain observable locally, and other eligible final responses SHALL remain forwardable.

#### Scenario: All branches time out
- **WHEN** a non-INVITE request has no forwardable final response after its attempts time out
- **THEN** the proxy reports the local timeout outcome and retires owned state without manufacturing an upstream 408.

#### Scenario: Downstream timeout response
- **WHEN** a downstream non-INVITE branch returns 408
- **THEN** the proxy records that branch outcome but does not forward the 408 upstream.

#### Scenario: Useful final rejection
- **WHEN** a non-INVITE 401 or 407 is eligible for forwarding while the original upstream transaction is usable
- **THEN** the response remains eligible under the normal aggregation rules.

#### Scenario: Enabled transaction rejects a timeout response
- **WHEN** an application attempts 408 on an RFC 4320-enabled non-INVITE server transaction
- **THEN** sending fails without transmission or transaction-state mutation.

### Requirement: Late non-INVITE response absorption
A stateful proxy SHALL NOT transmit a non-INVITE response without its matching non-terminated upstream server transaction, regardless of the transaction behavior option. A late or stray response SHALL NOT create a new transaction or fall back to stateless forwarding. This restriction SHALL NOT disable an explicitly stateless proxy's response path.

#### Scenario: Final response after upstream termination
- **WHEN** a non-INVITE final response arrives after the associated upstream server transaction terminates
- **THEN** it is discarded without a response send or transaction recreation.

### Requirement: Delayed Trying survives snapshot restoration
In RFC 4320 mode, snapshots SHALL preserve the pending Trying deadline and whether an initial response was sent. Activation SHALL retain elapsed time without replaying prior responses or starting a fresh full delay.

#### Scenario: Restored overdue Trying
- **WHEN** an RFC 4320 snapshot with no prior response and an elapsed Trying deadline is restored and activated
- **THEN** it becomes immediately eligible for 100 rather than waiting another full delay.

#### Scenario: Restore after a response
- **WHEN** an RFC 4320 transaction snapshot records a sent 100 or final response
- **THEN** activation does not replay that response or schedule a duplicate initial 100.

#### Scenario: Enabled snapshot lacks its required deadline
- **WHEN** an RFC 4320 snapshot has an active unanswered transaction but lacks the required Trying deadline
- **THEN** restoration fails with an inspectable validation error rather than guessing a new delay.

### Requirement: Snapshot behavior survives configuration changes
Snapshots SHALL preserve the selected non-INVITE server behavior. Missing behavior in older snapshots SHALL mean Legacy. Restoration SHALL retain the saved mode rather than apply current creation defaults or implicitly enable RFC 4320.

#### Scenario: Legacy active snapshot remains valid
- **WHEN** an older active snapshot has neither a behavior field nor a new Trying deadline
- **THEN** restoration retains Legacy behavior without inventing a Trying timer or rejecting the snapshot merely for those absent fields.

#### Scenario: Changed creation defaults do not upgrade recovery
- **WHEN** a saved Legacy transaction is restored while current creation options select RFC 4320
- **THEN** the restored transaction remains Legacy without implicit upgrade.

### Requirement: Non-INVITE forking preserves finite lifetime semantics
Non-INVITE forking SHALL preserve the fixed client transaction lifetime and applicable aggregation rules. Sequential scheduling SHALL NOT imply that upstream deadlines restart or that delayed negative responses are guaranteed to arrive before upstream timeout.

#### Scenario: Fast rejection and silent branch
- **WHEN** one branch returns 401 promptly and another produces no response until its timeout
- **THEN** the proxy preserves aggregation and lifetime rules, does not restart upstream timing, and absorbs the result if the original upstream transaction is no longer usable.
