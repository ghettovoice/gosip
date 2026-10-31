package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestContentEncoding_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.ContentEncoding
		opts []header.RenderOptions
		want string
	}{
		{"nil", header.ContentEncoding(nil), nil, ""},
		{"empty", header.ContentEncoding{}, nil, "Content-Encoding: "},
		{"empty elem", header.ContentEncoding{""}, nil, "Content-Encoding: "},
		{"full", header.ContentEncoding{"qwe", "ZIP", "tar"}, nil, "Content-Encoding: qwe, ZIP, tar"},
		{"compact", header.ContentEncoding{"qwe", "ZIP", "tar"}, []header.RenderOptions{{Compact: true}}, "e: qwe, ZIP, tar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Render(tt.opts...); got != tt.want {
				t.Errorf("hdr.Render(opts...) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContentEncoding_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.ContentEncoding
		wantRes string
		wantErr error
	}{
		{"nil", header.ContentEncoding(nil), "", nil},
		{"empty", header.ContentEncoding{}, "Content-Encoding: ", nil},
		{"full", header.ContentEncoding{"qwe", "ZIP", "tar"}, "Content-Encoding: qwe, ZIP, tar", nil},
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

func TestContentEncoding_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.ContentEncoding
		want string
	}{
		{"nil", header.ContentEncoding(nil), ""},
		{"empty", header.ContentEncoding{}, ""},
		{"full", header.ContentEncoding{"qwe", "ZIP", "tar"}, "qwe, ZIP, tar"},
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

func TestContentEncoding_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.ContentEncoding
		val  any
		want bool
	}{
		{"nil ptr to nil", header.ContentEncoding(nil), nil, false},
		{"nil ptr to nil ptr", header.ContentEncoding(nil), header.ContentEncoding(nil), true},
		{"zero ptr to nil ptr", header.ContentEncoding{}, header.ContentEncoding(nil), true},
		{"zero to zero", header.ContentEncoding{}, header.ContentEncoding{}, true},
		{"zero to zero ptr", header.ContentEncoding{}, &header.ContentEncoding{}, true},
		{"zero to nil ptr", header.ContentEncoding{}, (*header.ContentEncoding)(nil), false},
		{"not match 1", header.ContentEncoding{"qwe"}, header.ContentEncoding{}, false},
		{"not match 2", header.ContentEncoding{"qwe", "ZIP"}, header.ContentEncoding{"ZIP", "qwe"}, false},
		{"match", header.ContentEncoding{"qwe", "ZIP"}, header.ContentEncoding{"QWE", "zip"}, true},
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

func TestContentEncoding_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.ContentEncoding
		want bool
	}{
		{"nil", header.ContentEncoding(nil), false},
		{"empty", header.ContentEncoding{}, false},
		{"valid", header.ContentEncoding{"gzip", "QWE"}, true},
		{"invalid", header.ContentEncoding{"t a r"}, false},
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

func TestContentEncoding_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.ContentEncoding
	}{
		{"nil", nil},
		{"empty", header.ContentEncoding{}},
		{"full", header.ContentEncoding{"gzip", "QWE"}},
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

func TestContentEncoding_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.ContentEncoding
		want string
	}{
		{"nil", nil, "null"},
		{"empty", header.ContentEncoding{}, `{"name":"Content-Encoding","value":""}`},
		{"single", header.ContentEncoding{"gzip"}, `{"name":"Content-Encoding","value":"gzip"}`},
		{"multiple", header.ContentEncoding{"gzip", "tar"}, `{"name":"Content-Encoding","value":"gzip, tar"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v, want nil", err)
			}

			if got := string(got); got != tt.want {
				t.Fatalf("json.Marshal() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContentEncoding_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    header.ContentEncoding
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"gzip"}`, nil, true},
		{"empty value", `{"name":"Content-Encoding","value":""}`, header.ContentEncoding{}, false},
		{"wrong header", `{"name":"From","value":"<sip:alice@example.com>"}`, nil, true},
		{"invalid json", `{"name":"Content-Encoding","value":`, nil, true},
		{"invalid value", `{"name":"Content-Encoding","value":"g z i p"}`, nil, true},
		{"single", `{"name":"Content-Encoding","value":"gzip"}`, header.ContentEncoding{"gzip"}, false},
		{"multiple", `{"name":"Content-Encoding","value":"gzip, tar"}`, header.ContentEncoding{"gzip", "tar"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got header.ContentEncoding
			if err := json.Unmarshal([]byte(tt.data), &got); err != nil {
				if !tt.wantErr {
					t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
				}
				return
			}

			if tt.wantErr {
				t.Fatal("json.Unmarshal(data, got) error = nil, want error")
			}

			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.want, diff)
			}
		})
	}
}

func TestContentEncoding_RoundTripJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.ContentEncoding
	}{
		{"nil", nil},
		{"empty", header.ContentEncoding{}},
		{"single", header.ContentEncoding{"gzip"}},
		{"multiple", header.ContentEncoding{"gzip", "tar"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v, want nil", err)
			}

			var got header.ContentEncoding
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}
