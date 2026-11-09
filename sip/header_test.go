package sip_test

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

type customHeader struct {
	name string
	num  int
	str  string
}

func parseCustomHeader(name string, value []byte) (sip.Header, error) {
	parts := strings.Split(string(value), " ")
	num, _ := strconv.Atoi(parts[0])
	return &customHeader{name: name, num: num, str: parts[1]}, nil
}

func (hdr *customHeader) CanonicName() sip.HeaderName { return header.CanonicName(hdr.name) }

func (hdr *customHeader) CompactName() sip.HeaderName { return header.CanonicName(hdr.name) }

func (hdr *customHeader) Clone() sip.Header {
	if hdr == nil {
		return nil
	}

	hdr2 := *hdr

	return &hdr2
}

func (hdr *customHeader) Render(...header.RenderOptions) string {
	if hdr == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", hdr.CanonicName(), hdr.RenderValue())
}

func (hdr *customHeader) RenderTo(w io.Writer, opts ...header.RenderOptions) (int, error) {
	if hdr == nil {
		return 0, nil
	}
	return fmt.Fprint(w, hdr.Render(opts...))
}

func (hdr *customHeader) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return fmt.Sprintf("%d %s", hdr.num, hdr.str)
}

func (hdr *customHeader) Equal(val any) bool {
	var other *customHeader
	switch v := val.(type) {
	case *customHeader:
		other = v
	case customHeader:
		other = &v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return util.EqualFold(hdr.name, other.name) &&
		hdr.num == other.num &&
		util.EqualFold(hdr.str, other.str)
}

func (hdr *customHeader) IsValid() bool {
	return hdr != nil && grammar.IsToken(hdr.name) && hdr.num > 0 && len(hdr.str) > 0
}

func TestAllHeaderValues(t *testing.T) {
	t.Parallel()

	hdrs := make(sip.Headers).
		Append(
			header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
					Addr:      sip.MakeHostPortAddr("127.0.0.1", 5060),
				},
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TLS",
					Addr:      sip.MakeHostPortAddr("127.0.0.2", 5061),
				},
			},
			header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TCP",
					Addr:      sip.MakeHostPortAddr("127.0.0.3", 5062),
				},
			},
			header.Supported{"opt1"},
			header.Supported{"opt2", "opt3"},
		)

	tests := []struct {
		name string
		test func() error
	}{
		{
			"route",
			func() error {
				return testAllHeaderValues[header.Route](hdrs, "Route", "header.Route", []*header.NameAddr(nil))
			},
		},
		{
			"supported",
			func() error {
				return testAllHeaderValues[header.Supported](
					hdrs,
					"Supported",
					"header.Supported",
					func() []*header.OptionTag {
						ptrs := make([]*header.OptionTag, 3)
						for i, el := range []header.OptionTag{"opt1", "opt2", "opt3"} {
							ptrs[i] = &el
						}

						return ptrs
					}(),
				)
			},
		},
		{
			"via",
			func() error {
				return testAllHeaderValues[header.Via](
					hdrs,
					"Via",
					"header.Via",
					[]*header.ViaHop{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      sip.MakeHostPortAddr("127.0.0.1", 5060),
						},
						{
							Proto:     sip.ProtoVer20(),
							Transport: "TLS",
							Addr:      sip.MakeHostPortAddr("127.0.0.2", 5061),
						},
						{
							Proto:     sip.ProtoVer20(),
							Transport: "TCP",
							Addr:      sip.MakeHostPortAddr("127.0.0.3", 5062),
						},
					},
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.test(); err != nil {
				t.Error(err)
			}
		})
	}
}

func testAllHeaderValues[H ~[]E, E any](hdrs sip.Headers, hdrName sip.HeaderName, hdrType string, want []*E) error {
	got := slices.Collect(sip.AllHeaderValues[H](hdrs, hdrName))
	if diff := cmp.Diff(got, want); diff != "" {
		return errors.Errorf(
			"sip.AllHeaderValues[%s](hdrs, %q) = %+v, want %+v\ndiff (-got +want):\n%v",
			hdrType, hdrName, got, want, diff,
		)
	}

	return nil
}

func TestFirstHeader(t *testing.T) {
	t.Parallel()

	hdrs := make(sip.Headers).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "UDP",
				Addr:      sip.MakeHostPortAddr("127.0.0.1", 5060),
			},
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TLS",
				Addr:      sip.MakeHostPortAddr("127.0.0.2", 5061),
			},
		}).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      sip.MakeHostPortAddr("127.0.0.3", 5062),
			},
		}).
		Append(header.Supported{"opt1"}).
		Append(header.Supported{"opt2", "opt3"}).
		Set(header.ContentLength(6))

	tests := []struct {
		name    string
		hdrName sip.HeaderName
		fnName  string
		fn      func(sip.Headers, sip.HeaderName) (any, bool)
		wantHdr any
		wantOK  bool
	}{
		{
			"from",
			"From",
			"sip.FirstHeader[*header.From]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.FirstHeader[*header.From](hdrs, name)
			},
			(*header.From)(nil),
			false,
		},
		{
			"via",
			"Via",
			"sip.FirstHeader[header.Via]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.FirstHeader[header.Via](hdrs, name)
			},
			header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
					Addr:      sip.MakeHostPortAddr("127.0.0.1", 5060),
				},
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TLS",
					Addr:      sip.MakeHostPortAddr("127.0.0.2", 5061),
				},
			},
			true,
		},
		{
			"supported",
			"Supported",
			"sip.FirstHeader[header.Supported]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.FirstHeader[header.Supported](hdrs, name)
			},
			header.Supported{"opt1"},
			true,
		},
		{
			"content-length",
			"Content-Length",
			"sip.FirstHeader[header.ContentLength]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.FirstHeader[header.ContentLength](hdrs, name)
			},
			header.ContentLength(6),
			true,
		},
		{
			"max-forwards",
			"Max-Forwards",
			"sip.FirstHeader[header.MaxForwards]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.FirstHeader[header.MaxForwards](hdrs, name)
			},
			header.MaxForwards(0),
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotHdr, gotOK := tt.fn(hdrs, tt.hdrName)
			if diff := cmp.Diff([]any{gotHdr, gotOK}, []any{tt.wantHdr, tt.wantOK}); diff != "" {
				t.Errorf("%s(hdrs, %q) = %+v, want %+v\ndiff (-got +want):\n%v", tt.fnName, tt.hdrName, gotHdr, gotOK, diff)
			}
		})
	}
}

func TestLastHeader(t *testing.T) {
	t.Parallel()

	hdrs := make(sip.Headers).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "UDP",
				Addr:      sip.MakeHostPortAddr("127.0.0.1", 5060),
			},
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TLS",
				Addr:      sip.MakeHostPortAddr("127.0.0.2", 5061),
			},
		}).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      sip.MakeHostPortAddr("127.0.0.3", 5062),
			},
		}).
		Append(header.Supported{"opt1"}).
		Append(header.Supported{"opt2", "opt3"})

	tests := []struct {
		name    string
		hdrName sip.HeaderName
		fnName  string
		fn      func(sip.Headers, sip.HeaderName) (any, bool)
		wantHdr any
		wantOK  bool
	}{
		{
			"from",
			"From",
			"sip.LastHeader[*header.From]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.LastHeader[*header.From](hdrs, name)
			},
			(*header.From)(nil),
			false,
		},
		{
			"via",
			"Via",
			"sip.LastHeader[header.Via]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.LastHeader[header.Via](hdrs, name)
			},
			header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TCP",
					Addr:      sip.MakeHostPortAddr("127.0.0.3", 5062),
				},
			},
			true,
		},
		{
			"supported",
			"Supported",
			"sip.LastHeader[header.Supported]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.LastHeader[header.Supported](hdrs, name)
			},
			header.Supported{"opt2", "opt3"},
			true,
		},
		{
			"content-length",
			"Content-Length",
			"sip.LastHeader[header.ContentLength]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.LastHeader[header.ContentLength](hdrs, name)
			},
			header.ContentLength(0),
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotHdr, gotOK := tt.fn(hdrs, tt.hdrName)
			if diff := cmp.Diff([]any{gotHdr, gotOK}, []any{tt.wantHdr, tt.wantOK}); diff != "" {
				t.Errorf("%s(hdrs, %q) = %+v, want %+v\ndiff (-got +want):\n%v", tt.fnName, tt.hdrName, gotHdr, gotOK, diff)
			}
		})
	}
}

func TestFirstHeaderValue(t *testing.T) {
	t.Parallel()

	hdrs := make(sip.Headers).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "UDP",
				Addr:      sip.MakeHostPortAddr("127.0.0.1", 5060),
			},
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TLS",
				Addr:      sip.MakeHostPortAddr("127.0.0.2", 5061),
			},
		}).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      sip.MakeHostPortAddr("127.0.0.3", 5062),
			},
		}).
		Append(header.Supported{"opt1"}).
		Append(header.Supported{"opt2", "opt3"})

	tests := []struct {
		name     string
		hdrName  sip.HeaderName
		fnName   string
		fn       func(sip.Headers, sip.HeaderName) (any, bool)
		wantElem any
		wantOK   bool
	}{
		{
			"route",
			"Route",
			"sip.FirstHeaderValue[header.Route]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.FirstHeaderValue[header.Route](hdrs, name)
			},
			(*header.NameAddr)(nil),
			false,
		},
		{
			"via",
			"Via",
			"sip.FirstHeaderValue[header.Via]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.FirstHeaderValue[header.Via](hdrs, name)
			},
			&hdrs["Via"][0].(header.Via)[0],
			true,
		},
		{
			"supported",
			"Supported",
			"sip.FirstHeaderValue[header.Supported]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.FirstHeaderValue[header.Supported](hdrs, name)
			},
			&hdrs["Supported"][0].(header.Supported)[0],
			true,
		},
	}

	cmpOpts := []cmp.Option{
		cmp.Transformer("entityAddr", func(ptr *header.NameAddr) header.NameAddr {
			if ptr == nil {
				return header.NameAddr{}
			}
			return *ptr
		}),
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotEl, gotOK := tt.fn(hdrs, tt.hdrName)
			if diff := cmp.Diff([]any{gotEl, gotOK}, []any{tt.wantElem, tt.wantOK}, cmpOpts...); diff != "" {
				t.Errorf("%s(hdrs, %q) = %+v, want %+v\ndiff (-got +want):\n%v", tt.fnName, tt.hdrName, gotEl, gotOK, diff)
			}
		})
	}
}

func TestLastHeaderValue(t *testing.T) {
	t.Parallel()

	hdrs := make(sip.Headers).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "UDP",
				Addr:      sip.MakeHostPortAddr("127.0.0.1", 5060),
			},
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TLS",
				Addr:      sip.MakeHostPortAddr("127.0.0.2", 5061),
			},
		}).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      sip.MakeHostPortAddr("127.0.0.3", 5062),
			},
		}).
		Append(header.Supported{"opt1"}).
		Append(header.Supported{"opt2", "opt3"})

	tests := []struct {
		name     string
		hdrName  sip.HeaderName
		fnName   string
		fn       func(sip.Headers, sip.HeaderName) (any, bool)
		wantElem any
		wantOK   bool
	}{
		{
			"route",
			"Route",
			"sip.LastHeaderValue[header.Route]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.LastHeaderValue[header.Route](hdrs, name)
			},
			(*header.NameAddr)(nil),
			false,
		},
		{
			"via",
			"Via",
			"sip.LastHeaderValue[header.Via]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.LastHeaderValue[header.Via](hdrs, name)
			},
			&hdrs["Via"][1].(header.Via)[0],
			true,
		},
		{
			"supported",
			"Supported",
			"sip.LastHeaderValue[header.Supported]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.LastHeaderValue[header.Supported](hdrs, name)
			},
			&hdrs["Supported"][1].(header.Supported)[1],
			true,
		},
	}

	cmpOpts := []cmp.Option{
		cmp.Transformer("entityAddr", func(ptr *header.NameAddr) header.NameAddr {
			if ptr == nil {
				return header.NameAddr{}
			}
			return *ptr
		}),
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotElem, gotOK := tt.fn(hdrs, tt.hdrName)
			if diff := cmp.Diff([]any{gotElem, gotOK}, []any{tt.wantElem, tt.wantOK}, cmpOpts...); diff != "" {
				t.Errorf("%s(hdrs, %q) = %+v, want %+v\ndiff (-got +want):\n%v", tt.fnName, tt.hdrName, gotElem, gotOK, diff)
			}
		})
	}
}

func TestPopFirstHeaderValue(t *testing.T) {
	t.Parallel()

	hdrs := make(sip.Headers).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "UDP",
				Addr:      sip.MakeHostPortAddr("127.0.0.1", 5060),
			},
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TLS",
				Addr:      sip.MakeHostPortAddr("127.0.0.2", 5061),
			},
		}).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      sip.MakeHostPortAddr("127.0.0.3", 5062),
			},
		}).
		Append(header.Supported{"opt1"}).
		Append(header.Supported{"opt2", "opt3"})

	t.Run("route", func(t *testing.T) {
		t.Parallel()

		h := hdrs.Clone()

		if got, ok := sip.PopFirstHeaderValue[header.Route](h, "Route"); ok || got != nil {
			t.Errorf("sip.PopFirstHeaderValue[header.Route](hdrs, \"Route\") = %+v, %v, want nil, false", got, ok)
		}
	})

	t.Run("via", func(t *testing.T) {
		t.Parallel()

		h := hdrs.Clone()

		want := h["Via"][0].(header.Via)[0]

		got, ok := sip.PopFirstHeaderValue[header.Via](h, "Via")
		if diff := cmp.Diff(got, &want); !ok || diff != "" {
			t.Fatalf(
				"sip.PopFirstHeaderValue[header.Via](hdrs, \"Via\") = %+v, %v, want %+v, true\ndiff (-got +want):\n%v",
				got, ok, &want, diff,
			)
		}

		via := []sip.Header{
			header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TLS",
					Addr:      sip.MakeHostPortAddr("127.0.0.2", 5061),
				},
			},
			header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TCP",
					Addr:      sip.MakeHostPortAddr("127.0.0.3", 5062),
				},
			},
		}

		newVia := h.Get("Via")
		if diff := cmp.Diff(newVia, via); diff != "" {
			t.Fatalf("hdrs.Get(\"Via\") = %+v, want %+v\ndiff (-got +want):\n%v", newVia, via, diff)
		}
	})

	t.Run("supported", func(t *testing.T) {
		t.Parallel()

		h := hdrs.Clone()

		want := h["Supported"][0].(header.Supported)[0]

		got, ok := sip.PopFirstHeaderValue[header.Supported](h, "Supported")
		if diff := cmp.Diff(got, &want); !ok || diff != "" {
			t.Fatalf(
				"sip.PopFirstHeaderValue[header.Supported](hdrs, \"Supported\") = %+v, %v, want %+v, true\ndiff (-got +want):\n%v",
				got, ok, &want, diff,
			)
		}

		supported := []sip.Header{
			header.Supported{"opt2", "opt3"},
		}

		newSupported := h.Get("Supported")
		if diff := cmp.Diff(newSupported, supported); diff != "" {
			t.Fatalf("hdrs.Get(\"Supported\") = %+v, want %+v\ndiff (-got +want):\n%v", newSupported, supported, diff)
		}
	})
}

func TestPopLastHeaderValue(t *testing.T) {
	t.Parallel()

	hdrs := make(sip.Headers).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "UDP",
				Addr:      sip.MakeHostPortAddr("127.0.0.1", 5060),
			},
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TLS",
				Addr:      sip.MakeHostPortAddr("127.0.0.2", 5061),
			},
		}).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      sip.MakeHostPortAddr("127.0.0.3", 5062),
			},
		}).
		Append(header.Supported{"opt1"}).
		Append(header.Supported{"opt2", "opt3"})

	t.Run("route", func(t *testing.T) {
		t.Parallel()

		h := hdrs.Clone()

		if got, ok := sip.PopLastHeaderValue[header.Route](h, "Route"); ok || got != nil {
			t.Errorf("sip.PopLastHeaderValue[header.Route](hdrs, \"Route\") = %+v, %v, want nil, false", got, ok)
		}
	})

	t.Run("via", func(t *testing.T) {
		t.Parallel()

		h := hdrs.Clone()

		want := h["Via"][1].(header.Via)[0]

		got, ok := sip.PopLastHeaderValue[header.Via](h, "Via")
		if diff := cmp.Diff(got, &want); !ok || diff != "" {
			t.Fatalf(
				"sip.PopLastHeaderValue[header.Via](hdrs, \"Via\") = %+v, %v, want %+v, true\ndiff (-got +want):\n%v",
				got, ok, &want, diff,
			)
		}

		via := []sip.Header{
			header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
					Addr:      sip.MakeHostPortAddr("127.0.0.1", 5060),
				},
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TLS",
					Addr:      sip.MakeHostPortAddr("127.0.0.2", 5061),
				},
			},
		}

		newVia := h.Get("Via")
		if diff := cmp.Diff(newVia, via); diff != "" {
			t.Fatalf("hdrs.Get(\"Via\") = %+v, want %+v\ndiff (-got +want):\n%v", newVia, via, diff)
		}
	})

	t.Run("supported", func(t *testing.T) {
		t.Parallel()

		h := hdrs.Clone()

		want := h["Supported"][1].(header.Supported)[1]

		got, ok := sip.PopLastHeaderValue[header.Supported](h, "Supported")
		if diff := cmp.Diff(got, &want); !ok || diff != "" {
			t.Fatalf(
				"sip.PopLastHeaderValue[header.Supported](hdrs, \"Supported\") = %+v, %v, want %+v, true\ndiff (-got +want):\n%v",
				got, ok, &want, diff,
			)
		}

		supported := []sip.Header{
			header.Supported{"opt1"},
			header.Supported{"opt2"},
		}

		newSupported := h.Get("Supported")
		if diff := cmp.Diff(newSupported, supported); diff != "" {
			t.Fatalf("hdrs.Get(\"Supported\") = %+v, want %+v\ndiff (-got +want):\n%v", newSupported, supported, diff)
		}
	})
}
