package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/netip"
	"os"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/uri"
)

// This example demonstrates how to use transaction snapshots for persistence.
// It shows how to:
// 1. Create server and client transactions
// 2. Take a snapshot of their state
// 3. Serialize the snapshot to JSON
// 4. Restore the transactions from snapshots after a "restart"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Demo 1: Server transaction persistence
	fmt.Println("=== Server Transaction Persistence Demo ===")
	demoServerTransactionPersistence(logger)

	fmt.Println()

	// Demo 2: Client transaction persistence
	fmt.Println("=== Client Transaction Persistence Demo ===")
	demoClientTransactionPersistence(logger)
}

func demoServerTransactionPersistence(logger *slog.Logger) {
	// Create a sample INVITE request
	req := createSampleInboundRequest()

	ctx := context.Background()

	// Create a mock transport (in real app, use actual transport)
	tp := createMockServerTransport()

	// Create the transaction
	tx, err := sip.NewInviteServerTransaction(ctx, req, tp, &sip.ServerTransactionOptions{
		Logger: logger,
	})
	if err != nil {
		log.Fatalf("Failed to create server transaction: %v", err) //nolint:revive
	}

	// Subscribe to state changes to save snapshots
	var snapshotFile *os.File
	tx.OnStateChanged(func(ctx context.Context, from, to sip.TransactionState) {
		fmt.Printf("Server transaction state changed: %s -> %s\n", from, to)

		// Take a snapshot on each state change
		snapshot := tx.Snapshot()

		// In a real application, you would save this to your persistent store
		// (database, Redis, file system, etc.)
		var err error
		if snapshotFile, err = saveServerSnapshot(snapshot); err != nil {
			log.Printf("Failed to save server snapshot: %v", err)
		}
	})

	// Send a provisional response
	if err := tx.Respond(ctx, 180, nil); err != nil {
		log.Fatalf("Failed to send provisional response: %v", err) //nolint:revive
	}

	// Take a snapshot manually for this example
	snapshot := tx.Snapshot()
	if snapshotFile, err = saveServerSnapshot(snapshot); err != nil {
		log.Fatalf("Failed to save server snapshot: %v", err) //nolint:revive
	}

	// Simulate server restart by loading the snapshot
	fmt.Println("\n--- Simulating server restart ---")

	// Load the snapshot (in real app, load from your persistent store)
	loadedSnapshot, err := loadServerSnapshot(snapshotFile)
	if err != nil {
		log.Fatalf("Failed to load server snapshot: %v", err) //nolint:revive
	}

	// Restore the transaction
	restoredTx, err := sip.RestoreInviteServerTransaction(ctx, loadedSnapshot, tp, &sip.ServerTransactionOptions{
		Logger: logger,
	})
	if err != nil {
		log.Fatalf("Failed to restore server transaction: %v", err) //nolint:revive
	}

	// The restored transaction can continue working
	fmt.Printf("Restored server transaction in state: %s\n", restoredTx.State())
	fmt.Printf("Server transaction key: %v\n", restoredTx.Key())

	// Send final response from restored transaction
	if err := restoredTx.Respond(ctx, 200, nil); err != nil {
		log.Fatalf("Failed to send final response: %v", err) //nolint:revive
	}

	fmt.Printf("Final server transaction state: %s\n", restoredTx.State())
}

func demoClientTransactionPersistence(logger *slog.Logger) {
	// Create a sample outbound INVITE request
	req := createSampleOutboundRequest()

	ctx := context.Background()

	// Create a mock client transport
	tp := createMockClientTransport()

	// Create the INVITE client transaction
	tx, err := sip.NewInviteClientTransaction(ctx, req, tp, &sip.ClientTransactionOptions{
		Logger: logger,
	})
	if err != nil {
		log.Fatalf("Failed to create client transaction: %v", err) //nolint:revive
	}

	// Subscribe to state changes to save snapshots
	var snapshotFile *os.File
	tx.OnStateChanged(func(ctx context.Context, from, to sip.TransactionState) {
		fmt.Printf("Client transaction state changed: %s -> %s\n", from, to)

		// Take a snapshot on each state change
		snapshot := tx.Snapshot()
		var err error
		if snapshotFile, err = saveClientSnapshot(snapshot); err != nil {
			log.Printf("Failed to save client snapshot: %v", err)
		}
	})

	// Take a snapshot in Calling state
	snapshot := tx.Snapshot()
	if snapshotFile, err = saveClientSnapshot(snapshot); err != nil {
		log.Fatalf("Failed to save client snapshot: %v", err) //nolint:revive
	}
	fmt.Printf("Saved client transaction snapshot in state: %s\n", tx.State())

	// Simulate receiving a provisional response to move to Proceeding
	provisionalRes := createMockResponse(req, 180)
	if err := tx.RecvResponse(ctx, provisionalRes); err != nil {
		log.Fatalf("Failed to receive provisional response: %v", err) //nolint:revive
	}

	// Take another snapshot in Proceeding state
	snapshot = tx.Snapshot()
	if snapshotFile, err = saveClientSnapshot(snapshot); err != nil {
		log.Fatalf("Failed to save client snapshot: %v", err) //nolint:revive
	}

	// Simulate restart by loading the snapshot
	fmt.Println("\n--- Simulating client restart ---")

	loadedSnapshot, err := loadClientSnapshot(snapshotFile)
	if err != nil {
		log.Fatalf("Failed to load client snapshot: %v", err) //nolint:revive
	}

	// Restore the transaction
	restoredTx, err := sip.RestoreInviteClientTransaction(ctx, loadedSnapshot, tp, &sip.ClientTransactionOptions{
		Logger: logger,
	})
	if err != nil {
		log.Fatalf("Failed to restore client transaction: %v", err) //nolint:revive
	}

	fmt.Printf("Restored client transaction in state: %s\n", restoredTx.State())
	fmt.Printf("Client transaction key: %v\n", restoredTx.Key())

	// Re-register response callback (callbacks are not persisted)
	restoredTx.OnResponse(func(ctx context.Context, res *sip.InboundResponseEnvelope) {
		fmt.Printf("Restored client transaction received response: %d\n", res.Status())
	})

	// Simulate receiving final response
	finalRes := createMockResponse(req, 200)
	if err := restoredTx.RecvResponse(ctx, finalRes); err != nil {
		log.Fatalf("Failed to receive final response: %v", err) //nolint:revive
	}

	fmt.Printf("Final client transaction state: %s\n", restoredTx.State())
}

func createSampleInboundRequest() *sip.InboundRequestEnvelope {
	req, err := sip.NewInboundRequestEnvelope(
		&sip.Request{
			Proto:  sip.ProtoVer20(),
			Method: sip.RequestMethodInvite,
			URI: &uri.SIP{
				User: uri.User("bob"),
				Addr: uri.HostPort("example.com", 5060),
			},
			Headers: make(sip.Headers).
				Set(header.Via{
					{
						Proto:     sip.ProtoVer20(),
						Transport: "UDP",
						Addr:      header.HostPort("127.0.0.1", 5070),
						Params:    make(header.Values).Set("branch", sip.MagicCookie+".server-example"),
					},
				}).
				Set(&header.From{
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("127.0.0.1")},
					Params: make(header.Values).Set("tag", "fromtag123"),
				}).
				Set(&header.To{
					URI: &uri.SIP{User: uri.User("bob"), Addr: uri.Host("example.com")},
				}).
				Set(header.CallID("callid-server@127.0.0.1")).
				Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite}).
				Set(header.MaxForwards(70)),
		},
		"UDP",
		netip.MustParseAddrPort("127.0.0.1:5060"),
		netip.MustParseAddrPort("127.0.0.1:5070"),
	)
	if err != nil {
		log.Fatalf("Failed to create request: %v", err) //nolint:revive
	}
	return req
}

func createSampleOutboundRequest() *sip.OutboundRequestEnvelope {
	req, err := sip.NewOutboundRequestEnvelope(
		&sip.Request{
			Proto:  sip.ProtoVer20(),
			Method: sip.RequestMethodInvite,
			URI: &uri.SIP{
				User: uri.User("bob"),
				Addr: uri.HostPort("example.com", 5060),
			},
			Headers: make(sip.Headers).
				Set(header.Via{
					{
						Proto:     sip.ProtoVer20(),
						Transport: "UDP",
						Addr:      header.HostPort("127.0.0.1", 5060),
						Params:    make(header.Values).Set("branch", sip.MagicCookie+".client-example"),
					},
				}).
				Set(&header.From{
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("127.0.0.1")},
					Params: make(header.Values).Set("tag", "fromtag456"),
				}).
				Set(&header.To{
					URI: &uri.SIP{User: uri.User("bob"), Addr: uri.Host("example.com")},
				}).
				Set(header.CallID("callid-client@127.0.0.1")).
				Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite}).
				Set(header.MaxForwards(70)),
		},
	)
	if err != nil {
		log.Fatalf("Failed to create request: %v", err) //nolint:revive
	}
	req.SetLocalAddr(netip.MustParseAddrPort("127.0.0.1:5060"))
	req.SetRemoteAddr(netip.MustParseAddrPort("192.168.1.100:5060"))
	return req
}

func createMockResponse(req *sip.OutboundRequestEnvelope, status sip.ResponseStatus) *sip.InboundResponseEnvelope {
	msg, err := req.Message().NewResponse(status, nil)
	if err != nil {
		log.Fatalf("Failed to create response: %v", err) //nolint:revive
	}
	res, err := sip.NewInboundResponseEnvelope(msg, req.Transport(), req.RemoteAddr(), req.LocalAddr())
	if err != nil {
		log.Fatalf("Failed to create response: %v", err) //nolint:revive
	}
	return res
}

func createMockServerTransport() sip.ServerTransport {
	return &mockServerTransport{}
}

func createMockClientTransport() sip.ClientTransport {
	return &mockClientTransport{}
}

// mockServerTransport implements the ServerTransport interface for testing.
type mockServerTransport struct{}

func (*mockServerTransport) Reliable() bool { return false }

func (*mockServerTransport) SendResponse(ctx context.Context, res *sip.OutboundResponseEnvelope, opts *sip.SendResponseOptions) error {
	fmt.Printf("Mock sending response: %d\n", res.Status())
	return nil
}

// mockClientTransport implements the ClientTransport interface for testing.
type mockClientTransport struct{}

func (*mockClientTransport) Reliable() bool { return false }

func (*mockClientTransport) SendRequest(ctx context.Context, req *sip.OutboundRequestEnvelope, opts *sip.SendRequestOptions) error {
	fmt.Printf("Mock sending request: %s\n", req.Method())
	return nil
}

func (*mockClientTransport) Close() error { return nil }

// saveServerSnapshot saves a server transaction snapshot to persistent storage.
func saveServerSnapshot(snapshot *sip.ServerTransactionSnapshot) (*os.File, error) {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %w", err)
	}

	file, err := os.CreateTemp("", "server_transaction_snapshot_*.json")
	if err != nil {
		return nil, fmt.Errorf("write snapshot file: %w", err)
	}

	fmt.Printf("Server snapshot saved (%d bytes)\n", len(data))
	return file, nil
}

// loadServerSnapshot loads a server transaction snapshot from persistent storage.
func loadServerSnapshot(file *os.File) (*sip.ServerTransactionSnapshot, error) {
	data, err := os.ReadFile(file.Name())
	if err != nil {
		return nil, fmt.Errorf("read snapshot file: %w", err)
	}

	var snapshot sip.ServerTransactionSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}

	fmt.Printf("Server snapshot loaded (%d bytes)\n", len(data))
	return &snapshot, nil
}

// saveClientSnapshot saves a client transaction snapshot to persistent storage.
func saveClientSnapshot(snapshot *sip.ClientTransactionSnapshot) (*os.File, error) {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %w", err)
	}

	file, err := os.CreateTemp("", "client_transaction_snapshot_*.json")
	if err != nil {
		return nil, fmt.Errorf("write snapshot file: %w", err)
	}

	fmt.Printf("Client snapshot saved (%d bytes)\n", len(data))
	return file, nil
}

// loadClientSnapshot loads a client transaction snapshot from persistent storage.
func loadClientSnapshot(file *os.File) (*sip.ClientTransactionSnapshot, error) {
	data, err := os.ReadFile(file.Name())
	if err != nil {
		return nil, fmt.Errorf("read snapshot file: %w", err)
	}

	var snapshot sip.ClientTransactionSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}

	fmt.Printf("Client snapshot loaded (%d bytes)\n", len(data))
	return &snapshot, nil
}
