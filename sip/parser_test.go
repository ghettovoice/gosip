package sip_test

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("SIP", Label("sip", "parser"), func() {
	Describe("DefaultParser", func() {
		var p *sip.DefaultParser

		BeforeEach(func() {
			p = &sip.DefaultParser{
				HeaderParsers: map[string]sip.HeaderParser{
					"p-custom-header": parseCustomHeader,
				},
			}
		})

		DescribeTable("parsing a single message packet", Label("parsing"),
			// region
			func(in []byte, expectMsg sip.Message, expectErr any) {
				msg, err := p.ParsePacket(in)
				if expectMsg == nil {
					Expect(msg).To(BeNil(), "assert parsed message is nil")
				} else {
					Expect(msg).To(Equal(expectMsg), "assert parsed message is equal to the expected message")
				}
				if expectErr == nil {
					Expect(err).ToNot(HaveOccurred(), "assert parse error is nil")
				} else {
					Expect(err).To(MatchError(expectErr), "assert parse error matches the expected error")
				}
			},
			EntryDescription("%[1]q"),
			Entry(nil,
				[]byte{},
				nil,
				&sip.ParseError{
					Err:   io.EOF,
					State: sip.ParseStateStart,
				},
			),
			Entry(nil,
				[]byte("INVITE qwerty"),
				nil,
				&sip.ParseError{
					Err:   grammar.ErrMalformedInput,
					State: sip.ParseStateStart,
					Buf:   []byte("INVITE qwerty"),
				},
			),
			Entry(nil,
				[]byte("INVITE  \r\n\r\n"),
				nil,
				&sip.ParseError{
					Err:   grammar.ErrMalformedInput,
					State: sip.ParseStateStart,
					Buf:   []byte("INVITE  "),
				},
			),
			Entry(nil,
				[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n"+
					"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n"),
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
						}),
				},
				&sip.ParseError{
					Err:   io.ErrUnexpectedEOF,
					State: sip.ParseStateHeaders,
					Buf:   nil,
				},
			),
			Entry(nil,
				[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n"+
					"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n"+
					"qwerty\r\n"+
					"\r\n"),
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
						}),
				},
				&sip.ParseError{
					Err:   grammar.ErrMalformedInput,
					State: sip.ParseStateHeaders,
					Buf:   []byte("qwerty"),
				},
			),
			Entry(nil,
				[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n"+
					"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n"+
					"\r\n"+
					"hello\r\nworld"),
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
						}),
					Body: []byte("hello\r\nworld"),
				},
				nil,
			),
			Entry(nil,
				[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n"+
					"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n"+
					"Content-Length: 0\r\n"+
					"\r\n"+
					"hello\r\nworld"),
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
						}).
						Append(header.ContentLength(0)),
				},
				nil,
			),
			Entry(nil,
				[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n"+
					"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n"+
					"Content-Length: 0\r\n"+
					"\r\n"+
					"SIP/2.0 200 OK\r\n"+
					"Content-Length: 0\r\n"+
					"\r\n"),
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
						}).
						Append(header.ContentLength(0)),
				},
				nil,
			),
			Entry(nil,
				[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n"+
					"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n"+
					"\r\n"+
					"SIP/2.0 200 OK\r\n"+
					"Content-Length: 0\r\n"+
					"\r\n"),
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
						}),
					Body: []byte("SIP/2.0 200 OK\r\n" +
						"Content-Length: 0\r\n" +
						"\r\n"),
				},
				nil,
			),
			Entry(nil,
				[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n"+
					"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n"+
					"Content-Length: 20\r\n"+
					"\r\n"+
					"Hello world!"),
				&sip.Request{
					Method: sip.RequestMethodInvite,
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.Proto20,
					Headers: make(sip.Headers).
						Append(header.Via{
							{
								Proto:     sip.Proto20,
								Transport: sip.TransportProtoUDP,
								Addr:      sip.Host("a.example.com"),
								Params:    make(sip.Values).Append("branch", "qwerty"),
							},
						}).
						Append(header.ContentLength(20)),
					Body: append([]byte("Hello world!"), make([]byte, 8)...),
				},
				&sip.ParseError{
					Err:   io.ErrUnexpectedEOF,
					State: sip.ParseStateBody,
					Buf:   []byte("Hello world!"),
				},
			),
			Entry(nil,
				[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n"+
					"Via: SIP/2.0/UDP a.example.com;branch=qwerty,\r\n"+
					"\tSIP/2.0/UDP b.example.com;branch=asdf\r\n"+
					"Via: SIP/2.0/UDP c.example.com;branch=zxcvb\r\n"+
					"From: <sip:alice@a.example.com>;tag=abc\r\n"+
					"To: sip:bob@b.example.com\r\n"+
					"CSeq: 1 INVITE\r\n"+
					"Call-ID: zxc\r\n"+
					"Max-Forwards: 70\r\n"+
					"Contact: <sip:alice@a.example.com:5060>;transport=tcp\r\n"+
					"P-Custom-Header: 123 abc\r\n"+
					"X-Generic-Header: qwe\r\n"+
					"Content-Type: text/plain\r\n"+
					"Content-Length: 12\r\n"+
					"\r\n"+
					"Hello world!\r\n"),
				&sip.Request{
					Method: "INVITE",
					URI: &uri.SIP{
						User: uri.User("bob"),
						Addr: uri.Host("b.example.com"),
					},
					Proto: sip.Proto20,
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
								Params: make(sip.Values).Append("transport", "tcp"),
							},
						}).
						Append(&customHeader{"P-Custom-Header", 123, "abc"}).
						Append(&header.Any{Name: "X-Generic-Header", Value: "qwe"}).
						Append(&header.ContentType{
							Type:    "text",
							Subtype: "plain",
						}).
						Append(header.ContentLength(12)),
					Body: []byte("Hello world!"),
				},
				nil,
			),
			Entry(nil,
				[]byte("SIP/2.0 200 OK\r\n"+
					"Via: SIP/2.0/UDP a.example.com;branch=qwerty,\r\n"+
					"\tSIP/2.0/UDP b.example.com;branch=asdf\r\n"+
					"Via: SIP/2.0/UDP c.example.com;branch=zxcvb\r\n"+
					"From: <sip:alice@a.example.com>;tag=abc\r\n"+
					"To: <sip:bob@b.example.com>;tag=def\r\n"+
					"CSeq: 1 INVITE\r\n"+
					"Call-ID: zxc\r\n"+
					"Max-Forwards: 70\r\n"+
					"Contact: <sip:bob@b.example.com:5060>\r\n"+
					"P-Custom-Header: 123 abc\r\n"+
					"X-Generic-Header: qwe\r\n"+
					"Content-Type: text/plain\r\n"+
					"Content-Length: 6\r\n"+
					"\r\n"+
					"done\r\n"),
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
						Append(&customHeader{"P-Custom-Header", 123, "abc"}).
						Append(&header.Any{Name: "X-Generic-Header", Value: "qwe"}).
						Append(&header.ContentType{
							Type:    "text",
							Subtype: "plain",
						}).
						Append(header.ContentLength(6)),
					Body: []byte("done\r\n"),
				},
				nil,
			),
			// endregion
			// endregion
		)

		Describe("parsing a stream of messages", Label("parsing"), func() {
			var (
				sp *sip.DefaultStreamParser
				pw *io.PipeWriter
				pr *io.PipeReader
			)

			BeforeEach(func() {
				pr, pw = io.Pipe()
				sp = p.ParseStream(pr).(*sip.DefaultStreamParser)
			})

			When("parsing message start line", func() {
				Context("on any read error", func() {
					It("should yield error", func(ctx SpecContext) {
						var wg sync.WaitGroup
						wg.Add(2)
						go func() {
							defer wg.Done()
							defer GinkgoRecover()

							time.Sleep(time.Millisecond)

							Expect(pw.CloseWithError(errors.New("test error"))).To(Succeed(), "close pipe should succeed")
						}()
						go func() {
							defer wg.Done()
							defer GinkgoRecover()

							var results [][]any
							for msg, err := range sp.Messages() {
								results = append(results, []any{msg, err})
								if err != nil {
									break
								}
							}

							Expect(results).To(MatchAllElementsWithIndex(IndexIdentity, Elements{
								"0": MatchAllElementsWithIndex(IndexIdentity, Elements{
									"0": BeNil(),
									"1": MatchError(&sip.ParseError{Err: errors.New("test error"), State: sip.ParseStateStart}),
								}),
							}))
						}()

						done := make(chan struct{})
						go func() {
							wg.Wait()
							close(done)
						}()
						Eventually(ctx, done).Should(BeClosed())
					}, SpecTimeout(time.Second))
				})

				Context("on parse error", func() {
					It("should yield error", func(ctx SpecContext) {
						var wg sync.WaitGroup
						wg.Add(2)
						go func() {
							defer wg.Done()
							defer GinkgoRecover()

							_, err := pw.Write(bytes.Repeat([]byte{'a'}, 64<<10))
							Expect(err).ToNot(HaveOccurred(), "write to pipe should succeed")

							Expect(pw.Close()).To(Succeed(), "close pipe should succeed")
						}()
						go func() {
							defer wg.Done()
							defer GinkgoRecover()

							var results [][]any
							for msg, err := range sp.Messages() {
								results = append(results, []any{msg, err})
								if err != nil {
									break
								}
							}

							Expect(results).To(MatchAllElementsWithIndex(IndexIdentity, Elements{
								"0": MatchAllElementsWithIndex(IndexIdentity, Elements{
									"0": BeNil(),
									"1": MatchError(&sip.ParseError{
										Err:   grammar.ErrMalformedInput,
										State: sip.ParseStateStart,
										Buf:   bytes.Repeat([]byte{'a'}, 64<<10),
									}),
								}),
							}))
						}()

						done := make(chan struct{})
						go func() {
							wg.Wait()
							close(done)
						}()
						Eventually(ctx, done).Should(BeClosed())
					}, SpecTimeout(time.Minute))
				})
			})

			When("parsing message headers", func() {
				var preWrite func()

				BeforeEach(func() {
					preWrite = func() {
						_, err := pw.Write([]byte(
							"INVITE sip:bob@b.example.com SIP/2.0\r\n" +
								"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n",
						))
						Expect(err).ToNot(HaveOccurred(), "write to pipe should succeed")
					}
				})

				It("should yield message with Content-Length: 0", func(ctx SpecContext) {
					var wg sync.WaitGroup
					wg.Add(2)
					go func() {
						defer wg.Done()
						defer GinkgoRecover()

						preWrite()

						_, err := pw.Write([]byte("Content-Length: 0\r\n\r\n"))
						Expect(err).ToNot(HaveOccurred(), "write to pipe should succeed")

						Expect(pw.Close()).To(Succeed(), "close pipe should succeed")
					}()
					go func() {
						defer wg.Done()
						defer GinkgoRecover()

						for msg, err := range sp.Messages() {
							Expect(msg).To(Equal(&sip.Request{
								Method: sip.RequestMethodInvite,
								URI: &uri.SIP{
									User: uri.User("bob"),
									Addr: uri.Host("b.example.com"),
								},
								Proto: sip.Proto20,
								Headers: make(sip.Headers).
									Append(header.Via{
										{
											Proto:     sip.Proto20,
											Transport: sip.TransportProtoUDP,
											Addr:      sip.Host("a.example.com"),
											Params:    make(sip.Values).Append("branch", "qwerty"),
										},
									}).
									Append(header.ContentLength(0)),
							}))
							Expect(err).ToNot(HaveOccurred())
							break
						}
					}()

					done := make(chan struct{})
					go func() {
						wg.Wait()
						close(done)
					}()
					Eventually(ctx, done).Should(BeClosed())
				}, SpecTimeout(time.Second))

				Context("after unexpected close", func() {
					BeforeEach(func() {
						preWrite = func(base func()) func() {
							return func() {
								base()

								Expect(pw.Close()).To(Succeed(), "close pipe should succeed")
							}
						}(preWrite)
					})

					It("should yield incomplete message and parse error", func(ctx SpecContext) {
						var wg sync.WaitGroup
						wg.Add(2)
						go func() {
							defer wg.Done()
							defer GinkgoRecover()

							preWrite()
						}()
						go func() {
							defer wg.Done()
							defer GinkgoRecover()

							var results [][]any
							for msg, err := range sp.Messages() {
								results = append(results, []any{msg, err})
								if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
									break
								}
							}
							Expect(results).To(MatchAllElementsWithIndex(IndexIdentity, Elements{
								"0": MatchAllElementsWithIndex(IndexIdentity, Elements{
									"0": Equal(&sip.Request{
										Method: sip.RequestMethodInvite,
										URI: &uri.SIP{
											User: uri.User("bob"),
											Addr: uri.Host("b.example.com"),
										},
										Proto: sip.Proto20,
										Headers: make(sip.Headers).
											Append(header.Via{
												{
													Proto:     sip.Proto20,
													Transport: sip.TransportProtoUDP,
													Addr:      sip.Host("a.example.com"),
													Params:    make(sip.Values).Set("branch", "qwerty"),
												},
											}),
									}),
									"1": MatchError(&sip.ParseError{
										Err:   io.ErrUnexpectedEOF,
										State: sip.ParseStateHeaders,
										Buf:   nil,
									}),
								}),
							}))
						}()

						done := make(chan struct{})
						go func() {
							wg.Wait()
							close(done)
						}()
						Eventually(ctx, done).Should(BeClosed())
					}, SpecTimeout(time.Second))
				})

				Context("after malformed header", func() {
					BeforeEach(func() {
						preWrite = func(base func()) func() {
							return func() {
								base()

								_, err := pw.Write([]byte("qwerty\r\n\r\n"))
								Expect(err).ToNot(HaveOccurred(), "write to pipe should not fail")
							}
						}(preWrite)
					})

					It("should yield incomplete message, parse error and reset to initial state", func(ctx SpecContext) {
						var wg sync.WaitGroup
						wg.Add(2)
						go func() {
							defer wg.Done()
							defer GinkgoRecover()

							preWrite()

							_, err := pw.Write([]byte(
								"SIP/2.0 200 OK\r\n" +
									"Content-Length: 0\r\n" +
									"\r\n",
							))
							Expect(err).ToNot(HaveOccurred(), "write to pipe should not fail")

							Expect(pw.Close()).To(Succeed(), "close pipe should succeed")
						}()
						go func() {
							defer wg.Done()
							defer GinkgoRecover()

							var results [][]any
							for msg, err := range sp.Messages() {
								results = append(results, []any{msg, err})
								if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
									break
								}
							}
							Expect(results).To(MatchAllElementsWithIndex(IndexIdentity, Elements{
								// incomplete message + error
								"0": MatchAllElementsWithIndex(IndexIdentity, Elements{
									"0": Equal(&sip.Request{
										Method: sip.RequestMethodInvite,
										URI: &uri.SIP{
											User: uri.User("bob"),
											Addr: uri.Host("b.example.com"),
										},
										Proto: sip.Proto20,
										Headers: make(sip.Headers).
											Append(header.Via{
												{
													Proto:     sip.Proto20,
													Transport: sip.TransportProtoUDP,
													Addr:      sip.Host("a.example.com"),
													Params:    make(sip.Values).Set("branch", "qwerty"),
												},
											}),
									}),
									"1": MatchError(&sip.ParseError{
										Err:   grammar.ErrMalformedInput,
										State: sip.ParseStateHeaders,
										Buf:   []byte("qwerty"),
									}),
								}),
								"1": MatchAllElementsWithIndex(IndexIdentity, Elements{
									"0": BeNil(),
									"1": MatchError(&sip.ParseError{
										Err:   grammar.ErrEmptyInput,
										State: 0,
										Buf:   []byte(""),
									}),
								}),
								// next message
								"2": MatchAllElementsWithIndex(IndexIdentity, Elements{
									"0": Equal(&sip.Response{
										Status:  sip.ResponseStatusOK,
										Reason:  sip.ResponseStatusReason(sip.ResponseStatusOK),
										Proto:   sip.Proto20,
										Headers: make(sip.Headers).Append(header.ContentLength(0)),
									}),
									"1": BeNil(),
								}),
								"3": MatchAllElementsWithIndex(IndexIdentity, Elements{
									"0": BeNil(),
									"1": MatchError(io.EOF),
								}),
							}))
						}()

						done := make(chan struct{})
						go func() {
							wg.Wait()
							close(done)
						}()
						Eventually(ctx, done).Should(BeClosed())
					}, SpecTimeout(time.Second))
				})

				Context("and Content-Length header isn't present", func() {
					BeforeEach(func() {
						preWrite = func(base func()) func() {
							return func() {
								base()

								_, err := pw.Write([]byte("Content-Type: text/plain\r\n\r\n"))
								Expect(err).ToNot(HaveOccurred(), "write to pipe should not fail")
							}
						}(preWrite)
					})

					It("should yield incomplete message, parse error and reset to initial state", func(ctx SpecContext) {
						var wg sync.WaitGroup
						wg.Add(2)
						go func() {
							defer wg.Done()
							defer GinkgoRecover()

							preWrite()

							_, err := pw.Write([]byte(
								"SIP/2.0 200 OK\r\n" +
									"Content-Length: 0\r\n" +
									"\r\n",
							))
							Expect(err).ToNot(HaveOccurred(), "write to pipe should succeed")

							Expect(pw.Close()).To(Succeed(), "close pipe should succeed")
						}()
						go func() {
							defer wg.Done()
							defer GinkgoRecover()

							var results [][]any
							for msg, err := range sp.Messages() {
								results = append(results, []any{msg, err})
								if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
									break
								}
							}
							Expect(results).To(MatchAllElementsWithIndex(IndexIdentity, Elements{
								// incomplete message + error with invalid headers part
								"0": MatchAllElementsWithIndex(IndexIdentity, Elements{
									"0": Equal(&sip.Request{
										Method: sip.RequestMethodInvite,
										URI: &uri.SIP{
											User: uri.User("bob"),
											Addr: uri.Host("b.example.com"),
										},
										Proto: sip.Proto20,
										Headers: make(sip.Headers).
											Append(header.Via{
												{
													Proto:     sip.Proto20,
													Transport: sip.TransportProtoUDP,
													Addr:      sip.Host("a.example.com"),
													Params:    make(sip.Values).Append("branch", "qwerty"),
												},
											}).
											Append(&header.ContentType{
												Type:    "text",
												Subtype: "plain",
											}),
									}),
									"1": PointTo(MatchAllFields(Fields{
										"Err":   MatchError(`missing "Content-Length" header`),
										"State": Equal(sip.ParseStateHeaders),
										"Buf":   BeNil(),
									})),
								}),
								// next message
								"1": MatchAllElementsWithIndex(IndexIdentity, Elements{
									"0": Equal(&sip.Response{
										Status:  sip.ResponseStatusOK,
										Reason:  sip.ResponseStatusReason(sip.ResponseStatusOK),
										Proto:   sip.Proto20,
										Headers: make(sip.Headers).Append(header.ContentLength(0)),
									}),
									"1": BeNil(),
								}),
								"2": MatchAllElementsWithIndex(IndexIdentity, Elements{
									"0": BeNil(),
									"1": MatchError(io.EOF),
								}),
							}))
						}()

						done := make(chan struct{})
						go func() {
							wg.Wait()
							close(done)
						}()
						Eventually(ctx, done).Should(BeClosed())
					}, SpecTimeout(time.Second))
				})
			})

			When("parsing message body", func() {
				var preWrite func()

				BeforeEach(func() {
					preWrite = func() {
						_, err := pw.Write([]byte(
							"INVITE sip:bob@b.example.com SIP/2.0\r\n" +
								"Content-Length: 11\r\n" +
								"\r\n",
						))
						Expect(err).ToNot(HaveOccurred(), "write to pipe should succeed")
					}
				})

				It("should set body of length equal to the value of Content-Length and yield message", func(ctx SpecContext) {
					var wg sync.WaitGroup
					wg.Add(2)
					go func() {
						defer wg.Done()
						defer GinkgoRecover()

						preWrite()

						_, err := pw.Write([]byte("hello world"))
						Expect(err).ToNot(HaveOccurred(), "write to pipe should succeed")

						_, err = pw.Write([]byte("SIP/2.0 200 OK\r\nContent-Length: 0\r\n\r\n"))
						Expect(err).ToNot(HaveOccurred(), "write to pipe should succeed")

						Expect(pw.Close()).To(Succeed(), "close pipe should succeed")
					}()
					go func() {
						defer wg.Done()
						defer GinkgoRecover()

						for msg, err := range sp.Messages() {
							Expect(err).ToNot(HaveOccurred(), "parse message should succeed")
							Expect(msg).To(Equal(&sip.Request{
								Method: sip.RequestMethodInvite,
								Proto:  sip.Proto20,
								URI: &uri.SIP{
									User: uri.User("bob"),
									Addr: uri.Host("b.example.com"),
								},
								Headers: make(sip.Headers).Append(header.ContentLength(11)),
								Body:    []byte("hello world"),
							}))
							break
						}

						// drain pipe
						for _, err := range sp.Messages() {
							if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
								break
							}
						}
					}()

					done := make(chan struct{})
					go func() {
						wg.Wait()
						close(done)
					}()
					Eventually(done).Should(BeClosed())
				}, SpecTimeout(time.Second))

				Context("after unexpected close", func() {
					BeforeEach(func() {
						preWrite = func(base func()) func() {
							return func() {
								base()

								_, err := pw.Write([]byte("hello"))
								Expect(err).ToNot(HaveOccurred(), "write to pipe should succeed")

								Expect(pw.Close()).To(Succeed(), "close pipe should succeed")
							}
						}(preWrite)
					})

					It("should yield incomplete message and parse error", func(ctx SpecContext) {
						var wg sync.WaitGroup
						wg.Add(2)
						go func() {
							defer wg.Done()
							defer GinkgoRecover()

							preWrite()
						}()
						go func() {
							defer wg.Done()
							defer GinkgoRecover()

							for msg, err := range sp.Messages() {
								Expect(msg).To(Equal(&sip.Request{
									Method: sip.RequestMethodInvite,
									URI: &uri.SIP{
										User: uri.User("bob"),
										Addr: uri.Host("b.example.com"),
									},
									Proto:   sip.Proto20,
									Headers: make(sip.Headers).Append(header.ContentLength(11)),
									Body:    append([]byte("hello"), make([]byte, 6)...),
								}))
								Expect(err).To(MatchError(&sip.ParseError{
									Err:   io.ErrUnexpectedEOF,
									State: sip.ParseStateBody,
									Buf:   []byte("hello"),
								}))
								break
							}
						}()

						done := make(chan struct{})
						go func() {
							wg.Wait()
							close(done)
						}()
						Eventually(ctx, done).Should(BeClosed())
					}, SpecTimeout(time.Second))
				})
			})

			It("should build messages from bytes stream and yield them until loop break", func(ctx SpecContext) {
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()

					// a bit of trash
					_, err := pw.Write([]byte("\r\nqwerty\r\n"))
					Expect(err).ToNot(HaveOccurred(), "write to pipe should not fail")

					time.Sleep(10 * time.Millisecond)

					// the first message started
					_, err = pw.Write([]byte(
						"INVITE sip:bob@b.example.com SIP/2.0\r\n" +
							"Via: SIP/2.0/UDP ",
					))
					Expect(err).ToNot(HaveOccurred(), "write to pipe should not fail")

					time.Sleep(10 * time.Millisecond)

					_, err = pw.Write([]byte(
						"a.example.com;branch=qwerty\r\n" +
							"P-Custom-Header: 123 abc\r\n" +
							"Content-Length: 5\r\n",
					))
					Expect(err).ToNot(HaveOccurred(), "write to pipe should succeed")

					time.Sleep(10 * time.Millisecond)

					_, err = pw.Write([]byte("\r\nhello"))
					Expect(err).ToNot(HaveOccurred(), "write to pipe should succeed")

					// the second message started
					_, err = pw.Write([]byte(
						"SIP/2.0 200 OK\r\n" +
							"Content-Length: 0\r\n" +
							"\r\n",
					))
					Expect(err).ToNot(HaveOccurred(), "write to pipe should succeed")

					Expect(pw.Close()).To(Succeed(), "close pipe should succeed")
				}()
				go func() {
					defer wg.Done()
					defer GinkgoRecover()

					var results [][]any
					for msg, err := range sp.Messages() {
						results = append(results, []any{msg, err})
						if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
							break
						}
					}
					Expect(results).To(MatchAllElementsWithIndex(IndexIdentity, Elements{
						"0": MatchAllElementsWithIndex(IndexIdentity, Elements{
							"0": BeNil(),
							"1": MatchError(&sip.ParseError{
								Err:   grammar.ErrEmptyInput,
								State: sip.ParseStateStart,
								Buf:   []byte(""),
							}),
						}),
						"1": MatchAllElementsWithIndex(IndexIdentity, Elements{
							"0": BeNil(),
							"1": MatchError(&sip.ParseError{
								Err:   grammar.ErrMalformedInput,
								State: sip.ParseStateStart,
								Buf:   []byte("qwerty"),
							}),
						}),
						"2": MatchAllElementsWithIndex(IndexIdentity, Elements{
							"0": Equal(&sip.Request{
								Method: sip.RequestMethodInvite,
								URI: &uri.SIP{
									User: uri.User("bob"),
									Addr: uri.Host("b.example.com"),
								},
								Proto: sip.Proto20,
								Headers: make(sip.Headers).
									Append(header.Via{
										{
											Proto:     sip.Proto20,
											Transport: sip.TransportProtoUDP,
											Addr:      sip.Host("a.example.com"),
											Params:    make(sip.Values).Append("branch", "qwerty"),
										},
									}).
									Append(&customHeader{
										name: "P-Custom-Header",
										num:  123,
										str:  "abc",
									}).
									Append(header.ContentLength(5)),
								Body: []byte("hello"),
							}),
							"1": BeNil(),
						}),
						"3": MatchAllElementsWithIndex(IndexIdentity, Elements{
							"0": Equal(&sip.Response{
								Status: sip.ResponseStatusOK,
								Reason: sip.ResponseStatusReason(sip.ResponseStatusOK),
								Proto:  sip.Proto20,
								Headers: make(sip.Headers).
									Append(header.ContentLength(0)),
							}),
							"1": BeNil(),
						}),
						"4": MatchAllElementsWithIndex(IndexIdentity, Elements{
							"0": BeNil(),
							"1": MatchError(io.EOF),
						}),
					}))
				}()

				done := make(chan struct{})
				go func() {
					wg.Wait()
					close(done)
				}()
				Eventually(ctx, done).Should(BeClosed())
			}, SpecTimeout(time.Second))
		})
	})
})
