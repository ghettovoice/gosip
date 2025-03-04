package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestRoute_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Route
		want string
	}{
		{"nil", nil, ""},
		{"empty", header.Route{}, "Route: "},
		{"empty elem", header.Route{{}}, "Route: <>"},
		{
			"full",
			header.Route{
				{
					URI: &uri.SIP{
						User:   uri.User("foo"),
						Addr:   uri.Host("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("k", "v"),
				},
				{URI: &uri.Tel{Number: "+123", Params: make(header.Values).Set("ext", "555")}},
				{
					URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
					Params: make(header.Values).Set("a", "b"),
				},
			},
			"Route: <sip:foo@bar;lr>;k=v, <tel:+123;ext=555>, <sip:quux@quuz>;a=b",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Render(nil); got != c.want {
				t.Errorf("hdr.Render(nil) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRoute_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.Route
		wantRes string
		wantErr error
	}{
		{"nil", nil, "", nil},
		{"empty", header.Route{}, "Route: ", nil},
		{
			"full",
			header.Route{
				{
					URI: &uri.SIP{
						User:   uri.User("foo"),
						Addr:   uri.Host("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("k", "v"),
				},
				{URI: &uri.Tel{Number: "+123", Params: make(header.Values).Set("ext", "555")}},
				{
					URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
					Params: make(header.Values).Set("a", "b"),
				},
			},
			"Route: <sip:foo@bar;lr>;k=v, <tel:+123;ext=555>, <sip:quux@quuz>;a=b",
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := c.hdr.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo(sb, nil) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}
			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestRoute_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Route
		want string
	}{
		{"nil", nil, ""},
		{"empty", header.Route{}, ""},
		{
			"full",
			header.Route{
				{
					URI: &uri.SIP{
						User:   uri.User("foo"),
						Addr:   uri.Host("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("k", "v"),
				},
				{URI: &uri.Tel{Number: "+123", Params: make(header.Values).Set("ext", "555")}},
				{
					URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
					Params: make(header.Values).Set("a", "b"),
				},
			},
			"<sip:foo@bar;lr>;k=v, <tel:+123;ext=555>, <sip:quux@quuz>;a=b",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.String(); got != c.want {
				t.Errorf("hdr.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRoute_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Route
		val  any
		want bool
	}{
		{"nil ptr to nil", nil, nil, false},
		{"nil ptr to nil ptr", nil, header.Route(nil), true},
		{"zero ptr to nil ptr", header.Route{}, header.Route(nil), true},
		{"zero to zero", header.Route{}, header.Route{}, true},
		{"zero to zero ptr", header.Route{}, &header.Route{}, true},
		{"zero to nil ptr", header.Route{}, (*header.Route)(nil), false},
		{
			"not match",
			header.Route{
				{
					URI: &uri.SIP{
						User:   uri.User("foo"),
						Addr:   uri.Host("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("k", "v"),
				},
				{URI: &uri.SIP{User: uri.User("baz"), Addr: uri.Host("qux")}},
				{
					URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
					Params: make(header.Values).Set("a", "b"),
				},
			},
			header.Route{
				{URI: &uri.SIP{User: uri.User("baz"), Addr: uri.Host("qux")}},
				{
					URI: &uri.SIP{
						User:   uri.User("foo"),
						Addr:   uri.Host("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("k", "v"),
				},
				{
					URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
					Params: make(header.Values).Set("a", "b"),
				},
			},
			false,
		},
		{
			"match",
			header.Route{
				{
					URI: &uri.SIP{
						User:   uri.User("foo"),
						Addr:   uri.Host("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("k", "v"),
				},
				{URI: &uri.SIP{User: uri.User("baz"), Addr: uri.Host("qux")}},
				{
					URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
					Params: make(header.Values).Set("a", "b"),
				},
			},
			header.Route{
				{
					URI: &uri.SIP{
						User:   uri.User("foo"),
						Addr:   uri.Host("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("a", "b"),
				},
				{URI: &uri.SIP{User: uri.User("baz"), Addr: uri.Host("qux")}},
				{
					URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
					Params: make(header.Values).Set("k", "v"),
				},
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Equal(c.val); got != c.want {
				t.Errorf("hdr.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRoute_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Route
		want bool
	}{
		{"nil", header.Route(nil), false},
		{"empty", header.Route{}, false},
		{
			"valid",
			header.Route{{
				URI: &uri.Any{Scheme: "https", Host: "example.com", Path: "/a/b/c"},
			}},
			true,
		},
		{"invalid 1", header.Route{{URI: (*uri.Any)(nil)}}, false},
		{"invalid 2", header.Route{{URI: &uri.Any{}}}, false},
		{
			"invalid 3",
			header.Route{{
				URI:    &uri.Any{Scheme: "https", Host: "example.com"},
				Params: header.Values{"f i e l d": {"123"}},
			}},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.IsValid(); got != c.want {
				t.Errorf("hdr.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRoute_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Route
	}{
		{"nil", nil},
		{"empty", header.Route{}},
		{
			"full",
			header.Route{{
				URI:    &uri.Any{Scheme: "https", Host: "example.com", Path: "/a/b/c"},
				Params: header.Values{"expires": {"3600"}},
			}},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.hdr.Clone()
			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
