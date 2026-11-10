# Spec Delta

## Purpose

Expose forwarding provenance on SIP message envelopes so independent middleware can preserve transit message identity through preparation, cloning, and application-managed checkpoints.

## ADDED Requirements

### Requirement: Typed envelope forwarding provenance

The library SHALL expose typed public access to metadata-backed forwarding provenance on request and response envelopes, including response To-tag preservation. Unmarked envelopes SHALL retain endpoint behavior. Provenance SHALL NOT require access to private message fields or encode proxy-specific routing policy in the SIP core.

#### Scenario: Independently implemented forwarding middleware

- **WHEN** middleware outside the core package marks an owned request or response envelope for forwarding
- **THEN** core send paths recognize its transit provenance through public APIs without private-field access.

#### Scenario: Ordinary endpoint message

- **WHEN** an application creates an unmarked endpoint request or response
- **THEN** existing Via, From-tag, and To-tag preparation defaults remain unchanged.

### Requirement: Transit request preparation preserves identity

Preparing a forwarded request SHALL preserve its dialog identity and existing upstream Via entries. Core helpers SHALL NOT manufacture a From tag or an endpoint Via for it. Transport finalization SHALL update only an explicitly designated local top Via, preserving its branch and all earlier hops without recognizing a proxy-specific branch namespace.

#### Scenario: Forwarded request lacks a From tag

- **WHEN** a marked forwarded request with a designated local Via passes through element, transaction, and transport preparation
- **THEN** its absent From tag remains absent, its branch stays unchanged, and only the designated hop receives transport or sent-by updates.

#### Scenario: Top Via is not the designated local hop

- **WHEN** a marked forwarded request contains only an upstream Via or a top branch that differs from its local-hop designation
- **THEN** preparation neither rewrites that upstream hop nor invents a new endpoint hop.

### Requirement: Transit response preparation preserves To identity

A response envelope marked for forwarding or explicit To-tag preservation SHALL retain the downstream To header, including an absent tag, across element and transport send preparation. Locally generated unmarked responses SHALL retain the existing endpoint tag-generation rules.

#### Scenario: Untagged downstream response

- **WHEN** a forwarded response without a To tag is sent through an upstream transaction and transport
- **THEN** no endpoint convenience introduces a new To tag.

### Requirement: Provenance survives envelope persistence

Forwarding and To-tag-preservation metadata SHALL survive envelope cloning and JSON serialization/deserialization. Updating a clone's provenance SHALL NOT modify its source envelope. Provenance SHALL remain local envelope data and SHALL NOT add or alter SIP wire headers or bodies.

#### Scenario: Forwarded envelope JSON round trip

- **WHEN** a marked request or response is cloned, encoded as JSON, decoded, and prepared for sending
- **THEN** its forwarding protections remain effective and its metadata does not appear in the rendered SIP message.

#### Scenario: Independent clone provenance

- **WHEN** the provenance of one cloned envelope is replaced or cleared
- **THEN** the original envelope and its sibling copies keep their own provenance values.

### Requirement: Existing transaction checkpoints retain provenance meaning

Transaction checkpoint restoration SHALL preserve the meanings of existing forwarded-request and preserved-response-tag fields. Checkpoints lacking the new envelope metadata SHALL remain restorable through those saved fields, without resetting a present provenance marker because a redundant legacy field is absent.

#### Scenario: Legacy client transaction checkpoint

- **WHEN** an existing INVITE or non-INVITE client checkpoint records a forwarded request but has no new provenance metadata
- **THEN** restoration reestablishes forwarded request preparation behavior.

#### Scenario: Legacy server transaction checkpoint

- **WHEN** an existing INVITE or non-INVITE server checkpoint records To-tag preservation on its last response
- **THEN** restored cached-response sends preserve the saved To header even without new envelope metadata.

#### Scenario: Envelope metadata without a legacy true flag

- **WHEN** a checkpoint contains valid forwarding metadata and an omitted or false redundant legacy marker
- **THEN** restoration does not silently remove the envelope's forwarding protection.
