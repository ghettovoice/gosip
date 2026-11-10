package sip_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/ghettovoice/gosip/sip"
)

// provenanceLegacyField is the state of a redundant legacy checkpoint field
// in a serialized transaction snapshot.
type provenanceLegacyField int

const (
	provenanceLegacyAbsent provenanceLegacyField = iota
	provenanceLegacyFalse
	provenanceLegacyTrue
)

// provenanceFixtureStates are the checkpoint compatibility states every
// transaction family must restore without losing forwarding protection.
var provenanceFixtureStates = []struct {
	name     string
	metadata bool
	legacy   provenanceLegacyField
}{
	{"legacy only", false, provenanceLegacyTrue},
	{"metadata only", true, provenanceLegacyAbsent},
	{"metadata and legacy true", true, provenanceLegacyTrue},
	{"metadata and legacy false", true, provenanceLegacyFalse},
}

// rewriteSnapLegacyField serializes the captured snapshot, verifies that the
// legacy field keeps its serialized name and mirrors the captured provenance,
// then rewrites the field to the fixture state and decodes the result.
func rewriteSnapLegacyField[S any](
	tb testing.TB,
	snap *S,
	key string,
	wantCaptured bool,
	legacy provenanceLegacyField,
) *S {
	tb.Helper()

	data, err := json.Marshal(snap)
	if err != nil {
		tb.Fatalf("json.Marshal(snapshot) error = %v, want nil", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		tb.Fatalf("json.Unmarshal(snapshot fields) error = %v, want nil", err)
	}

	captured, ok := fields[key]
	switch {
	case wantCaptured && (!ok || string(captured) != "true"):
		tb.Fatalf("captured snapshot field %q = %s (present %v), want true", key, captured, ok)
	case !wantCaptured && ok:
		tb.Fatalf("captured snapshot field %q = %s, want absent", key, captured)
	}

	switch legacy {
	case provenanceLegacyAbsent:
		delete(fields, key)
	case provenanceLegacyFalse:
		fields[key] = json.RawMessage("false")
	case provenanceLegacyTrue:
		fields[key] = json.RawMessage("true")
	}

	if data, err = json.Marshal(fields); err != nil {
		tb.Fatalf("json.Marshal(snapshot fields) error = %v, want nil", err)
	}
	out := new(S)
	if err := json.Unmarshal(data, out); err != nil {
		tb.Fatalf("json.Unmarshal(snapshot) error = %v, want nil", err)
	}
	return out
}

func terminateProvenanceTx(tb testing.TB, tx sip.Transaction) {
	tb.Helper()

	if err := tx.Terminate(tb.Context(), nil); err != nil {
		tb.Fatalf("tx.Terminate(ctx, nil) error = %v, want nil", err)
	}
}

func dropResToTag(res *sip.ResponseEnvelope) {
	res.WithMessage(func(r *sip.Response) {
		if to, ok := r.Headers.To(); ok && to != nil {
			to.Params.Delete("tag")
		}
	})
}

// preparingSrvTransp emulates transport send preparation on top of the stub:
// each response passes through the endpoint To-tag envelope operation, as the
// real transport does before writing to the wire.
type preparingSrvTransp struct {
	*stubSrvTransp
}

func (tp preparingSrvTransp) SendResponse(
	ctx context.Context,
	res *sip.ResponseEnvelope,
	opts ...sip.SendResponseOptions,
) error {
	res.EnsureToTag("prepared-local-tag")
	return tp.stubSrvTransp.SendResponse(ctx, res, opts...)
}

type snapshotClnTx interface {
	sip.ClientTransaction
	Snapshot() (*sip.ClientTransactionSnapshot, error)
}

type snapshotSrvTx interface {
	sip.ServerTransaction
	Snapshot() (*sip.ServerTransactionSnapshot, error)
}

func TestTransaction_ProvenanceCompatibility(t *testing.T) {
	t.Parallel()

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	clientFamilies := []struct {
		name    string
		newReq  func(testing.TB, sip.TransportProto, string, netip.AddrPort, netip.AddrPort) *sip.RequestEnvelope
		newTx   func(*sip.RequestEnvelope, sip.ClientTransport) (snapshotClnTx, error)
		restore func(*sip.ClientTransactionSnapshot, sip.ClientTransport) (sip.ClientTransaction, error)
	}{
		{
			name:   "INVITE client",
			newReq: newOutInviteReq,
			newTx: func(req *sip.RequestEnvelope, tp sip.ClientTransport) (snapshotClnTx, error) {
				return sip.NewInviteClientTransaction(req, tp)
			},
			restore: func(snap *sip.ClientTransactionSnapshot, tp sip.ClientTransport) (sip.ClientTransaction, error) {
				return sip.RestoreInviteClientTransaction(snap, tp)
			},
		},
		{
			name:   "non-INVITE client",
			newReq: newOutNonInviteReq,
			newTx: func(req *sip.RequestEnvelope, tp sip.ClientTransport) (snapshotClnTx, error) {
				return sip.NewNonInviteClientTransaction(req, tp)
			},
			restore: func(snap *sip.ClientTransactionSnapshot, tp sip.ClientTransport) (sip.ClientTransaction, error) {
				return sip.RestoreNonInviteClientTransaction(snap, tp)
			},
		},
	}

	for _, fam := range clientFamilies {
		for _, st := range provenanceFixtureStates {
			t.Run(fam.name+"/"+st.name, func(t *testing.T) {
				t.Parallel()

				synctest.Test(t, func(t *testing.T) {
					const branch = sip.MagicCookie + ".provenance-client"

					req := fam.newReq(t, "UDP", branch, local, remote)
					if st.metadata {
						req.MarkForwarded()
						req.SetForwardedViaBranch(branch)
					}
					tp := newStubClnTransp(false)
					tx, err := fam.newTx(req, tp)
					if err != nil {
						t.Fatalf("new client transaction error = %v, want nil", err)
					}
					startClnTx(t, tx)
					tp.waitSendReq(t)

					snap := rewriteSnapLegacyField(t, mustClnTxSnapshot(t, tx), "forwarded_request", st.metadata, st.legacy)
					terminateProvenanceTx(t, tx)

					restored, err := fam.restore(snap, newStubClnTransp(false))
					if err != nil {
						t.Fatalf("restore client transaction error = %v, want nil", err)
					}

					got := restored.Request()
					if !got.Forwarded() {
						t.Errorf("restored.Request().Forwarded() = false, want true")
					}
					if gotBranch := got.ForwardedViaBranch(); gotBranch != branch {
						t.Errorf("restored.Request().ForwardedViaBranch() = %q, want %q", gotBranch, branch)
					}
				})
			})
		}
	}

	serverFamilies := []struct {
		name    string
		newReq  func(testing.TB, sip.TransportProto, string, netip.AddrPort, netip.AddrPort) *sip.RequestEnvelope
		newTx   func(*sip.RequestEnvelope, sip.ServerTransport) (snapshotSrvTx, error)
		restore func(*sip.ServerTransactionSnapshot, sip.ServerTransport) (sip.ServerTransaction, error)
	}{
		{
			name:   "INVITE server",
			newReq: newInInviteReq,
			newTx: func(req *sip.RequestEnvelope, tp sip.ServerTransport) (snapshotSrvTx, error) {
				return sip.NewInviteServerTransaction(req, tp)
			},
			restore: func(snap *sip.ServerTransactionSnapshot, tp sip.ServerTransport) (sip.ServerTransaction, error) {
				return sip.RestoreInviteServerTransaction(snap, tp)
			},
		},
		{
			name:   "non-INVITE server",
			newReq: newInNonInviteReq,
			newTx: func(req *sip.RequestEnvelope, tp sip.ServerTransport) (snapshotSrvTx, error) {
				return sip.NewNonInviteServerTransaction(req, tp)
			},
			restore: func(snap *sip.ServerTransactionSnapshot, tp sip.ServerTransport) (sip.ServerTransaction, error) {
				return sip.RestoreNonInviteServerTransaction(snap, tp)
			},
		},
	}

	for _, fam := range serverFamilies {
		for _, st := range provenanceFixtureStates {
			t.Run(fam.name+"/"+st.name, func(t *testing.T) {
				t.Parallel()

				synctest.Test(t, func(t *testing.T) {
					ctx := t.Context()
					req := fam.newReq(t, "UDP", sip.MagicCookie+".provenance-server", local, remote)
					tx, err := fam.newTx(req, newStubSrvTransp(false))
					if err != nil {
						t.Fatalf("new server transaction error = %v, want nil", err)
					}
					startSrvTx(t, tx)

					res := newInRes(t, req, sip.ResponseStatusBusyHere)
					dropResToTag(res)
					if st.metadata {
						res.SetPreserveToTag(true)
					}
					if err := tx.SendResponse(ctx, res); err != nil {
						t.Fatalf("tx.SendResponse(ctx, 486) error = %v, want nil", err)
					}

					snap := rewriteSnapLegacyField(
						t, mustSrvTxSnapshot(t, tx), "preserve_response_to_tag", st.metadata, st.legacy,
					)
					terminateProvenanceTx(t, tx)

					restoredTP := preparingSrvTransp{newStubSrvTransp(false)}
					restored, err := fam.restore(snap, restoredTP)
					if err != nil {
						t.Fatalf("restore server transaction error = %v, want nil", err)
					}
					if !restored.LastResponse().PreservesToTag() {
						t.Errorf("restored.LastResponse().PreservesToTag() = false, want true")
					}

					// A retransmitted request makes the restored transaction
					// resend its cached response through transport preparation.
					startSrvTx(t, restored)
					if err := restored.RecvRequest(ctx, req.Clone().(*sip.RequestEnvelope)); err != nil {
						t.Fatalf("restored.RecvRequest(ctx, retransmit) error = %v, want nil", err)
					}
					sent := restoredTP.waitSendRes(t)
					var tag string
					sent.res.WithMessage(func(r *sip.Response) { tag = resToTag(t, r) })
					if tag != "" {
						t.Errorf("resent response To tag = %q, want absent", tag)
					}
					terminateProvenanceTx(t, restored)
				})
			})
		}
	}
}

func TestNewCancelRequestEnvelope_Provenance(t *testing.T) {
	t.Parallel()

	const branch = sip.MagicCookie + ".provenance-cancel"

	tests := []struct {
		name       string
		forwarded  bool
		wantBranch string
	}{
		{"forwarded INVITE", true, branch},
		{"endpoint INVITE", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := newOutInviteReq(
				t, "UDP", branch, netip.MustParseAddrPort("11.11.11.11:5070"), netip.MustParseAddrPort("55.55.55.55:5060"),
			)
			if tt.forwarded {
				req.MarkForwarded()
				req.SetForwardedViaBranch(branch)
			}
			// The transaction binds its key to the request metadata; the
			// CANCEL must not inherit that stale INVITE binding.
			tx, err := sip.NewInviteClientTransaction(req, newStubClnTransp(false))
			if err != nil {
				t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
			}
			inv := tx.Request()
			if !inv.Metadata().Has("sip.transaction_key") {
				t.Fatal("INVITE metadata lacks transaction binding, want bound fixture")
			}

			cnc, err := sip.NewCancelRequestEnvelope(inv)
			if err != nil {
				t.Fatalf("sip.NewCancelRequestEnvelope(INVITE) error = %v, want nil", err)
			}

			if got := cnc.Forwarded(); got != tt.forwarded {
				t.Errorf("cnc.Forwarded() = %v, want %v", got, tt.forwarded)
			}
			if got := cnc.ForwardedViaBranch(); got != tt.wantBranch {
				t.Errorf("cnc.ForwardedViaBranch() = %q, want %q", got, tt.wantBranch)
			}
			if got := cnc.Metadata().Has("sip.forwarded_via_branch"); got != tt.forwarded {
				t.Errorf("cnc metadata has designation = %v, want %v", got, tt.forwarded)
			}
			for _, key := range []string{"sip.transaction_key", "sip.transaction_type"} {
				if cnc.Metadata().Has(key) {
					t.Errorf("cnc metadata has %q, want absent", key)
				}
			}

			var wire strings.Builder
			if _, err := cnc.RenderTo(&wire); err != nil {
				t.Fatalf("cnc.RenderTo() error = %v, want nil", err)
			}
			for _, key := range []string{"sip.forwarded", "forwarded_via_branch"} {
				if strings.Contains(wire.String(), key) {
					t.Errorf("rendered CANCEL contains metadata key %q:\n%s", key, wire.String())
				}
			}
		})
	}
}

// resToTag returns the To header tag parameter of a parsed wire response.
func resToTag(tb testing.TB, res *sip.Response) string {
	tb.Helper()

	to, ok := res.Headers.To()
	if !ok || to == nil {
		tb.Fatal("wire response has no To header")
	}
	tag, _ := to.Tag()
	return tag
}
