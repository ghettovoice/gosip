package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestAny_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.Any
		want string
	}{
		{"nil", (*header.Any)(nil), ""},
		{"zero", &header.Any{}, ": "},
		{"empty name", &header.Any{Name: ""}, ": "},
		{"empty value", &header.Any{Name: "x-custom"}, "X-Custom: "},
		{"full", &header.Any{Name: "x-custom", Value: "abc"}, "X-Custom: abc"},
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

func TestAny_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     *header.Any
		wantRes string
		wantErr error
	}{
		{"nil", (*header.Any)(nil), "", nil},
		{"zero", &header.Any{}, ": ", nil},
		{"full", &header.Any{Name: "x-custom", Value: "abc"}, "X-Custom: abc", nil},
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

func TestAny_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.Any
		want string
	}{
		{"nil", (*header.Any)(nil), ""},
		{"zero", &header.Any{}, ""},
		{"full", &header.Any{Name: "x-custom", Value: "abc"}, "abc"},
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

func TestAny_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.Any
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.Any)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.Any)(nil), (*header.Any)(nil), true},
		{"zero ptr to nil ptr", &header.Any{}, (*header.Any)(nil), false},
		{"zero to zero", &header.Any{}, header.Any{}, true},
		{"not match 1", &header.Any{Name: "x-custom"}, &header.Any{}, false},
		{"not match 2", &header.Any{Name: "x-custom", Value: "abc"}, &header.Any{Name: "x-custom", Value: "def"}, false},
		{"not match 3", &header.Any{Name: "x-custom", Value: "abc"}, &header.Any{Name: "x-custom", Value: "ABC"}, false},
		{"match", &header.Any{Name: "x-custom", Value: "abc"}, &header.Any{Name: "X-Custom", Value: "abc"}, true},
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

func TestAny_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.Any
		want bool
	}{
		{"nil", (*header.Any)(nil), false},
		{"zero", &header.Any{}, false},
		{"invalid", &header.Any{Name: "a b c"}, false},
		{"valid 1", &header.Any{Name: "x-custom"}, true},
		{"valid 2", &header.Any{Name: "x-custom", Value: "abc"}, true},
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

func TestAny_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.Any
	}{
		{"nil", nil},
		{"zero", &header.Any{}},
		{"full", &header.Any{Name: "X-Custom", Value: "ABC"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.hdr.Clone()
			if tt.hdr == nil {
				if got != nil {
					t.Errorf("hdr.Clone() = %+v, want nil", got)
				}
				return
			}

			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}

func TestAny_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.Any
		want string
	}{
		{"nil", nil, "null"},
		{"zero", &header.Any{}, `{"name":"","value":""}`},
		{"empty name", &header.Any{Name: "", Value: "abc"}, `{"name":"","value":"abc"}`},
		{"empty value", &header.Any{Name: "x-custom", Value: ""}, `{"name":"X-Custom","value":""}`},
		{"full", &header.Any{Name: "x-custom", Value: "abc"}, `{"name":"X-Custom","value":"abc"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			if got := string(got); got != tt.want {
				t.Fatalf("json.Marshal(hdr) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAny_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    *header.Any
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, &header.Any{}, false},
		{"empty name and value", `{"name":"","value":""}`, &header.Any{}, false},
		{"empty name", `{"name":"","value":"abc"}`, &header.Any{Name: "", Value: "abc"}, false},
		{"empty value", `{"name":"X-Custom","value":""}`, &header.Any{Name: "X-Custom", Value: ""}, false},
		{"invalid json", `{"name":"X-Custom","value":`, nil, true},
		{"wrong header type", `{"name":"Accept","value":"text/plain"}`, nil, true},
		{"full", `{"name":"X-Custom","value":"abc"}`, &header.Any{Name: "X-Custom", Value: "abc"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got *header.Any
			if err := json.Unmarshal([]byte(tt.data), &got); err != nil {
				if !tt.wantErr {
					t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
				}
				return
			}

			if tt.wantErr {
				t.Fatalf("json.Unmarshal(data, got) error = nil, want error")
			}

			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.want, diff)
			}
		})
	}
}

func TestAny_RoundTripJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.Any
	}{
		{"nil", nil},
		{"zero", &header.Any{}},
		{"empty name", &header.Any{Name: "", Value: "abc"}},
		{"empty value", &header.Any{Name: "X-Custom", Value: ""}},
		{"full", &header.Any{Name: "X-Custom", Value: "abc"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			var got *header.Any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}
