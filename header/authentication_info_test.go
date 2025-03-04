package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestAuthenticationInfo_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.AuthenticationInfo
		want string
	}{
		{"nil", (*header.AuthenticationInfo)(nil), ""},
		{"zero", &header.AuthenticationInfo{}, "Authentication-Info: "},
		{
			"full",
			&header.AuthenticationInfo{
				NextNonce:  "qwerty",
				QOP:        "auth-int",
				RspAuth:    "abcdef",
				CNonce:     "1q2w3e",
				NonceCount: 5,
			},
			`Authentication-Info: cnonce="1q2w3e", nc=00000005, nextnonce="qwerty", qop=auth-int, rspauth="abcdef"`,
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

func TestAuthenticationInfo_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.AuthenticationInfo
		wantRes string
		wantErr error
	}{
		{"nil", (*header.AuthenticationInfo)(nil), "", nil},
		{"zero", &header.AuthenticationInfo{}, "Authentication-Info: ", nil},
		{
			"full",
			&header.AuthenticationInfo{
				NextNonce:  "qwerty",
				QOP:        "auth-int",
				RspAuth:    "abcdef",
				CNonce:     "1q2w3e",
				NonceCount: 5,
			},
			`Authentication-Info: cnonce="1q2w3e", nc=00000005, nextnonce="qwerty", qop=auth-int, rspauth="abcdef"`,
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

func TestAuthenticationInfo_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.AuthenticationInfo
		want string
	}{
		{"nil", (*header.AuthenticationInfo)(nil), ""},
		{"zero", &header.AuthenticationInfo{}, ""},
		{
			"full",
			&header.AuthenticationInfo{
				NextNonce:  "qwerty",
				QOP:        "auth-int",
				RspAuth:    "abcdef",
				CNonce:     "1q2w3e",
				NonceCount: 5,
			},
			`cnonce="1q2w3e", nc=00000005, nextnonce="qwerty", qop=auth-int, rspauth="abcdef"`,
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

func TestAuthenticationInfo_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.AuthenticationInfo
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.AuthenticationInfo)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.AuthenticationInfo)(nil), (*header.AuthenticationInfo)(nil), true},
		{"zero ptr to nil ptr", &header.AuthenticationInfo{}, (*header.AuthenticationInfo)(nil), false},
		{"zero to zero", &header.AuthenticationInfo{}, header.AuthenticationInfo{}, true},
		{
			"not match 1",
			&header.AuthenticationInfo{
				NextNonce:  "qwerty",
				QOP:        "auth",
				RspAuth:    "abcdef",
				CNonce:     "1q2w3e",
				NonceCount: 5,
			},
			&header.AuthenticationInfo{
				NextNonce:  "QWERTY",
				QOP:        "auth",
				RspAuth:    "abcdef",
				CNonce:     "1q2w3e",
				NonceCount: 5,
			},
			false,
		},
		{
			"not match 2",
			&header.AuthenticationInfo{
				NextNonce:  "qwerty",
				QOP:        "auth",
				RspAuth:    "abcdef",
				CNonce:     "1q2w3e",
				NonceCount: 5,
			},
			&header.AuthenticationInfo{
				NextNonce:  "qwerty",
				QOP:        "auth",
				RspAuth:    "abcdef",
				CNonce:     "1q2w3e",
				NonceCount: 10,
			},
			false,
		},
		{
			"match",
			&header.AuthenticationInfo{
				NextNonce:  "qwerty",
				QOP:        "auth",
				RspAuth:    "abcdef",
				CNonce:     "1q2w3e",
				NonceCount: 5,
			},
			header.AuthenticationInfo{
				NextNonce:  "qwerty",
				QOP:        "AUTH",
				RspAuth:    "abcdef",
				CNonce:     "1q2w3e",
				NonceCount: 5,
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

func TestAuthenticationInfo_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.AuthenticationInfo
		want bool
	}{
		{"nil", (*header.AuthenticationInfo)(nil), false},
		{"zero", &header.AuthenticationInfo{}, false},
		{"invalid 1", &header.AuthenticationInfo{QOP: "a b c"}, false},
		{"invalid 2", &header.AuthenticationInfo{QOP: "auth"}, false},
		{"valid 1", &header.AuthenticationInfo{NextNonce: "qwerty"}, true},
		{"valid 2", &header.AuthenticationInfo{NextNonce: "qwerty", QOP: "auth"}, true},
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

func TestAuthenticationInfo_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.AuthenticationInfo
	}{
		{"nil", nil},
		{"zero", &header.AuthenticationInfo{}},
		{
			"full",
			&header.AuthenticationInfo{
				NextNonce:  "qwerty",
				QOP:        "auth",
				RspAuth:    "abcdef",
				CNonce:     "1q2w3e",
				NonceCount: 5,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.hdr.Clone()
			if c.hdr == nil {
				if got != nil {
					t.Errorf("hdr.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
