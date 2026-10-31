package header_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestProxyAuthenticate_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.ProxyAuthenticate
		want string
	}{
		{"nil", nil, ""},
		{"zero", &header.ProxyAuthenticate{}, "Proxy-Authenticate: "},
		{
			"digest",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{
					Realm: "atlanta.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.MakeHostAddr("ss1.carrier.com")},
						&uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
						&uri.Any{Path: "/a/b/c"},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "MD5",
					Opaque:    "qwerty",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Proxy-Authenticate: Digest algorithm=MD5, nonce=\"f84f1cec41e6cbe5aea9c8e88d359\", " +
				"opaque=\"qwerty\", qop=\"auth,auth-int\", realm=\"atlanta.com\", stale=true, " +
				"domain=\"sip:ss1.carrier.com http://example.com /a/b/c\", p1=abc, p2=\"a b c\"",
		},
		{
			"bearer",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.BearerChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Proxy-Authenticate: Bearer error=\"qwerty\", realm=\"atlanta.com\", scope=\"abc\", " +
				"authz_server=\"http://example.com\", p1=abc, p2=\"a b c\"",
		},
		{
			"custom",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Proxy-Authenticate: Custom p1=abc, p2=\"a b c\"",
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

func TestProxyAuthenticate_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     *header.ProxyAuthenticate
		wantRes string
		wantErr error
	}{
		{"nil", nil, "", nil},
		{"zero", &header.ProxyAuthenticate{}, "Proxy-Authenticate: ", nil},
		{
			"custom",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Proxy-Authenticate: Custom p1=abc, p2=\"a b c\"",
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

func TestProxyAuthenticate_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.ProxyAuthenticate
		want string
	}{
		{"nil", (*header.ProxyAuthenticate)(nil), ""},
		{"zero", &header.ProxyAuthenticate{}, ""},
		{
			"custom",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Custom p1=abc, p2=\"a b c\"",
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

func TestProxyAuthenticate_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.ProxyAuthenticate
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.ProxyAuthenticate)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.ProxyAuthenticate)(nil), (*header.ProxyAuthenticate)(nil), true},
		{"zero ptr to nil ptr", &header.ProxyAuthenticate{}, (*header.ProxyAuthenticate)(nil), false},
		{"zero to zero", &header.ProxyAuthenticate{}, header.ProxyAuthenticate{}, true},
		{
			"not match 1",
			&header.ProxyAuthenticate{},
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Qwerty",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			false,
		},
		{
			"not match 2",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{
					Realm: "atlanta.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.MakeHostAddr("ss1.carrier.com")},
						&uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
						&uri.Any{Path: "/a/b/c"},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "MD5",
					Opaque:    "qwerty",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			&header.ProxyAuthenticate{
				AuthChallenge: &header.BearerChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			false,
		},
		{
			"match",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "ABC").Set("p2", `"a b c"`),
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

func TestProxyAuthenticate_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.ProxyAuthenticate
		want bool
	}{
		{"nil", (*header.ProxyAuthenticate)(nil), false},
		{"zero", &header.ProxyAuthenticate{}, false},
		{
			"invalid 1",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{Realm: "ATLANTA.com"},
			},
			false,
		},
		{"invalid 2", &header.ProxyAuthenticate{AuthChallenge: &header.BearerChallenge{}}, false},
		{"invalid 3", &header.ProxyAuthenticate{AuthChallenge: (*header.AnyChallenge)(nil)}, false},
		{
			"valid",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc"),
				},
			},
			true,
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

func TestProxyAuthenticate_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.ProxyAuthenticate
	}{
		{"nil", nil},
		{"zero", &header.ProxyAuthenticate{}},
		{
			"digest",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{
					Realm: "ATLANTA.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.MakeHostAddr("SS1.CARRIER.COM")},
						&uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
						&uri.Any{Path: "/a/b/c"},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "md5",
					Opaque:    "qwerty",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
		},
		{
			"bearer",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.BearerChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
		},
		{
			"custom",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.hdr.Clone()
			if tt.hdr == nil {
				if got != nil {
					t.Errorf("hdr.Clone() = %+v, want nil", got)
				}
				return
			}

			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}

func TestProxyAuthenticate_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.ProxyAuthenticate
		want string
	}{
		{"nil", nil, "null"},
		{"zero", &header.ProxyAuthenticate{}, `{"name":"Proxy-Authenticate","value":""}`},
		{
			"digest",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{
					Realm: "atlanta.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.MakeHostAddr("ss1.carrier.com")},
						&uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
						&uri.Any{Path: "/a/b/c"},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "MD5",
					Opaque:    "qwerty",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			`{"name":"Proxy-Authenticate","value":"Digest algorithm=MD5, nonce=\"f84f1cec41e6cbe5aea9c8e88d359\", opaque=\"qwerty\", qop=\"auth,auth-int\", realm=\"atlanta.com\", stale=true, domain=\"sip:ss1.carrier.com http://example.com /a/b/c\", p1=abc, p2=\"a b c\""}`,
		},
		{
			"bearer",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.BearerChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			`{"name":"Proxy-Authenticate","value":"Bearer error=\"qwerty\", realm=\"atlanta.com\", scope=\"abc\", authz_server=\"http://example.com\", p1=abc, p2=\"a b c\""}`,
		},
		{
			"custom",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			`{"name":"Proxy-Authenticate","value":"Custom p1=abc, p2=\"a b c\""}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			if got := string(got); got != tt.want {
				t.Fatalf("json.Marshal(hdr) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProxyAuthenticate_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    *header.ProxyAuthenticate
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"Custom p1=abc"}`, nil, true},
		{"empty value", `{"name":"Proxy-Authenticate","value":""}`, &header.ProxyAuthenticate{}, false},
		{"wrong header", `{"name":"WWW-Authenticate","value":"Digest realm=\"atlanta.com\""}`, nil, true},
		{"invalid json", `{"name":"Proxy-Authenticate","value":`, nil, true},
		{
			"digest",
			`{"name":"Proxy-Authenticate","value":"Digest algorithm=MD5, nonce=\"f84f1cec41e6cbe5aea9c8e88d359\", opaque=\"qwerty\", qop=\"auth,auth-int\", realm=\"atlanta.com\", stale=true, domain=\"sip:ss1.carrier.com http://example.com /a/b/c\", p1=abc, p2=\"a b c\""}`,
			&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{
					Realm: "atlanta.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.MakeHostAddr("ss1.carrier.com")},
						&uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
						&uri.Any{Path: "/a/b/c"},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "MD5",
					Opaque:    "qwerty",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			false,
		},
		{
			"bearer",
			`{"name":"Proxy-Authenticate","value":"Bearer error=\"qwerty\", realm=\"atlanta.com\", scope=\"abc\", authz_server=\"http://example.com\", p1=abc, p2=\"a b c\""}`,
			&header.ProxyAuthenticate{
				AuthChallenge: &header.BearerChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			false,
		},
		{
			"custom",
			`{"name":"Proxy-Authenticate","value":"Custom p1=abc, p2=\"a b c\""}`,
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got *header.ProxyAuthenticate
			if err := json.Unmarshal([]byte(tt.data), &got); err != nil {
				if !tt.wantErr {
					t.Fatalf("json.Unmarshal() error = %v, want nil", err)
				}
				return
			}

			if tt.wantErr {
				t.Fatal("json.Unmarshal() error = nil, want error")
			}

			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.want, diff)
			}
		})
	}
}

func TestProxyAuthenticate_RoundTripJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.ProxyAuthenticate
	}{
		{"nil", nil},
		{"zero", &header.ProxyAuthenticate{}},
		{
			"digest",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{
					Realm: "atlanta.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.MakeHostAddr("ss1.carrier.com")},
						&uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
						&uri.Any{Path: "/a/b/c"},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "MD5",
					Opaque:    "qwerty",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
		},
		{
			"bearer",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.BearerChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
		},
		{
			"custom",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			var got *header.ProxyAuthenticate
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}
