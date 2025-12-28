# Transaction Persistence

This document describes how to use transaction snapshots for persistence and recovery after server restarts.

## Overview

SIP transactions in `gosip` support serialization and restoration through a **snapshot pattern**. This allows you to:

- **Persist transaction state** to external storage (database, Redis, filesystem, etc.)
- **Restore transactions** after server restart or failure
- **Migrate transactions** between different server instances

**Supported transaction types:**

- ✅ INVITE server transactions
- ✅ Non-INVITE server transactions
- ✅ INVITE client transactions
- ✅ Non-INVITE client transactions

## Key Concepts

### Snapshot

`ServerTransactionSnapshot` and `ClientTransactionSnapshot` are serializable structures that contain all the data needed to restore a transaction:

- Transaction state (Trying, Proceeding, Accepted, Completed, etc.)
- Transaction key
- Original request and last response
- Timer states (automatically managed by `SerializableTimer`)
- Send options used for the request/response

> **Note**: The snapshot creates deep copies of the request, last response, and send options. This ensures that the snapshot is independent of the original transaction and can be safely serialized and stored without worrying about concurrent modifications.

### Versioning

Snapshots are currently version 1. The version is not explicitly stored in the snapshot structure but may be added in future versions for backward compatibility.

## API

### Taking a Snapshot

```go
tx, _ := sip.NewInviteServerTransaction(req, transport, &sip.ServerTransactionOptions{
    Log: logger,
})

// Take a snapshot at any time
snapshot := tx.Snapshot()

// Serialize to JSON
data, _ := json.Marshal(snapshot)

// Save to your persistent store
db.Save(tx.Key(), data)
```

### Restoring from Snapshot

```go
// Load from your persistent store
data := db.Load(key)

// Deserialize
var snapshot sip.ServerTransactionSnapshot
json.Unmarshal(data, &snapshot)

// Restore the transaction
tx, err := sip.RestoreInviteServerTransaction(&snapshot, transport, &sip.ServerTransactionOptions{
    Log: logger,
})
```

### Non-INVITE Server Transaction Example

```go
// Create non-INVITE transaction
tx, _ := sip.NewNonInviteServerTransaction(req, transport, opts)

// Take snapshot
snapshot := tx.Snapshot()

// Restore non-INVITE transaction
restoredTx, _ := sip.RestoreNonInviteServerTransaction(snapshot, transport, opts)
```

### INVITE Client Transaction Example

```go
// Create INVITE client transaction
tx, _ := sip.NewInviteClientTransaction(req, transport, &sip.ClientTransactionOptions{
    Log: logger,
})

// Take snapshot
snapshot := tx.Snapshot()

// Serialize to JSON
data, _ := json.Marshal(snapshot)

// Restore INVITE client transaction
var snapCopy sip.ClientTransactionSnapshot
json.Unmarshal(data, &snapCopy)
restoredTx, _ := sip.RestoreInviteClientTransaction(&snapCopy, transport, &sip.ClientTransactionOptions{
    Log: logger,
})
```

### Non-INVITE Client Transaction Example

```go
// Create non-INVITE client transaction
tx, _ := sip.NewNonInviteClientTransaction(req, transport, opts)

// Take snapshot
snapshot := tx.Snapshot()

// Restore non-INVITE client transaction
restoredTx, _ := sip.RestoreNonInviteClientTransaction(snapshot, transport, opts)
```

### Automatic Persistence on State Changes

The recommended approach is to use `OnStateChanged` callback to automatically persist transactions:

```go
tx, _ := sip.NewInviteServerTransaction(req, transport, &sip.ServerTransactionOptions{
    Log: logger,
})

tx.OnStateChanged(func(ctx context.Context, from, to sip.TransactionState) {
    snapshot := tx.Snapshot()
    data, _ := json.Marshal(snapshot)
    
    // Save to your persistent store
    myStore.Save(tx.Key(), data)
})
```

## Timer Restoration

Timers are automatically restored from snapshots with the following behavior:

1. **Running timers** - If a timer was running when the snapshot was taken, it will be restored and continue counting from where it left off
2. **Expired timers** - If a timer has already expired according to the snapshot, it will not be restarted
3. **Timer callbacks** - All timer callbacks are reconnected to the FSM after restoration

Thanks to `SerializableTimer`, you don't need to manually manage timer state.

## Important Considerations

### What Gets Restored

✅ Transaction state and key  
✅ Original request and last response  
✅ Timer states and expiration times  
✅ Send options (remote address, compact mode)  
✅ FSM state machine configuration  

### What Doesn't Get Restored

❌ Active network connections  
❌ Context (a new context is created)  
❌ State change callbacks (need to be re-registered)  
❌ Error callbacks (`OnError`) (need to be re-registered)  
❌ ACK callbacks (`OnAck`) for INVITE server transactions (need to be re-registered)
❌ Response callbacks (`OnResponse`) for client transactions (need to be re-registered)  

### FSM State Restoration

When a transaction is restored, the FSM is set to the saved state **without triggering OnEntry actions**. This prevents:

- Duplicate message sends
- Unwanted side effects
- Race conditions

The restored transaction can immediately continue normal operation from its saved state.

## Best Practices

### 1. Persist on State Changes

Only persist critical state changes, not every internal event:

```go
tx.OnStateChanged(func(ctx context.Context, from, to sip.TransactionState) {
    // Only persist meaningful states
    switch to {
    case sip.TransactionStateProceeding,
         sip.TransactionStateAccepted,
         sip.TransactionStateCompleted:
        saveSnapshot(tx.Snapshot())
    }
})
```

### 2. Use Transaction Key as Storage Key

The transaction key uniquely identifies each transaction:

```go
snapshot := tx.Snapshot()
storageKey := snapshot.Key.Branch // or serialize the whole key
db.Save(storageKey, snapshot)
```

### 3. Handle Restore Errors

```go
tx, err := sip.RestoreInviteServerTransaction(snapshot, tp, opts)
if err != nil {
    switch {
    case errors.Is(err, sip.ErrInvalidArgument):
        // Invalid snapshot data
    default:
        // Other errors
    }
}
```

### 4. Clean Up Terminated Transactions

Remove terminated transactions from persistent storage:

```go
tx.OnStateChanged(func(ctx context.Context, from, to sip.TransactionState) {
    if to == sip.TransactionStateTerminated {
        db.Delete(tx.Key())
    }
})
```

### 5. Non-INVITE Transaction Persistence

Non-INVITE transactions work the same way but use different functions:

```go
// Create non-INVITE server transaction
tx, _ := sip.NewNonInviteServerTransaction(req, transport, opts)

// Restore from snapshot
restoredTx, _ := sip.RestoreNonInviteServerTransaction(snapshot, transport, opts)
```

### 6. Client Transaction Persistence

Client transactions support the same snapshot/restore pattern:

```go
// INVITE client transaction
inviteTx, _ := sip.NewInviteClientTransaction(req, transport, opts)
snapshot := inviteTx.Snapshot()
restoredInvite, _ := sip.RestoreInviteClientTransaction(snapshot, transport, opts)

// Non-INVITE client transaction
nonInviteTx, _ := sip.NewNonInviteClientTransaction(req, transport, opts)
snapshot := nonInviteTx.Snapshot()
restoredNonInvite, _ := sip.RestoreNonInviteClientTransaction(snapshot, transport, opts)
```

**Client transaction timers restored:**

- INVITE: TimerA (retransmit), TimerB (timeout), TimerD (wait for retransmits), TimerM (wait for 2xx retransmits)
- Non-INVITE: TimerE (retransmit), TimerF (timeout), TimerK (wait for retransmits)

### 7. Version Check on Restore

```go
var snapshot sip.ServerTransactionSnapshot
json.Unmarshal(data, &snapshot)

// Currently no explicit version field in snapshot
// Future versions may include versioning for compatibility
if snapshot.Request == nil {
    // Handle invalid or incompatible snapshot
}
```

## Example Use Cases

### 1. High Availability Setup

Save transaction state to a shared database (Redis, PostgreSQL):

```go
// Server A
tx.OnStateChanged(func(ctx context.Context, from, to sip.TransactionState) {
    redis.Set(tx.Key().Branch, tx.Snapshot())
})

// Server B (after failover)
snapshot := redis.Get(key)
tx, _ := sip.RestoreInviteServerTransaction(snapshot, transport, opts)
```

### 2. Graceful Restart

Save all active transactions before shutdown:

```go
// Before shutdown
for _, tx := range activeTransactions {
    snapshot := tx.Snapshot()
    db.Save(tx.Key(), snapshot)
}

// After restart
snapshots := db.LoadAll()
for _, snapshot := range snapshots {
    tx, _ := sip.RestoreInviteServerTransaction(snapshot, transport, opts)
    
    // Re-register callbacks
    tx.OnStateChanged(func(ctx context.Context, from, to sip.TransactionState) {
        // Handle state changes
    })
    tx.OnError(func(ctx context.Context, err error) {
        // Handle errors
    })
    // For INVITE transactions
    tx.OnAck(func(ctx context.Context, ack *sip.Request) {
        // Handle 2xx ACK
    })
}
```

### 3. Transaction Migration

Move transactions between servers:

```go
// Server A
snapshot := tx.Snapshot()
sendToServerB(snapshot)

// Server B
tx := sip.RestoreInviteServerTransaction(snapshot, localTransport, opts)
```

## Performance Considerations

- **Snapshot creation** - Creates deep copies of request, response, and send options, but doesn't block the transaction
- **Memory overhead** - Each snapshot allocates new memory for cloned data structures
- **JSON serialization** - Average snapshot is ~1-2KB in JSON format
- **Timer precision** - Restored timers may have slight time drift (typically <100ms)
- **No callbacks** - Callbacks are not serialized, only data state

## Limitations

1. **Transport dependency** - You must provide a valid transport when restoring
2. **No cross-version guarantees** - Snapshots from different library versions may not be compatible
3. **State change callbacks** - Must be re-registered after restoration
4. **Error callbacks (`OnError`)** - Must be re-registered after restoration

## Example Code

See `examples/transaction_persistence/main.go` for a complete working example.

## Testing

The snapshot/restore functionality is integrated into the main transaction tests:

- `transaction_server_invite_test.go` - INVITE server transaction snapshots
- `transaction_server_non_invite_test.go` - Non-INVITE server transaction snapshots
- `transaction_client_invite_test.go` - INVITE client transaction snapshots
- `transaction_client_non_invite_test.go` - Non-INVITE client transaction snapshots

Run tests:

```bash
# Test INVITE server transactions (includes snapshot functionality)
go test -v -run TestInviteServerTransaction

# Test Non-INVITE server transactions (includes snapshot functionality)
go test -v -run TestNonInviteServerTransaction

# Test INVITE client transactions (includes snapshot functionality)
go test -v -run TestInviteClientTransaction

# Test Non-INVITE client transactions (includes snapshot functionality)
go test -v -run TestNonInviteClientTransaction

# Test all transaction functionality
go test -v ./...
```

You can also test the persistence functionality manually by running the example:

```bash
go run examples/transaction_persistence/main.go
```
