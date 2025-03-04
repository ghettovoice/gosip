package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestSubject_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Subject
		want string
	}{
		{"zero", header.Subject(""), "Subject: "},
		{"full", header.Subject("Tech Support"), "Subject: Tech Support"},
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

func TestSubject_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.Subject
		wantRes string
		wantErr error
	}{
		{"zero", header.Subject(""), "Subject: ", nil},
		{"full", header.Subject("Tech Support"), "Subject: Tech Support", nil},
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

func TestSubject_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Subject
		val  any
		want bool
	}{
		{"zero to nil", header.Subject(""), nil, false},
		{"zero to nil ptr", header.Subject(""), (*header.Subject)(nil), false},
		{"zero to zero", header.Subject(""), header.Subject(""), true},
		{"not match 1", header.Subject("Tech Support"), header.Subject(""), false},
		{"not match 2", header.Subject("Tech Support"), header.Subject("tech support"), false},
		{"match", header.Subject("Tech Support"), header.Subject("Tech Support"), true},
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

func TestSubject_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Subject
		want bool
	}{
		{"zero", header.Subject(""), true},
		{"valid", header.Subject("Tech Support"), true},
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

func TestSubject_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Subject
	}{
		{"zero", header.Subject("")},
		{"full", header.Subject("Tech Support")},
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
