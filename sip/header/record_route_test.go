package header_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestRecordRoute_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
						User:   uri.MakeUserInfo("foo"),
						Addr:   uri.MakeHostAddr("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("k", "v"),
				},
				{URI: &uri.Tel{Number: "+123", Params: make(header.Values).Set("ext", "555")}},
				{
					URI:    &uri.SIP{User: uri.MakeUserInfo("quux"), Addr: uri.MakeHostAddr("quuz")},
					Params: make(header.Values).Set("a", "b"),
				},
			},
			"Record-Route: <sip:foo@bar;lr>;k=v, <tel:+123;ext=555>, <sip:quux@quuz>;a=b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Render(); got != tt.want {
				t.Errorf("hdr.Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRecordRoute_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
						User:   uri.MakeUserInfo("foo"),
						Addr:   uri.MakeHostAddr("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("k", "v"),
				},
				{URI: &uri.Tel{Number: "+123", Params: make(header.Values).Set("ext", "555")}},
				{
					URI:    &uri.SIP{User: uri.MakeUserInfo("quux"), Addr: uri.MakeHostAddr("quuz")},
					Params: make(header.Values).Set("a", "b"),
				},
			},
			"Record-Route: <sip:foo@bar;lr>;k=v, <tel:+123;ext=555>, <sip:quux@quuz>;a=b",
			nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder

			_, err := tt.hdr.RenderTo(&sb)
			if diff := cmp.Diff(err, tt.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo(&sb) error = %v, want %v\ndiff (-got +want):\n%v", err, tt.wantErr, diff)
			}

			if got := sb.String(); got != tt.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, tt.wantRes)
			}
		})
	}
}

func TestRecordRoute_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
						User:   uri.MakeUserInfo("foo"),
						Addr:   uri.MakeHostAddr("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("k", "v"),
				},
				{URI: &uri.Tel{Number: "+123", Params: make(header.Values).Set("ext", "555")}},
				{
					URI:    &uri.SIP{User: uri.MakeUserInfo("quux"), Addr: uri.MakeHostAddr("quuz")},
					Params: make(header.Values).Set("a", "b"),
				},
			},
			"<sip:foo@bar;lr>;k=v, <tel:+123;ext=555>, <sip:quux@quuz>;a=b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.String(); got != tt.want {
				t.Errorf("hdr.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRecordRoute_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
						User:   uri.MakeUserInfo("foo"),
						Addr:   uri.MakeHostAddr("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("k", "v"),
				},
				{URI: &uri.SIP{User: uri.MakeUserInfo("baz"), Addr: uri.MakeHostAddr("qux")}},
				{
					URI:    &uri.SIP{User: uri.MakeUserInfo("quux"), Addr: uri.MakeHostAddr("quuz")},
					Params: make(header.Values).Set("a", "b"),
				},
			},
			header.RecordRoute{
				{URI: &uri.SIP{User: uri.MakeUserInfo("baz"), Addr: uri.MakeHostAddr("qux")}},
				{
					URI: &uri.SIP{
						User:   uri.MakeUserInfo("foo"),
						Addr:   uri.MakeHostAddr("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("k", "v"),
				},
				{
					URI:    &uri.SIP{User: uri.MakeUserInfo("quux"), Addr: uri.MakeHostAddr("quuz")},
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
						User:   uri.MakeUserInfo("foo"),
						Addr:   uri.MakeHostAddr("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("k", "v"),
				},
				{URI: &uri.SIP{User: uri.MakeUserInfo("baz"), Addr: uri.MakeHostAddr("qux")}},
				{
					URI:    &uri.SIP{User: uri.MakeUserInfo("quux"), Addr: uri.MakeHostAddr("quuz")},
					Params: make(header.Values).Set("a", "b"),
				},
			},
			header.RecordRoute{
				{
					URI: &uri.SIP{
						User:   uri.MakeUserInfo("foo"),
						Addr:   uri.MakeHostAddr("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("a", "b"),
				},
				{URI: &uri.SIP{User: uri.MakeUserInfo("baz"), Addr: uri.MakeHostAddr("qux")}},
				{
					URI:    &uri.SIP{User: uri.MakeUserInfo("quux"), Addr: uri.MakeHostAddr("quuz")},
					Params: make(header.Values).Set("k", "v"),
				},
			},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Equal(tt.val); got != tt.want {
				t.Errorf("hdr.Equal(val) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecordRoute_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.IsValid(); got != tt.want {
				t.Errorf("hdr.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecordRoute_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.hdr.Clone()
			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}
