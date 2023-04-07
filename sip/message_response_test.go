package sip_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("SIP", Label("sip", "message"), func() {
	Describe("Response", func() {
		DescribeTable("parsing", Label("parsing"),
			// region
			func(in string, hdrPrs map[string]sip.HeaderParser, expectResp *sip.Response, expectErr any) {
				msg, err := sip.ParseMessage(in, hdrPrs)
				if expectErr == nil {
					Expect(msg).ToNot(BeNil(), "assert parsed message isn't nil")
					resp, ok := msg.(*sip.Response)
					Expect(ok).To(BeTrue(), "assert parsed message is of type *sip.Response")
					Expect(resp).To(Equal(expectResp), "assert parsed response is equal to the expected response")
					Expect(err).To(BeNil(), "parsed error is nil")
				} else {
					Expect(msg).To(BeNil(), "parsed message is nil")
					Expect(err).To(MatchError(expectErr), "parse error matches the expected error")
				}
			},
			EntryDescription("%[1]q"),
			Entry(nil, "", nil, nil, grammar.ErrEmptyInput),
			Entry(nil, "qwerty\r\n", nil, nil, grammar.ErrMalformedInput),
			Entry(nil, "SIP/2.0 12 \r\n", nil, nil, grammar.ErrMalformedInput),
			Entry(nil, "SIP/2.0 999 \r\n\r\n", nil, &sip.Response{Proto: sip.Proto20, Status: 999}, nil),
			Entry(nil,
				"SIP/2.0 200 OK\r\n"+
					"Via: SIP/2.0/UDP a.example.com;branch=qwerty,\r\n"+
					"\tSIP/2.0/UDP b.example.com;branch=asdf\r\n"+
					"Via: SIP/2.0/UDP c.example.com;branch=zxcvb\r\n"+
					"From: <sip:alice@a.example.com>;tag=abc\r\n"+
					"To: <sip:bob@b.example.com>;tag=def\r\n"+
					"CSeq: 1 INVITE\r\n"+
					"Call-ID: zxc\r\n"+
					"Max-Forwards: 70\r\n"+
					"Contact: <sip:bob@b.example.com:5060>\r\n"+
					"X-Custom-Header: 123\r\n"+
					"Content-Type: text/plain\r\n"+
					"Content-Length: 6\r\n"+
					"\r\n"+
					"done\r\n",
				nil,
				&sip.Response{
					Status: 200,
					Reason: "OK",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("b.example.com"),
								Params:    make(sip.Values).Append("branch", "asdf"),
							},
						}).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("c.example.com"),
								Params:    make(sip.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(sip.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
							Params: make(sip.Values).Append("tag", "def"),
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
						Append(&header.Any{Name: "X-Custom-Header", Value: "123"}).
						Append(&header.ContentType{
							Type:    "text",
							Subtype: "plain",
						}).
						Append(header.ContentLength(6)),
					Body: []byte("done\r\n"),
				},
				nil,
			),
			Entry(nil,
				"SIP/2.0 200 OK\r\n"+
					"Via: SIP/2.0/UDP c.example.com;branch=zxcvb\r\n"+
					"X-Custom-Header: 123\r\n"+
					"Content-Length: 6\r\n"+
					"P-Custom-Header: 123 abc\r\n"+
					"\r\n"+
					"done\r\n",
				map[string]sip.HeaderParser{
					"p-custom-header": parseCustomHeader,
				},
				&sip.Response{
					Status: 200,
					Reason: "OK",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("c.example.com"),
								Params:    make(sip.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&header.Any{Name: "X-Custom-Header", Value: "123"}).
						Append(header.ContentLength(6)).
						Append(&customHeader{"P-Custom-Header", 123, "abc"}),
					Body: []byte("done\r\n"),
				},
				nil,
			),
			// endregion
		)

		DescribeTable("rendering", Label("rendering"),
			// region
			func(res *sip.Response, expect string) {
				Expect(res.RenderMessage()).To(Equal(expect))
			},
			EntryDescription("%#[1]v"),
			Entry(nil, (*sip.Response)(nil), ""),
			Entry(nil, &sip.Response{}, "/ 0 \r\n\r\n"),
			Entry(nil,
				&sip.Response{
					Status: 200,
					Reason: "Ok",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("c.example.com"),
								Params:    make(sip.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(sip.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
							Params: make(sip.Values).Append("tag", "def"),
						}).
						Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
						Append(header.CallID("zxc")).
						Append(header.Contact{
							{
								URI: &uri.SIP{
									User: uri.User("bob"),
									Addr: uri.HostPort("b.example.com", 5060),
								},
							},
						}),
				},
				"SIP/2.0 200 Ok\r\n"+
					"Via: SIP/2.0/UDP c.example.com;branch=zxcvb\r\n"+
					"From: <sip:alice@a.example.com>;tag=abc\r\n"+
					"To: <sip:bob@b.example.com>;tag=def\r\n"+
					"Call-ID: zxc\r\n"+
					"CSeq: 1 INVITE\r\n"+
					"Contact: <sip:bob@b.example.com:5060>\r\n"+
					"\r\n",
			),
			Entry(nil,
				&sip.Response{
					Status: 200,
					Reason: "OK",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("b.example.com"),
								Params:    make(sip.Values).Append("branch", "asdf"),
							},
						}).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("c.example.com"),
								Params:    make(sip.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(sip.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
							Params: make(sip.Values).Append("tag", "def"),
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
				"SIP/2.0 200 OK\r\n"+
					"Via: SIP/2.0/UDP a.example.com;branch=qwerty, SIP/2.0/UDP b.example.com;branch=asdf\r\n"+
					"Via: SIP/2.0/UDP c.example.com;branch=zxcvb\r\n"+
					"From: <sip:alice@a.example.com>;tag=abc\r\n"+
					"To: <sip:bob@b.example.com>;tag=def\r\n"+
					"Call-ID: zxc\r\n"+
					"CSeq: 1 INVITE\r\n"+
					"Contact: <sip:bob@b.example.com:5060>\r\n"+
					"Max-Forwards: 70\r\n"+
					"Content-Type: text/plain\r\n"+
					"Content-Length: 6\r\n"+
					"P-Custom-Header: 321\r\n"+
					"X-Custom-Header: 123\r\n"+
					"\r\n"+
					"done\r\n",
			),
			// endregion
		)

		DescribeTable("comparing", Label("comparing"),
			// region
			func(res *sip.Response, val any, expect bool) {
				Expect(res.Equal(val)).To(Equal(expect))
			},
			EntryDescription("%#[1]v with value = %#[2]v"),
			Entry(nil, (*sip.Response)(nil), nil, false),
			Entry(nil, (*sip.Response)(nil), (*sip.Response)(nil), true),
			Entry(nil, (*sip.Response)(nil), &sip.Response{}, false),
			Entry(nil, &sip.Response{}, &sip.Response{}, true),
			Entry(nil,
				&sip.Response{
					Status: 200,
					Reason: "OK",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("b.example.com"),
								Params:    make(sip.Values).Append("branch", "asdf"),
							},
						}).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("c.example.com"),
								Params:    make(sip.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(sip.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
							Params: make(sip.Values).Append("tag", "def"),
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
				sip.Response{
					Status: 200,
					Reason: "OK",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("b.example.com"),
								Params:    make(sip.Values).Append("branch", "asdf"),
							},
						}).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("c.example.com"),
								Params:    make(sip.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(sip.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
							Params: make(sip.Values).Append("tag", "def"),
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
				true,
			),
			Entry(nil,
				&sip.Response{
					Status: 200,
					Reason: "OK",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("b.example.com"),
								Params:    make(sip.Values).Append("branch", "asdf"),
							},
						}).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("c.example.com"),
								Params:    make(sip.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(sip.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
							Params: make(sip.Values).Append("tag", "def"),
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
				&sip.Response{
					Status: 100,
					Reason: "Trying",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("b.example.com"),
								Params:    make(sip.Values).Append("branch", "asdf"),
							},
						}).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("c.example.com"),
								Params:    make(sip.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(sip.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
							Params: make(sip.Values).Append("tag", "def"),
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
				false,
			),
			Entry(nil,
				&sip.Response{
					Status: 200,
					Reason: "OK",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("b.example.com"),
								Params:    make(sip.Values).Append("branch", "asdf"),
							},
						}).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("c.example.com"),
								Params:    make(sip.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(sip.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
							Params: make(sip.Values).Append("tag", "def"),
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
				&sip.Response{
					Status: 200,
					Reason: "OK",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("b.example.com"),
								Params:    make(sip.Values).Append("branch", "asdf"),
							},
						}).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("c.example.com"),
								Params:    make(sip.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(sip.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
							Params: make(sip.Values).Append("tag", "def"),
						}).
						Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
						Append(header.CallID("xxx")).
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
				false,
			),
			Entry(nil,
				&sip.Response{
					Status: 200,
					Reason: "OK",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("b.example.com"),
								Params:    make(sip.Values).Append("branch", "asdf"),
							},
						}).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("c.example.com"),
								Params:    make(sip.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(sip.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
							Params: make(sip.Values).Append("tag", "def"),
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
				&sip.Response{
					Status: 200,
					Reason: "OK",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("b.example.com"),
								Params:    make(sip.Values).Append("branch", "asdf"),
							},
						}).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("c.example.com"),
								Params:    make(sip.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(sip.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
							Params: make(sip.Values).Append("tag", "def"),
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
					Body: []byte("qwerty\r\n"),
				},
				false,
			),
			// endregion
		)

		DescribeTable("validating", Label("validating"),
			// region
			func(res *sip.Response, expect bool) {
				Expect(res.IsValid()).To(Equal(expect))
			},
			EntryDescription("%[1]q"),
			Entry(nil, (*sip.Response)(nil), false),
			Entry(nil, &sip.Response{}, false),
			Entry(nil, &sip.Response{Status: 10}, false),
			Entry(nil,
				&sip.Response{
					Status:  200,
					Headers: make(sip.Headers).Append(&header.CSeq{}),
				},
				false,
			),
			Entry(nil,
				&sip.Response{
					Status: 200,
					Reason: "OK",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(sip.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
							Params: make(sip.Values).Append("tag", "def"),
						}),
					Body: []byte("done\r\n"),
				},
				false,
			),
			Entry(nil,
				&sip.Response{
					Status: 200,
					Reason: "OK",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("b.example.com"),
								Params:    make(sip.Values).Append("branch", "asdf"),
							},
						}).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("c.example.com"),
								Params:    make(sip.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(sip.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
							Params: make(sip.Values).Append("tag", "def"),
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
				true,
			),
			// endregion
		)

		DescribeTable("cloning", Label("cloning"),
			// region
			func(res1 *sip.Response) {
				res2 := res1.Clone()
				if res1 == nil {
					Expect(res2).To(BeNil(), "assert cloned response is nil")
				} else {
					res2 := res2.(*sip.Response)
					Expect(res1).To(Equal(res2), "assert cloned response is equal to the original response")
					Expect(reflect.ValueOf(res2).Pointer()).
						ToNot(Equal(reflect.ValueOf(res1).Pointer()), "assert cloned response pointer is different than the original")
					if res2.Headers != nil {
						Expect(reflect.ValueOf(res2.Headers).Pointer()).
							ToNot(Equal(reflect.ValueOf(res1.Headers).Pointer()), "assert cloned headers pointer is different than the original")
					}
					if res2.Body != nil {
						Expect(reflect.ValueOf(res2.Body).Pointer()).
							ToNot(Equal(reflect.ValueOf(res1.Body).Pointer()), "assert cloned body pointer is different than the original")
					}
					if res2.Metadata != nil {
						Expect(reflect.ValueOf(res2.Metadata).Pointer()).
							ToNot(Equal(reflect.ValueOf(res1.Metadata).Pointer()), "assert cloned metadata pointer is different than the original")
					}
				}
			},
			EntryDescription("%#v"),
			Entry(nil, (*sip.Response)(nil)),
			Entry(nil, &sip.Response{}),
			Entry(nil,
				&sip.Response{
					Status: 200,
					Reason: "OK",
					Proto:  sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("b.example.com"),
								Params:    make(sip.Values).Append("branch", "asdf"),
							},
						}).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("c.example.com"),
								Params:    make(sip.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&header.From{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.Host("a.example.com"),
							},
							Params: make(sip.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
							Params: make(sip.Values).Append("tag", "def"),
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
			),
			// endregion
		)
	})
})
