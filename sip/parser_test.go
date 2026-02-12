package sip_test

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/uri"
)

func TestParsePacket(t *testing.T) {
	cases := []struct {
		name    string
		input   []byte
		wantMsg sip.Message
		wantErr error
	}{
		{"empty", []byte{}, nil, io.EOF},
		{
			"malformed start line 1",
			[]byte("INVITE qwerty"),
			nil,
			&sip.ParseError{
				Err:   grammar.ErrMalformedInput,
				State: sip.ParseStateStart,
				Data:  []byte("INVITE qwerty"),
			},
		},
		{
			"malformed start line 2",
			[]byte("INVITE  \r\n\r\n"),
			nil,
			&sip.ParseError{
				Err:   grammar.ErrMalformedInput,
				State: sip.ParseStateStart,
				Data:  []byte("INVITE  "),
			},
		},
		{
			"malformed headers 1",
			[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n" +
				"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n"),
			nil,
			&sip.ParseError{
				Err:   sip.NewInvalidMessageError("incomplete headers"),
				State: sip.ParseStateHeaders,
				Msg: &sip.Request{
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
								Addr:      header.Host("a.example.com"),
								Params:    make(header.Values).Append("branch", "qwerty"),
							},
						}),
				},
			},
		},
		{
			"malformed headers 2",
			[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n" +
				"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n" +
				"qwerty\r\n" +
				"\r\n"),
			nil,
			&sip.ParseError{
				Err:   grammar.ErrMalformedInput,
				State: sip.ParseStateHeaders,
				Data:  []byte("qwerty"),
				Msg: &sip.Request{
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
								Addr:      header.Host("a.example.com"),
								Params:    make(header.Values).Append("branch", "qwerty"),
							},
						}),
				},
			},
		},
		{
			"valid request 1",
			[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n" +
				"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n" +
				"\r\n" +
				"hello\r\nworld"),
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
							Addr:      header.Host("a.example.com"),
							Params:    make(header.Values).Append("branch", "qwerty"),
						},
					}),
				Body: []byte("hello\r\nworld"),
			},
			nil,
		},
		{
			"valid request 2",
			[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n" +
				"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n" +
				"Content-Length: 0\r\n" +
				"\r\n" +
				"hello\r\nworld"),
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
							Addr:      header.Host("a.example.com"),
							Params:    make(header.Values).Append("branch", "qwerty"),
						},
					}).
					Append(header.ContentLength(0)),
			},
			nil,
		},
		{
			"multiple messages 1",
			[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n" +
				"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n" +
				"Content-Length: 0\r\n" +
				"\r\n" +
				"SIP/2.0 200 OK\r\n" +
				"Content-Length: 0\r\n" +
				"\r\n"),
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
							Addr:      header.Host("a.example.com"),
							Params:    make(header.Values).Append("branch", "qwerty"),
						},
					}).
					Append(header.ContentLength(0)),
			},
			nil,
		},
		{
			"multiple messages 2",
			[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n" +
				"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n" +
				"\r\n" +
				"SIP/2.0 200 OK\r\n" +
				"Content-Length: 0\r\n" +
				"\r\n"),
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
							Addr:      header.Host("a.example.com"),
							Params:    make(header.Values).Append("branch", "qwerty"),
						},
					}),
				Body: []byte("SIP/2.0 200 OK\r\n" +
					"Content-Length: 0\r\n" +
					"\r\n"),
			},
			nil,
		},
		{
			"incomplete body 1",
			[]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n" +
				"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n" +
				"Content-Length: 20\r\n" +
				"\r\n" +
				"Hello world!"),
			nil,
			&sip.ParseError{
				Err:   sip.NewInvalidMessageError("incomplete body"),
				State: sip.ParseStateBody,
				Data:  []byte("Hello world!"),
				Msg: &sip.Request{
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
								Addr:      header.Host("a.example.com"),
								Params:    make(header.Values).Append("branch", "qwerty"),
							},
						}).
						Append(header.ContentLength(20)),
					Body: append([]byte("Hello world!"), make([]byte, 8)...),
				},
			},
		},
		{
			"custom headers",
			[]byte("SIP/2.0 200 OK\r\n" +
				"Via: SIP/2.0/UDP a.example.com;branch=qwerty,\r\n" +
				"\tSIP/2.0/UDP b.example.com;branch=asdf\r\n" +
				"Via: SIP/2.0/UDP c.example.com;branch=zxcvb\r\n" +
				"From: <sip:alice@a.example.com>;tag=abc\r\n" +
				"To: <sip:bob@b.example.com>;tag=def\r\n" +
				"CSeq: 1 INVITE\r\n" +
				"Call-ID: zxc\r\n" +
				"Max-Forwards: 70\r\n" +
				"P-Custom-Header: 123 abc\r\n" +
				"X-Generic-Header: qwe\r\n" +
				"\r\n" +
				"done\r\n"),
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
					Append(&header.CSeq{SeqNum: 1, Method: "INVITE"}).
					Append(header.CallID("zxc")).
					Append(header.MaxForwards(70)).
					Append(&customHeader{"P-Custom-Header", 123, "abc"}).
					Append(&header.Any{Name: "X-Generic-Header", Value: "qwe"}),
				Body: []byte("done\r\n"),
			},
			nil,
		},
	}

	header.RegisterParser("p-custom-header", parseCustomHeader)
	defer header.UnregisterParser("p-custom-header")

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg, err := sip.ParsePacket(c.input)
			input := util.Ellipsis(string(c.input), 35)
			if c.wantErr == nil {
				if diff := cmp.Diff(msg, c.wantMsg); diff != "" {
					t.Errorf("sip.ParsePacket(%q) = %+v, want %+v\ndiff (-got +want):\n%v",
						input, msg, c.wantMsg, diff,
					)
				}
				if err != nil {
					t.Errorf("sip.ParsePacket(%q) error = %v, want nil", input, err)
				}
			} else {
				if got, want := err, c.wantErr; !cmpParseError(got, want) {
					t.Errorf("sip.ParsePacket(%q) error = %v, want %v\ndiff (-got +want):\n%v",
						input, got, want,
						cmp.Diff(got, want, cmpopts.EquateErrors()),
					)
				}
			}
		})
	}
}

func TestParsePacket_ContentLengthTooLarge(t *testing.T) {
	t.Parallel()

	contentLen := sip.MaxMsgSize + 1
	input := []byte("INVITE sip:bob@b.example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n" +
		"Content-Length: " + strconv.Itoa(int(contentLen)) + "\r\n" +
		"\r\n")

	msg, err := sip.ParsePacket(input)
	if msg != nil {
		t.Fatalf("sip.ParsePacket(input) msg = %+v, want nil", msg)
	}

	want := &sip.ParseError{
		Err:   sip.ErrEntityTooLarge,
		State: sip.ParseStateHeaders,
		Msg: &sip.Request{
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
						Addr:      header.Host("a.example.com"),
						Params:    make(header.Values).Append("branch", "qwerty"),
					},
				}).
				Append(header.ContentLength(contentLen)),
		},
	}
	if !cmpParseError(err, want) {
		t.Fatalf("sip.ParsePacket(input) error = %v, want %v\ndiff (-got +want):\n%v",
			err, want,
			cmp.Diff(err, want, cmpopts.EquateErrors()),
		)
	}
}

func TestParseStream(t *testing.T) {
	inputs := [][]byte{
		[]byte("INVITE "), []byte("qwerty 123"), []byte(" 321\r\n"),

		[]byte("OPTIONS sip:bob"), []byte("@example.com SIP/2.0\r\n"),
		[]byte("Content-Length: 37\r\n"),
		[]byte("\r\n"),
		[]byte("SIP/2.0 200 OK\r\nContent-Length: 0\r\n\r\n"),

		[]byte("SIP/2.0 200 OK\r\nContent-Length: 0\r\n\r\ndone\r\n"),

		[]byte("INVITE sip:alice@example.com SIP/2.0\r\n"),
		[]byte("Via: SIP/2.0/UDP localhost:5060\r\n"),
		[]byte("\r\n"),

		[]byte("INVITE sip:alice@example.com SIP/2.0\r\n"),
		[]byte("Via: SIP/2.0/UDP localhost:5060\r\n"),
		[]byte("Content-Length: 5\r\n"),
		[]byte("\r\n"),
		[]byte("12345SIP/2.0 100 Trying\r\n"),
		[]byte("Content-Length: 10\r\n\r\n"),
		[]byte("123"),
	}
	type result struct {
		msg sip.Message
		err error
	}
	wantResults := []result{
		{
			err: &sip.ParseError{
				State: sip.ParseStateStart,
				Err:   grammar.ErrMalformedInput,
				Data:  []byte("INVITE qwerty 123 321"),
			},
		},
		{
			msg: &sip.Request{
				Method: "OPTIONS",
				URI: &uri.SIP{
					User: uri.User("bob"),
					Addr: uri.Host("example.com"),
				},
				Proto:   sip.ProtoVer20(),
				Headers: make(sip.Headers).Set(header.ContentLength(37)),
				Body:    []byte("SIP/2.0 200 OK\r\nContent-Length: 0\r\n\r\n"),
			},
		},
		{
			msg: &sip.Response{
				Status:  200,
				Reason:  "OK",
				Proto:   sip.ProtoVer20(),
				Headers: make(sip.Headers).Set(header.ContentLength(0)),
			},
		},
		{
			err: &sip.ParseError{
				State: sip.ParseStateStart,
				Err:   grammar.ErrMalformedInput,
				Data:  []byte("done"),
			},
		},
		{
			err: &sip.ParseError{
				State: sip.ParseStateHeaders,
				Err:   sip.NewInvalidMessageError("missing mandatory header \"Content-Length\""),
				Msg: &sip.Request{
					Method: "INVITE",
					URI: &uri.SIP{
						User: uri.User("alice"),
						Addr: uri.Host("example.com"),
					},
					Proto: sip.ProtoVer20(),
					Headers: make(sip.Headers).Set(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      uri.HostPort("localhost", 5060),
						},
					}),
				},
			},
		},
		{
			msg: &sip.Request{
				Method: "INVITE",
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.Host("example.com"),
				},
				Proto: sip.ProtoVer20(),
				Headers: make(sip.Headers).
					Set(header.Via{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      uri.HostPort("localhost", 5060),
						},
					}).
					Set(header.ContentLength(5)),
				Body: []byte("12345"),
			},
		},
		{
			err: &sip.ParseError{
				State: sip.ParseStateBody,
				Err:   sip.NewInvalidMessageError("incomplete body"),
				Data:  []byte("123"),
				Msg: &sip.Response{
					Status:  100,
					Reason:  "Trying",
					Proto:   sip.ProtoVer20(),
					Headers: make(sip.Headers).Set(header.ContentLength(10)),
					Body:    append([]byte("123"), 0, 0, 0, 0, 0, 0, 0),
				},
			},
		},
		{
			err: io.EOF,
		},
	}

	pr, pw := io.Pipe()
	wg := sync.WaitGroup{}
	wg.Go(func() {
		for _, in := range inputs {
			if _, err := pw.Write(in); err != nil {
				t.Errorf("pw.Write(buf) error = %v, want nil", err)
			}
		}
		pw.Close()
	})

	gotResults := make([]result, 0)
	for msg, err := range sip.ParseStream(pr) {
		gotResults = append(gotResults, result{msg, err})
		if errors.Is(err, io.EOF) {
			break
		}
	}

	wg.Wait()

	cmpOpts := []cmp.Option{
		cmp.AllowUnexported(result{}),
		cmp.Comparer(cmpParseError),
	}
	if diff := cmp.Diff(gotResults, wantResults, cmpOpts...); diff != "" {
		t.Errorf("sip.ParseStream() = %+v, want %+v\ndiff (-got +want):\n%v", gotResults, wantResults, diff)
	}
}

func TestParseStream_ContentLengthTooLarge(t *testing.T) {
	t.Parallel()

	contentLen := sip.MaxMsgSize + 1
	input := []byte("INVITE sip:bob@b.example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n" +
		"Content-Length: " + strconv.Itoa(int(contentLen)) + "\r\n" +
		"\r\n")

	var msg sip.Message
	var err error
	for m, e := range sip.ParseStream(bytes.NewReader(input)) {
		msg = m
		err = e
		break
	}

	if msg != nil {
		t.Fatalf("sip.ParseStream(input) first msg = %+v, want nil", msg)
	}

	want := &sip.ParseError{
		Err:   sip.ErrEntityTooLarge,
		State: sip.ParseStateHeaders,
		Msg: &sip.Request{
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
						Addr:      header.Host("a.example.com"),
						Params:    make(header.Values).Append("branch", "qwerty"),
					},
				}).
				Append(header.ContentLength(contentLen)),
		},
	}
	if !cmpParseError(err, want) {
		t.Fatalf("sip.ParseStream(input) first error = %v, want %v\ndiff (-got +want):\n%v",
			err, want,
			cmp.Diff(err, want, cmpopts.EquateErrors()),
		)
	}
}

func cmpParseError(e1, e2 error) bool {
	//nolint:errorlint
	if e1 == e2 || errors.Is(e1, e2) || errors.Is(e2, e1) {
		return true
	}

	var pe1, pe2 *sip.ParseError
	if !errors.As(e1, &pe1) || !errors.As(e2, &pe2) {
		return false
	}
	return pe1.State == pe2.State &&
		(errors.Is(pe1.Err, pe2.Err) || errors.Is(pe2.Err, pe1.Err) || pe1.Err.Error() == pe2.Err.Error()) &&
		bytes.Equal(pe1.Data, pe2.Data) &&
		cmp.Equal(pe1.Msg, pe2.Msg)
}
