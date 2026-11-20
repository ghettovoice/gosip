package useragent_test

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.uber.org/goleak"

	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/useragent"
)

func TestUserAgent_OperationIntegration(t *testing.T) {
	t.Parallel()

	t.Run("message chain follows a redirect into a digest challenge", func(t *testing.T) {
		t.Parallel()

		// C is the digest-protected receiver: the anonymous first send
		// draws the 401 challenge and the credentialed retry draws the
		// 200.
		rcv := newDigestReceiver(t)
		cTarget := uriFor(rcv.addr)

		// B answers every inbound MESSAGE with a 302 redirect to C and
		// lets the automatic answer cover OPTIONS.
		var bRecv atomic.Int32
		wB := newWireElem(t)
		bTarget := uriFor(wB.addr)
		bob, err := sip.ParseURI("sip:bob@ua.test")
		if err != nil {
			t.Fatalf("sip.ParseURI() error = %v, want nil", err)
		}
		uaB, err := useragent.New(useragent.Options{
			AOR: bob,
			OnMessage: func(ctx context.Context, in *useragent.IncomingMessage) error {
				bRecv.Add(1)
				return in.Respond(ctx, sip.ResponseStatusMovedTemporarily, sip.ResponseOptions{
					Headers: sip.Headers{}.Set(header.Contact{{URI: cTarget}}),
				})
			},
		})
		if err != nil {
			t.Fatalf("useragent.New() error = %v, want nil", err)
		}
		if unbind := wB.elm.UseComponent(uaB); unbind == nil {
			t.Fatal("wB.elm.UseComponent() unbind = nil, want non-nil")
		}

		// A follows the approved redirect and answers C's challenge
		// through the digest client. The gate's detached request still
		// carries the previous hop's URI, so each call logs the hop as
		// "previous target -> approved target".
		var hops []string
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout: 10 * time.Second,
			AllowTarget: func(
				_ context.Context,
				req *sip.RequestEnvelope,
				target *sip.URI,
			) (bool, error) {
				hops = append(hops, shared.Render(req.URI())+" -> "+shared.Render(target))
				return true, nil
			},
		}, newDigestClient(t, "secret"))

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		msgOp, err := fx.ua.Message(ctx, bTarget, useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if msgOp == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}
		optOp, err := fx.ua.Options(ctx, bTarget, useragent.OptionsOperationOptions{
			Timing: noRetransmitTiming,
		})
		if err != nil {
			t.Fatalf("ua.Options() error = %v, want nil", err)
		}
		if optOp == nil {
			t.Fatal("ua.Options() op = nil, want non-nil")
		}

		// The chain drives itself through the live elements: B's 302,
		// the approved hop to C, C's 401 challenge, the credentialed
		// retry, and the resolving 200.
		res, err := msgOp.Wait(t.Context())
		if err != nil {
			t.Fatalf("msgOp.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("msgOp.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}

		// The Options op started in parallel resolved against B's
		// automatic capabilities answer.
		ores, err := optOp.Wait(t.Context())
		if err != nil {
			t.Fatalf("optOp.Wait() error = %v, want nil", err)
		}
		if got := ores.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("optOp.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}
		wantAllow := header.Allow{sip.RequestMethodMessage, sip.RequestMethodOptions}
		if diff := cmp.Diff(wantAllow, collectAllow(ores)); diff != "" {
			t.Fatalf("OPTIONS Allow header mismatch (-want +got):\n%s", diff)
		}

		// The op log shows the hop order: exactly one gated hop, from
		// the original B target to C.
		wantHops := []string{shared.Render(bTarget) + " -> " + shared.Render(cTarget)}
		if diff := cmp.Diff(wantHops, hops); diff != "" {
			t.Fatalf("redirect hop order mismatch (-want +got):\n%s", diff)
		}

		// B saw the initial send only — the chain never revisits it —
		// while C saw the anonymous send and the credentialed retry.
		if got := bRecv.Load(); got != 1 {
			t.Errorf("B MESSAGE count = %d, want 1", got)
		}
		if got := rcv.sendCount.Load(); got != 2 {
			t.Errorf("C MESSAGE count = %d, want 2", got)
		}
		if got := rcv.principal.Load(); got != "alice" {
			t.Errorf("C authenticated principal = %q, want alice", got)
		}
		if got := rcv.lookupCalls.Load(); got != 1 {
			t.Errorf("C LookupHA1 calls = %d, want 1", got)
		}
	})

	t.Run("cancel and shutdown race to a single outcome", func(t *testing.T) {
		t.Parallel()

		fx := newMsgSender(t)

		// Every operation gets its own raw peer, so a captured request
		// identifies its op deterministically without any Call-ID
		// correlation.
		const opCount = 6
		var (
			peers     = make([]*net.UDPConn, opCount)
			ops       = make([]*useragent.MessageOperation, opCount)
			captured  = make([]*sip.RequestEnvelope, opCount)
			srcs      = make([]netip.AddrPort, opCount)
			progress  = make([]atomic.Int32, opCount)
			completed = make([]chan useragent.Result, opCount)
		)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		for i := range opCount {
			peers[i] = newRawPeer(t)
			completed[i] = make(chan useragent.Result, 2)
			op, err := fx.ua.Message(ctx, uriFor(msgPeerAddr(t, peers[i])), useragent.MessageOperationOptions{
				Timing: noRetransmitTiming,
				Body:   []byte("ping"),
				OnProgress: func(context.Context, *sip.ResponseEnvelope) {
					progress[i].Add(1)
				},
				OnComplete: func(_ context.Context, res useragent.Result) {
					completed[i] <- res
				},
			})
			if err != nil {
				t.Fatalf("ua.Message() op %d error = %v, want nil", i, err)
			}
			if op == nil {
				t.Fatalf("ua.Message() op %d = nil, want non-nil", i)
			}
			ops[i] = op
		}

		// Every op is provably in flight once its request is captured.
		for i := range opCount {
			captured[i], srcs[i] = readReq(t, peers[i])
		}

		// The first half is canceled mid-flight: the captured send, then
		// op.Cancel, then the terminal canceled outcome. Datagrams sent
		// after the cancellation must fire no progress and no second
		// completion.
		const cancelHalf = opCount / 2
		for i := range cancelHalf {
			if err := ops[i].Cancel(t.Context()); err != nil {
				t.Fatalf("op%d.Cancel() error = %v, want nil", i, err)
			}
			waitChan(t, ops[i].Done(), "op.Done()")
			if _, err := ops[i].Wait(t.Context()); !errclass.IsCanceled(err) {
				t.Fatalf("op%d.Wait() error = %v, want canceled", i, err)
			}
			replyPeerRes(t, peers[i], captured[i], srcs[i], sip.ResponseStatusRinging)
			replyPeerRes(t, peers[i], captured[i], srcs[i], sip.ResponseStatusOK)
		}

		// The rest races the seal: one gets a final response in flight
		// while Shutdown cancels the remaining ops mid-flight. The
		// goroutine reports over the channel only — never through t.
		const racingIdx = cancelHalf
		replyPeerRes(t, peers[racingIdx], captured[racingIdx], srcs[racingIdx], sip.ResponseStatusOK)
		shutdownDone := make(chan error, 1)
		go func() { shutdownDone <- fx.ua.Shutdown(t.Context()) }()
		if err := waitResCh(t, shutdownDone, "ua.Shutdown() result"); err != nil {
			t.Fatalf("ua.Shutdown() error = %v, want nil", err)
		}

		// The drain guarantees every worker and completion callback
		// exited, so the checks below are final: one terminal outcome
		// per op shared by Wait, a repeated Wait, and exactly one
		// OnComplete.
		for i, op := range ops {
			res, err := op.Wait(t.Context())
			resCanceled := errclass.IsCanceled(err)
			if i == racingIdx {
				// The racing reply may have won or lost the seal: a 200
				// and a cancellation are both single terminal outcomes.
				switch {
				case err == nil:
					if res == nil || res.Status() != sip.ResponseStatusOK {
						t.Errorf("op%d.Wait() response = %v, want 200", i, res)
					}
				case resCanceled:
				default:
					t.Errorf("op%d.Wait() error = %v, want nil or canceled", i, err)
				}
			} else {
				if !resCanceled {
					t.Errorf("op%d.Wait() error = %v, want canceled", i, err)
				}
				if res != nil {
					t.Errorf("op%d.Wait() response = %v, want nil", i, res)
				}
			}

			// A repeated Wait resolves the same published outcome.
			res2, err2 := op.Wait(t.Context())
			if errclass.IsCanceled(err2) != resCanceled {
				t.Errorf("op%d second Wait() error = %v, inconsistent with %v", i, err2, err)
			}
			if err == nil && err2 == nil && (res2 == nil || res == nil || res2.Status() != res.Status()) {
				t.Errorf("op%d second Wait() response = %v, want %v", i, res2, res)
			}

			// No progress or response callbacks fire after cancellation:
			// the single OnComplete is the only callback the op may
			// still produce.
			if got := progress[i].Load(); got != 0 {
				t.Errorf("op%d OnProgress calls = %d, want 0", i, got)
			}
			select {
			case comp := <-completed[i]:
				if errclass.IsCanceled(comp.Err) != resCanceled {
					t.Errorf("op%d OnComplete error = %v, inconsistent with Wait() %v", i, comp.Err, err)
				}
				if comp.Err == nil && err == nil &&
					(comp.Response == nil || res == nil || comp.Response.Status() != res.Status()) {
					t.Errorf("op%d OnComplete response = %v, want %v", i, comp.Response, res)
				}
			default:
				t.Errorf("op%d OnComplete never fired", i)
			}
			if extra := len(completed[i]); extra != 0 {
				t.Errorf("op%d OnComplete fired %d extra times, want exactly once total", i, extra)
			}
		}
	})
}

//nolint:paralleltest // the process-wide goroutine baseline must not race sibling tests
func TestUserAgent_OperationLifecycleLeak(t *testing.T) {
	// Never parallel: the goroutine baseline is process-wide. The leak
	// check is registered before any wire fixture so the LIFO cleanups
	// run the harness teardown — ua.Close and the joined Serve/Shutdown
	// waits — before the verification.
	baseline := goleak.IgnoreCurrent()
	t.Cleanup(func() { goleak.VerifyNone(t, baseline) })

	fx := newMsgSender(t)
	// newMsgSender deliberately leaves the sender unregistered, so no
	// element lifecycle closes it: the explicit seal lands here.
	t.Cleanup(func() {
		if err := fx.ua.Close(); err != nil {
			t.Errorf("fx.ua.Close() error = %v, want nil", err)
		}
	})

	wB := newWireElem(t)
	bob, err := sip.ParseURI("sip:bob@ua.test")
	if err != nil {
		t.Fatalf("sip.ParseURI() error = %v, want nil", err)
	}
	uaB, err := useragent.New(useragent.Options{
		AOR: bob,
		OnMessage: func(ctx context.Context, in *useragent.IncomingMessage) error {
			return in.Respond(ctx, sip.ResponseStatusOK)
		},
	})
	if err != nil {
		t.Fatalf("useragent.New() error = %v, want nil", err)
	}
	if unbind := wB.elm.UseComponent(uaB); unbind == nil {
		t.Fatal("wB.elm.UseComponent() unbind = nil, want non-nil")
	}

	// A completed MESSAGE and OPTIONS exchange leaves finished
	// operations, their transactions, and inbound handles behind.
	ctxA := sip.ContextWithElement(t.Context(), fx.elm)
	msgOp, err := fx.ua.Message(ctxA, uriFor(wB.addr), useragent.MessageOperationOptions{Body: []byte("ping")})
	if err != nil {
		t.Fatalf("ua.Message() error = %v, want nil", err)
	}
	res, err := msgOp.Wait(t.Context())
	if err != nil {
		t.Fatalf("msgOp.Wait() error = %v, want nil", err)
	}
	if got := res.Status(); got != sip.ResponseStatusOK {
		t.Fatalf("msgOp.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
	}

	optOp, err := fx.ua.Options(ctxA, uriFor(wB.addr))
	if err != nil {
		t.Fatalf("ua.Options() error = %v, want nil", err)
	}
	res, err = optOp.Wait(t.Context())
	if err != nil {
		t.Fatalf("optOp.Wait() error = %v, want nil", err)
	}
	if got := res.Status(); got != sip.ResponseStatusOK {
		t.Fatalf("optOp.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
	}

	// An operation still in flight at the seal is canceled and drained.
	peer := newRawPeer(t)
	pending, err := fx.ua.Message(ctxA, uriFor(msgPeerAddr(t, peer)), useragent.MessageOperationOptions{
		Timing: noRetransmitTiming,
		Body:   []byte("ping"),
	})
	if err != nil {
		t.Fatalf("ua.Message() pending error = %v, want nil", err)
	}
	if pending == nil {
		t.Fatal("ua.Message() pending op = nil, want non-nil")
	}
	// The send reaching the peer proves the op is in flight at the seal.
	_, _ = readReq(t, peer)

	if err := fx.ua.Shutdown(t.Context()); err != nil {
		t.Fatalf("fx.ua.Shutdown() error = %v, want nil", err)
	}
	if _, err := pending.Wait(t.Context()); !errclass.IsCanceled(err) {
		t.Fatalf("pending.Wait() error = %v, want canceled", err)
	}

	// The registered receiver is sealed directly as well; the element
	// teardown repeats both lifecycle paths idempotently.
	if err := uaB.Close(); err != nil {
		t.Fatalf("uaB.Close() error = %v, want nil", err)
	}
}

func TestNoCANCELForMessage(t *testing.T) {
	t.Parallel()

	fx := newMsgSender(t)
	peer := newRawPeer(t)

	opCtx, cancel := context.WithCancel(sip.ContextWithElement(t.Context(), fx.elm))
	t.Cleanup(cancel)
	op, err := fx.ua.Message(opCtx, uriFor(msgPeerAddr(t, peer)), useragent.MessageOperationOptions{
		Timing: noRetransmitTiming,
		Body:   []byte("ping"),
	})
	if err != nil {
		t.Fatalf("ua.Message() error = %v, want nil", err)
	}
	if op == nil {
		t.Fatal("ua.Message() op = nil, want non-nil")
	}

	captured, src := readReq(t, peer)
	var methodsSeen atomic.Value
	methodsSeen.Store(string(captured.Method()))

	// Cancel between the captured initial send and the peer's response.
	cancel()
	if _, err := op.Wait(t.Context()); !errclass.IsCanceled(err) {
		t.Fatalf("op.Wait() err = %v, want canceled", err)
	}

	// Even the late final response draws no wire CANCEL: non-INVITE
	// cancellation drops only the local wait, never the wire exchange.
	replyPeerRes(t, peer, captured, src, sip.ResponseStatusOK)

	// The packet deadline only bounds this guard — it does not drive
	// scheduling: any further datagram inside the window may be a
	// MESSAGE retransmission, never a CANCEL.
	if err := peer.SetReadDeadline(time.Now().Add(wireNoResponseWindow)); err != nil {
		t.Fatalf("useragent: set read deadline: %v", err)
	}
	buf := make([]byte, 64*1024)
	for {
		num, _, rerr := peer.ReadFrom(buf)
		if rerr != nil {
			break
		}
		msg, perr := sip.ParseMessage(buf[:num])
		if perr != nil {
			t.Fatalf("useragent: parse datagram: %v", perr)
		}
		req, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("useragent: peer received %T, want *sip.Request", msg)
		}
		methodsSeen.Store(methodsSeen.Load().(string) + "," + string(req.Method))
	}

	if got := methodsSeen.Load(); got != "MESSAGE" {
		t.Fatalf("peer saw methods %q, want only MESSAGE (no CANCEL)", got)
	}
}
