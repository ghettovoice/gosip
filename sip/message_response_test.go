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
)

func TestResponse_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		res  *sip.Response
		want string
	}{
		{"nil", (*sip.Response)(nil), ""},
		{"zero", &sip.Response{}, "/ 0 \r\n\r\n"},
		{
			"full",
			&sip.Response{
				Status: 200,
				Reason: "OK",
				Proto:  sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Append(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      sip.AddrFromHost("a.example.com"),
							Params:    make(sip.Values).Append("branch", "qwerty"),
						},
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      sip.AddrFromHost("b.example.com"),
							Params:    make(sip.Values).Append("branch", "asdf"),
						},
					}).
					Append(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      sip.AddrFromHost("c.example.com"),
							Params:    make(sip.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &sip.URI{
							User: sip.UserWithName("alice"),
							Addr: sip.AddrFromHost("a.example.com"),
						},
						Params: make(sip.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &sip.URI{
							User: sip.UserWithName("bob"),
							Addr: sip.AddrFromHost("b.example.com"),
						},
						Params: make(sip.Values).Append("tag", "def"),
					}).
					Append(&header.Any{Name: "P-Custom-Header", Value: "321"}).
					Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
					Append(header.CallID("zxc")).
					Append(sip.DefaultMaxForwards).
					Append(header.Contact{
						{
							URI: &sip.URI{
								User: sip.UserWithName("bob"),
								Addr: sip.AddrFromHostPort("b.example.com", 5060),
							},
						},
					}).
					Append(&header.Any{Name: "X-Custom-Header", Value: "123"}).
					Append(&header.ContentType{
						Type:    "text",
						Subtype: "plain",
					}).
					Append(header.ContentLength(6)),
				Body: []byte("done\r\n"),
			},
			"SIP/2.0 200 OK\r\n" +
				"Via: SIP/2.0/UDP a.example.com;branch=qwerty, SIP/2.0/UDP b.example.com;branch=asdf\r\n" +
				"Via: SIP/2.0/UDP c.example.com;branch=zxcvb\r\n" +
				"From: <sip:alice@a.example.com>;tag=abc\r\n" +
				"To: <sip:bob@b.example.com>;tag=def\r\n" +
				"Call-ID: zxc\r\n" +
				"CSeq: 1 INVITE\r\n" +
				"Contact: <sip:bob@b.example.com:5060>\r\n" +
				"Max-Forwards: 70\r\n" +
				"Content-Type: text/plain\r\n" +
				"Content-Length: 6\r\n" +
				"P-Custom-Header: 321\r\n" +
				"X-Custom-Header: 123\r\n" +
				"\r\n" +
				"done\r\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.res.Render()
			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Errorf("res.Render() = %q, want %q\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestResponse_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		res     *sip.Response
		wantRes string
		wantErr error
	}{
		{"nil", (*sip.Response)(nil), "", nil},
		{"zero", &sip.Response{}, "/ 0 \r\n\r\n", nil},
		{
			"full",
			&sip.Response{
				Proto:  sip.ProtoVer20(),
				Status: 200,
				Reason: "OK",
				Headers: make(sip.Headers).
					Append(header.ContentLength(14)),
				Body: []byte("Hello world!\r\n"),
			},
			"SIP/2.0 200 OK\r\n" +
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

			_, err := c.res.RenderTo(&sb)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Fatalf("res.RenderTo(&sb) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}

			got := sb.String()
			if diff := cmp.Diff(got, c.wantRes); diff != "" {
				t.Fatalf("sb.String() = %q, want %q\ndiff (-got +want):\n%v", got, c.wantRes, diff)
			}
		})
	}
}

func TestResponse_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		res  *sip.Response
		want string
	}{
		{"nil", (*sip.Response)(nil), "<nil>"},
		{"zero", &sip.Response{}, "/ 0 "},
		{
			"full",
			&sip.Response{
				Proto:  sip.ProtoVer20(),
				Status: 200,
				Reason: "OK",
				Headers: make(sip.Headers).
					Append(header.ContentLength(14)),
				Body: []byte("Hello world!\r\n"),
			},
			"SIP/2.0 200 OK",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.res.String()
			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Errorf("res.String() = %q, want %q\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestResponse_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		res  *sip.Response
		val  any
		want bool
	}{
		{"nil ptr to nil", (*sip.Response)(nil), nil, false},
		{"nil ptr to nil ptr", (*sip.Response)(nil), (*sip.Response)(nil), true},
		{"zero ptr to nil ptr", &sip.Response{}, (*sip.Response)(nil), false},
		{"nil ptr to zero ptr", (*sip.Response)(nil), &sip.Response{}, false},
		{"zero ptr to zero ptr", &sip.Response{}, &sip.Response{}, true},
		{"zero ptr to zero val", &sip.Response{}, sip.Response{}, true},
		{
			"not match 1",
			&sip.Response{Status: 200},
			&sip.Response{Status: 404},
			false,
		},
		{
			"not match 2",
			&sip.Response{
				Proto:  sip.ProtoVer20(),
				Status: 200,
			},
			&sip.Response{
				Proto:  sip.ProtoInfo{Name: "Qwe", Version: "1.0"},
				Status: 200,
			},
			false,
		},
		{
			"not match 3",
			&sip.Response{
				Proto:  sip.ProtoVer20(),
				Status: 200,
				Reason: "OK",
			},
			&sip.Response{
				Proto:  sip.ProtoVer20(),
				Status: 200,
				Reason: "Accepted",
			},
			false,
		},
		{
			"not match 4",
			&sip.Response{
				Proto:  sip.ProtoVer20(),
				Status: 200,
				Reason: "OK",
				Headers: make(sip.Headers).
					Set(&header.From{
						URI: &sip.URI{
							User: sip.UserWithName("alice"),
							Addr: sip.AddrFromHost("example.com"),
						},
						Params: make(sip.Values).Append("tag", "abc"),
					}),
			},
			&sip.Response{
				Proto:  sip.ProtoVer20(),
				Status: 200,
				Reason: "OK",
				Headers: make(sip.Headers).
					Set(&header.From{
						URI: &sip.URI{
							User: sip.UserWithName("bob"),
							Addr: sip.AddrFromHost("localhost"),
						},
						Params: make(sip.Values).Append("tag", "abc"),
					}),
			},
			false,
		},
		{
			"not match 5",
			&sip.Response{
				Proto:  sip.ProtoVer20(),
				Status: 200,
				Reason: "OK",
				Body:   []byte("Hello world!\r\n"),
			},
			&sip.Response{
				Proto:  sip.ProtoVer20(),
				Status: 200,
				Reason: "OK",
				Body:   []byte("Chao!"),
			},
			false,
		},
		{
			"match",
			&sip.Response{
				Proto:  sip.ProtoVer20(),
				Status: 200,
				Reason: "OK",
				Headers: make(sip.Headers).
					Append(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      sip.AddrFromHost("c.example.com"),
							Params:    make(sip.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &sip.URI{
							User: sip.UserWithName("alice"),
							Addr: sip.AddrFromHost("a.example.com"),
						},
						Params: make(sip.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &sip.URI{
							User: sip.UserWithName("bob"),
							Addr: sip.AddrFromHost("b.example.com"),
						},
					}).
					Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
					Append(header.CallID("zxc")),
				Body: []byte("Hello world!\r\n"),
			},
			sip.Response{
				Proto:  sip.ProtoVer20(),
				Status: 200,
				Reason: "ok",
				Headers: make(sip.Headers).
					Append(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      sip.AddrFromHost("c.example.com"),
							Params:    make(sip.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &sip.URI{
							User: sip.UserWithName("alice"),
							Addr: sip.AddrFromHost("a.example.com"),
						},
						Params: make(sip.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &sip.URI{
							User: sip.UserWithName("bob"),
							Addr: sip.AddrFromHost("b.example.com"),
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

			if got := c.res.Equal(c.val); got != c.want {
				t.Errorf("res.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestResponse_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		res  *sip.Response
		want bool
	}{
		{"nil", (*sip.Response)(nil), false},
		{"zero", &sip.Response{}, false},
		{"invalid 1", &sip.Response{Status: 200}, false},
		{"invalid 2", &sip.Response{Status: 200, Reason: "OK", Proto: sip.ProtoVer20()}, false},
		{"invalid 3", &sip.Response{Status: 20, Reason: "OK", Proto: sip.ProtoVer20()}, false},
		{
			"valid",
			&sip.Response{
				Status: 100,
				Proto:  sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Append(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      sip.AddrFromHost("c.example.com"),
							Params:    make(sip.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &sip.URI{
							User: sip.UserWithName("alice"),
							Addr: sip.AddrFromHost("a.example.com"),
						},
						Params: make(sip.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &sip.URI{
							User: sip.UserWithName("bob"),
							Addr: sip.AddrFromHost("b.example.com"),
						},
					}).
					Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
					Append(header.CallID("zxc")).
					Append(sip.DefaultMaxForwards),
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.res.IsValid(); got != c.want {
				t.Errorf("res.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestResponse_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		res  *sip.Response
	}{
		{"nil", nil},
		{"zero", &sip.Response{}},
		{
			"full",
			&sip.Response{
				Status: 200,
				Reason: "OK",
				Proto:  sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Append(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      sip.AddrFromHost("c.example.com"),
							Params:    make(sip.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &sip.URI{
							User: sip.UserWithName("alice"),
							Addr: sip.AddrFromHost("a.example.com"),
						},
						Params: make(sip.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &sip.URI{
							User: sip.UserWithName("bob"),
							Addr: sip.AddrFromHost("b.example.com"),
						},
					}).
					Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
					Append(header.CallID("zxc")).
					Append(sip.DefaultMaxForwards),
				Body: []byte("Hello world!\r\n"),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.res.Clone()
			if c.res == nil {
				if got != nil {
					t.Errorf("res.Clone() = %+v, want nil", got)
				}
				return
			}

			if diff := cmp.Diff(got, c.res); diff != "" {
				t.Errorf("res.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.res, diff)
			}
		})
	}
}

func TestResponse_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		res  *sip.Response
	}{
		{
			name: "nil",
			res:  (*sip.Response)(nil),
		},
		{
			name: "zero",
			res:  &sip.Response{},
		},
		{
			name: "with headers body metadata",
			res: &sip.Response{
				Status: sip.ResponseStatusOK,
				Reason: sip.ResponseReason("OK"),
				Proto:  sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Set(
						header.Via{
							{
								Proto:     sip.ProtoVer20(),
								Transport: "UDP",
								Addr:      sip.AddrFromHost("proxy.example.com"),
								Params:    make(sip.Values).Set("branch", "z9hG4bK-9876"),
							},
						},
						&header.From{
							URI: &sip.URI{
								User: sip.UserWithName("alice"),
								Addr: sip.AddrFromHost("example.com"),
							},
							Params: make(sip.Values).Set("tag", "abc"),
						},
						&header.To{
							URI: &sip.URI{
								User: sip.UserWithName("bob"),
								Addr: sip.AddrFromHost("example.net"),
							},
						},
						&header.CSeq{SeqNum: 21, Method: sip.RequestMethodInvite},
						header.CallID("call-21"),
					).
					Append(&header.ContentType{Type: "application", Subtype: "sdp"}).
					Append(header.ContentLength(7)),
				Body: []byte("reply\n"),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.res)
			if err != nil {
				t.Fatalf("json.Marshal(res) error = %v, want nil", err)
			}

			var got sip.Response
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			var want *sip.Response
			switch c.res {
			case nil:
				want = &sip.Response{}
			default:
				want = c.res
			}

			if diff := cmp.Diff(&got, want); diff != "" {
				t.Errorf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%s", &got, want, diff)
			}
		})
	}
}

func TestResponseEnvelope_RoundTripJSONWithAddr(t *testing.T) {
	t.Parallel()

	newEnvelope := func(tb testing.TB) *sip.ResponseEnvelope {
		tb.Helper()

		res := &sip.Response{
			Status: sip.ResponseStatusOK,
			Proto:  sip.ProtoVer20(),
			Headers: make(sip.Headers).
				Set(
					header.Via{{
						Proto:     sip.ProtoVer20(),
						Transport: "UDP",
						Addr:      sip.AddrFromHost("proxy.example.com"),
						Params:    make(sip.Values).Set("branch", "z9hG4bK-res-1"),
					}},
					&header.From{
						URI: &sip.URI{
							User: sip.UserWithName("alice"),
							Addr: sip.AddrFromHost("example.com"),
						},
						Params: make(sip.Values).Set("tag", "from-tag"),
					},
					&header.To{
						URI: &sip.URI{
							User: sip.UserWithName("bob"),
							Addr: sip.AddrFromHost("example.net"),
						},
						Params: make(sip.Values).Set("tag", "to-tag"),
					},
					&header.CSeq{SeqNum: 9, Method: sip.RequestMethodInvite},
					header.CallID("call-9"),
				).
				Append(header.ContentLength(5)),
			Body: []byte("reply"),
		}

		env := sip.NewResponseEnvelope(res).
			SetTransport(sip.UDPMetadata()).
			SetLocalAddr(netip.MustParseAddrPort("192.0.2.30:5060")).
			SetRemoteAddr(netip.MustParseAddrPort("198.51.100.40:5090"))
		env.Metadata().
			Set("trace_id", "inbound-res").
			Set("validated", true)

		return env
	}

	cases := []struct {
		name string
		env  *sip.ResponseEnvelope
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

			var got *sip.ResponseEnvelope
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
				t.Errorf("got.Transport() = %+v, want %+v\ndiff (-got +want):\n%s", got.Transport(), c.env.Transport(), diff)
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

			for _, key := range []string{"trace_id", "validated"} {
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

func TestResponseEnvelope_RoundTripJSON(t *testing.T) {
	t.Parallel()

	newEnvelope := func(tb testing.TB) *sip.ResponseEnvelope {
		tb.Helper()

		res := &sip.Response{
			Status: sip.ResponseStatusAccepted,
			Proto:  sip.ProtoVer20(),
			Headers: make(sip.Headers).
				Set(
					header.Via{{
						Proto:     sip.ProtoVer20(),
						Transport: "TCP",
						Addr:      sip.AddrFromHost("proxy.example.com"),
						Params:    make(sip.Values).Set("branch", "z9hG4bK-res-2"),
					}},
					&header.From{
						URI: &sip.URI{
							User: sip.UserWithName("alice"),
							Addr: sip.AddrFromHost("example.com"),
						},
						Params: make(sip.Values).Set("tag", "from-tag"),
					},
					&header.To{
						URI: &sip.URI{
							User: sip.UserWithName("bob"),
							Addr: sip.AddrFromHost("example.net"),
						},
						Params: make(sip.Values).Set("tag", "to-tag"),
					},
					&header.CSeq{SeqNum: 10, Method: sip.RequestMethodInvite},
					header.CallID("call-10"),
				).
				Append(header.ContentLength(0)),
		}

		env := sip.NewResponseEnvelope(res).
			SetTransport(sip.TLSMetadata()).
			SetLocalAddr(netip.MustParseAddrPort("203.0.113.30:5071")).
			SetRemoteAddr(netip.MustParseAddrPort("203.0.113.40:5081"))
		env.Metadata().Set("trace_id", "outbound-res").Set("retry", false)

		return env
	}

	cases := []struct {
		name string
		env  *sip.ResponseEnvelope
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
				var got *sip.ResponseEnvelope
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
				}

				if got != nil {
					t.Errorf("json.Unmarshal(null, got) = %+v, want nil", got)
				}

				return
			}

			var got *sip.ResponseEnvelope

			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got.Message(), c.env.Message()); diff != "" {
				t.Errorf("got.Message() = %+v, want %+v\ndiff (-got +want):\n%s", got.Message(), c.env.Message(), diff)
			}

			if diff := cmp.Diff(got.Transport(), c.env.Transport()); diff != "" {
				t.Errorf("got.Transport() = %+v, want %+v\ndiff (-got +want):\n%s", got.Transport(), c.env.Transport(), diff)
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

			for _, key := range []string{"trace_id", "retry"} {
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

func TestResponseEnvelope_UnmarshalJSON_EdgeCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		data string
	}{
		{"null", `null`},
		{"empty object", `{}`},
		{"message null", `{"message":null}`},
		{"message zero", `{"message":{}}`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var env *sip.ResponseEnvelope

			if err := json.Unmarshal([]byte(c.data), &env); err != nil {
				t.Fatalf("json.Unmarshal(%q) error = %v, want nil", c.data, err)
			}

			if c.data == "null" {
				if env != nil {
					t.Errorf("json.Unmarshal(%q) = %+v, want nil", c.data, env)
				}
				return
			}

			msg := env.Message()
			if msg == nil {
				t.Fatal("env.Message() = nil, want non-nil pointer")
			}

			if diff := cmp.Diff(msg, &sip.Response{}); diff != "" {
				t.Errorf("env.Message() not zero: diff (-got +want):\n%s", diff)
			}

			if env.MessageTime().IsZero() {
				t.Errorf("env.MessageTime() is zero, want non-zero")
			}

			if env.Metadata() == nil {
				t.Errorf("env.Metadata() = nil, want non-nil")
			}
		})
	}
}
