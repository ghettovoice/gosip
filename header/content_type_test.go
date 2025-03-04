package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestContentType_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ContentType
		opts *header.RenderOptions
		want string
	}{
		{"nil", (*header.ContentType)(nil), nil, ""},
		{"zero", &header.ContentType{}, nil, "Content-Type: /"},
		{
			"full",
			&header.ContentType{
				Type:    "application",
				Subtype: "sdp",
				Params:  make(header.Values).Set("charset", "UTF-8"),
			}, nil, "Content-Type: application/sdp;charset=UTF-8",
		},
		{
			"compact",
			&header.ContentType{
				Type:    "application",
				Subtype: "sdp",
				Params:  make(header.Values).Set("charset", "UTF-8"),
			}, &header.RenderOptions{Compact: true}, "c: application/sdp;charset=UTF-8",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Render(c.opts); got != c.want {
				t.Errorf("hdr.Render(opts) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestContentType_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.ContentType
		wantRes string
		wantErr error
	}{
		{"nil", (*header.ContentType)(nil), "", nil},
		{"zero", &header.ContentType{}, "Content-Type: /", nil},
		{
			"full",
			&header.ContentType{
				Type:    "application",
				Subtype: "sdp",
				Params:  make(header.Values).Set("charset", "UTF-8"),
			},
			"Content-Type: application/sdp;charset=UTF-8",
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

func TestContentType_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ContentType
		want string
	}{
		{"nil", (*header.ContentType)(nil), ""},
		{"zero", &header.ContentType{}, "/"},
		{
			"full",
			&header.ContentType{
				Type:    "application",
				Subtype: "sdp",
				Params:  make(header.Values).Set("charset", "UTF-8"),
			},
			"application/sdp;charset=UTF-8",
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

func TestContentType_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ContentType
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.ContentType)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.ContentType)(nil), (*header.ContentType)(nil), true},
		{"zero ptr to nil ptr", &header.ContentType{}, (*header.ContentType)(nil), false},
		{"zero ptr to zero val", &header.ContentType{}, header.ContentType{}, true},
		{
			"not match 1",
			&header.ContentType{},
			&header.ContentType{Type: "application", Subtype: "sdp"},
			false,
		},
		{
			"not match 2",
			&header.ContentType{Type: "application", Subtype: "*"},
			&header.ContentType{Type: "application", Subtype: "sdp"},
			false,
		},
		{
			"not match 3",
			&header.ContentType{Type: "application", Subtype: "sdp"},
			&header.ContentType{
				Type:    "application",
				Subtype: "sdp",
				Params:  make(header.Values).Set("charset", "UTF-8"),
			},
			false,
		},
		{
			"match",
			&header.ContentType{
				Type:    "application",
				Subtype: "sdp",
				Params:  make(header.Values).Set("charset", "UTF-8"),
			},
			header.ContentType{
				Type:    "application",
				Subtype: "sdp",
				Params:  make(header.Values).Set("charset", "UTF-8"),
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

func TestContentType_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ContentType
		want bool
	}{
		{"nil", (*header.ContentType)(nil), false},
		{"zero", &header.ContentType{}, false},
		{"invalid 1", &header.ContentType{Type: "text"}, false},
		{"invalid 2", &header.ContentType{Subtype: "plain"}, false},
		{"invalid 3", &header.ContentType{Type: "t e x t", Subtype: "plain"}, false},
		{"valid", &header.ContentType{Type: "*", Subtype: "*"}, true},
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

func TestContentType_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ContentType
	}{
		{"nil", nil},
		{"zero", &header.ContentType{}},
		{
			"full",
			&header.ContentType{
				Type:    "text",
				Subtype: "plain",
				Params:  make(header.Values).Set("charset", "UTF-8"),
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
