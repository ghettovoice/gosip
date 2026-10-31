package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestMIMEVersion_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.MIMEVersion
		want string
	}{
		{"zero", header.MIMEVersion(""), "MIME-Version: "},
		{"full", header.MIMEVersion("1.5"), "MIME-Version: 1.5"},
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

func TestMIMEVersion_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.MIMEVersion
		wantRes string
		wantErr error
	}{
		{"zero", header.MIMEVersion(""), "MIME-Version: ", nil},
		{"full", header.MIMEVersion("1.5"), "MIME-Version: 1.5", nil},
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

func TestMIMEVersion_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.MIMEVersion
		val  any
		want bool
	}{
		{"zero to nil", header.MIMEVersion(""), nil, false},
		{"zero to nil ptr", header.MIMEVersion(""), (*header.MIMEVersion)(nil), false},
		{"zero to zero", header.MIMEVersion(""), header.MIMEVersion(""), true},
		{"not match 1", header.MIMEVersion("1.5"), header.MIMEVersion(""), false},
		{"not match 2", header.MIMEVersion("1.5"), header.MIMEVersion("2.0"), false},
		{"match", header.MIMEVersion("1.5"), header.MIMEVersion("1.5"), true},
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

func TestMIMEVersion_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.MIMEVersion
		want bool
	}{
		{"zero", header.MIMEVersion(""), false},
		{"invalid", header.MIMEVersion("1.5 abc"), false},
		{"valid", header.MIMEVersion("1.5"), true},
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

func TestMIMEVersion_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.MIMEVersion
	}{
		{"zero", header.MIMEVersion("")},
		{"full", header.MIMEVersion("1.5")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Clone(); got != tt.hdr {
				t.Errorf("hdr.Clone() = %+v, want %+v", got, tt.hdr)
			}
		})
	}
}
