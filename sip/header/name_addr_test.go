package header_test

import (
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestNameAddr_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr header.NameAddr
		want string
	}{
		{"zero", header.NameAddr{}, "<>"},
		{
			"full 1",
			header.NameAddr{
				DisplayName: "Darth Vader",
				URI: &uri.SIP{
					Secured: true,
					User:    uri.User("user"),
					Addr:    uri.AddrFromHost("example.com"),
					Params:  make(uri.Values).Set("foo", "bar"),
				},
				Params: make(header.Values).Set("foo", "bar").Set("baz", ""),
			},
			`"Darth Vader" <sips:user@example.com;foo=bar>;baz;foo=bar`,
		},
		{
			"full 2",
			header.NameAddr{
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

func TestNameAddr_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr header.NameAddr
		val  any
		want bool
	}{
		{"zero to nil", header.NameAddr{}, nil, false},
		{"zero to zero", header.NameAddr{}, header.NameAddr{}, true},
		{"zero to zero ptr", header.NameAddr{}, &header.NameAddr{}, true},
		{"zero to nil ptr", header.NameAddr{}, (*header.NameAddr)(nil), false},
		{"not match 1", header.NameAddr{}, header.NameAddr{URI: &uri.SIP{User: uri.User("root")}}, false},
		{
			"not match 2",
			header.NameAddr{URI: &uri.SIP{User: uri.User("ROOT")}},
			header.NameAddr{URI: &uri.SIP{User: uri.User("root")}},
			false,
		},
		{
			"not match 3",
			header.NameAddr{
				URI:    &uri.SIP{User: uri.User("ROOT")},
				Params: make(types.Values).Set("expires", "123"),
			},
			header.NameAddr{
				URI: &uri.SIP{User: uri.User("root")},
			},
			false,
		},
		{
			"not match 3",
			header.NameAddr{
				URI:    &uri.SIP{User: uri.User("ROOT")},
				Params: make(types.Values).Set("expires", "123"),
			},
			header.NameAddr{
				URI:    &uri.SIP{User: uri.User("root")},
				Params: make(types.Values).Set("expires", "1"),
			},
			false,
		},
		{
			"match",
			header.NameAddr{
				DisplayName: "qwe ABC",
				URI:         &uri.SIP{User: uri.User("root")},
				Params:      make(types.Values).Set("expires", "1"),
			},
			header.NameAddr{
				DisplayName: "zxc",
				URI:         &uri.SIP{User: uri.User("root")},
				Params:      make(types.Values).Set("expires", "1").Set("foo", "bar"),
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

func TestNameAddr_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr header.NameAddr
		want bool
	}{
		{"zero", header.NameAddr{}, false},
		{"valid", header.NameAddr{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}}}, true},
		{"invalid 1", header.NameAddr{URI: (*uri.SIP)(nil)}, false},
		{
			"invalid 2",
			header.NameAddr{
				URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
				Params: header.Values{"f i e l d": {"123"}},
			},
			false,
		},
		{
			"invalid 3",
			header.NameAddr{
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

func TestNameAddr_IsZero(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr header.NameAddr
		want bool
	}{
		{"zero", header.NameAddr{}, true},
		{"not zero 1", header.NameAddr{DisplayName: "q"}, false},
		{"not zero 2", header.NameAddr{URI: (*uri.Any)(nil)}, false},
		{"not zero 3", header.NameAddr{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}}}, false},
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

func TestNameAddr_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr header.NameAddr
	}{
		{"zero", header.NameAddr{}},
		{
			"full",
			header.NameAddr{
				DisplayName: "qwe",
				URI:         &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
				Params:      header.Values{"expires": {"123"}, "foo": {"bar"}},
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

func TestNameAddr_MarshalText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr header.NameAddr
		want string
	}{
		{"zero", header.NameAddr{}, "<>"},
		{
			"with display uri params",
			header.NameAddr{
				DisplayName: "Alice",
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
					Params: make(uri.Values).
						Set("transport", "tcp").
						Set("lr", ""),
				},
				Params: make(header.Values).
					Set("expires", "3600").
					Set("foo", "bar"),
			},
			`"Alice" <sip:alice@example.com;lr;transport=tcp>;expires=3600;foo=bar`,
		},
		{
			"any uri with query",
			header.NameAddr{
				URI: &uri.Any{
					URL: url.URL{
						Scheme:   "https",
						Host:     "example.com",
						Path:     "/a/b",
						RawQuery: "foo=bar&baz=qux",
					},
				},
				Params: make(header.Values).
					Set("foo", "bar"),
			},
			"<https://example.com/a/b?foo=bar&baz=qux>;foo=bar",
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

func TestNameAddr_UnmarshalText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    header.NameAddr
		wantErr bool
	}{
		{"empty", "", header.NameAddr{}, false},
		{"brackets", "<>", header.NameAddr{}, false},
		{"invalid", "not a valid addr", header.NameAddr{}, true},
		{
			"with display uri params",
			`"Alice" <sip:alice@example.com;lr;transport=tcp>;expires=3600;foo=bar`,
			header.NameAddr{
				DisplayName: "Alice",
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
					Params: make(uri.Values).
						Set("lr", "").
						Set("transport", "tcp"),
				},
				Params: make(header.Values).
					Set("expires", "3600").
					Set("foo", "bar"),
			},
			false,
		},
		{
			"any uri with query",
			"<https://example.com/a/b?foo=bar&baz=qux>;foo=bar",
			header.NameAddr{
				URI: &uri.Any{
					URL: url.URL{
						Scheme:   "https",
						Host:     "example.com",
						Path:     "/a/b",
						RawQuery: "foo=bar&baz=qux",
					},
				},
				Params: make(header.Values).
					Set("foo", "bar"),
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.NameAddr
			if err := got.UnmarshalText([]byte(c.data)); err != nil {
				if !c.wantErr {
					t.Fatalf("addr.UnmarshalText(data) error = %v, want nil", err)
				}
				return
			}

			if c.wantErr {
				t.Fatal("addr.UnmarshalText(data) error = nil, want error")
			}

			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestNameAddr_RoundTripText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr header.NameAddr
	}{
		{"zero", header.NameAddr{}},
		{
			"with display uri params",
			header.NameAddr{
				DisplayName: "Alice",
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
					Params: make(uri.Values).
						Set("lr", "").
						Set("transport", "tcp"),
				},
				Params: make(header.Values).
					Set("expires", "3600").
					Set("foo", "bar"),
			},
		},
		{
			"any uri with query",
			header.NameAddr{
				URI: &uri.Any{
					URL: url.URL{
						Scheme:   "https",
						Host:     "example.com",
						Path:     "/a/b",
						RawQuery: "foo=bar&baz=qux",
					},
				},
				Params: make(header.Values).
					Set("foo", "bar"),
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

			var got header.NameAddr
			if err := got.UnmarshalText(data); err != nil {
				t.Fatalf("addr.UnmarshalText(data) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.addr); diff != "" {
				t.Fatalf("round-trip mismatched: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.addr, diff)
			}
		})
	}
}
