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

	// Create a mock transport (in real app, use actual transport)
	tp := createMockServerTransport()

	// Create the transaction
	tx, err := sip.NewInviteServerTransaction(req, tp, &sip.ServerTransactionOptions{
		Log: logger,
	})
	if err != nil {
		log.Fatalf("Failed to create server transaction: %v", err)
	}

	// Subscribe to state changes to save snapshots
	tx.OnStateChanged(func(ctx context.Context, _ sip.Transaction, from, to sip.TransactionState) {
		fmt.Printf("Server transaction state changed: %s -> %s\n", from, to)

		// Take a snapshot on each state change
		snapshot := tx.Snapshot()

		// In a real application, you would save this to your persistent store
		// (database, Redis, file system, etc.)
		if err := saveServerSnapshot(snapshot); err != nil {
			log.Printf("Failed to save server snapshot: %v", err)
		}
	})

	// Send a provisional response
	ctx := context.Background()
	if err := tx.Respond(ctx, 180, nil); err != nil {
		log.Fatalf("Failed to send provisional response: %v", err)
	}

	// Take a snapshot manually for this example
	snapshot := tx.Snapshot()
	if err := saveServerSnapshot(snapshot); err != nil {
		log.Fatalf("Failed to save server snapshot: %v", err)
	}

	// Simulate server restart by loading the snapshot
	fmt.Println("\n--- Simulating server restart ---")

	// Load the snapshot (in real app, load from your persistent store)
	loadedSnapshot, err := loadServerSnapshot()
	if err != nil {
		log.Fatalf("Failed to load server snapshot: %v", err)
	}

	// Restore the transaction
	restoredTx, err := sip.RestoreInviteServerTransaction(loadedSnapshot, tp, &sip.ServerTransactionOptions{
		Log: logger,
	})
	if err != nil {
		log.Fatalf("Failed to restore server transaction: %v", err)
	}

	// The restored transaction can continue working
	fmt.Printf("Restored server transaction in state: %s\n", restoredTx.State())
	fmt.Printf("Server transaction key: %v\n", restoredTx.Key())

	// Send final response from restored transaction
	if err := restoredTx.Respond(ctx, 200, nil); err != nil {
		log.Fatalf("Failed to send final response: %v", err)
	}

	fmt.Printf("Final server transaction state: %s\n", restoredTx.State())
}

func demoClientTransactionPersistence(logger *slog.Logger) {
	// Create a sample outbound INVITE request
	req := createSampleOutboundRequest()

	// Create a mock client transport
	tp := createMockClientTransport()

	// Create the INVITE client transaction
	tx, err := sip.NewInviteClientTransaction(req, tp, &sip.ClientTransactionOptions{
		Log: logger,
	})
	if err != nil {
		log.Fatalf("Failed to create client transaction: %v", err)
	}

	// Subscribe to state changes to save snapshots
	tx.OnStateChanged(func(ctx context.Context, _ sip.Transaction, from, to sip.TransactionState) {
		fmt.Printf("Client transaction state changed: %s -> %s\n", from, to)

		// Take a snapshot on each state change
		snapshot := tx.Snapshot()

		if err := saveClientSnapshot(snapshot); err != nil {
			log.Printf("Failed to save client snapshot: %v", err)
		}
	})

	// Take a snapshot in Calling state
	snapshot := tx.Snapshot()
	if err := saveClientSnapshot(snapshot); err != nil {
		log.Fatalf("Failed to save client snapshot: %v", err)
	}
	fmt.Printf("Saved client transaction snapshot in state: %s\n", tx.State())

	// Simulate receiving a provisional response to move to Proceeding
	ctx := context.Background()
	provisionalRes := createMockResponse(req, 180)
	if err := tx.RecvResponse(ctx, provisionalRes); err != nil {
		log.Fatalf("Failed to receive provisional response: %v", err)
	}

	// Take another snapshot in Proceeding state
	snapshot = tx.Snapshot()
	if err := saveClientSnapshot(snapshot); err != nil {
		log.Fatalf("Failed to save client snapshot: %v", err)
	}

	// Simulate restart by loading the snapshot
	fmt.Println("\n--- Simulating client restart ---")

	loadedSnapshot, err := loadClientSnapshot()
	if err != nil {
		log.Fatalf("Failed to load client snapshot: %v", err)
	}

	// Restore the transaction
	restoredTx, err := sip.RestoreInviteClientTransaction(loadedSnapshot, tp, &sip.ClientTransactionOptions{
		Log: logger,
	})
	if err != nil {
		log.Fatalf("Failed to restore client transaction: %v", err)
	}

	fmt.Printf("Restored client transaction in state: %s\n", restoredTx.State())
	fmt.Printf("Client transaction key: %v\n", restoredTx.Key())

	// Re-register response callback (callbacks are not persisted)
	restoredTx.OnResponse(func(ctx context.Context, _ sip.ClientTransaction, res *sip.InboundResponse) {
		fmt.Printf("Restored client transaction received response: %d\n", res.Status())
	})

	// Simulate receiving final response
	finalRes := createMockResponse(req, 200)
	if err := restoredTx.RecvResponse(ctx, finalRes); err != nil {
		log.Fatalf("Failed to receive final response: %v", err)
	}

	fmt.Printf("Final client transaction state: %s\n", restoredTx.State())
}

func createSampleInboundRequest() *sip.InboundRequest {
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
		netip.MustParseAddrPort("127.0.0.1:5060"),
		netip.MustParseAddrPort("127.0.0.1:5070"),
	)
}

func createSampleOutboundRequest() *sip.OutboundRequest {
	req := sip.NewOutboundRequest(
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
	req.SetLocalAddr(netip.MustParseAddrPort("127.0.0.1:5060"))
	req.SetRemoteAddr(netip.MustParseAddrPort("192.168.1.100:5060"))
	return req
}

func createMockResponse(req *sip.OutboundRequest, status sip.ResponseStatus) *sip.InboundResponse {
	msg, err := req.Message().NewResponse(status, nil)
	if err != nil {
		log.Fatalf("Failed to create response: %v", err)
	}
	return sip.NewInboundResponse(msg, req.RemoteAddr(), req.LocalAddr())
}

func createMockServerTransport() sip.ServerTransport {
	return &mockServerTransport{}
}

func createMockClientTransport() sip.ClientTransport {
	return &mockClientTransport{}
}

// mockServerTransport implements the ServerTransport interface for testing
type mockServerTransport struct{}

func (m *mockServerTransport) Proto() sip.TransportProto { return "UDP" }

func (m *mockServerTransport) Network() string { return "udp" }

func (m *mockServerTransport) LocalAddr() netip.AddrPort {
	return netip.MustParseAddrPort("127.0.0.1:5060")
}

func (m *mockServerTransport) Reliable() bool { return false }

func (m *mockServerTransport) Secured() bool { return false }

func (m *mockServerTransport) Streamed() bool { return false }

func (m *mockServerTransport) DefaultPort() uint16 { return 5060 }

func (m *mockServerTransport) SendResponse(ctx context.Context, res *sip.OutboundResponse, opts *sip.SendResponseOptions) error {
	fmt.Printf("Mock sending response: %d\n", res.Status())
	return nil
}

func (m *mockServerTransport) OnRequest(fn sip.TransportRequestHandler) (cancel func()) {
	return func() {}
}

func (m *mockServerTransport) Close() error { return nil }

// mockClientTransport implements the ClientTransport interface for testing
type mockClientTransport struct{}

func (m *mockClientTransport) Proto() sip.TransportProto { return "UDP" }

func (m *mockClientTransport) Network() string { return "udp" }

func (m *mockClientTransport) LocalAddr() netip.AddrPort {
	return netip.MustParseAddrPort("127.0.0.1:5060")
}

func (m *mockClientTransport) Reliable() bool { return false }

func (m *mockClientTransport) Secured() bool { return false }

func (m *mockClientTransport) Streamed() bool { return false }

func (m *mockClientTransport) DefaultPort() uint16 { return 5060 }

func (m *mockClientTransport) SendRequest(ctx context.Context, req *sip.OutboundRequest, opts *sip.SendRequestOptions) error {
	fmt.Printf("Mock sending request: %s\n", req.Method())
	return nil
}

func (m *mockClientTransport) OnResponse(fn sip.TransportResponseHandler) (cancel func()) {
	return func() {}
}

func (m *mockClientTransport) Close() error { return nil }

// saveServerSnapshot saves a server transaction snapshot to persistent storage
func saveServerSnapshot(snapshot *sip.ServerTransactionSnapshot) error {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	if err := os.WriteFile("/tmp/server_transaction_snapshot.json", data, 0644); err != nil {
		return fmt.Errorf("write snapshot file: %w", err)
	}

	fmt.Printf("Server snapshot saved (%d bytes)\n", len(data))
	return nil
}

// loadServerSnapshot loads a server transaction snapshot from persistent storage
func loadServerSnapshot() (*sip.ServerTransactionSnapshot, error) {
	data, err := os.ReadFile("/tmp/server_transaction_snapshot.json")
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

// saveClientSnapshot saves a client transaction snapshot to persistent storage
func saveClientSnapshot(snapshot *sip.ClientTransactionSnapshot) error {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	if err := os.WriteFile("/tmp/client_transaction_snapshot.json", data, 0644); err != nil {
		return fmt.Errorf("write snapshot file: %w", err)
	}

	fmt.Printf("Client snapshot saved (%d bytes)\n", len(data))
	return nil
}

// loadClientSnapshot loads a client transaction snapshot from persistent storage
func loadClientSnapshot() (*sip.ClientTransactionSnapshot, error) {
	data, err := os.ReadFile("/tmp/client_transaction_snapshot.json")
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
