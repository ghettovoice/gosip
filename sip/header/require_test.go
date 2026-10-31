package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestRequire_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Require
		want string
	}{
		{"nil", nil, ""},
		{"empty", header.Require{}, "Require: "},
		{"empty elem", header.Require{""}, "Require: "},
		{"full", header.Require{"100rel", "Foo", "Bar"}, "Require: 100rel, Foo, Bar"},
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

func TestRequire_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.Require
		wantRes string
		wantErr error
	}{
		{"nil", nil, "", nil},
		{"empty", header.Require{}, "Require: ", nil},
		{"full", header.Require{"100rel", "Foo", "Bar"}, "Require: 100rel, Foo, Bar", nil},
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

func TestRequire_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Require
		want string
	}{
		{"nil", nil, ""},
		{"empty", header.Require{}, ""},
		{"full", header.Require{"100rel", "Foo", "Bar"}, "100rel, Foo, Bar"},
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

func TestRequire_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Require
		val  any
		want bool
	}{
		{"nil ptr to nil", nil, nil, false},
		{"nil ptr to nil ptr", nil, header.Require(nil), true},
		{"zero ptr to nil ptr", header.Require{}, header.Require(nil), true},
		{"zero to zero", header.Require{}, header.Require{}, true},
		{"zero to zero ptr", header.Require{}, &header.Require{}, true},
		{"zero to nil ptr", header.Require{}, (*header.Require)(nil), false},
		{"not match 1", header.Require{"100rel"}, header.Require{}, false},
		{"not match 2", header.Require{"100rel", "foo"}, header.Require{"foo", "100rel"}, false},
		{"match", header.Require{"100rel", "FOO"}, header.Require{"100rel", "foo"}, true},
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

func TestRequire_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Require
		want bool
	}{
		{"nil", nil, false},
		{"empty", header.Require{}, false},
		{"valid", header.Require{"100rel", "abc"}, true},
		{"invalid", header.Require{"a b c"}, false},
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

func TestRequire_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Require
	}{
		{"nil", nil},
		{"empty", header.Require{}},
		{"full", header.Require{"100rel", "foo", "bar"}},
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
