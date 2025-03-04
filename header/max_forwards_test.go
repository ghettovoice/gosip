package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestMaxForwards_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.MaxForwards
		want string
	}{
		{"zero", header.MaxForwards(0), "Max-Forwards: 0"},
		{"full", header.MaxForwards(123), "Max-Forwards: 123"},
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

func TestMaxForwards_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.MaxForwards
		wantRes string
		wantErr error
	}{
		{"zero", header.MaxForwards(0), "Max-Forwards: 0", nil},
		{"full", header.MaxForwards(123), "Max-Forwards: 123", nil},
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

func TestMaxForwards_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.MaxForwards
		val  any
		want bool
	}{
		{"zero to nil", header.MaxForwards(0), nil, false},
		{"zero to nil ptr", header.MaxForwards(0), (*header.MaxForwards)(nil), false},
		{"zero to zero", header.MaxForwards(0), header.MaxForwards(0), true},
		{"not match 1", header.MaxForwards(123), header.MaxForwards(0), false},
		{"not match 2", header.MaxForwards(123), header.MaxForwards(456), false},
		{"match", header.MaxForwards(123), header.MaxForwards(123), true},
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

func TestMaxForwards_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.MaxForwards
		want bool
	}{
		{"zero", header.MaxForwards(0), true},
		{"full", header.MaxForwards(123), true},
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

func TestMaxForwards_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.MaxForwards
	}{
		{"zero", header.MaxForwards(0)},
		{"full", header.MaxForwards(123)},
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
