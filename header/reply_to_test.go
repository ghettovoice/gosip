package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestReplyTo_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ReplyTo
		want string
	}{
		{"nil", nil, ""},
		{"zero", &header.ReplyTo{}, "Reply-To: <>"},
		{
			"full",
			&header.ReplyTo{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			"Reply-To: \"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>;tag=a48s",
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

func TestReplyTo_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.ReplyTo
		wantRes string
		wantErr error
	}{
		{"nil", nil, "", nil},
		{"zero", &header.ReplyTo{}, "Reply-To: <>", nil},
		{
			"full",
			&header.ReplyTo{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			"Reply-To: \"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>;tag=a48s",
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

func TestReplyTo_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ReplyTo
		want string
	}{
		{"nil", nil, ""},
		{"zero", &header.ReplyTo{}, "<>"},
		{
			"full",
			&header.ReplyTo{
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

func TestReplyTo_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ReplyTo
		val  any
		want bool
	}{
		{"nil ptr to nil", nil, nil, false},
		{"nil ptr to nil ptr", nil, (*header.ReplyTo)(nil), true},
		{"zero ptr to nil ptr", &header.ReplyTo{}, (*header.ReplyTo)(nil), false},
		{"zero ptr to zero val", &header.ReplyTo{}, header.ReplyTo{}, true},
		{
			"not match 1",
			&header.ReplyTo{},
			header.ReplyTo{
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
			&header.ReplyTo{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			&header.ReplyTo{
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
			&header.ReplyTo{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s").Set("x", "def"),
			},
			&header.ReplyTo{
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
			&header.ReplyTo{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			header.ReplyTo{
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

func TestReplyTo_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ReplyTo
		want bool
	}{
		{"nil", nil, false},
		{"zero", &header.ReplyTo{}, false},
		{"invalid", &header.ReplyTo{URI: (*uri.SIP)(nil)}, false},
		{
			"valid",
			&header.ReplyTo{
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

func TestReplyTo_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ReplyTo
	}{
		{"nil", nil},
		{"zero", &header.ReplyTo{}},
		{
			"full",
			&header.ReplyTo{
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
