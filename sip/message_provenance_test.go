package sip_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

func newProvenanceReqEnvelope() *sip.RequestEnvelope {
	return sip.NewRequestEnvelope(&sip.Request{
		Method: sip.RequestMethodInvite,
		URI: &sip.URI{
			User: sip.MakeUserInfo("bob"),
			Addr: sip.MakeHostAddr("b.example.com"),
		},
		Proto: sip.ProtoVer20(),
	})
}

func newProvenanceResEnvelope() *sip.ResponseEnvelope {
	return sip.NewResponseEnvelope(&sip.Response{Status: sip.ResponseStatusOK, Proto: sip.ProtoVer20()})
}

func TestRequestEnvelope_PrepareForwarded(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name        string
		branch      string
		designation string
		endpoint    bool
		update      bool
	}{
		{name: "no via", designation: "z9hG4bK-local"},
		{name: "upstream only", branch: "z9hG4bK-upstream", designation: "z9hG4bK-local"},
		{name: "designation mismatch", branch: "z9hG4bK-local", designation: "z9hG4bK-other"},
		{name: "designated top", branch: "z9hG4bK-local", designation: "z9hG4bK-local", update: true},
		{name: "missing from tag", branch: "z9hG4bK-local"},
		{name: "unmarked control", endpoint: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := newProvenanceReqEnvelope()
			env.WithMessage(func(r *sip.Request) {
				r.Headers = make(sip.Headers)
				r.Headers.Set(&header.From{URI: r.URI.Clone()})
				if tt.branch != "" {
					r.Headers.PrependVia(header.ViaHop{
						Proto: sip.ProtoVer20(), Transport: "UDP",
						Addr:   sip.MakeHostPortAddr("upstream.example", 5060),
						Params: sip.Values{"branch": {"z9hG4bK-earlier"}},
					})
					r.Headers.PrependVia(header.ViaHop{
						Proto: sip.ProtoVer20(), Transport: "UDP",
						Addr:   sip.MakeHostPortAddr("old.example", 5060),
						Params: sip.Values{"branch": {tt.branch}},
					})
				}
			})
			if !tt.endpoint {
				env.MarkForwarded()
			}
			env.SetForwardedViaBranch(tt.designation)
			var wantURI sip.AnyURI
			var wantHeaders sip.Headers
			env.WithMessage(func(r *sip.Request) { wantURI = r.URI.Clone(); wantHeaders = r.Headers.Clone() })
			addr := sip.MakeHostPortAddr("local.example", 5070)
			env.PrepareVia("TCP", addr)
			env.EnsureFromTag()
			env.WithMessage(func(r *sip.Request) {
				if diff := cmp.Diff(wantURI, r.URI); diff != "" {
					t.Errorf("URI (-want +got): %s", diff)
				}
				if tt.endpoint {
					via, ok := r.Headers.FirstVia()
					if !ok {
						t.Fatal("sip.PrepareVia() did not create endpoint Via")
					}
					branch, _ := via.Branch()
					if !sip.IsRFC3261Branch(branch) || via.Transport != "TCP" || !via.Addr.Equal(addr) {
						t.Errorf("endpoint Via = %+v, want TCP local Via with RFC3261 branch", via)
					}
					from, _ := r.Headers.From()
					tag, _ := from.Tag()
					if tag == "" {
						t.Error("sip.EnsureFromTag() left endpoint tag absent")
					}
					return
				}
				if tt.update {
					via, _ := wantHeaders.FirstVia()
					via.Transport = "TCP"
					via.Addr = addr
				}
				if diff := cmp.Diff(wantHeaders, r.Headers); diff != "" {
					t.Errorf("headers (-want +got): %s", diff)
				}
			})
		})
	}
}

func TestResponseEnvelope_PrepareForwarded(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name                        string
		forwarded, preserve, trying bool
		want                        string
	}{
		{name: "forwarded", forwarded: true},
		{name: "preserve", preserve: true},
		{name: "endpoint", want: "endpoint-tag"},
		{name: "100 control", trying: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := newProvenanceResEnvelope()
			env.WithMessage(func(r *sip.Response) {
				r.Headers = make(sip.Headers)
				r.Headers.Set(&header.To{URI: &sip.URI{Addr: sip.MakeHostAddr("peer.example")}})
				if tt.trying {
					r.Status = sip.ResponseStatusTrying
				}
			})
			if tt.forwarded {
				env.MarkForwarded()
			}
			env.SetPreserveToTag(tt.preserve)
			var want sip.Headers
			env.WithMessage(func(r *sip.Response) { want = r.Headers.Clone() })
			env.EnsureToTag("endpoint-tag")
			env.WithMessage(func(r *sip.Response) {
				to, _ := r.Headers.To()
				tag, _ := to.Tag()
				if tag != tt.want {
					t.Errorf("sip.EnsureToTag() tag = %q, want %q", tag, tt.want)
				}
				if tt.want == "" {
					if diff := cmp.Diff(want, r.Headers); diff != "" {
						t.Errorf("headers (-want +got): %s", diff)
					}
				}
			})
		})
	}
}

func TestMessageEnvelope_Provenance(t *testing.T) {
	t.Parallel()

	t.Run("absent marker reads false", func(t *testing.T) {
		t.Parallel()

		env := newProvenanceReqEnvelope()
		if got := env.Forwarded(); got != false {
			t.Fatalf("env.Forwarded() = %v, want false for absent marker", got)
		}
	})

	t.Run("mark forwarded reads true", func(t *testing.T) {
		t.Parallel()

		env := newProvenanceReqEnvelope()
		env.MarkForwarded()
		if got := env.Forwarded(); got != true {
			t.Errorf("env.Forwarded() = %v, want true after MarkForwarded", got)
		}
	})

	tests := []struct {
		name string
		val  any
		want bool
	}{
		{"false marker", false, false},
		{"string marker", "true", false},
		{"int marker", 1, false},
		{"nil marker", nil, false},
		{"bool true marker", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newProvenanceReqEnvelope()
			env.Metadata().Set("sip.forwarded", tt.val)
			if got := env.Forwarded(); got != tt.want {
				t.Errorf("env.Forwarded() = %v, want %v for marker %#v", got, tt.want, tt.val)
			}
		})
	}

	t.Run("clone keeps independent marker", func(t *testing.T) {
		t.Parallel()

		env := newProvenanceReqEnvelope()
		env.MarkForwarded()

		clone, ok := env.Clone().(*sip.RequestEnvelope)
		if !ok {
			t.Fatalf("env.Clone() = %T, want *sip.RequestEnvelope", env.Clone())
		}
		clone.Metadata().Delete("sip.forwarded")

		if got := env.Forwarded(); got != true {
			t.Errorf("env.Forwarded() = %v, want true after clone marker delete", got)
		}
		if got := clone.Forwarded(); got != false {
			t.Errorf("clone.Forwarded() = %v, want false after clone marker delete", got)
		}

		sibling, ok := env.Clone().(*sip.RequestEnvelope)
		if !ok {
			t.Fatalf("env.Clone() = %T, want *sip.RequestEnvelope", env.Clone())
		}
		if got := sibling.Forwarded(); got != true {
			t.Errorf("sibling.Forwarded() = %v, want true for sibling clone", got)
		}
	})
}

func TestRequestEnvelope_ForwardedViaBranch(t *testing.T) {
	t.Parallel()

	t.Run("absent branch reads empty", func(t *testing.T) {
		t.Parallel()

		env := newProvenanceReqEnvelope()
		if got := env.ForwardedViaBranch(); got != "" {
			t.Fatalf("env.ForwardedViaBranch() = %q, want \"\" for absent branch", got)
		}
	})

	t.Run("set branch reads back", func(t *testing.T) {
		t.Parallel()

		env := newProvenanceReqEnvelope()
		env.SetForwardedViaBranch("z9hG4bK-branch-1")
		if got := env.ForwardedViaBranch(); got != "z9hG4bK-branch-1" {
			t.Errorf("env.ForwardedViaBranch() = %q, want %q", got, "z9hG4bK-branch-1")
		}
	})

	tests := []struct {
		name string
		val  any
	}{
		{"bool branch", true},
		{"int branch", 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newProvenanceReqEnvelope()
			env.Metadata().Set("sip.forwarded_via_branch", tt.val)
			if got := env.ForwardedViaBranch(); got != "" {
				t.Errorf("env.ForwardedViaBranch() = %q, want \"\" for branch %#v", got, tt.val)
			}
		})
	}
}

func TestResponseEnvelope_PreserveToTag(t *testing.T) {
	t.Parallel()

	t.Run("absent marker reads false", func(t *testing.T) {
		t.Parallel()

		env := newProvenanceResEnvelope()
		if got := env.PreservesToTag(); got != false {
			t.Fatalf("env.PreservesToTag() = %v, want false for absent marker", got)
		}
	})

	t.Run("set preserve reads true", func(t *testing.T) {
		t.Parallel()

		env := newProvenanceResEnvelope()
		env.SetPreserveToTag(true)
		if got := env.PreservesToTag(); got != true {
			t.Errorf("env.PreservesToTag() = %v, want true after SetPreserveToTag(true)", got)
		}
	})

	t.Run("set preserve false reads false", func(t *testing.T) {
		t.Parallel()

		env := newProvenanceResEnvelope()
		env.SetPreserveToTag(true)
		env.SetPreserveToTag(false)
		if got := env.PreservesToTag(); got != false {
			t.Errorf("env.PreservesToTag() = %v, want false after SetPreserveToTag(false)", got)
		}
	})

	tests := []struct {
		name string
		val  any
		want bool
	}{
		{"false marker", false, false},
		{"string marker", "true", false},
		{"int marker", 1, false},
		{"nil marker", nil, false},
		{"bool true marker", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newProvenanceResEnvelope()
			env.Metadata().Set("sip.preserve_to_tag", tt.val)
			if got := env.PreservesToTag(); got != tt.want {
				t.Errorf("env.PreservesToTag() = %v, want %v for marker %#v", got, tt.want, tt.val)
			}
		})
	}
}

func TestMessageEnvelope_ProvenanceRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("request provenance survives json round trip", func(t *testing.T) {
		t.Parallel()

		env := newProvenanceReqEnvelope()
		env.MarkForwarded()
		env.SetForwardedViaBranch("z9hG4bK-fwd-1")

		data, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("json.Marshal(env) error = %v, want nil", err)
		}

		var got *sip.RequestEnvelope
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
		}
		if got == nil {
			t.Fatal("json.Unmarshal(data, got) = nil, want non-nil")
		}

		if v := got.Forwarded(); v != true {
			t.Errorf("got.Forwarded() = %v, want true after round trip", v)
		}
		if v := got.ForwardedViaBranch(); v != "z9hG4bK-fwd-1" {
			t.Errorf("got.ForwardedViaBranch() = %q, want %q after round trip", v, "z9hG4bK-fwd-1")
		}
	})

	t.Run("response provenance survives json round trip", func(t *testing.T) {
		t.Parallel()

		env := newProvenanceResEnvelope()
		env.SetPreserveToTag(true)

		data, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("json.Marshal(env) error = %v, want nil", err)
		}

		var got *sip.ResponseEnvelope
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
		}
		if got == nil {
			t.Fatal("json.Unmarshal(data, got) = nil, want non-nil")
		}

		if v := got.PreservesToTag(); v != true {
			t.Errorf("got.PreservesToTag() = %v, want true after round trip", v)
		}
	})

	t.Run("rendered message omits provenance keys", func(t *testing.T) {
		t.Parallel()

		env := newProvenanceReqEnvelope()
		env.MarkForwarded()
		env.SetForwardedViaBranch("z9hG4bK-fwd-1")

		for _, key := range []string{"sip.forwarded", "sip.preserve_to_tag", "sip.forwarded_via_branch"} {
			if strings.Contains(env.String(), key) {
				t.Errorf("env.String() contains provenance key %q, want omitted from SIP rendering", key)
			}
		}

		res := newProvenanceResEnvelope()
		res.SetPreserveToTag(true)
		if strings.Contains(res.String(), "sip.preserve_to_tag") {
			t.Errorf("res.String() contains provenance key %q, want omitted from SIP rendering", "sip.preserve_to_tag")
		}
	})
}
