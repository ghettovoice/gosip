package sip_test

import (
	"fmt"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("SIP", Label("sip", "message"), func() {
	Describe("Request", func() {
		DescribeTable("parsing", Label("parsing"),
			// region
			func(in string, hdrPrs map[string]sip.HeaderParser, expectReq *sip.Request, expectErr any) {
				msg, err := sip.ParseMessage(in, hdrPrs)
				if expectErr == nil {
					Expect(msg).ToNot(BeNil(), "assert parsed message isn't nil")
					req, ok := msg.(*sip.Request)
					Expect(ok).To(BeTrue(), "assert parsed message is of type *sip.Request")
					Expect(req).To(Equal(expectReq), "assert parsed request is equal to the expected request")
					Expect(err).ToNot(HaveOccurred(), "assert parsed error is nil")
				} else {
					Expect(msg).To(BeNil(), "assert parsed message is nil")
					Expect(err).To(MatchError(expectErr.(error)), "assert parse error matches the expected error") //nolint:forcetypeassert
				}
			},
			EntryDescription("%[1]q"),
			Entry(nil, "", nil, nil, grammar.ErrEmptyInput),
			Entry(nil, "INVITE  \r\n\r\n", nil, nil, grammar.ErrMalformedInput),
			Entry(nil, "INVITE qwerty \r\n\r\n", nil, nil, grammar.ErrMalformedInput),
			Entry(nil, "INVITE sip:bob@b.example.com \r\n\r\n", nil, nil, grammar.ErrMalformedInput),
			Entry(nil, "INVITE sip:bob@b.example.com SIP/2.0\r\n\r\n",
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
			),
			Entry(nil,
				"INVITE sip:bob@b.example.com SIP/2.0\r\n"+
					"Via: SIP/2.0/UDP a.example.com;branch=qwerty,\r\n"+
					"\tSIP/2.0/UDP b.example.com;branch=asdf\r\n"+
					"Via: SIP/2.0/UDP c.example.com;branch=zxcvb\r\n"+
					"From: <sip:alice@a.example.com>;tag=abc\r\n"+
					"To: sip:bob@b.example.com\r\n"+
					"CSeq: 1 INVITE\r\n"+
					"Call-ID: zxc\r\n"+
					"Max-Forwards: 70\r\n"+
					"Contact: <sip:alice@a.example.com:5060>;transport=tcp\r\n"+
					"X-Custom-Header: 123\r\n"+
					"Content-Type: text/plain\r\n"+
					"Content-Length: 14\r\n"+
					"\r\n"+
					"Hello world!\r\n",
				nil,
				&sip.Request{
					Method: "INVITE",
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
						}).
						Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
						Append(header.CallID("zxc")).
						Append(header.MaxForwards(70)).
						Append(header.Contact{
							{
								URI: &uri.SIP{
									User: uri.User("alice"),
									Addr: uri.HostPort("a.example.com", 5060),
								},
								Params: make(header.Values).Append("transport", "tcp"),
							},
						}).
						Append(&header.Any{Name: "X-Custom-Header", Value: "123"}).
						Append(&header.ContentType{
							Type:    "text",
							Subtype: "plain",
						}).
						Append(header.ContentLength(14)),
					Body: []byte("Hello world!\r\n"),
				},
				nil,
			),
			Entry(nil,
				"INVITE sip:bob@b.example.com SIP/2.0\r\n"+
					"Via: SIP/2.0/UDP c.example.com;branch=zxcvb\r\n"+
					"P-Custom-Header: 123 abc\r\n"+
					"X-Generic-Header: qwe\r\n"+
					"Content-Length: 0\r\n"+
					"\r\n",
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
						Append(header.Via{
							{
								Proto:     sip.ProtoVer20(),
								Transport: "UDP",
								Addr:      header.Host("c.example.com"),
								Params:    make(header.Values).Append("branch", "zxcvb"),
							},
						}).
						Append(&customHeader{"P-Custom-Header", 123, "abc"}).
						Append(&header.Any{Name: "X-Generic-Header", Value: "qwe"}).
						Append(header.ContentLength(0)),
				},
				nil,
			),
			// endregion
		)

		DescribeTable("rendering", Label("rendering"),
			// region
			func(req *sip.Request, expect string) {
				Expect(req.Render()).To(Equal(expect))
			},
			EntryDescription("%#[1]v"),
			Entry(nil, (*sip.Request)(nil), ""),
			Entry(nil, &sip.Request{}, "  /\r\n\r\n"),
			Entry(nil,
				&sip.Request{
					Method: "INVITE",
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
				},
				"INVITE sip:bob@b.example.com SIP/2.0\r\n"+
					"Via: SIP/2.0/UDP a.example.com;branch=qwerty, SIP/2.0/UDP b.example.com;branch=asdf\r\n"+
					"From: <sip:alice@a.example.com>;tag=abc\r\n"+
					"To: <sip:bob@b.example.com>\r\n"+
					"Call-ID: zxc\r\n"+
					"CSeq: 1 INVITE\r\n"+
					"\r\n",
			),
			Entry(nil,
				&sip.Request{
					Method: "INVITE",
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
						}).
						Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
						Append(header.CallID("zxc")).
						Append(header.MaxForwards(70)).
						Append(header.Contact{
							{
								URI: &uri.SIP{
									User: uri.User("alice"),
									Addr: uri.HostPort("a.example.com", 5060),
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
				"INVITE sip:bob@b.example.com SIP/2.0\r\n"+
					"Via: SIP/2.0/UDP a.example.com;branch=qwerty, SIP/2.0/UDP b.example.com;branch=asdf\r\n"+
					"Via: SIP/2.0/UDP c.example.com;branch=zxcvb\r\n"+
					"From: <sip:alice@a.example.com>;tag=abc\r\n"+
					"To: <sip:bob@b.example.com>\r\n"+
					"Call-ID: zxc\r\n"+
					"CSeq: 1 INVITE\r\n"+
					"Contact: <sip:alice@a.example.com:5060>\r\n"+
					"Max-Forwards: 70\r\n"+
					"Content-Type: text/plain\r\n"+
					"Content-Length: 14\r\n"+
					"P-Custom-Header: 123\r\n"+
					"X-Custom-Header: 123\r\n"+
					"\r\n"+
					"Hello world!\r\n",
			),
			// endregion
		)

		DescribeTable("comparing", Label("comparing"),
			// region
			func(req *sip.Request, val any, expect bool) {
				Expect(req.Equal(val)).To(Equal(expect))
			},
			EntryDescription("%#[1]v with value = %#[2]v"),
			Entry(nil, (*sip.Request)(nil), nil, false),
			Entry(nil, (*sip.Request)(nil), (*sip.Request)(nil), true),
			Entry(nil, &sip.Request{}, (*sip.Request)(nil), false),
			Entry(nil, &sip.Request{}, &sip.Request{}, true),
			Entry(nil,
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
				&sip.Request{
					Method: sip.RequestMethodBye,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
				false,
			),
			Entry(nil,
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("alice"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
				false,
			),
			Entry(nil,
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoInfo{Name: "SIP", Version: "3.0"},
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
				false,
			),
			Entry(nil,
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
								User: uri.User("john"),
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
				false,
			),
			Entry(nil,
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
					Body: []byte("Goodbye world!\r\n"),
				},
				false,
			),
			Entry(nil,
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
				sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
			),
			// endregion
		)

		DescribeTable("validating", Label("validating"),
			// region
			func(req *sip.Request, expect bool) {
				Expect(req.IsValid()).To(Equal(expect))
			},
			EntryDescription("%[1]q"),
			Entry(nil, (*sip.Request)(nil), false),
			Entry(nil, &sip.Request{}, false),
			Entry(nil, &sip.Request{Method: sip.RequestMethodInvite}, false),
			Entry(nil,
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
				},
				false,
			),
			Entry(nil,
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
				},
				false,
			),
			Entry(nil,
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
					Headers: make(sip.Headers).
						Append(&header.From{
							Params: make(header.Values).Append("tag", "abc"),
						}).
						Append(&header.To{
							URI: &uri.SIP{
								User: uri.User("bob"),
								Addr: uri.Host("b.example.com"),
							},
						}),
				},
				false,
			),
			Entry(nil,
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
			),
			Entry(nil,
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.ProtoVer20(),
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
				true,
			),
			// endregion
		)

		DescribeTable("cloning", Label("cloning"),
			// region
			func(req1 *sip.Request) {
				req2 := req1.Clone()
				if req1 == nil {
					Expect(req2).To(BeNil(), "assert cloned request is nil")
				} else {
					req2, ok := req2.(*sip.Request)
					Expect(ok).To(BeTrue(), fmt.Sprintf("assert cloned request is of type %T", req1))
					Expect(req1).To(Equal(req2), "assert cloned request is equal to the original request")
					Expect(reflect.ValueOf(req2).Pointer()).ToNot(Equal(reflect.ValueOf(req1).Pointer()), "assert cloned request pointer is different than the original")
					if req1.URI != nil {
						Expect(reflect.ValueOf(req2.URI).Pointer()).ToNot(Equal(reflect.ValueOf(req1.URI).Pointer()), "assert cloned URI pointer is different than the original")
					}
					if req2.Headers != nil {
						Expect(reflect.ValueOf(req2.Headers).Pointer()).ToNot(Equal(reflect.ValueOf(req1.Headers).Pointer()), "assert cloned headers pointer is different than the original")
					}
					if req2.Body != nil {
						Expect(reflect.ValueOf(req2.Body).Pointer()).ToNot(Equal(reflect.ValueOf(req1.Body).Pointer()), "assert cloned body pointer is different than the original")
					}
					if req2.Metadata != nil {
						Expect(reflect.ValueOf(req2.Metadata).Pointer()).ToNot(Equal(reflect.ValueOf(req1.Metadata).Pointer()), "assert cloned metadata pointer is different than the original")
					}
				}
			},
			EntryDescription("%#v"),
			Entry(nil, (*sip.Request)(nil)),
			Entry(nil, &sip.Request{
				Method: "INVITE",
				URI: &uri.SIP{
					User: uri.User("bob"),
					Addr: uri.Host("b.example.com"),
				},
				Proto: sip.ProtoVer20(),
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
					}).
					Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
					Append(header.CallID("zxc")).
					Append(header.MaxForwards(70)).
					Append(header.Contact{
						{
							URI: &uri.SIP{
								User: uri.User("alice"),
								Addr: uri.HostPort("a.example.com", 5060),
							},
						},
					}).
					Append(&header.Any{Name: "X-Custom-Header", Value: "123"}).
					Append(&header.ContentType{
						Type:    "text",
						Subtype: "plain",
					}).
					Append(header.ContentLength(14)),
				Body: []byte("Hello world!\r\n"),
				Metadata: sip.MessageMetadata{
					"foo": "bar",
					"bar": "foo",
				},
			}),
			// endregion
		)
	})
})
