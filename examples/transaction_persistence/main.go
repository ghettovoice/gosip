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
// 1. Create a transaction
// 2. Take a snapshot of its state
// 3. Serialize the snapshot to JSON
// 4. Restore the transaction from the snapshot after a "restart"

func main() {
	// Create a sample INVITE request
	req := createSampleRequest()

	// Create a mock transport (in real app, use actual transport)
	tp := createMockTransport()

	// Create the transaction
	tx, err := sip.NewInviteServerTransaction(req, tp, &sip.ServerTransactionOptions{
		Log: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})),
	})
	if err != nil {
		log.Fatalf("Failed to create transaction: %v", err)
	}

	// Subscribe to state changes to save snapshots
	tx.OnStateChanged(func(ctx context.Context, from, to sip.TransactionState) {
		fmt.Printf("Transaction state changed: %s -> %s\n", from, to)

		// Take a snapshot on each state change
		snapshot := tx.Snapshot()

		// In a real application, you would save this to your persistent store
		// (database, Redis, file system, etc.)
		if err := saveSnapshot(snapshot); err != nil {
			log.Printf("Failed to save snapshot: %v", err)
		}
	})

	// Send a provisional response
	ctx := context.Background()
	if err := tx.Respond(ctx, 180, nil); err != nil {
		log.Fatalf("Failed to send provisional response: %v", err)
	}

	// Take a snapshot manually for this example
	snapshot := tx.Snapshot()
	if err := saveSnapshot(snapshot); err != nil {
		log.Fatalf("Failed to save snapshot: %v", err)
	}

	// Simulate server restart by loading the snapshot
	fmt.Println("\n=== Simulating server restart ===")

	// Load the snapshot (in real app, load from your persistent store)
	loadedSnapshot, err := loadSnapshot()
	if err != nil {
		log.Fatalf("Failed to load snapshot: %v", err)
	}

	// Restore the transaction
	restoredTx, err := sip.RestoreInviteServerTransaction(loadedSnapshot, tp, &sip.ServerTransactionOptions{
		Log: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})),
	})
	if err != nil {
		log.Fatalf("Failed to restore transaction: %v", err)
	}

	// The restored transaction can continue working
	fmt.Printf("Restored transaction in state: %s\n", restoredTx.State())
	fmt.Printf("Transaction key: %v\n", restoredTx.Key())

	// Send final response from restored transaction
	if err := restoredTx.Respond(ctx, 200, nil); err != nil {
		log.Fatalf("Failed to send final response: %v", err)
	}

	fmt.Printf("Final transaction state: %s\n", restoredTx.State())
}

func createSampleRequest() *sip.InboundRequest {
	return sip.NewInboundRequest(
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
						Params:    make(header.Values).Set("branch", sip.MagicCookie+".example123"),
					},
				}).
				Set(&header.From{
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("127.0.0.1")},
					Params: make(header.Values).Set("tag", "fromtag123"),
				}).
				Set(&header.To{
					URI: &uri.SIP{User: uri.User("bob"), Addr: uri.Host("example.com")},
				}).
				Set(header.CallID("callid123@127.0.0.1")).
				Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite}).
				Set(header.MaxForwards(70)),
		},
		netip.MustParseAddrPort("127.0.0.1:5060"),
		netip.MustParseAddrPort("127.0.0.1:5070"),
	)
}

func createMockTransport() sip.ServerTransport {
	// In a real application, create your actual transport here
	// For this example, we'll use a mock transport
	return &mockTransport{}
}

// mockTransport implements the ServerTransport interface for testing
type mockTransport struct{}

func (m *mockTransport) Proto() sip.TransportProto { return "UDP" }

func (m *mockTransport) Network() string { return "udp" }

func (m *mockTransport) LocalAddr() netip.AddrPort { return netip.MustParseAddrPort("127.0.0.1:5060") }

func (m *mockTransport) Reliable() bool { return false }

func (m *mockTransport) Secured() bool { return false }

func (m *mockTransport) Streamed() bool { return false }

func (m *mockTransport) DefaultPort() uint16 { return 5060 }

func (m *mockTransport) SendResponse(ctx context.Context, res *sip.OutboundResponse, opts *sip.SendResponseOptions) error {
	// Mock implementation - just log the response
	fmt.Printf("Mock sending response: %d\n", res.Status())
	return nil
}

func (m *mockTransport) OnRequest(fn sip.RequestHandler) (cancel func()) {
	return func() {}
}

func (m *mockTransport) Close() error { return nil }

// saveSnapshot saves the snapshot to persistent storage
// In a real application, this would write to a database, Redis, etc.
func saveSnapshot(snapshot *sip.ServerTransactionSnapshot) error {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	// For this example, save to a file
	if err := os.WriteFile("/tmp/transaction_snapshot.json", data, 0644); err != nil {
		return fmt.Errorf("write snapshot file: %w", err)
	}

	fmt.Printf("Snapshot saved (%d bytes)\n", len(data))
	return nil
}

// loadSnapshot loads the snapshot from persistent storage
// In a real application, this would read from a database, Redis, etc.
func loadSnapshot() (*sip.ServerTransactionSnapshot, error) {
	data, err := os.ReadFile("/tmp/transaction_snapshot.json")
	if err != nil {
		return nil, fmt.Errorf("read snapshot file: %w", err)
	}

	var snapshot sip.ServerTransactionSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}

	fmt.Printf("Snapshot loaded (%d bytes)\n", len(data))
	return &snapshot, nil
}
