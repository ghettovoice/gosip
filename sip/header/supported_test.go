package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestSupported_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Supported
		want string
	}{
		{"nil", nil, ""},
		{"empty", header.Supported{}, "Supported: "},
		{"empty elem", header.Supported{""}, "Supported: "},
		{"full", header.Supported{"100rel", "Foo", "Bar"}, "Supported: 100rel, Foo, Bar"},
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

func TestSupported_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.Supported
		wantRes string
		wantErr error
	}{
		{"nil", nil, "", nil},
		{"empty", header.Supported{}, "Supported: ", nil},
		{"full", header.Supported{"100rel", "Foo", "Bar"}, "Supported: 100rel, Foo, Bar", nil},
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

func TestSupported_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Supported
		want string
	}{
		{"nil", nil, ""},
		{"empty", header.Supported{}, ""},
		{"full", header.Supported{"100rel", "Foo", "Bar"}, "100rel, Foo, Bar"},
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

func TestSupported_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Supported
		val  any
		want bool
	}{
		{"nil ptr to nil", nil, nil, false},
		{"nil ptr to nil ptr", nil, header.Supported(nil), true},
		{"zero ptr to nil ptr", header.Supported{}, header.Supported(nil), true},
		{"zero to zero", header.Supported{}, header.Supported{}, true},
		{"zero to zero ptr", header.Supported{}, &header.Supported{}, true},
		{"zero to nil ptr", header.Supported{}, (*header.Supported)(nil), false},
		{"not match 1", header.Supported{"100rel"}, header.Supported{}, false},
		{"not match 2", header.Supported{"100rel", "foo"}, header.Supported{"foo", "100rel"}, false},
		{"match", header.Supported{"100rel", "FOO"}, header.Supported{"100rel", "foo"}, true},
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

func TestSupported_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Supported
		want bool
	}{
		{"nil", nil, false},
		{"empty", header.Supported{}, true},
		{"valid", header.Supported{"100rel", "abc"}, true},
		{"invalid", header.Supported{"a b c"}, false},
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

func TestSupported_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Supported
	}{
		{"nil", nil},
		{"empty", header.Supported{}},
		{"full", header.Supported{"100rel", "foo", "bar"}},
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
