package sip_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/uri"
)

func TestParseMessage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   any
		hdrPrs  map[string]header.Parser
		wantMsg sip.Message
		wantErr error
	}{
		{"empty string", "", nil, nil, grammar.ErrEmptyInput},
		{"empty bytes", []byte{}, nil, nil, grammar.ErrEmptyInput},
		{"trash", "qwerty", nil, nil, grammar.ErrMalformedInput},
		{"trash bytes", []byte("qwerty"), nil, nil, grammar.ErrMalformedInput},

		{"invalid request 1", "INVITE  \r\n\r\n", nil, nil, grammar.ErrMalformedInput},
		{"invalid request 2", "INVITE sip:bob@b.example.com \r\n\r\n", nil, nil, grammar.ErrMalformedInput},
		{
			"valid request 1",
			"INVITE sip:bob@b.example.com SIP/2.0\r\n\r\n",
			nil,
			&sip.Request{
				Method: "INVITE",
				URI: &uri.SIP{
					User: uri.User("bob"),
					Addr: uri.Host("b.example.com"),
				},
				Proto: sip.ProtoVer20(),
			},
			nil,
		},
		{
			"valid request 2",
			"INVITE sip:bob@b.example.com SIP/2.0\r\n" +
				"Via: SIP/2.0/UDP a.example.com;branch=qwerty,\r\n" +
				"\tSIP/2.0/UDP b.example.com;branch=asdf\r\n" +
				"Via: SIP/2.0/UDP c.example.com;branch=zxcvb\r\n" +
				"From: <sip:alice@a.example.com>;tag=abc\r\n" +
				"To: sip:bob@b.example.com\r\n" +
				"CSeq: 1 INVITE\r\n" +
				"Call-ID: zxc\r\n" +
				"Max-Forwards: 70\r\n" +
				"Contact: <sip:alice@a.example.com:5060>;transport=tcp\r\n" +
				"X-Generic-Header: 123\r\n" +
				"Content-Type: text/plain\r\n" +
				"Content-Length: 14\r\n" +
				"P-Custom-Header: 123 abc\r\n" +
				"\r\n" +
				"Hello world!\r\n",
			map[string]sip.HeaderParser{
				"p-custom-header": parseCustomHeader,
			},
			&sip.Request{
				Method: "INVITE",
				URI: &uri.SIP{
					User: uri.User("bob"),
					Addr: uri.Host("b.example.com"),
				},
				Proto: sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Append(
						header.Via{
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
						},
						header.Via{
							{
								Proto:     sip.ProtoVer20(),
								Transport: "UDP",
								Addr:      header.Host("c.example.com"),
								Params:    make(header.Values).Append("branch", "zxcvb"),
							},
						},
					).
					Set(
						&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(header.Values).Append("tag", "abc"),
						},
						&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
						},
					).
					Set(
						&header.CSeq{SeqNum: 1, Method: "INVITE"},
						header.CallID("zxc"),
						header.MaxForwards(70),
					).
					Set(header.Contact{
						{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.HostPort("a.example.com", 5060),
							},
							Params: make(header.Values).Append("transport", "tcp"),
						},
					}).
					Set(&header.Any{Name: "X-Generic-Header", Value: "123"}).
					Set(&header.ContentType{Type: "text", Subtype: "plain"}).
					Set(header.ContentLength(14)).
					Set(&customHeader{name: "P-Custom-Header", num: 123, str: "abc"}),
				Body: []byte("Hello world!\r\n"),
			},
			nil,
		},

		{"invalid response", "SIP/2.0 12 \r\n", nil, nil, grammar.ErrMalformedInput},
		{"valid response 1", "SIP/2.0 999 \r\n\r\n", nil, &sip.Response{Proto: sip.ProtoVer20(), Status: 999}, nil},
		{
			"valid response 2",
			"SIP/2.0 200 OK\r\n" +
				"Via: SIP/2.0/UDP a.example.com;branch=qwerty,\r\n" +
				"\tSIP/2.0/UDP b.example.com;branch=asdf\r\n" +
				"Via: SIP/2.0/UDP c.example.com;branch=zxcvb;rport=98761\r\n" +
				"From: <sip:alice@a.example.com>;tag=abc\r\n" +
				"To: <sip:bob@b.example.com>;tag=def\r\n" +
				"CSeq: 1 INVITE\r\n" +
				"Call-ID: zxc\r\n" +
				"Max-Forwards: 70\r\n" +
				"Contact: <sip:bob@b.example.com:5060>\r\n" +
				"X-Generic-Header: 123\r\n" +
				"Content-Type: text/plain\r\n" +
				"P-Custom-Header: 123 abc\r\n" +
				"Content-Length: 6\r\n" +
				"\r\n" +
				"done\r\n",
			map[string]sip.HeaderParser{
				"p-custom-header": parseCustomHeader,
			},
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
							Params: make(header.Values).
								Append("branch", "zxcvb").
								Append("rport", "98761"),
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
					Append(&header.Any{Name: "X-Generic-Header", Value: "123"}).
					Append(&header.ContentType{
						Type:    "text",
						Subtype: "plain",
					}).
					Append(&customHeader{name: "P-Custom-Header", num: 123, str: "abc"}).
					Append(header.ContentLength(6)),
				Body: []byte("done\r\n"),
			},
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for n, p := range c.hdrPrs {
				header.RegisterParser(n, p)
			}
			defer func() {
				for n := range c.hdrPrs {
					header.UnregisterParser(n)
				}
			}()

			var (
				gotMsg sip.Message
				gotErr error
			)
			switch input := c.input.(type) {
			case string:
				gotMsg, gotErr = sip.ParseMessage(input)
			case []byte:
				gotMsg, gotErr = sip.ParseMessage(input)
			}
			input := util.Ellipsis(fmt.Sprintf("%v", c.input), 35)
			if c.wantErr == nil {
				if diff := cmp.Diff(gotMsg, c.wantMsg); diff != "" {
					t.Errorf("sip.ParseMessage(%q) = %+v, want %+v\ndiff (-got +want):\n%v",
						input, gotMsg, c.wantMsg, diff,
					)
				}
				if gotErr != nil {
					t.Errorf("sip.ParseMessage(%q) error = %v, want nil", input, gotErr)
				}
			} else {
				if got, want := gotErr, c.wantErr; !errors.Is(got, want) {
					t.Errorf("sip.ParseMessage(%q) error = %v, want %v", input, got, want)
				}
			}
		})
	}
}
