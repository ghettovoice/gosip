package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestTo_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.To
		want string
	}{
		{"nil", (*header.To)(nil), ""},
		{"zero", &header.To{}, "To: <>"},
		{
			"full",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			"To: \"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>;tag=a48s",
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

func TestTo_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.To
		wantRes string
		wantErr error
	}{
		{"nil", (*header.To)(nil), "", nil},
		{"zero", &header.To{}, "To: <>", nil},
		{
			"full",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			"To: \"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>;tag=a48s",
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

func TestTo_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.To
		want string
	}{
		{"nil", (*header.To)(nil), ""},
		{"zero", &header.To{}, "<>"},
		{
			"full",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			"\"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>;tag=a48s",
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

func TestTo_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.To
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.To)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.To)(nil), (*header.To)(nil), true},
		{"zero ptr to nil ptr", &header.To{}, (*header.To)(nil), false},
		{"zero ptr to zero val", &header.To{}, header.To{}, true},
		{
			"not match 1",
			&header.To{},
			header.To{
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
			},
			false,
		},
		{
			"not match 2",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User: uri.User("AGB"),
					Addr: uri.Host("bell-telephone.com"),
				},
				Params: make(header.Values).Set("tag", "qwerty"),
			},
			false,
		},
		{
			"not match 3",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s").Set("x", "def"),
			},
			&header.To{
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s").Set("x", "abc"),
			},
			false,
		},
		{
			"match",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			header.To{
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s").Set("x", "abc"),
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

func TestTo_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.To
		want bool
	}{
		{"nil", (*header.To)(nil), false},
		{"zero", &header.To{}, false},
		{"invalid", &header.To{URI: (*uri.SIP)(nil)}, false},
		{
			"valid",
			&header.To{
				URI: &uri.SIP{Addr: uri.Host("bell-telephone.com")},
			},
			true,
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

func TestTo_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.To
	}{
		{"nil", (*header.To)(nil)},
		{"zero", &header.To{}},
		{
			"full",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s").Set("x", "def"),
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
