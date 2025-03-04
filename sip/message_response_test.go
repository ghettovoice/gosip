package sip_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/uri"
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
							Addr:      header.Host("a.example.com"),
							Params:    make(header.Values).Append("branch", "qwerty"),
						},
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      header.Host("b.example.com"),
							Params:    make(header.Values).Append("branch", "asdf"),
						},
					}).
					Append(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      header.Host("c.example.com"),
							Params:    make(header.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.Host("a.example.com"),
						},
						Params: make(header.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.Host("b.example.com"),
						},
						Params: make(header.Values).Append("tag", "def"),
					}).
					Append(&header.Any{Name: "P-Custom-Header", Value: "321"}).
					Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
					Append(header.CallID("zxc")).
					Append(header.MaxForwards(70)).
					Append(header.Contact{
						{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.HostPort("b.example.com", 5060),
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

			got := c.res.Render(nil)
			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Errorf("res.Render(nil) = %q, want %q\ndiff (-got +want):\n%v", got, c.want, diff)
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
			_, err := c.res.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Fatalf("res.RenderTo(sb, nil) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
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
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.Host("example.com"),
						},
						Params: make(header.Values).Append("tag", "abc"),
					}),
			},
			&sip.Response{
				Proto:  sip.ProtoVer20(),
				Status: 200,
				Reason: "OK",
				Headers: make(sip.Headers).
					Set(&header.From{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.Host("localhost"),
						},
						Params: make(header.Values).Append("tag", "abc"),
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
							Addr:      header.Host("c.example.com"),
							Params:    make(header.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.Host("a.example.com"),
						},
						Params: make(header.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.Host("b.example.com"),
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
							Addr:      header.Host("c.example.com"),
							Params:    make(header.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.Host("a.example.com"),
						},
						Params: make(header.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.Host("b.example.com"),
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
							Addr:      header.Host("c.example.com"),
							Params:    make(header.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.Host("a.example.com"),
						},
						Params: make(header.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.Host("b.example.com"),
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
							Addr:      header.Host("c.example.com"),
							Params:    make(header.Values).Append("branch", "zxcvb"),
						},
					}).
					Append(&header.From{
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.Host("a.example.com"),
						},
						Params: make(header.Values).Append("tag", "abc"),
					}).
					Append(&header.To{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.Host("b.example.com"),
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
			name: "with_headers_body_metadata",
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
								Addr:      header.Host("proxy.example.com"),
								Params:    make(header.Values).Set("branch", "z9hG4bK-9876"),
							},
						},
						&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("example.com"),
							},
							Params: make(header.Values).Set("tag", "abc"),
						},
						&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("example.net"),
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
