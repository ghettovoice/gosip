package proxy_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/proxy"
)

func TestMetadataFrom(t *testing.T) {
	t.Parallel()

	t.Run("stage and mode matrix", func(t *testing.T) {
		t.Parallel()

		for _, stage := range []proxy.Stage{
			proxy.StageClaim,
			proxy.StageForward,
			proxy.StageResponse,
			proxy.StageUpstream,
		} {
			for _, mode := range []proxy.ForwardMode{proxy.ForwardModeStateful, proxy.ForwardModeStateless} {
				want := proxy.Metadata{
					RequestID:        "req-1",
					RelatedRequestID: "rel-1",
					TargetID:         "tgt-1",
					AttemptID:        "att-1",
					RouteName:        "route-a",
					Mode:             mode,
					Stage:            stage,
				}
				meta := &sip.MessageMetadata{}
				meta.Set(proxy.MetadataKey, want)

				got, ok := proxy.MetadataFrom(meta)
				if !ok {
					t.Errorf("proxy.MetadataFrom(stage=%q, mode=%v) ok = false, want true", stage, mode)
					continue
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("proxy.MetadataFrom(stage=%q, mode=%v) mismatch (-want +got):\n%s", stage, mode, diff)
				}
			}
		}
	})

	t.Run("stored values", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			value  any
			set    bool
			want   proxy.Metadata
			wantOK bool
		}{
			{name: "absent key"},
			{name: "nil value", value: nil, set: true},
			{name: "string container", value: "sip.proxy", set: true},
			{name: "slice container", value: []any{"mode", 0}, set: true},
			{name: "float container", value: float64(0), set: true},
			{
				name:  "unsupported mode",
				value: proxy.Metadata{Mode: proxy.ForwardMode(2), Stage: proxy.StageClaim},
				set:   true,
			},
			{
				name:  "overflow mode",
				value: proxy.Metadata{Mode: proxy.ForwardMode(0xFF), Stage: proxy.StageForward},
				set:   true,
			},
			{
				name:  "unsupported stage",
				value: proxy.Metadata{Mode: proxy.ForwardModeStateful, Stage: proxy.Stage("bogus")},
				set:   true,
			},
			{
				name:   "valid",
				value:  proxy.Metadata{Mode: proxy.ForwardModeStateless, Stage: proxy.StageResponse},
				set:    true,
				want:   proxy.Metadata{Mode: proxy.ForwardModeStateless, Stage: proxy.StageResponse},
				wantOK: true,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				meta := &sip.MessageMetadata{}
				if tt.set {
					meta.Set(proxy.MetadataKey, tt.value)
				}

				got, ok := proxy.MetadataFrom(meta)
				if ok != tt.wantOK {
					t.Fatalf("proxy.MetadataFrom() ok = %v, want %v", ok, tt.wantOK)
				}
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("proxy.MetadataFrom() mismatch (-want +got):\n%s", diff)
				}
			})
		}
	})

	t.Run("decoded maps", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			value  map[string]any
			want   proxy.Metadata
			wantOK bool
		}{
			{
				name:   "minimal",
				value:  map[string]any{"mode": 1, "stage": "response"},
				want:   proxy.Metadata{Mode: proxy.ForwardModeStateless, Stage: proxy.StageResponse},
				wantOK: true,
			},
			{
				name: "full",
				value: map[string]any{
					"request_id":         "req-1",
					"related_request_id": "rel-1",
					"target_id":          "tgt-1",
					"attempt_id":         "att-1",
					"route_name":         "route-a",
					"mode":               0,
					"stage":              "claim",
				},
				want: proxy.Metadata{
					RequestID:        "req-1",
					RelatedRequestID: "rel-1",
					TargetID:         "tgt-1",
					AttemptID:        "att-1",
					RouteName:        "route-a",
					Mode:             proxy.ForwardModeStateful,
					Stage:            proxy.StageClaim,
				},
				wantOK: true,
			},
			{
				name:   "json float mode",
				value:  map[string]any{"mode": float64(1), "stage": "upstream"},
				want:   proxy.Metadata{Mode: proxy.ForwardModeStateless, Stage: proxy.StageUpstream},
				wantOK: true,
			},
			{
				name: "unknown fields ignored",
				value: map[string]any{
					"mode":         0,
					"stage":        "claim",
					"future_field": "x",
					"nested":       map[string]any{"a": 1},
				},
				want:   proxy.Metadata{Mode: proxy.ForwardModeStateful, Stage: proxy.StageClaim},
				wantOK: true,
			},
			{name: "mode out of range", value: map[string]any{"mode": 2, "stage": "claim"}},
			{name: "mode negative", value: map[string]any{"mode": -1, "stage": "claim"}},
			{name: "mode fractional", value: map[string]any{"mode": 0.5, "stage": "claim"}},
			{name: "mode string", value: map[string]any{"mode": "stateful", "stage": "claim"}},
			{name: "mode null", value: map[string]any{"mode": nil, "stage": "claim"}},
			{name: "mode missing", value: map[string]any{"stage": "claim"}},
			{name: "stage bogus", value: map[string]any{"mode": 0, "stage": "bogus"}},
			{name: "stage null", value: map[string]any{"mode": 0, "stage": nil}},
			{name: "stage missing", value: map[string]any{"mode": 0}},
			{name: "request_id non-string", value: map[string]any{"mode": 0, "stage": "claim", "request_id": 5}},
			{name: "request_id null", value: map[string]any{"mode": 0, "stage": "claim", "request_id": nil}},
			{name: "target_id non-string", value: map[string]any{"mode": 0, "stage": "claim", "target_id": true}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				meta := &sip.MessageMetadata{}
				meta.Set(proxy.MetadataKey, tt.value)

				got, ok := proxy.MetadataFrom(meta)
				if ok != tt.wantOK {
					t.Fatalf("proxy.MetadataFrom(%v) ok = %v, want %v", tt.value, ok, tt.wantOK)
				}
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("proxy.MetadataFrom(%v) mismatch (-want +got):\n%s", tt.value, diff)
				}
			})
		}
	})

	t.Run("nil container", func(t *testing.T) {
		t.Parallel()

		if md, ok := proxy.MetadataFrom(nil); ok || md != (proxy.Metadata{}) {
			t.Errorf("proxy.MetadataFrom(nil) = (%+v, %v), want (proxy.Metadata{}, false)", md, ok)
		}
	})
}

func TestSetMetadata(t *testing.T) {
	t.Parallel()

	t.Run("nil container is a no-op", func(t *testing.T) {
		t.Parallel()

		proxy.SetMetadata(nil, proxy.Metadata{Mode: proxy.ForwardModeStateful, Stage: proxy.StageClaim})
	})

	t.Run("stores the whole value", func(t *testing.T) {
		t.Parallel()

		want := proxy.Metadata{
			RequestID:        "req-1",
			RelatedRequestID: "rel-1",
			TargetID:         "tgt-1",
			AttemptID:        "att-1",
			RouteName:        "route-a",
			Mode:             proxy.ForwardModeStateless,
			Stage:            proxy.StageForward,
		}
		meta := &sip.MessageMetadata{}
		proxy.SetMetadata(meta, want)

		stored, ok := meta.Get(proxy.MetadataKey)
		if !ok {
			t.Fatal("meta.Get(proxy.MetadataKey) ok = false, want true")
		}
		md, ok := stored.(proxy.Metadata)
		if !ok {
			t.Fatalf("stored value type = %T, want proxy.Metadata", stored)
		}
		if diff := cmp.Diff(want, md); diff != "" {
			t.Errorf("stored value mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("whole-value replacement", func(t *testing.T) {
		t.Parallel()

		first := proxy.Metadata{RequestID: "req-1", Mode: proxy.ForwardModeStateful, Stage: proxy.StageClaim}
		second := proxy.Metadata{RequestID: "req-2", TargetID: "tgt-1", Mode: proxy.ForwardModeStateless, Stage: proxy.StageResponse}
		meta := &sip.MessageMetadata{}

		proxy.SetMetadata(meta, first)
		proxy.SetMetadata(meta, second)

		got, ok := proxy.MetadataFrom(meta)
		if !ok {
			t.Fatal("proxy.MetadataFrom() ok = false, want true")
		}
		if diff := cmp.Diff(second, got); diff != "" {
			t.Errorf("proxy.MetadataFrom() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("reads do not mutate the stored map", func(t *testing.T) {
		t.Parallel()

		src := map[string]any{
			"request_id": "req-1",
			"mode":       0,
			"stage":      "claim",
			"extra":      "kept",
		}
		before := maps.Clone(src)
		meta := &sip.MessageMetadata{}
		meta.Set(proxy.MetadataKey, src)

		if _, ok := proxy.MetadataFrom(meta); !ok {
			t.Fatal("proxy.MetadataFrom() ok = false, want true")
		}
		if diff := cmp.Diff(before, src); diff != "" {
			t.Errorf("stored map mismatch after read (-before +after):\n%s", diff)
		}
	})

	t.Run("sibling and snapshot isolation", func(t *testing.T) {
		t.Parallel()

		first := proxy.Metadata{RequestID: "req-1", Mode: proxy.ForwardModeStateful, Stage: proxy.StageClaim}
		second := proxy.Metadata{RequestID: "req-2", Mode: proxy.ForwardModeStateless, Stage: proxy.StageResponse}

		meta := &sip.MessageMetadata{}
		sibling := &sip.MessageMetadata{}
		proxy.SetMetadata(meta, first)

		if got, ok := proxy.MetadataFrom(sibling); ok || got != (proxy.Metadata{}) {
			t.Errorf("proxy.MetadataFrom(sibling) = (%+v, %v), want (proxy.Metadata{}, false)", got, ok)
		}

		snapshot := meta.Clone()
		proxy.SetMetadata(meta, second)

		got, ok := proxy.MetadataFrom(snapshot)
		if !ok {
			t.Fatal("proxy.MetadataFrom(snapshot) ok = false, want true")
		}
		if diff := cmp.Diff(first, got); diff != "" {
			t.Errorf("snapshot metadata mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestMetadata_CloneJSON(t *testing.T) {
	t.Parallel()

	want := proxy.Metadata{
		RequestID:        "req-1",
		RelatedRequestID: "rel-1",
		TargetID:         "tgt-1",
		AttemptID:        "att-1",
		RouteName:        "route-a",
		Mode:             proxy.ForwardModeStateless,
		Stage:            proxy.StageResponse,
	}
	newMeta := func() *sip.MessageMetadata {
		meta := &sip.MessageMetadata{}
		proxy.SetMetadata(meta, want)
		return meta
	}

	t.Run("clone", func(t *testing.T) {
		t.Parallel()

		got, ok := proxy.MetadataFrom(newMeta().Clone())
		if !ok {
			t.Fatal("proxy.MetadataFrom(clone) ok = false, want true")
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("cloned metadata mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("json round trip", func(t *testing.T) {
		t.Parallel()

		data, err := json.Marshal(newMeta())
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		var restored sip.MessageMetadata
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		got, ok := proxy.MetadataFrom(&restored)
		if !ok {
			t.Fatalf("proxy.MetadataFrom() ok = false, want true; data = %s", data)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("restored metadata mismatch (-want +got):\n%s", diff)
		}
	})
}

// TestMetadata_LogValue pins the structured log contract of a proxy
// stamp: one slog group whose keys mirror the serialized field names in
// a stable order, with empty correlation fields omitted rather than
// rendered as placeholders.
func TestMetadata_LogValue(t *testing.T) {
	t.Parallel()

	// group returns the ordered attribute list of md's log value,
	// failing the test when the value is not a slog group.
	group := func(tb testing.TB, md proxy.Metadata) []slog.Attr {
		tb.Helper()

		v := md.LogValue()
		if v.Kind() != slog.KindGroup {
			tb.Fatalf("md.LogValue().Kind() = %v, want slog.KindGroup", v.Kind())
		}
		return v.Group()
	}

	t.Run("group keys and values", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			md   proxy.Metadata
			want []slog.Attr
		}{
			{
				name: "full",
				md: proxy.Metadata{
					RequestID:        "req-1",
					RelatedRequestID: "rel-1",
					TargetID:         "tgt-1",
					AttemptID:        "att-1",
					RouteName:        "route-a",
					Mode:             proxy.ForwardModeStateless,
					Stage:            proxy.StageForward,
				},
				want: []slog.Attr{
					slog.String("request_id", "req-1"),
					slog.String("related_request_id", "rel-1"),
					slog.String("target_id", "tgt-1"),
					slog.String("attempt_id", "att-1"),
					slog.String("route_name", "route-a"),
					slog.String("mode", "stateless"),
					slog.String("stage", "forward"),
				},
			},
			{
				name: "stateless response partial",
				md:   proxy.Metadata{Mode: proxy.ForwardModeStateless, Stage: proxy.StageResponse},
				want: []slog.Attr{
					slog.String("mode", "stateless"),
					slog.String("stage", "response"),
				},
			},
			{
				// The zero ForwardMode is real stateful, not an empty
				// correlation field: mode is never omitted.
				name: "zero-value stateful claim",
				md:   proxy.Metadata{Stage: proxy.StageClaim},
				want: []slog.Attr{
					slog.String("mode", "stateful"),
					slog.String("stage", "claim"),
				},
			},
			{
				// ForwardMode.String names every unsupported value
				// "stateful", so an invalid mode keeps its numeric form
				// rather than being mislabeled.
				name: "unsupported mode keeps its numeric form",
				md:   proxy.Metadata{Mode: proxy.ForwardMode(7), Stage: proxy.StageClaim},
				want: []slog.Attr{
					slog.Uint64("mode", 7),
					slog.String("stage", "claim"),
				},
			},
			{
				name: "unsupported stage renders as stored",
				md:   proxy.Metadata{Mode: proxy.ForwardModeStateful, Stage: proxy.Stage("bogus")},
				want: []slog.Attr{
					slog.String("mode", "stateful"),
					slog.String("stage", "bogus"),
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if diff := cmp.Diff(tt.want, group(t, tt.md)); diff != "" {
					t.Errorf("md.LogValue().Group() mismatch (-want +got):\n%s", diff)
				}
				if v := slog.Any("proxy", tt.md).Value.Resolve(); v.Kind() != slog.KindGroup {
					t.Errorf("slog.Any(\"proxy\", md).Value.Resolve().Kind() = %v, want slog.KindGroup", v.Kind())
				}
			})
		}
	})

	t.Run("decoded json", func(t *testing.T) {
		t.Parallel()

		want := proxy.Metadata{
			RequestID:        "req-1",
			RelatedRequestID: "rel-1",
			TargetID:         "tgt-1",
			AttemptID:        "att-1",
			RouteName:        "route-a",
			Mode:             proxy.ForwardModeStateful,
			Stage:            proxy.StageClaim,
		}
		data, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		meta := &sip.MessageMetadata{}
		meta.Set(proxy.MetadataKey, m)

		decoded, ok := proxy.MetadataFrom(meta)
		if !ok {
			t.Fatal("proxy.MetadataFrom() ok = false, want true")
		}
		if diff := cmp.Diff(group(t, want), group(t, decoded)); diff != "" {
			t.Errorf("decoded log group mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("no foreign fields", func(t *testing.T) {
		t.Parallel()

		meta := &sip.MessageMetadata{}
		meta.Set(proxy.MetadataKey, map[string]any{
			"request_id":    "req-1",
			"mode":          0,
			"stage":         "claim",
			"auth_material": "s3cret",
			"app":           map[string]any{"trace": "x"},
		})
		decoded, ok := proxy.MetadataFrom(meta)
		if !ok {
			t.Fatal("proxy.MetadataFrom() ok = false, want true")
		}

		want := proxy.Metadata{RequestID: "req-1", Mode: proxy.ForwardModeStateful, Stage: proxy.StageClaim}
		got := group(t, decoded)
		if diff := cmp.Diff(group(t, want), got); diff != "" {
			t.Errorf("log group carries foreign fields (-want +got):\n%s", diff)
		}
	})

	t.Run("slog handlers", func(t *testing.T) {
		t.Parallel()

		md := proxy.Metadata{
			RequestID: "req-1",
			Mode:      proxy.ForwardModeStateless,
			Stage:     proxy.StageResponse,
		}

		var jbuf bytes.Buffer
		slog.New(slog.NewJSONHandler(&jbuf, nil)).
			InfoContext(t.Context(), "evt", slog.Any("proxy", md))
		var rec map[string]any
		if err := json.Unmarshal(jbuf.Bytes(), &rec); err != nil {
			t.Fatalf("json.Unmarshal() error = %v; line = %s", err, jbuf.String())
		}
		gr, ok := rec["proxy"].(map[string]any)
		if !ok {
			t.Fatalf("recorded proxy attr type = %T, want a JSON object; line = %s", rec["proxy"], jbuf.String())
		}
		want := map[string]any{"request_id": "req-1", "mode": "stateless", "stage": "response"}
		if diff := cmp.Diff(want, gr); diff != "" {
			t.Errorf("json handler proxy group mismatch (-want +got):\n%s", diff)
		}

		var tbuf bytes.Buffer
		slog.New(slog.NewTextHandler(&tbuf, nil)).
			InfoContext(t.Context(), "evt", slog.Any("proxy", md))
		for _, sub := range []string{"proxy.request_id=req-1", "proxy.mode=stateless", "proxy.stage=response"} {
			if !strings.Contains(tbuf.String(), sub) {
				t.Errorf("text handler output %q lacks %q", tbuf.String(), sub)
			}
		}
	})
}

// stampedEnv is the common envelope surface a stage stamp is recorded on.
type stampedEnv interface {
	Metadata() *sip.MessageMetadata
	String() string
}

// assertWireFree asserts that rewriting the proxy metadata of env leaves
// its rendered wire output — headers, body, and control counters —
// byte-identical: the stamp is envelope-local state and never reaches the
// wire.
func assertWireFree(tb testing.TB, env stampedEnv) {
	tb.Helper()

	before := env.String()
	proxy.SetMetadata(env.Metadata(), proxy.Metadata{
		RequestID: "rewritten-id",
		TargetID:  "rewritten-target",
		AttemptID: "rewritten-attempt",
		Mode:      proxy.ForwardModeStateless,
		Stage:     proxy.StageClaim,
	})
	if got := env.String(); got != before {
		tb.Fatalf("metadata stamp changed the rendered message (-before +after):\n%s\n%s", before, got)
	}
}

// TestProxy_MetadataAttribution pins the stage stamps the proxy records at
// its real processing boundaries: StageForward on every owned downstream
// request copy carrying the request, target, and physical-attempt
// correlation, StageResponse on every attributed inbound response, and
// StageUpstream on every owned upstream copy — with the attempt fields
// recorded only when the send owns a real single-attempt source.
func TestProxy_MetadataAttribution(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}
	targetHosts := map[string]string{"A": "10.0.0.9", "B": "10.0.0.10", "C": "10.0.0.11"}

	// targetOf maps an outbound branch copy back to its plan target via the
	// Request-URI host; unknown hosts return an empty ID.
	targetOf := func(req *sip.RequestEnvelope) string {
		uri, ok := req.URI().(*sip.URI)
		if !ok {
			return ""
		}
		for id, host := range targetHosts {
			if uri.Addr.Host() == host {
				return id
			}
		}
		return ""
	}
	// reqStamp returns the stamp recorded on an owned request copy.
	reqStamp := func(tb testing.TB, env *sip.RequestEnvelope) proxy.Metadata {
		tb.Helper()
		md, ok := proxy.MetadataFrom(env.Metadata())
		if !ok {
			tb.Fatalf("proxy.MetadataFrom() = (%+v, %v), want a valid proxy stamp", md, ok)
		}
		return md
	}
	// resStamp returns the stamp recorded on an owned response copy.
	resStamp := func(tb testing.TB, env *sip.ResponseEnvelope) proxy.Metadata {
		tb.Helper()
		md, ok := proxy.MetadataFrom(env.Metadata())
		if !ok {
			tb.Fatalf("proxy.MetadataFrom() = (%+v, %v), want a valid proxy stamp", md, ok)
		}
		return md
	}

	t.Run("fork two", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("attr-fork", obsLitPlan("A", "B")), opts)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".attr-fork", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()

			claim := reqStamp(t, req)
			sends := drainReqs(tp)
			if len(sends) != 2 {
				t.Fatalf("outbound sends=%d, want 2", len(sends))
			}
			byTarget := make(map[string]*sip.RequestEnvelope, len(sends))
			for _, out := range sends {
				byTarget[targetOf(out)] = out
			}
			copy1, copy2 := byTarget["A"], byTarget["B"]
			if copy1 == nil || copy2 == nil {
				t.Fatalf("fork copies = %v, want one copy per target A/B", slices.Sorted(maps.Keys(byTarget)))
			}

			fork1, _ := proxy.MetadataFrom(copy1.Metadata())
			fork2, _ := proxy.MetadataFrom(copy2.Metadata())
			if fork1.RequestID != fork2.RequestID {
				t.Errorf("fork copies diverged: %q vs %q", fork1.RequestID, fork2.RequestID)
			}
			if fork1.TargetID == fork2.TargetID || fork1.AttemptID == fork2.AttemptID {
				t.Errorf("forks share target/attempt: %+v vs %+v", fork1, fork2)
			}
			if fork1.Stage != proxy.StageForward || fork2.Stage != proxy.StageForward {
				t.Errorf("downstream stages = %q/%q, want forward", fork1.Stage, fork2.Stage)
			}
			for id, fork := range map[string]proxy.Metadata{"A": fork1, "B": fork2} {
				if fork.RequestID != claim.RequestID {
					t.Errorf("fork %s RequestID = %q, want the claim ID %q", id, fork.RequestID, claim.RequestID)
				}
				if fork.TargetID != id {
					t.Errorf("fork %s TargetID = %q, want %q", id, fork.TargetID, id)
				}
				if !strings.HasPrefix(fork.AttemptID, id+"#") {
					t.Errorf("fork %s AttemptID = %q, want the %q format", id, fork.AttemptID, id+"#N")
				}
				if fork.RouteName != "attr-fork" || fork.Mode != proxy.ForwardModeStateful {
					t.Errorf("fork %s route/mode = %q/%v, want attr-fork/stateful", id, fork.RouteName, fork.Mode)
				}
			}

			// Sibling copies own independent metadata containers, and the
			// stamp never reaches the rendered wire output.
			if copy1.Metadata() == copy2.Metadata() {
				t.Error("fork copies share one metadata container, want independent values")
			}
			assertWireFree(t, copy1)
			assertWireFree(t, copy2)
		})
	})

	t.Run("address retry two", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			log := &attemptActionLog{}
			var lookups atomic.Int32
			loc := &attemptLocator{
				log:     log,
				lookups: &lookups,
				candidates: map[string][]attemptCandidate{
					"10.0.0.9": {
						udpAttemptCandidate("first", "10.0.0.9:5060"),
						udpAttemptCandidate("second", "10.0.0.10:5060"),
					},
				},
			}
			elm, prx, tp := newAttemptTestProxy(t,
				attemptRoute("attr-retry", attemptPlan(
					[]proxy.Target{attemptURITarget("A", "10.0.0.9")},
				)),
				opts, loc,
			)

			var sentMu sync.Mutex
			var sentReqs []*sip.RequestEnvelope
			tp.sendReqHook = func(call sendReqCall, _ int) error {
				if !call.req.Method().Equal(sip.RequestMethodInvite) {
					return nil
				}
				sentMu.Lock()
				sentReqs = append(sentReqs, call.req)
				first := len(sentReqs) == 1
				sentMu.Unlock()
				if first {
					return errors.ErrorWrap("first candidate write failed")
				}
				return nil
			}

			admitAttemptReq(ctx, t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".attr-retry", laddr, raddr))
			synctest.Wait()

			sentMu.Lock()
			got := slices.Clone(sentReqs)
			sentMu.Unlock()
			if len(got) != 2 {
				t.Fatalf("outbound sends=%d, want 2", len(got))
			}
			att1, att2 := reqStamp(t, got[0]), reqStamp(t, got[1])
			if att1.RequestID != att2.RequestID || att1.RequestID == "" {
				t.Errorf("retry request IDs = %q vs %q, want one shared nonempty identity", att1.RequestID, att2.RequestID)
			}
			if att1.TargetID != "A" || att2.TargetID != "A" {
				t.Errorf("retry target IDs = %q/%q, want the same target A", att1.TargetID, att2.TargetID)
			}
			if att1.AttemptID == att2.AttemptID {
				t.Errorf("physical attempts share %q, want a fresh attempt ID on the retry", att1.AttemptID)
			}
			if att1.Stage != proxy.StageForward || att2.Stage != proxy.StageForward {
				t.Errorf("retry stages = %q/%q, want forward", att1.Stage, att2.Stage)
			}
			for _, att := range []proxy.Metadata{att1, att2} {
				if !strings.HasPrefix(att.AttemptID, "A#") {
					t.Errorf("retry AttemptID = %q, want the %q format", att.AttemptID, "A#N")
				}
			}
		})
	})

	t.Run("immediate response in send", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("attr-early", obsLitPlan("A")), opts)

			// A response delivered from inside the initial send callback is
			// still attributed: the attempt identity and the forward stamp
			// exist before the send runs.
			resErr := make(chan error, 1)
			var once sync.Once
			tp.sendReqHook = func(call sendReqCall, _ int) error {
				if !call.req.Method().Equal(sip.RequestMethodInvite) {
					return nil
				}
				once.Do(func() {
					key, err := sip.MakeClientTransactionKey(call.req)
					if err != nil {
						resErr <- errors.Wrap(err)
						return
					}
					tx, ok := elm.LoadClientTransaction(key)
					if !ok {
						resErr <- errors.ErrorWrap("forwarding client transaction not registered inside send")
						return
					}
					msg, err := call.req.Message().NewResponse(sip.ResponseStatusRinging)
					if err != nil {
						resErr <- errors.Wrap(err)
						return
					}
					res := sip.NewResponseEnvelope(msg)
					go func() { resErr <- errors.Wrap(tx.RecvResponse(call.ctx, res)) }()
				})
				return nil
			}

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".attr-early", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()

			if err := <-resErr; err != nil {
				t.Fatalf("clnTx.RecvResponse(180) inside send error = %v, want nil", err)
			}
			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound sends=%d, want 1", len(sends))
			}
			upstream := drainRess(tp)
			if len(upstream) != 1 {
				t.Fatalf("upstream response sends=%d, want 1 forwarded provisional", len(upstream))
			}

			fwd := reqStamp(t, sends[0])
			up := resStamp(t, upstream[0])
			if up.Stage != proxy.StageUpstream {
				t.Fatalf("upstream stage = %q, want %q", up.Stage, proxy.StageUpstream)
			}
			if up.RequestID != fwd.RequestID || up.RequestID == "" {
				t.Errorf("upstream RequestID = %q, want the send's %q", up.RequestID, fwd.RequestID)
			}
			if up.TargetID != fwd.TargetID || up.AttemptID != fwd.AttemptID ||
				up.TargetID == "" || up.AttemptID == "" {
				t.Errorf("upstream source = %q/%q, want the send's attempt %q/%q",
					up.TargetID, up.AttemptID, fwd.TargetID, fwd.AttemptID)
			}
		})
	})

	t.Run("udp size switch", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			log := &attemptActionLog{}
			var lookups atomic.Int32
			loc := &attemptLocator{
				log:     log,
				lookups: &lookups,
				candidates: map[string][]attemptCandidate{
					"10.0.0.9": {
						udpAttemptCandidate("a-first", "10.0.0.9:5060"),
					},
				},
			}
			elm, prx, tp := newAttemptTestProxy(t,
				attemptRoute("attr-switch", attemptPlan(
					[]proxy.Target{attemptURITarget("A", "10.0.0.9")},
				)),
				opts, loc,
			)

			tcp := newStubTransp()
			tcp.md = sip.TCPMetadata()
			if err := elm.TrackTransport(tcp); err != nil {
				t.Fatalf("elm.TrackTransport(tcp) error = %v, want nil", err)
			}

			var sentMu sync.Mutex
			var udpSends, tcpSends []*sip.RequestEnvelope
			tcp.sendReqHook = func(call sendReqCall, _ int) error {
				if !call.req.Method().Equal(sip.RequestMethodInvite) {
					return nil
				}
				sentMu.Lock()
				tcpSends = append(tcpSends, call.req)
				sentMu.Unlock()
				return nil
			}
			var tooLarge atomic.Bool
			tooLarge.Store(true)
			tp.sendReqHook = func(call sendReqCall, _ int) error {
				if !call.req.Method().Equal(sip.RequestMethodInvite) {
					return nil
				}
				sentMu.Lock()
				udpSends = append(udpSends, call.req)
				sentMu.Unlock()
				if tooLarge.CompareAndSwap(true, false) {
					return sip.ErrMessageTooLarge
				}
				return nil
			}

			admitAttemptReq(t.Context(), t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".attr-switch", laddr, raddr))
			synctest.Wait()

			sentMu.Lock()
			defer sentMu.Unlock()
			if len(udpSends) != 1 || len(tcpSends) != 1 {
				t.Fatalf("size-switch sends = %d UDP/%d TCP, want 1/1", len(udpSends), len(tcpSends))
			}
			udpBranch := attemptTopBranch(t, udpSends[0])
			if tcpBranch := attemptTopBranch(t, tcpSends[0]); tcpBranch != udpBranch {
				t.Fatalf("size-switch branch=%q -> %q, want reused", udpBranch, tcpBranch)
			}

			// The pre-wire transport upgrade is the same logical send: both
			// physical copies carry the identical attempt attribution.
			udpMD, tcpMD := reqStamp(t, udpSends[0]), reqStamp(t, tcpSends[0])
			if udpMD != tcpMD {
				t.Errorf("size-switch stamps = %+v vs %+v, want identical attribution", udpMD, tcpMD)
			}
			if udpMD.Stage != proxy.StageForward || udpMD.TargetID != "A" || udpMD.AttemptID == "" {
				t.Errorf("size-switch stamp = %+v, want a forward-stage stamp of target A", udpMD)
			}
		})
	})

	t.Run("retransmit same attempt", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("attr-retrans", obsLitPlan("A")), opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			fwdIntercept(t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".attr-retrans", laddr, raddr))
			synctest.Wait()

			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound sends=%d, want 1", len(sends))
			}
			first := reqStamp(t, sends[0])
			if first.Stage != proxy.StageForward || first.AttemptID == "" {
				t.Fatalf("initial stamp = %+v, want a forward-stage stamp with an attempt ID", first)
			}

			// The unreliable client transaction retransmits on its own
			// timers: every wire copy stays the same logical attempt.
			time.Sleep(1100 * time.Millisecond)
			synctest.Wait()
			rexs := drainReqs(tp)
			if len(rexs) == 0 {
				t.Fatal("no transaction retransmission observed")
			}
			for i, rex := range rexs {
				if md := reqStamp(t, rex); md != first {
					t.Errorf("retransmission %d stamp = %+v, want unchanged %+v", i, md, first)
				}
			}
			if got := len(rec.kindSeqs(proxy.EventKindForwardAttemptFinished)); got != 1 {
				t.Errorf("attempt observations=%d, want 1 (no new logical attempt)", got)
			}
		})
	})

	t.Run("local final response", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("attr-local", obsLitPlan("A")), opts)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".attr-local", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()

			claim := reqStamp(t, req)
			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound sends=%d, want 1", len(sends))
			}
			// The branch times out without a response: the request is
			// answered by a locally generated 408 that owns no downstream
			// attempt.
			tx := fwdClnTx(t, elm, sends[0])
			if err := tx.Terminate(ctx, sip.NewTransactionTimedOutError()); err != nil {
				t.Fatalf("clnTx.Terminate() error = %v, want nil", err)
			}
			synctest.Wait()

			upstream := drainRess(tp)
			if len(upstream) != 1 {
				t.Fatalf("upstream response sends=%d, want 1 generated response", len(upstream))
			}
			if got := upstream[0].Status(); !got.Equal(sip.ResponseStatusRequestTimeout) {
				t.Fatalf("upstream status = %v, want %v", got, sip.ResponseStatusRequestTimeout)
			}
			md := resStamp(t, upstream[0])
			if md.Stage != proxy.StageUpstream || md.RequestID != claim.RequestID {
				t.Errorf("local response stamp = %+v, want request ID %q at stage upstream", md, claim.RequestID)
			}
			if md.TargetID != "" || md.AttemptID != "" {
				t.Errorf("local response stamp = %+v, want empty target/attempt", md)
			}
			assertWireFree(t, upstream[0])
		})
	})

	t.Run("aggregated final", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("attr-agg", obsLitPlan("A", "B")), opts)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".attr-agg", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()

			claim := reqStamp(t, req)
			sends := drainReqs(tp)
			if len(sends) != 2 {
				t.Fatalf("outbound sends=%d, want 2", len(sends))
			}
			byTarget := make(map[string]*sip.RequestEnvelope, len(sends))
			for _, out := range sends {
				byTarget[targetOf(out)] = out
			}
			txA := fwdClnTx(t, elm, byTarget["A"])
			txB := fwdClnTx(t, elm, byTarget["B"])

			// Two same-class branch finals aggregate: the earliest arrival
			// wins the committed decision, but the forwarded copy invents no
			// single-attempt source for it.
			if err := txA.RecvResponse(ctx, newInRes(t, byTarget["A"], sip.ResponseStatusNotFound)); err != nil {
				t.Fatalf("txA.RecvResponse(404) error = %v, want nil", err)
			}
			synctest.Wait()
			if err := txB.RecvResponse(ctx, newInRes(t, byTarget["B"], sip.ResponseStatusBusyHere)); err != nil {
				t.Fatalf("txB.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()

			upstream := drainRess(tp)
			if len(upstream) != 1 {
				t.Fatalf("upstream response sends=%d, want 1 committed final", len(upstream))
			}
			if got := upstream[0].Status(); !got.Equal(sip.ResponseStatusNotFound) {
				t.Fatalf("upstream status = %v, want %v (earliest same-class arrival)", got, sip.ResponseStatusNotFound)
			}
			md := resStamp(t, upstream[0])
			if md.Stage != proxy.StageUpstream || md.RequestID != claim.RequestID {
				t.Errorf("aggregated final stamp = %+v, want request ID %q at stage upstream", md, claim.RequestID)
			}
			if md.TargetID != "" || md.AttemptID != "" {
				t.Errorf("aggregated final stamp = %+v, want no invented winning attempt", md)
			}

			// The stored inbound candidates keep their own real response
			// attribution in arrival order.
			key, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			rq, ok := prx.LookupRequest(elm, key)
			if !ok {
				t.Fatal("claimed request not indexed by its upstream key")
			}
			snap, err := rq.Snapshot()
			if err != nil {
				t.Fatalf("rq.Snapshot() error = %v, want nil", err)
			}
			if len(snap.Candidates) != 2 {
				t.Fatalf("response candidates=%d, want 2", len(snap.Candidates))
			}
			for i, want := range []struct {
				sts    sip.ResponseStatus
				target string
			}{
				{sip.ResponseStatusNotFound, "A"},
				{sip.ResponseStatusBusyHere, "B"},
			} {
				cand := snap.Candidates[i]
				if got := cand.Status(); !got.Equal(want.sts) {
					t.Errorf("candidate %d status = %v, want %v", i, got, want.sts)
				}
				md := resStamp(t, cand)
				if md.Stage != proxy.StageResponse || md.RequestID != claim.RequestID ||
					md.TargetID != want.target || !strings.HasPrefix(md.AttemptID, want.target+"#") {
					t.Errorf("candidate %d stamp = %+v, want response-stage attribution of target %s",
						i, md, want.target)
				}
			}
			assertWireFree(t, upstream[0])
		})
	})

	t.Run("repeated invite 2xx", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("attr-2xx", obsLitPlan("A")), opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".attr-2xx", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()

			claim := reqStamp(t, req)
			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound sends=%d, want 1", len(sends))
			}
			tx := fwdClnTx(t, elm, sends[0])
			if err := tx.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusOK)); err != nil {
				t.Fatalf("tx.RecvResponse(200) error = %v, want nil", err)
			}
			synctest.Wait()
			// A retransmitted success of the settled attempt still forwards
			// with its real source attribution and reopens no completion or
			// scheduling decision.
			if err := tx.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusOK)); err != nil {
				t.Fatalf("tx.RecvResponse(retransmitted 200) error = %v, want nil", err)
			}
			synctest.Wait()

			upstream := drainRess(tp)
			if len(upstream) != 2 {
				t.Fatalf("upstream response sends=%d, want 2 forwarded successes", len(upstream))
			}
			md1, md2 := resStamp(t, upstream[0]), resStamp(t, upstream[1])
			if md1 != md2 {
				t.Errorf("repeated success stamps = %+v vs %+v, want the same real source", md1, md2)
			}
			if md1.Stage != proxy.StageUpstream || md1.RequestID != claim.RequestID ||
				md1.TargetID != "A" || md1.AttemptID == "" {
				t.Errorf("success stamp = %+v, want upstream-stage attribution of target A", md1)
			}
			counts := rec.counts()
			if counts[proxy.EventKindRequestCompleted] != 1 {
				t.Errorf("completions=%d, want 1 (completion not reopened)", counts[proxy.EventKindRequestCompleted])
			}
			if counts[proxy.EventKindResponseForwarded] != 2 {
				t.Errorf("response observations=%d, want 2", counts[proxy.EventKindResponseForwarded])
			}
		})
	})
}
