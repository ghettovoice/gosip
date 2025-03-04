package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestPriority_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Priority
		want string
	}{
		{"zero", header.Priority(""), "Priority: "},
		{"full", header.Priority("non-urgent"), "Priority: non-urgent"},
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

func TestPriority_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.Priority
		wantRes string
		wantErr error
	}{
		{"zero", header.Priority(""), "Priority: ", nil},
		{"full", header.Priority("non-urgent"), "Priority: non-urgent", nil},
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

func TestPriority_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Priority
		val  any
		want bool
	}{
		{"zero to nil", header.Priority(""), nil, false},
		{"zero to nil ptr", header.Priority(""), (*header.Priority)(nil), false},
		{"zero to zero", header.Priority(""), header.Priority(""), true},
		{"not match 1", header.Priority("non-urgent"), header.Priority(""), false},
		{"not match 2", header.Priority("non-urgent"), header.Priority("aaa-bbb-ccc"), false},
		{"match", header.Priority("non-urgent"), header.Priority("NON-URGENT"), true},
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

func TestPriority_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Priority
		want bool
	}{
		{"zero", header.Priority(""), false},
		{"valid", header.Priority("non-urgent"), true},
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

func TestPriority_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Priority
	}{
		{"zero", header.Priority("")},
		{"full", header.Priority("non-urgent")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Clone(); got != c.hdr {
				t.Errorf("hdr.Clone() = %+v, want %+v", got, c.hdr)
			}
		})
	}
}
