package sip_test

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestRequest_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		req  *sip.Request
		opts *sip.RenderOptions
		want string
	}{
		{"nil", (*sip.Request)(nil), nil, ""},
		{"zero", &sip.Request{}, nil, "  /\r\n\r\n"},
		{
			"full",
			&sip.Request{
				Method: "INVITE",
				URI: &uri.SIP{
					User: uri.User("bob"),
					Addr: uri.AddrFromHost("b.example.com"),
				},
				Proto: sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Append(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      header.AddrFromHost("a.example.com"),
							Params:    make(header.Values).Append("branch", "qwerty"),
						},
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      header.AddrFromHost("b.example.com"),
							Params:    make(header.Values).Append("branch", "asdf"),
						},
					}).
					Append(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      header.AddrFromHost("c.example.com"),
							Params:    make(header.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.AddrFromHost("a.example.com"),
						},
						Params: make(header.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.AddrFromHost("b.example.com"),
						},
					}).
					Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
					Append(header.CallID("zxc")).
					Append(header.MaxForwards(70)).
					Append(header.Contact{
						{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.AddrFromHostPort("a.example.com", 5060),
							},
						},
					}).
					Append(&header.Any{Name: "X-Custom-Header", Value: "123"}).
					Append(&header.ContentType{
						Type:    "text",
						Subtype: "plain",
					}).
					Append(header.ContentLength(14)).
					Append(&header.Any{Name: "P-Custom-Header", Value: "123"}),
				Body: []byte("Hello world!\r\n"),
			},
			&sip.RenderOptions{Compact: true},
			"INVITE sip:bob@b.example.com SIP/2.0\r\n" +
				"v: SIP/2.0/UDP a.example.com;branch=qwerty, SIP/2.0/UDP b.example.com;branch=asdf\r\n" +
				"v: SIP/2.0/UDP c.example.com;branch=zxcvb\r\n" +
				"f: <sip:alice@a.example.com>;tag=abc\r\n" +
				"t: <sip:bob@b.example.com>\r\n" +
				"i: zxc\r\n" +
				"CSeq: 1 INVITE\r\n" +
				"m: <sip:alice@a.example.com:5060>\r\n" +
				"Max-Forwards: 70\r\n" +
				"c: text/plain\r\n" +
				"l: 14\r\n" +
				"P-Custom-Header: 123\r\n" +
				"X-Custom-Header: 123\r\n" +
				"\r\n" +
				"Hello world!\r\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.req.Render(c.opts)
			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Errorf("req.Render(opts) = %q, want %q\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestRequest_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		req     *sip.Request
		wantRes string
		wantErr error
	}{
		{"nil", (*sip.Request)(nil), "", nil},
		{"zero", &sip.Request{}, "  /\r\n\r\n", nil},
		{
			"full",
			&sip.Request{
				Method: "INVITE",
				URI: &uri.SIP{
					User: uri.User("bob"),
					Addr: uri.AddrFromHost("b.example.com"),
				},
				Proto: sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Append(header.ContentLength(14)),
				Body: []byte("Hello world!\r\n"),
			},
			"INVITE sip:bob@b.example.com SIP/2.0\r\n" +
				"Content-Length: 14\r\n" +
				"\r\n" +
				"Hello world!\r\n",
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder

			_, err := c.req.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Fatalf("req.RenderTo(sb, nil) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}

			got := sb.String()
			if diff := cmp.Diff(got, c.wantRes); diff != "" {
				t.Fatalf("sb.String() = %q, want %q\ndiff (-got +want):\n%v", got, c.wantRes, diff)
			}
		})
	}
}

func TestRequest_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		req  *sip.Request
		want string
	}{
		{"nil", (*sip.Request)(nil), "<nil>"},
		{"zero", &sip.Request{}, "  /"},
		{
			"full",
			&sip.Request{
				Method: "INVITE",
				URI: &uri.SIP{
					User: uri.User("bob"),
					Addr: uri.AddrFromHost("b.example.com"),
				},
				Proto: sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Append(header.CallID("zxc")),
			},
			"INVITE sip:bob@b.example.com SIP/2.0",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.req.String()
			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Errorf("req.String() = %q, want %q\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestRequest_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		req  *sip.Request
		val  any
		want bool
	}{
		{"nil ptr to nil", (*sip.Request)(nil), nil, false},
		{"nil ptr to nil ptr", (*sip.Request)(nil), (*sip.Request)(nil), true},
		{"zero ptr to nil ptr", &sip.Request{}, (*sip.Request)(nil), false},
		{"nil ptr to zero ptr", (*sip.Request)(nil), &sip.Request{}, false},
		{"zero ptr to zero ptr", &sip.Request{}, &sip.Request{}, true},
		{"zero ptr to zero val", &sip.Request{}, sip.Request{}, true},
		{
			"not match 1",
			&sip.Request{Method: sip.RequestMethodInvite},
			&sip.Request{Method: sip.RequestMethodBye},
			false,
		},
		{
			"not match 2",
			&sip.Request{
				Method: sip.RequestMethodInvite,
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
			},
			&sip.Request{
				Method: sip.RequestMethodBye,
				URI: &uri.SIP{
					User: uri.User("bob"),
					Addr: uri.AddrFromHost("example.com"),
				},
			},
			false,
		},
		{
			"not match 3",
			&sip.Request{
				Method: sip.RequestMethodInvite,
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
				Proto: sip.ProtoVer20(),
			},
			&sip.Request{
				Method: sip.RequestMethodBye,
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
				Proto: sip.ProtoInfo{Name: "Qwe", Version: "1.0"},
			},
			false,
		},
		{
			"not match 4",
			&sip.Request{
				Method: sip.RequestMethodInvite,
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
				Proto: sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Set(&header.From{
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.AddrFromHost("example.com"),
						},
						Params: make(header.Values).Append("tag", "abc"),
					}),
			},
			&sip.Request{
				Method: sip.RequestMethodBye,
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
				Proto: sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Set(&header.From{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.AddrFromHost("localhost"),
						},
						Params: make(header.Values).Append("tag", "abc"),
					}),
			},
			false,
		},
		{
			"not match 5",
			&sip.Request{
				Method: sip.RequestMethodInvite,
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
				Proto: sip.ProtoVer20(),
				Body:  []byte("Hello world!\r\n"),
			},
			&sip.Request{
				Method: sip.RequestMethodBye,
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
				Proto: sip.ProtoVer20(),
				Body:  []byte("Hello world!"),
			},
			false,
		},
		{
			"match",
			&sip.Request{
				Method: sip.RequestMethodInvite,
				URI: &uri.SIP{
					User: uri.User("bob"),
					Addr: uri.AddrFromHost("b.example.com"),
				},
				Proto: sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Append(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      header.AddrFromHost("c.example.com"),
							Params:    make(header.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.AddrFromHost("a.example.com"),
						},
						Params: make(header.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.AddrFromHost("b.example.com"),
						},
					}).
					Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
					Append(header.CallID("zxc")),
				Body: []byte("Hello world!\r\n"),
			},
			sip.Request{
				Method: sip.RequestMethodInvite,
				URI: &uri.SIP{
					User: uri.User("bob"),
					Addr: uri.AddrFromHost("b.example.com"),
				},
				Proto: sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Append(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      header.AddrFromHost("c.example.com"),
							Params:    make(header.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.AddrFromHost("a.example.com"),
						},
						Params: make(header.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.AddrFromHost("b.example.com"),
						},
					}).
					Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
					Append(header.CallID("zxc")),
				Body: []byte("Hello world!\r\n"),
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.req.Equal(c.val); got != c.want {
				t.Errorf("req.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRequest_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		req  *sip.Request
		want bool
	}{
		{"nil", (*sip.Request)(nil), false},
		{"zero", &sip.Request{}, false},
		{"invalid 1", &sip.Request{Method: sip.RequestMethodInvite}, false},
		{
			"invalid 2",
			&sip.Request{
				Method: sip.RequestMethodInvite,
				URI:    &uri.SIP{Addr: uri.AddrFromHostPort("example.com", 5060)},
			},
			false,
		},
		{
			"invalid 3",
			&sip.Request{
				Method: sip.RequestMethodInvite,
				URI: &uri.SIP{
					User: uri.User("bob"),
					Addr: uri.AddrFromHost("b.example.com"),
				},
				Proto: sip.ProtoVer20(),
			},
			false,
		},
		{
			"valid",
			&sip.Request{
				Method: sip.RequestMethodInvite,
				URI: &uri.SIP{
					User: uri.User("bob"),
					Addr: uri.AddrFromHost("b.example.com"),
				},
				Proto: sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Append(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      header.AddrFromHost("c.example.com"),
							Params:    make(header.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.AddrFromHost("a.example.com"),
						},
						Params: make(header.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.AddrFromHost("b.example.com"),
						},
					}).
					Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
					Append(header.CallID("zxc")).
					Append(header.MaxForwards(70)),
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.req.IsValid(); got != c.want {
				t.Errorf("req.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRequest_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		req  *sip.Request
	}{
		{"nil", nil},
		{"zero", &sip.Request{}},
		{
			"full",
			&sip.Request{
				Method: sip.RequestMethodInvite,
				URI: &uri.SIP{
					User: uri.User("bob"),
					Addr: uri.AddrFromHost("b.example.com"),
				},
				Proto: sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Append(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      header.AddrFromHost("c.example.com"),
							Params:    make(header.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.AddrFromHost("a.example.com"),
						},
						Params: make(header.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.AddrFromHost("b.example.com"),
						},
					}).
					Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
					Append(header.CallID("zxc")).
					Append(header.MaxForwards(70)),
				Body: []byte("Hello world!\r\n"),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.req.Clone()
			if c.req == nil {
				if got != nil {
					t.Errorf("req.Clone() = %+v, want nil", got)
				}
				return
			}

			if diff := cmp.Diff(got, c.req); diff != "" {
				t.Errorf("req.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.req, diff)
			}
		})
	}
}

func TestRequest_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		req  *sip.Request
	}{
		{
			name: "nil",
			req:  (*sip.Request)(nil),
		},
		{
			name: "zero",
			req:  &sip.Request{},
		},
		{
			name: "with uri headers body",
			req: &sip.Request{
				Method: sip.RequestMethodInvite,
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
					Params: make(uri.Values).
						Append("transport", "tcp").
						Append("ttl", "10"),
					Headers: make(uri.Values).
						Append("subject", "test"),
				},
				Proto: sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Set(
						&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.AddrFromHost("example.com"),
							},
							Params: make(header.Values).Set("tag", "abc"),
						},
						&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.AddrFromHost("example.net"),
							},
						},
						&header.CSeq{SeqNum: 42, Method: sip.RequestMethodInvite},
						header.CallID("call-42"),
						header.MaxForwards(70),
					).
					Append(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      header.AddrFromHost("proxy.example.com"),
							Params:    make(header.Values).Set("branch", "z9hG4bK-4321"),
						},
					}).
					Append(&header.ContentType{Type: "application", Subtype: "sdp"}).
					Append(header.ContentLength(4)),
				Body: []byte("body"),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.req)
			if err != nil {
				t.Fatalf("json.Marshal(req) error = %v, want nil", err)
			}

			var got *sip.Request
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.req); diff != "" {
				t.Errorf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%s", got, c.req, diff)
			}
		})
	}
}

func TestRequest_NewResponse(t *testing.T) {
	t.Parallel()

	buildBaseHeaders := func(to *header.To) sip.Headers {
		hdrs := make(sip.Headers).
			Append(header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
					Addr:      header.AddrFromHost("proxy.example.com"),
					Params:    make(header.Values).Set("branch", "z9hG4bK-req"),
				},
			}).
			Append(&header.From{
				URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.AddrFromHost("example.com")},
				Params: make(header.Values).Set("tag", "from-tag"),
			}).
			Append(to).
			Append(&header.CSeq{SeqNum: 42, Method: "INVITE"}).
			Append(header.CallID("call-123"))

		return hdrs
	}

	t.Run("applies options and copies request data", func(t *testing.T) {
		t.Parallel()

		to := &header.To{
			URI: &uri.SIP{User: uri.User("bob"), Addr: uri.AddrFromHost("example.net")},
		}
		req := &sip.Request{
			Proto:   sip.ProtoVer20(),
			Headers: buildBaseHeaders(to),
		}

		hdrs := make(sip.Headers).
			Append(header.Via{{Proto: sip.ProtoVer20(), Transport: "UDP", Addr: header.AddrFromHost("extra.example.com")}}).
			Append(&header.Any{Name: "X-Trace-Id", Value: "42"}).
			Append(&header.Any{Name: "X-Extra", Value: "first"}).
			Append(&header.Any{Name: "X-Extra", Value: "second"})
		body := []byte("payload")
		reason := sip.ResponseReason("Accepted")
		locTag := "local-tag"

		res, err := req.NewResponse(sip.ResponseStatusOK, &sip.ResponseOptions{
			Reason:   reason,
			Headers:  hdrs,
			Body:     body,
			LocalTag: locTag,
		})
		if err != nil {
			t.Fatalf("req.NewResponse(200, opts) error = %v, want = nil", err)
		}

		if got, want := res.Status, sip.ResponseStatusOK; got != want {
			t.Fatalf("res.Status = %v, want %v", got, want)
		}

		if got, want := res.Reason, reason; got != want {
			t.Fatalf("res.Reason = %v, want %v", got, want)
		}

		if got, want := res.Proto, req.Proto; got != want {
			t.Fatalf("res.Proto = %#v, want %#v", got, want)
		}

		if diff := cmp.Diff(res.Body, body); diff != "" {
			t.Fatalf("res.Body mismatch (-got +want):\n%v", diff)
		}

		if viaCount := len(res.Headers.Get("Via")); viaCount != len(req.Headers.Get("Via")) {
			t.Fatalf("unexpected Via count: got %d, want %d", viaCount, len(req.Headers.Get("Via")))
		}

		if foo := res.Headers.Get("X-Trace-Id"); len(foo) != 1 {
			t.Fatalf("expected 1 X-Trace-Id, got %d", len(foo))
		} else if v, ok := foo[0].(*header.Any); !ok || v.Value != "42" {
			t.Fatalf("X-Trace-Id value = %q, want %q", v.Value, "42")
		}

		if extras := res.Headers.Get("X-Extra"); len(extras) != 2 {
			t.Fatalf("expected 2 X-Extra headers, got %d", len(extras))
		}

		toHdr, _ := res.Headers.To()
		if toTag, _ := toHdr.Tag(); toTag != locTag {
			t.Fatalf("To tag = %q, want %q", toTag, locTag)
		}

		res.Headers.Append(&header.Any{Name: "X-New", Value: "value"})

		if req.Headers.Has("X-New") {
			t.Fatal("request headers mutated by response")
		}
	})

	t.Run("preserves existing to tag", func(t *testing.T) {
		t.Parallel()

		to := &header.To{
			URI:    &uri.SIP{User: uri.User("bob"), Addr: uri.AddrFromHost("example.net")},
			Params: make(header.Values).Set("tag", "existing"),
		}
		req := &sip.Request{
			Proto:   sip.ProtoVer20(),
			Headers: buildBaseHeaders(to),
		}

		res, err := req.NewResponse(sip.ResponseStatusOK, &sip.ResponseOptions{
			LocalTag: "ignored-tag",
		})
		if err != nil {
			t.Fatalf("req.NewResponse(200, opts) error = %v, want nil", err)
		}

		toHdr, _ := res.Headers.To()
		if toTag, _ := toHdr.Tag(); toTag != "existing" {
			t.Fatalf("To tag = %q, want %q", toTag, "existing")
		}
	})

	t.Run("trying response leaves tag unset", func(t *testing.T) {
		t.Parallel()

		to := &header.To{
			URI: &uri.SIP{User: uri.User("bob"), Addr: uri.AddrFromHost("example.net")},
		}
		req := &sip.Request{
			Proto:   sip.ProtoVer20(),
			Headers: buildBaseHeaders(to),
		}

		reason := sip.ResponseReason("Trying")

		res, err := req.NewResponse(sip.ResponseStatusTrying, &sip.ResponseOptions{
			Reason: reason,
		})
		if err != nil {
			t.Fatalf("req.NewResponse(486, opts) error = %v, want nil", err)
		}

		if got, want := res.Reason, reason; got != want {
			t.Fatalf("res.Reason = %v, want %v", got, want)
		}

		toHdr, _ := res.Headers.To()
		if toTag, _ := toHdr.Tag(); toTag != "" {
			t.Fatalf("To tag unexpectedly set: %q", toTag)
		}
	})
}

func TestInboundRequestEnvelope_RoundTripJSON(t *testing.T) {
	t.Parallel()

	newEnvelope := func(tb testing.TB) *sip.InboundRequestEnvelope {
		tb.Helper()

		req := &sip.Request{
			Method: sip.RequestMethodInvite,
			URI: &uri.SIP{
				User: uri.User("alice"),
				Addr: uri.AddrFromHost("example.com"),
			},
			Proto: sip.ProtoVer20(),
			Headers: make(sip.Headers).
				Set(
					&header.From{
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.AddrFromHost("example.com"),
						},
						Params: make(header.Values).Set("tag", "abc"),
					},
					&header.To{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.AddrFromHost("example.net"),
						},
					},
					&header.CSeq{SeqNum: 7, Method: sip.RequestMethodInvite},
					header.CallID("call-7"),
				).
				Append(header.ContentLength(4)),
			Body: []byte("body"),
		}

		env, err := sip.NewInboundRequestEnvelope(
			req,
			sip.TransportProto("UDP"),
			netip.MustParseAddrPort("192.0.2.10:5060"),
			netip.MustParseAddrPort("198.51.100.20:5090"),
		)
		if err != nil {
			tb.Fatalf("sip.NewInboundRequestEnvelope(req, ...) error = %v, want nil", err)
		}

		env.Metadata().Set("trace_id", "inbound-req").Set("authenticated", true)

		return env
	}

	cases := []struct {
		name string
		env  *sip.InboundRequestEnvelope
	}{
		{"nil", nil},
		{"full", newEnvelope(t)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.env)
			if err != nil {
				t.Fatalf("json.Marshal(env) error = %v, want nil", err)
			}

			var got *sip.InboundRequestEnvelope
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if c.env == nil {
				if got != nil {
					t.Errorf("json.Unmarshal(null, got) = %+v, want nil", got)
				}
				return
			}

			if got == nil {
				t.Fatal("json.Unmarshal(data, got) = nil, want non-nil")
			}

			if diff := cmp.Diff(got.Message(), c.env.Message()); diff != "" {
				t.Errorf("got.Message() = %+v, want %+v\ndiff (-got +want):\n%s", got.Message(), c.env.Message(), diff)
			}

			if diff := cmp.Diff(got.Transport(), c.env.Transport()); diff != "" {
				t.Errorf("got.Transport() = %q, want %q\ndiff (-got +want):\n%s", got.Transport(), c.env.Transport(), diff)
			}

			if got.LocalAddr() != c.env.LocalAddr() {
				t.Errorf("got.LocalAddr() = %v, want %v", got.LocalAddr(), c.env.LocalAddr())
			}

			if got.RemoteAddr() != c.env.RemoteAddr() {
				t.Errorf("got.RemoteAddr() = %v, want %v", got.RemoteAddr(), c.env.RemoteAddr())
			}

			if !got.MessageTime().Equal(c.env.MessageTime()) {
				t.Errorf("got.MessageTime() = %v, want %v", got.MessageTime(), c.env.MessageTime())
			}

			gotMeta := got.Metadata()

			wantMeta := c.env.Metadata()
			if gotMeta == nil || wantMeta == nil {
				t.Fatalf("got.Metadata() = %v, want non-nil and want.Metadata() = %v", gotMeta, wantMeta)
			}

			for _, key := range []string{"trace_id", "authenticated"} {
				gotVal, gotOK := gotMeta.Get(key)

				wantVal, wantOK := wantMeta.Get(key)
				if diff := cmp.Diff(gotOK, wantOK); diff != "" {
					t.Errorf("got.Metadata().Get(%q) ok = %v, want %v\ndiff (-got +want):\n%s", key, gotOK, wantOK, diff)
				}

				if diff := cmp.Diff(gotVal, wantVal); diff != "" {
					t.Errorf("got.Metadata().Get(%q) = %+v, want %+v\ndiff (-got +want):\n%s", key, gotVal, wantVal, diff)
				}
			}

			roundData, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("json.Marshal(got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(string(roundData), string(data)); diff != "" {
				t.Errorf("round-trip data mismatch: got = %s, want %s\ndiff (-got +want):\n%s", roundData, data, diff)
			}
		})
	}
}

func TestOutboundRequestEnvelope_RoundTripJSON(t *testing.T) {
	t.Parallel()

	newEnvelope := func(tb testing.TB) *sip.OutboundRequestEnvelope {
		tb.Helper()

		req := &sip.Request{
			Method: sip.RequestMethodAck,
			URI: &uri.SIP{
				User: uri.User("bob"),
				Addr: uri.AddrFromHost("example.net"),
			},
			Proto: sip.ProtoVer20(),
			Headers: make(sip.Headers).
				Set(
					&header.From{
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.AddrFromHost("example.com"),
						},
						Params: make(header.Values).Set("tag", "local"),
					},
					&header.To{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.AddrFromHost("example.net"),
						},
						Params: make(header.Values).Set("tag", "remote"),
					},
					&header.CSeq{SeqNum: 8, Method: sip.RequestMethodAck},
					header.CallID("call-8"),
				).
				Append(header.ContentLength(0)),
		}

		env, err := sip.NewOutboundRequestEnvelope(req)
		if err != nil {
			tb.Fatalf("sip.NewOutboundRequestEnvelope(req) error = %v, want nil", err)
		}

		env.SetTransport(sip.TransportProto("TCP"))
		env.SetLocalAddr(netip.MustParseAddrPort("203.0.113.10:5070"))
		env.SetRemoteAddr(netip.MustParseAddrPort("203.0.113.20:5080"))
		env.Metadata().Set("trace_id", "outbound-req").Set("retransmit", false)

		return env
	}

	cases := []struct {
		name string
		env  *sip.OutboundRequestEnvelope
	}{
		{"nil", nil},
		{"full", newEnvelope(t)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.env)
			if err != nil {
				t.Fatalf("json.Marshal(env) error = %v, want nil", err)
			}

			if c.env == nil {
				var got *sip.OutboundRequestEnvelope
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
				}

				if got != nil {
					t.Errorf("json.Unmarshal(null, got) = %+v, want nil", got)
				}

				return
			}

			got, err := sip.NewOutboundRequestEnvelope(&sip.Request{})
			if err != nil {
				t.Fatalf("sip.NewOutboundRequestEnvelope(&sip.Request{}) error = %v, want nil", err)
			}

			if err := json.Unmarshal(data, got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got.Message(), c.env.Message()); diff != "" {
				t.Errorf("got.Message() = %+v, want %+v\ndiff (-got +want):\n%s", got.Message(), c.env.Message(), diff)
			}

			if diff := cmp.Diff(got.Transport(), c.env.Transport()); diff != "" {
				t.Errorf("got.Transport() = %q, want %q\ndiff (-got +want):\n%s", got.Transport(), c.env.Transport(), diff)
			}

			if got.LocalAddr() != c.env.LocalAddr() {
				t.Errorf("got.LocalAddr() = %v, want %v", got.LocalAddr(), c.env.LocalAddr())
			}

			if got.RemoteAddr() != c.env.RemoteAddr() {
				t.Errorf("got.RemoteAddr() = %v, want %v", got.RemoteAddr(), c.env.RemoteAddr())
			}

			if !got.MessageTime().Equal(c.env.MessageTime()) {
				t.Errorf("got.MessageTime() = %v, want %v", got.MessageTime(), c.env.MessageTime())
			}

			gotMeta := got.Metadata()

			wantMeta := c.env.Metadata()
			if gotMeta == nil || wantMeta == nil {
				t.Fatalf("got.Metadata() = %v, want non-nil and want.Metadata() = %v", gotMeta, wantMeta)
			}

			for _, key := range []string{"trace_id", "retransmit"} {
				gotVal, gotOK := gotMeta.Get(key)

				wantVal, wantOK := wantMeta.Get(key)
				if diff := cmp.Diff(gotOK, wantOK); diff != "" {
					t.Errorf("got.Metadata().Get(%q) ok = %v, want %v\ndiff (-got +want):\n%s", key, gotOK, wantOK, diff)
				}

				if diff := cmp.Diff(gotVal, wantVal); diff != "" {
					t.Errorf("got.Metadata().Get(%q) = %+v, want %+v\ndiff (-got +want):\n%s", key, gotVal, wantVal, diff)
				}
			}

			roundData, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("json.Marshal(got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(string(roundData), string(data)); diff != "" {
				t.Errorf("round-trip data mismatch: got = %s, want %s\ndiff (-got +want):\n%s", roundData, data, diff)
			}
		})
	}
}
