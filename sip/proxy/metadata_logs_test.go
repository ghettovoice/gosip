package proxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/proxy"
)

// Sentinel application metadata planted on test envelopes: no proxy log
// line may ever render its key or value — the envelope LogValue contract
// excludes metadata, and the proxy stamp is the only metadata a proxy
// record may carry.
const (
	logSentinelKey = "private_secret"
	logSentinelVal = "do-not-log-this-test-value"
)

// logSink is a mutex-guarded buffer feeding an ordinary
// [slog.JSONHandler]: request coordinators and transaction callbacks emit
// records from their own goroutines while the test goroutine reads
// completed lines.
type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *logSink) lines() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]byte
	for line := range bytes.SplitSeq(s.buf.Bytes(), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			out = append(out, bytes.Clone(line))
		}
	}
	return out
}

// newLogSink returns the capture sink and an element whose
// [sip.Element.BaseLogger] writes every Debug record into it.
func newLogSink() (*logSink, *sip.Element) {
	sink := &logSink{}
	logger := slog.New(slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return sink, &sip.Element{BaseLogger: logger}
}

// logRec is one decoded JSON log record together with its raw line.
type logRec struct {
	line []byte
	rec  map[string]any
}

// logRecords decodes every captured line. Each must be a valid JSON
// record, and no proxy-owned line may carry the planted secret key or
// value anywhere — nested leakage included. Element-internal transaction
// records are not proxy-owned: their own request dumps render envelope
// metadata by design and stay out of this check.
func logRecords(tb testing.TB, sink *logSink) []logRec {
	tb.Helper()

	lines := sink.lines()
	recs := make([]logRec, 0, len(lines))
	for _, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			tb.Fatalf("json.Unmarshal(%q) error = %v, want nil", line, err)
		}
		if msg, _ := rec["msg"].(string); logOwnedMsg(msg) &&
			(bytes.Contains(line, []byte(logSentinelKey)) ||
				bytes.Contains(line, []byte(logSentinelVal))) {
			tb.Errorf("arbitrary app metadata leaked into proxy log: %s", line)
		}
		recs = append(recs, logRec{line: line, rec: rec})
	}
	return recs
}

// logOwnedMsg reports whether msg names a proxy-owned log site: every
// correlation-aware proxy message starts with the "proxy " prefix, and the
// request-drain panic line is the one coordinator message outside it.
func logOwnedMsg(msg string) bool {
	return strings.HasPrefix(msg, "proxy ") ||
		msg == "panic occurred in proxy request drain"
}

// logRecsByMsg returns the decoded records carrying msg in emission order.
func logRecsByMsg(recs []logRec, msg string) []logRec {
	var out []logRec
	for _, e := range recs {
		if e.rec["msg"] == msg {
			out = append(out, e)
		}
	}
	return out
}

// wantGroup renders the expected proxy group of md as the JSON decoder
// sees it: empty correlation fields stay absent and mode keeps its
// readable [proxy.ForwardMode] name.
func wantGroup(md proxy.Metadata) map[string]any {
	grp := make(map[string]any)
	if md.RequestID != "" {
		grp["request_id"] = md.RequestID
	}
	if md.RelatedRequestID != "" {
		grp["related_request_id"] = md.RelatedRequestID
	}
	if md.TargetID != "" {
		grp["target_id"] = md.TargetID
	}
	if md.AttemptID != "" {
		grp["attempt_id"] = md.AttemptID
	}
	if md.RouteName != "" {
		grp["route_name"] = md.RouteName
	}
	grp["mode"] = md.Mode.String()
	grp["stage"] = string(md.Stage)
	return grp
}

// assertLogRec asserts one expected proxy log record: the severity stays
// as before, exactly one proxy group is attached, and the group equals
// want's [proxy.Metadata.LogValue] rendering.
func assertLogRec(tb testing.TB, e logRec, level string, want proxy.Metadata) {
	tb.Helper()

	if got := e.rec["level"]; got != level {
		tb.Errorf("log level = %v, want %q", got, level)
	}
	if cnt := bytes.Count(e.line, []byte(`"proxy":`)); cnt != 1 {
		tb.Fatalf("proxy group count = %d, want 1: %s", cnt, e.line)
	}
	grp, ok := e.rec["proxy"].(map[string]any)
	if !ok {
		tb.Fatalf("log record has no proxy group: %s", e.line)
	}
	if diff := cmp.Diff(wantGroup(want), grp); diff != "" {
		tb.Errorf("proxy group mismatch (-want +got):\n%s", diff)
	}
}

// mustLogRec returns the single record carrying msg: existing
// messages stay one-shot at their log site.
func mustLogRec(tb testing.TB, recs []logRec, msg string) logRec {
	tb.Helper()

	got := logRecsByMsg(recs, msg)
	if len(got) != 1 {
		tb.Fatalf("%q records = %d, want 1", msg, len(got))
	}
	return got[0]
}

// countLogPrxRecs returns the number of records carrying any proxy
// group, so lines outside the correlation-aware sites stay unattributed.
func countLogPrxRecs(recs []logRec) int {
	cnt := 0
	for _, e := range recs {
		if _, ok := e.rec["proxy"]; ok {
			cnt++
		}
	}
	return cnt
}

// mustClaimMD reads the claim-stage stamp the proxy stored on req.
func mustClaimMD(tb testing.TB, req *sip.RequestEnvelope) proxy.Metadata {
	tb.Helper()

	md, ok := proxy.MetadataFrom(req.Metadata())
	if !ok || md.Stage != proxy.StageClaim {
		tb.Fatalf("proxy.MetadataFrom(req) = (%+v, %t), want a claim stamp", md, ok)
	}
	return md
}

// mangleSecondVia rewrites the sent-by address of the response's second
// Via hop. The downstream client transaction still matches the response
// on its first hop, while the forwarded copy — its proxy Via stripped —
// exposes the mangled hop to the upstream transaction's response match.
func mangleSecondVia(tb testing.TB, res *sip.ResponseEnvelope) {
	tb.Helper()

	res.WithMessage(func(msg *sip.Response) {
		hops := 0
		for _, h := range msg.Headers.Get("Via") {
			via, ok := h.(header.Via)
			if !ok {
				continue
			}
			for i := range via {
				hops++
				if hops == 2 {
					via[i].Addr = sip.MakeHostAddr("unmatched.invalid")
					return
				}
			}
		}
		tb.Fatal("response has no second Via hop")
	})
}

func TestProxy_MetadataLogs(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	served := []string{"alice.voip.com"}

	intercept := func(tb testing.TB, ctx context.Context, elm *sip.Element, prx *proxy.Proxy, req *sip.RequestEnvelope) {
		tb.Helper()

		err := prx.InterceptInboundRequest(
			sip.ContextWithElement(ctx, elm),
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil }),
			req,
		)
		if err != nil {
			tb.Fatalf("prx.InterceptInboundRequest(%v) error = %v, want nil", req.Method(), err)
		}
	}

	plantSecret := func(req *sip.RequestEnvelope) {
		req.Metadata().Set(logSentinelKey, logSentinelVal)
	}

	t.Run("claim stamp on route resolved", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			sink, elm := newLogSink()

			route := &proxy.Route{
				Name:        "log-claim",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{
						{statelessLitTarget("A", "10.0.0.9")},
					}}, nil
				},
			}
			_, prx, _ := newConfiguredTestProxy(t, route, proxy.Options{ServedDomains: served}, elm)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".log-claim", laddr, raddr)
			plantSecret(req)
			intercept(t, ctx, elm, prx, req)
			synctest.Wait()

			want := mustClaimMD(t, req)
			recs := logRecords(t, sink)

			resolved := mustLogRec(t, recs, "proxy route resolved")
			assertLogRec(t, resolved, "DEBUG", want)
			if got := countLogPrxRecs(recs); got != 1 {
				t.Fatalf("records carrying a proxy group = %d, want 1", got)
			}
		})
	})

	t.Run("forward stamp on target selection failure", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			sink, elm := newLogSink()

			route := &proxy.Route{
				Name:        "log-selection",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{
						{statelessLitTarget("A", "10.0.0.9")},
					}}, nil
				},
				SelectTargets: func(context.Context, proxy.Selection) ([]string, error) {
					return nil, errors.ErrorWrap("selection declined")
				},
			}
			_, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{ServedDomains: served}, elm)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".log-selection", laddr, raddr)
			plantSecret(req)
			intercept(t, ctx, elm, prx, req)
			synctest.Wait()

			claim := mustClaimMD(t, req)
			wantFwd := proxy.Metadata{
				RequestID: claim.RequestID,
				RouteName: route.Name,
				Mode:      route.ForwardMode,
				Stage:     proxy.StageForward,
			}
			recs := logRecords(t, sink)

			resolved := mustLogRec(t, recs, "proxy route resolved")
			assertLogRec(t, resolved, "DEBUG", claim)

			failed := mustLogRec(t, recs, "proxy target selection failed")
			assertLogRec(t, failed, "WARN", wantFwd)

			if got := countLogPrxRecs(recs); got != 2 {
				t.Fatalf("records carrying a proxy group = %d, want 2", got)
			}
			// The abandoned request answers upstream with a mapped 500.
			if got := len(drainRess(tp)); got != 1 {
				t.Fatalf("upstream responses=%d, want 1", got)
			}
		})
	})

	t.Run("upstream stamp on response send failures", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			sink, elm := newLogSink()

			route := &proxy.Route{
				Name:        "log-upstream",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{
						{statelessLitTarget("A", "10.0.0.9")},
					}}, nil
				},
			}
			_, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{ServedDomains: served}, elm)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".log-upstream", laddr, raddr)
			plantSecret(req)
			intercept(t, ctx, elm, prx, req)
			synctest.Wait()

			sends := drainReqs(tp)
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}
			fwd, ok := proxy.MetadataFrom(sends[0].Metadata())
			if !ok || fwd.Stage != proxy.StageForward {
				t.Fatalf("proxy.MetadataFrom(outbound copy) = (%+v, %t), want a forward stamp", fwd, ok)
			}

			key, err := sip.MakeClientTransactionKey(sends[0])
			if err != nil {
				t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
			}
			txA, ok := elm.LoadClientTransaction(key)
			if !ok {
				t.Fatal("forwarding client transaction not registered")
			}

			// The downstream response keeps the branch's top Via intact so
			// the client transaction accepts it, while the second hop —
			// the one the forwarded copy exposes after the proxy's own Via
			// is stripped — mismatches the upstream transaction's request:
			// the upstream send is rejected at transaction level.
			res180 := newInRes(t, sends[0], sip.ResponseStatusRinging)
			mangleSecondVia(t, res180)
			if err := txA.RecvResponse(ctx, res180); err != nil {
				t.Fatalf("txA.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()

			// A lone 503 exhausts the single-candidate runner and is
			// rewritten into a generated 500 clone by the final selection:
			// the clone is a distinct envelope the send dedup has not seen,
			// so the committed upstream send runs and fails the same way.
			res503 := newInRes(t, sends[0], sip.ResponseStatusServiceUnavailable)
			mangleSecondVia(t, res503)
			if err := txA.RecvResponse(ctx, res503); err != nil {
				t.Fatalf("txA.RecvResponse(503) error = %v, want nil", err)
			}
			synctest.Wait()

			recs := logRecords(t, sink)

			resolved := mustLogRec(t, recs, "proxy route resolved")
			assertLogRec(t, resolved, "DEBUG", mustClaimMD(t, req))

			// The failed provisional send keeps the owning attempt's
			// correlation; the failed committed final is answered without a
			// single-attempt source.
			provFailed := mustLogRec(t, recs, "proxy response forwarding failed")
			assertLogRec(t, provFailed, "DEBUG", proxy.Metadata{
				RequestID: fwd.RequestID,
				TargetID:  fwd.TargetID,
				AttemptID: fwd.AttemptID,
				RouteName: fwd.RouteName,
				Mode:      fwd.Mode,
				Stage:     proxy.StageUpstream,
			})

			finalFailed := mustLogRec(t, recs, "proxy final response forwarding failed")
			assertLogRec(t, finalFailed, "DEBUG", proxy.Metadata{
				RequestID: fwd.RequestID,
				RouteName: fwd.RouteName,
				Mode:      fwd.Mode,
				Stage:     proxy.StageUpstream,
			})

			if got := countLogPrxRecs(recs); got != 3 {
				t.Fatalf("records carrying a proxy group = %d, want 3", got)
			}
		})
	})

	t.Run("claim stamp on request drain panic", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			sink, elm := newLogSink()

			route := &proxy.Route{
				Name:        "log-panic",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{
						{statelessLitTarget("A", "10.0.0.9")},
					}}, nil
				},
				SelectTargets: func(context.Context, proxy.Selection) ([]string, error) {
					panic("selection exploded")
				},
			}
			_, prx, _ := newConfiguredTestProxy(t, route, proxy.Options{ServedDomains: served}, elm)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".log-panic", laddr, raddr)
			plantSecret(req)
			intercept(t, ctx, elm, prx, req)
			synctest.Wait()

			// The panic runs inside a coordinator effect: the request's
			// stored claim stamp correlates the line — the drain is no
			// stage boundary of its own.
			want := mustClaimMD(t, req)
			recs := logRecords(t, sink)

			resolved := mustLogRec(t, recs, "proxy route resolved")
			assertLogRec(t, resolved, "DEBUG", want)

			panicked := mustLogRec(t, recs, "panic occurred in proxy request drain")
			assertLogRec(t, panicked, "ERROR", want)

			if got := countLogPrxRecs(recs); got != 2 {
				t.Fatalf("records carrying a proxy group = %d, want 2", got)
			}
		})
	})
}
