package sip_test

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"

	"braces.dev/errtrace"
	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

type customHeader struct {
	name string
	num  int
	str  string
}

func parseCustomHeader(name string, value []byte) sip.Header {
	parts := strings.Split(string(value), " ")
	num, _ := strconv.Atoi(parts[0])
	return &customHeader{name: name, num: num, str: parts[1]}
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

func (hdr *customHeader) Render(opts *header.RenderOptions) string {
	if hdr == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", hdr.CanonicName(), hdr.RenderValue())
}

func (hdr *customHeader) RenderTo(w io.Writer, opts *header.RenderOptions) (int, error) {
	if hdr == nil {
		return 0, nil
	}
	return errtrace.Wrap2(fmt.Fprint(w, hdr.Render(opts)))
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

	return util.EqFold(hdr.name, other.name) &&
		hdr.num == other.num &&
		util.EqFold(hdr.str, other.str)
}

func (hdr *customHeader) IsValid() bool {
	return hdr != nil && grammar.IsToken(hdr.name) && hdr.num > 0 && len(hdr.str) > 0
}

func TestAllHeaderElems(t *testing.T) {
	t.Parallel()

	hdrs := make(sip.Headers).
		Append(
			header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
					Addr:      header.HostPort("127.0.0.1", 5060),
				},
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TLS",
					Addr:      header.HostPort("127.0.0.2", 5061),
				},
			},
			header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TCP",
					Addr:      header.HostPort("127.0.0.3", 5062),
				},
			},
			header.Supported{"opt1"},
			header.Supported{"opt2", "opt3"},
		)

	cases := []struct {
		name string
		test func() error
	}{
		{
			"route",
			func() error {
				return errtrace.Wrap(testAllHeaderElems[header.Route](hdrs, "Route", "header.Route", []*header.NameAddr(nil)))
			},
		},
		{
			"supported",
			func() error {
				return errtrace.Wrap(testAllHeaderElems[header.Supported](
					hdrs,
					"Supported",
					"header.Supported",
					func() []*string {
						ptrs := make([]*string, 3)
						for i, el := range []string{"opt1", "opt2", "opt3"} {
							ptrs[i] = &el
						}
						return ptrs
					}(),
				))
			},
		},
		{
			"via",
			func() error {
				return errtrace.Wrap(testAllHeaderElems[header.Via](
					hdrs,
					"Via",
					"header.Via",
					[]*header.ViaHop{
						{
							Proto:     sip.ProtoVer20(),
							Transport: "UDP",
							Addr:      header.HostPort("127.0.0.1", 5060),
						},
						{
							Proto:     sip.ProtoVer20(),
							Transport: "TLS",
							Addr:      header.HostPort("127.0.0.2", 5061),
						},
						{
							Proto:     sip.ProtoVer20(),
							Transport: "TCP",
							Addr:      header.HostPort("127.0.0.3", 5062),
						},
					},
				))
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if err := c.test(); err != nil {
				t.Error(err)
			}
		})
	}
}

func testAllHeaderElems[H ~[]E, E any](hdrs sip.Headers, hname sip.HeaderName, htype string, want []*E) error {
	got := slices.Collect(sip.AllHeaderElems[H](hdrs, hname))
	if diff := cmp.Diff(got, want); diff != "" {
		return errtrace.Wrap(fmt.Errorf(
			"sip.AllHeaderElems[%s](hdrs, %q) = %+v, want %+v\ndiff (-got +want):\n%v",
			htype, hname, got, want, diff,
		))
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
				Addr:      header.HostPort("127.0.0.1", 5060),
			},
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TLS",
				Addr:      header.HostPort("127.0.0.2", 5061),
			},
		}).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      header.HostPort("127.0.0.3", 5062),
			},
		}).
		Append(header.Supported{"opt1"}).
		Append(header.Supported{"opt2", "opt3"}).
		Set(header.ContentLength(6))

	cases := []struct {
		name    string
		hname   sip.HeaderName
		fnname  string
		fn      func(sip.Headers, sip.HeaderName) (any, bool)
		wantHdr any
		wantOk  bool
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
					Addr:      header.HostPort("127.0.0.1", 5060),
				},
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TLS",
					Addr:      header.HostPort("127.0.0.2", 5061),
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			gotHdr, gotOk := c.fn(hdrs, c.hname)
			if diff := cmp.Diff([]any{gotHdr, gotOk}, []any{c.wantHdr, c.wantOk}); diff != "" {
				t.Errorf("%s(hdrs, %q) = %+v, want %+v\ndiff (-got +want):\n%v", c.fnname, c.hname, gotHdr, gotOk, diff)
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
				Addr:      header.HostPort("127.0.0.1", 5060),
			},
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TLS",
				Addr:      header.HostPort("127.0.0.2", 5061),
			},
		}).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      header.HostPort("127.0.0.3", 5062),
			},
		}).
		Append(header.Supported{"opt1"}).
		Append(header.Supported{"opt2", "opt3"})

	cases := []struct {
		name    string
		hname   sip.HeaderName
		fnname  string
		fn      func(sip.Headers, sip.HeaderName) (any, bool)
		wantHdr any
		wantOk  bool
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
					Addr:      header.HostPort("127.0.0.3", 5062),
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			gotHdr, gotOk := c.fn(hdrs, c.hname)
			if diff := cmp.Diff([]any{gotHdr, gotOk}, []any{c.wantHdr, c.wantOk}); diff != "" {
				t.Errorf("%s(hdrs, %q) = %+v, want %+v\ndiff (-got +want):\n%v", c.fnname, c.hname, gotHdr, gotOk, diff)
			}
		})
	}
}

func TestFirstHeaderElem(t *testing.T) {
	t.Parallel()

	hdrs := make(sip.Headers).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "UDP",
				Addr:      header.HostPort("127.0.0.1", 5060),
			},
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TLS",
				Addr:      header.HostPort("127.0.0.2", 5061),
			},
		}).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      header.HostPort("127.0.0.3", 5062),
			},
		}).
		Append(header.Supported{"opt1"}).
		Append(header.Supported{"opt2", "opt3"})

	//nolint:forcetypeassert
	cases := []struct {
		name     string
		hname    sip.HeaderName
		fnname   string
		fn       func(sip.Headers, sip.HeaderName) (any, bool)
		wantElem any
		wantOk   bool
	}{
		{
			"route",
			"Route",
			"sip.FirstHeaderElem[header.Route]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.FirstHeaderElem[header.Route](hdrs, name)
			},
			(*header.NameAddr)(nil),
			false,
		},
		{
			"via",
			"Via",
			"sip.FirstHeaderElem[header.Via]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.FirstHeaderElem[header.Via](hdrs, name)
			},
			&hdrs["Via"][0].(header.Via)[0],
			true,
		},
		{
			"supported",
			"Supported",
			"sip.FirstHeaderElem[header.Supported]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.FirstHeaderElem[header.Supported](hdrs, name)
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
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			gotEl, gotOk := c.fn(hdrs, c.hname)
			if diff := cmp.Diff([]any{gotEl, gotOk}, []any{c.wantElem, c.wantOk}, cmpOpts...); diff != "" {
				t.Errorf("%s(hdrs, %q) = %+v, want %+v\ndiff (-got +want):\n%v", c.fnname, c.hname, gotEl, gotOk, diff)
			}
		})
	}
}

func TestLastHeaderElem(t *testing.T) {
	t.Parallel()

	hdrs := make(sip.Headers).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "UDP",
				Addr:      header.HostPort("127.0.0.1", 5060),
			},
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TLS",
				Addr:      header.HostPort("127.0.0.2", 5061),
			},
		}).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      header.HostPort("127.0.0.3", 5062),
			},
		}).
		Append(header.Supported{"opt1"}).
		Append(header.Supported{"opt2", "opt3"})

	//nolint:forcetypeassert
	cases := []struct {
		name     string
		hname    sip.HeaderName
		fnname   string
		fn       func(sip.Headers, sip.HeaderName) (any, bool)
		wantElem any
		wantOk   bool
	}{
		{
			"route",
			"Route",
			"sip.LastHeaderElem[header.Route]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.LastHeaderElem[header.Route](hdrs, name)
			},
			(*header.NameAddr)(nil),
			false,
		},
		{
			"via",
			"Via",
			"sip.LastHeaderElem[header.Via]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.LastHeaderElem[header.Via](hdrs, name)
			},
			&hdrs["Via"][1].(header.Via)[0],
			true,
		},
		{
			"supported",
			"Supported",
			"sip.LastHeaderElem[header.Supported]",
			func(hdrs sip.Headers, name sip.HeaderName) (any, bool) {
				return sip.LastHeaderElem[header.Supported](hdrs, name)
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
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			gotElem, gotOk := c.fn(hdrs, c.hname)
			if diff := cmp.Diff([]any{gotElem, gotOk}, []any{c.wantElem, c.wantOk}, cmpOpts...); diff != "" {
				t.Errorf("%s(hdrs, %q) = %+v, want %+v\ndiff (-got +want):\n%v", c.fnname, c.hname, gotElem, gotOk, diff)
			}
		})
	}
}

func TestPopFirstHeaderElem(t *testing.T) {
	t.Parallel()

	hdrs := make(sip.Headers).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "UDP",
				Addr:      header.HostPort("127.0.0.1", 5060),
			},
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TLS",
				Addr:      header.HostPort("127.0.0.2", 5061),
			},
		}).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      header.HostPort("127.0.0.3", 5062),
			},
		}).
		Append(header.Supported{"opt1"}).
		Append(header.Supported{"opt2", "opt3"})

	t.Run("route", func(t *testing.T) {
		if got, ok := sip.PopFirstHeaderElem[header.Route](hdrs, "Route"); ok || got != nil {
			t.Errorf("sip.PopFirstHeaderElem[header.Route](hdrs, \"Route\") = %+v, %v, want nil, false", got, ok)
		}
	})

	t.Run("via", func(t *testing.T) {
		want := hdrs["Via"][0].(header.Via)[0] //nolint:forcetypeassert
		got, ok := sip.PopFirstHeaderElem[header.Via](hdrs, "Via")
		if diff := cmp.Diff(got, &want); !ok || diff != "" {
			t.Fatalf("sip.PopFirstHeaderElem[header.Via](hdrs, \"Via\") = %+v, %v, want %+v, true\ndiff (-got +want):\n%v",
				got, ok, &want, diff,
			)
		}

		via := []sip.Header{
			header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TLS",
					Addr:      header.HostPort("127.0.0.2", 5061),
				},
			},
			header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TCP",
					Addr:      header.HostPort("127.0.0.3", 5062),
				},
			},
		}
		newVia := hdrs.Get("Via")
		if diff := cmp.Diff(newVia, via); diff != "" {
			t.Fatalf("hdrs.Get(\"Via\") = %+v, want %+v\ndiff (-got +want):\n%v", newVia, via, diff)
		}
	})

	t.Run("supported", func(t *testing.T) {
		want := hdrs["Supported"][0].(header.Supported)[0] //nolint:forcetypeassert
		got, ok := sip.PopFirstHeaderElem[header.Supported](hdrs, "Supported")
		if diff := cmp.Diff(got, &want); !ok || diff != "" {
			t.Fatalf("sip.PopFirstHeaderElem[header.Supported](hdrs, \"Supported\") = %+v, %v, want %+v, true\ndiff (-got +want):\n%v",
				got, ok, &want, diff,
			)
		}

		supported := []sip.Header{
			header.Supported{"opt2", "opt3"},
		}
		newSupported := hdrs.Get("Supported")
		if diff := cmp.Diff(newSupported, supported); diff != "" {
			t.Fatalf("hdrs.Get(\"Supported\") = %+v, want %+v\ndiff (-got +want):\n%v", newSupported, supported, diff)
		}
	})
}

func TestPopLastHeaderElem(t *testing.T) {
	t.Parallel()

	hdrs := make(sip.Headers).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "UDP",
				Addr:      header.HostPort("127.0.0.1", 5060),
			},
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TLS",
				Addr:      header.HostPort("127.0.0.2", 5061),
			},
		}).
		Append(header.Via{
			{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      header.HostPort("127.0.0.3", 5062),
			},
		}).
		Append(header.Supported{"opt1"}).
		Append(header.Supported{"opt2", "opt3"})

	t.Run("route", func(t *testing.T) {
		if got, ok := sip.PopLastHeaderElem[header.Route](hdrs, "Route"); ok || got != nil {
			t.Errorf("sip.PopLastHeaderElem[header.Route](hdrs, \"Route\") = %+v, %v, want nil, false", got, ok)
		}
	})

	t.Run("via", func(t *testing.T) {
		want := hdrs["Via"][1].(header.Via)[0] //nolint:forcetypeassert
		got, ok := sip.PopLastHeaderElem[header.Via](hdrs, "Via")
		if diff := cmp.Diff(got, &want); !ok || diff != "" {
			t.Fatalf("sip.PopLastHeaderElem[header.Via](hdrs, \"Via\") = %+v, %v, want %+v, true\ndiff (-got +want):\n%v",
				got, ok, &want, diff,
			)
		}

		via := []sip.Header{
			header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
					Addr:      header.HostPort("127.0.0.1", 5060),
				},
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TLS",
					Addr:      header.HostPort("127.0.0.2", 5061),
				},
			},
		}
		newVia := hdrs.Get("Via")
		if diff := cmp.Diff(newVia, via); diff != "" {
			t.Fatalf("hdrs.Get(\"Via\") = %+v, want %+v\ndiff (-got +want):\n%v", newVia, via, diff)
		}
	})

	t.Run("supported", func(t *testing.T) {
		want := hdrs["Supported"][1].(header.Supported)[1] //nolint:forcetypeassert
		got, ok := sip.PopLastHeaderElem[header.Supported](hdrs, "Supported")
		if diff := cmp.Diff(got, &want); !ok || diff != "" {
			t.Fatalf("sip.PopLastHeaderElem[header.Supported](hdrs, \"Supported\") = %+v, %v, want %+v, true\ndiff (-got +want):\n%v",
				got, ok, &want, diff,
			)
		}

		supported := []sip.Header{
			header.Supported{"opt1"},
			header.Supported{"opt2"},
		}
		newSupported := hdrs.Get("Supported")
		if diff := cmp.Diff(newSupported, supported); diff != "" {
			t.Fatalf("hdrs.Get(\"Supported\") = %+v, want %+v\ndiff (-got +want):\n%v", newSupported, supported, diff)
		}
	})
}
