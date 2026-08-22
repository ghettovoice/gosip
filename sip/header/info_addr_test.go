package header_test

import (
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestInfoAddr_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr header.InfoAddr
		want string
	}{
		{"zero", header.InfoAddr{}, "<>"},
		{
			"full 1",
			header.InfoAddr{
				URI: &uri.SIP{
					Secured: true,
					User:    uri.User("user"),
					Addr:    uri.AddrFromHost("example.com"),
					Params:  make(uri.Values).Set("foo", "bar"),
				},
				Params: make(header.Values).Set("foo", "bar").Set("baz", ""),
			},
			"<sips:user@example.com;foo=bar>;baz;foo=bar",
		},
		{
			"full 2",
			header.InfoAddr{
				URI: &uri.Any{
					URL: url.URL{
						Scheme:   "https",
						Host:     "example.com",
						Path:     "/a/b/c",
						RawQuery: "foo=bar",
					},
				},
				Params: make(header.Values).Set("foo", "bar").Set("baz", ""),
			},
			"<https://example.com/a/b/c?foo=bar>;baz;foo=bar",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.addr.String(); got != c.want {
				t.Errorf("addr.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestInfoAddr_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr header.InfoAddr
		val  any
		want bool
	}{
		{"zero to nil", header.InfoAddr{}, nil, false},
		{"zero to zero", header.InfoAddr{}, header.InfoAddr{}, true},
		{"zero to zero ptr", header.InfoAddr{}, &header.InfoAddr{}, true},
		{"zero to nil ptr", header.InfoAddr{}, (*header.InfoAddr)(nil), false},
		{"not match 1", header.InfoAddr{}, header.InfoAddr{URI: &uri.SIP{User: uri.User("user")}}, false},
		{
			"not match 2",
			header.InfoAddr{
				URI: &uri.Any{
					URL: url.URL{
						Scheme: "https",
						Host:   "example.com",
					},
				},
			},
			header.InfoAddr{
				URI: &uri.Any{
					URL: url.URL{
						Scheme:   "https",
						Host:     "example.com",
						Path:     "/a/b/c",
						RawQuery: "foo=bar",
					},
				},
			},
			false,
		},
		{
			"not match 3",
			header.InfoAddr{
				URI: &uri.Any{
					URL: url.URL{
						Scheme:   "https",
						Host:     "example.com",
						Path:     "/a/b/c",
						RawQuery: "foo=bar",
					},
				},
			},
			header.InfoAddr{
				URI: &uri.Any{
					URL: url.URL{
						Scheme:   "https",
						Host:     "example.com",
						Path:     "/a/b/c",
						RawQuery: "foo=bar",
					},
				},
				Params: make(header.Values).Set("purpose", "qwe"),
			},
			false,
		},
		{
			"not match 4",
			header.InfoAddr{
				URI: &uri.Any{
					URL: url.URL{
						Scheme:   "https",
						Host:     "example.com",
						Path:     "/a/b/c",
						RawQuery: "foo=bar",
					},
				},
				Params: make(header.Values).Set("purpose", "asd"),
			},
			header.InfoAddr{
				URI: &uri.Any{
					URL: url.URL{
						Scheme:   "https",
						Host:     "example.com",
						Path:     "/a/b/c",
						RawQuery: "foo=bar",
					},
				},
				Params: make(header.Values).Set("purpose", "qwe"),
			},
			false,
		},
		{
			"match",
			header.InfoAddr{
				URI: &uri.Any{
					URL: url.URL{
						Scheme:   "https",
						Host:     "example.com",
						Path:     "/a/b/c",
						RawQuery: "foo=bar",
					},
				},
				Params: make(header.Values).Set("purpose", "qwe"),
			},
			header.InfoAddr{
				URI: &uri.Any{
					URL: url.URL{
						Scheme:   "https",
						Host:     "example.com",
						Path:     "/a/b/c",
						RawQuery: "foo=bar",
					},
				},
				Params: make(header.Values).Set("purpose", "qwe").Set("foo", "bar"),
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.addr.Equal(c.val); got != c.want {
				t.Errorf("addr.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestInfoAddr_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr header.InfoAddr
		want bool
	}{
		{"zero", header.InfoAddr{}, false},
		{"valid", header.InfoAddr{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}}}, true},
		{"invalid 1", header.InfoAddr{URI: (*uri.Any)(nil)}, false},
		{
			"invalid 2",
			header.InfoAddr{
				URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
				Params: header.Values{"f i e l d": {"123"}},
			},
			false,
		},
		{
			"invalid 3",
			header.InfoAddr{
				URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
				Params: header.Values{"field": {" a b c "}},
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.addr.IsValid(); got != c.want {
				t.Errorf("addr.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestInfoAddr_IsZero(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr header.InfoAddr
		want bool
	}{
		{"zero", header.InfoAddr{}, true},
		{"not zero 1", header.InfoAddr{URI: (*uri.Any)(nil)}, false},
		{"not zero 2", header.InfoAddr{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}}}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.addr.IsZero(); got != c.want {
				t.Errorf("addr.IsZero() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestInfoAddr_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr header.InfoAddr
	}{
		{"zero", header.InfoAddr{}},
		{
			"full",
			header.InfoAddr{
				URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
				Params: header.Values{"purpose": {"qwe"}, "foo": {"bar"}},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.addr.Clone()
			if diff := cmp.Diff(got, c.addr); diff != "" {
				t.Errorf("addr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.addr, diff)
			}
		})
	}
}

func TestInfoAddr_MarshalText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr header.InfoAddr
		want string
	}{
		{"zero", header.InfoAddr{}, "<>"},
		{
			"full",
			header.InfoAddr{
				URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/tones/main"}},
				Params: make(header.Values).Set("purpose", "ring").Set("foo", "bar"),
			},
			"<https://example.com/tones/main>;foo=bar;purpose=ring",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := c.addr.MarshalText()
			if err != nil {
				t.Fatalf("addr.MarshalText() error = %v, want nil", err)
			}

			if string(got) != c.want {
				t.Fatalf("addr.MarshalText() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestInfoAddr_UnmarshalText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    header.InfoAddr
		wantErr bool
	}{
		{"empty", "", header.InfoAddr{}, false},
		{"brackets", "<>", header.InfoAddr{}, false},
		{"invalid", "<bad uri", header.InfoAddr{}, true},
		{
			"full",
			"<https://example.com/tones/main>;purpose=ring",
			header.InfoAddr{
				URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/tones/main"}},
				Params: make(header.Values).Set("purpose", "ring"),
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var addr header.InfoAddr
			if err := addr.UnmarshalText([]byte(c.data)); err != nil {
				if !c.wantErr {
					t.Fatalf("addr.UnmarshalText() error = %v, want nil", err)
				}
				return
			}

			if c.wantErr {
				t.Fatal("addr.UnmarshalText() error = nil, want error")
			}

			if diff := cmp.Diff(addr, c.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", addr, c.want, diff)
			}
		})
	}
}

func TestInfoAddr_RoundTripText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr header.InfoAddr
	}{
		{"zero", header.InfoAddr{}},
		{
			"full",
			header.InfoAddr{
				URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/tones/main"}},
				Params: make(header.Values).Set("purpose", "ring").Set("foo", "bar"),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := c.addr.MarshalText()
			if err != nil {
				t.Fatalf("addr.MarshalText() error = %v, want nil", err)
			}

			var got header.InfoAddr
			if err := got.UnmarshalText(data); err != nil {
				t.Fatalf("addr.UnmarshalText(data) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.addr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.addr, diff)
			}
		})
	}
}
