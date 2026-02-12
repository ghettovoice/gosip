package header_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestRecordRoute_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.RecordRoute
		want string
	}{
		{"nil", nil, ""},
		{"empty", header.RecordRoute{}, "Record-Route: "},
		{"empty elem", header.RecordRoute{{}}, "Record-Route: <>"},
		{
			"full",
			header.RecordRoute{
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
			"Record-Route: <sip:foo@bar;lr>;k=v, <tel:+123;ext=555>, <sip:quux@quuz>;a=b",
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

func TestRecordRoute_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.RecordRoute
		wantRes string
		wantErr error
	}{
		{"nil", nil, "", nil},
		{"empty", header.RecordRoute{}, "Record-Route: ", nil},
		{
			"full",
			header.RecordRoute{
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
			"Record-Route: <sip:foo@bar;lr>;k=v, <tel:+123;ext=555>, <sip:quux@quuz>;a=b",
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

func TestRecordRoute_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.RecordRoute
		want string
	}{
		{"nil", nil, ""},
		{"empty", header.RecordRoute{}, ""},
		{
			"full",
			header.RecordRoute{
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

func TestRecordRoute_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.RecordRoute
		val  any
		want bool
	}{
		{"nil ptr to nil", nil, nil, false},
		{"nil ptr to nil ptr", nil, header.RecordRoute(nil), true},
		{"zero ptr to nil ptr", header.RecordRoute{}, header.RecordRoute(nil), true},
		{"zero to zero", header.RecordRoute{}, header.RecordRoute{}, true},
		{"zero to zero ptr", header.RecordRoute{}, &header.RecordRoute{}, true},
		{"zero to nil ptr", header.RecordRoute{}, (*header.RecordRoute)(nil), false},
		{
			"not match",
			header.RecordRoute{
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
			header.RecordRoute{
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
			header.RecordRoute{
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
			header.RecordRoute{
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

func TestRecordRoute_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.RecordRoute
		want bool
	}{
		{"nil", nil, false},
		{"empty", header.RecordRoute{}, false},
		{
			"valid",
			header.RecordRoute{{
				URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
			}},
			true,
		},
		{"invalid 1", header.RecordRoute{{URI: (*uri.Any)(nil)}}, false},
		{"invalid 2", header.RecordRoute{{URI: &uri.Any{}}}, false},
		{
			"invalid 3",
			header.RecordRoute{{
				URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com"}},
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

func TestRecordRoute_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.RecordRoute
	}{
		{"nil", nil},
		{"empty", header.RecordRoute{}},
		{
			"full",
			header.RecordRoute{{
				URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
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
