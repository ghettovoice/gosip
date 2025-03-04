package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestCSeq_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.CSeq
		want string
	}{
		{"nil", (*header.CSeq)(nil), ""},
		{"zero", &header.CSeq{}, "CSeq: 0 "},
		{"full", &header.CSeq{SeqNum: 4711, Method: "INVITE"}, "CSeq: 4711 INVITE"},
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

func TestCSeq_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.CSeq
		wantRes string
		wantErr error
	}{
		{"nil", (*header.CSeq)(nil), "", nil},
		{"zero", &header.CSeq{}, "CSeq: 0 ", nil},
		{"full", &header.CSeq{SeqNum: 4711, Method: "INVITE"}, "CSeq: 4711 INVITE", nil},
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

func TestCSeq_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.CSeq
		want string
	}{
		{"nil", (*header.CSeq)(nil), ""},
		{"zero", &header.CSeq{}, "0 "},
		{"full", &header.CSeq{SeqNum: 4711, Method: "INVITE"}, "4711 INVITE"},
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

func TestCSeq_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.CSeq
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.CSeq)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.CSeq)(nil), (*header.CSeq)(nil), true},
		{"zero ptr to nil ptr", &header.CSeq{}, (*header.CSeq)(nil), false},
		{"zero ptr to zero val", &header.CSeq{}, header.CSeq{}, true},
		{
			"not match 1",
			&header.CSeq{},
			header.CSeq{SeqNum: 4711, Method: "INVITE"},
			false,
		},
		{
			"not match 2",
			&header.CSeq{SeqNum: 4711, Method: "INVITE"},
			header.CSeq{SeqNum: 4711, Method: "BYE"},
			false,
		},
		{
			"not match 3",
			&header.CSeq{SeqNum: 4711, Method: "INVITE"},
			header.CSeq{SeqNum: 123, Method: "INVITE"},
			false,
		},
		{
			"match",
			&header.CSeq{SeqNum: 4711, Method: "INVITE"},
			header.CSeq{SeqNum: 4711, Method: "invite"},
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

func TestCSeq_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.CSeq
		want bool
	}{
		{"nil", (*header.CSeq)(nil), false},
		{"zero", &header.CSeq{}, false},
		{"invalid 1", &header.CSeq{Method: "INVITE"}, false},
		{"invalid 2", &header.CSeq{SeqNum: 4711, Method: "a c k"}, false},
		{"valid", &header.CSeq{SeqNum: 4711, Method: "INVITE"}, true},
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

func TestCSeq_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.CSeq
	}{
		{"nil", nil},
		{"zero", &header.CSeq{}},
		{"full", &header.CSeq{SeqNum: 4711, Method: "INVITE"}},
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
