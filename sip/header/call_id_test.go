package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestCallID_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.CallID
		opts []header.RenderOptions
		want string
	}{
		{"zero", header.CallID(""), nil, "Call-ID: "},
		{"full", header.CallID("qweRTY"), []header.RenderOptions{{Compact: true}}, "i: qweRTY"},
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

func TestCallID_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.CallID
		wantRes string
		wantErr error
	}{
		{"zero", header.CallID(""), "Call-ID: ", nil},
		{"full", header.CallID("qweRTY"), "Call-ID: qweRTY", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder

			_, err := tt.hdr.RenderTo(&sb)
			if diff := cmp.Diff(err, tt.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo(&sb) error = %v, want %v\ndiff (-got +want):\n%v", err, tt.wantErr, diff)
			}

			if got, want := sb.String(), tt.wantRes; got != want {
				t.Errorf("sb.String() = %q, want %q", got, want)
			}
		})
	}
}

func TestCallID_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.CallID
		val  any
		want bool
	}{
		{"zero to nil", header.CallID(""), nil, false},
		{"zero to nil ptr", header.CallID(""), (*header.CallID)(nil), false},
		{"zero to zero", header.CallID(""), header.CallID(""), true},
		{"not match 1", header.CallID("qweRTY"), header.CallID(""), false},
		{"not match 2", header.CallID("qweRTY"), header.CallID("qwerty"), false},
		{"match", header.CallID("qweRTY"), header.CallID("qweRTY"), true},
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

func TestCallID_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.CallID
		want bool
	}{
		{"zero", header.CallID(""), false},
		{"valid", header.CallID("qweRTY"), true},
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

func TestCallID_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.CallID
	}{
		{"zero", header.CallID("")},
		{"full", header.CallID("qwe")},
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

func TestCallID_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.CallID
		want string
	}{
		{"empty", "", `{"name":"Call-ID","value":""}`},
		{
			"simple",
			"f81d4fae-7dec-11d0-a765-00a0c91e6bf6",
			`{"name":"Call-ID","value":"f81d4fae-7dec-11d0-a765-00a0c91e6bf6"}`,
		},
		{
			"with host",
			"f81d4fae-7dec-11d0-a765-00a0c91e6bf6@foo.bar.com",
			`{"name":"Call-ID","value":"f81d4fae-7dec-11d0-a765-00a0c91e6bf6@foo.bar.com"}`,
		},
		{
			"alphanumeric",
			"qweRTY123",
			`{"name":"Call-ID","value":"qweRTY123"}`,
		},
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

func TestCallID_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    header.CallID
		wantErr bool
	}{
		{"null", "null", "", false},
		{"empty object", "{}", "", true},
		{"empty name", `{"value":"123"}`, "", true},
		{"empty value", `{"name":"Call-ID"}`, "", false},
		{"invalid json", `{"name":"Call-ID","value":`, "", true},
		{"wrong header type", `{"name":"From","value":"<sip:alice@example.com>"}`, "", true},
		{
			"simple",
			`{"name":"Call-ID","value":"f81d4fae-7dec-11d0-a765-00a0c91e6bf6"}`,
			"f81d4fae-7dec-11d0-a765-00a0c91e6bf6",
			false,
		},
		{
			"with host",
			`{"name":"Call-ID","value":"f81d4fae-7dec-11d0-a765-00a0c91e6bf6@foo.bar.com"}`,
			"f81d4fae-7dec-11d0-a765-00a0c91e6bf6@foo.bar.com",
			false,
		},
		{
			"compact form",
			`{"name":"i","value":"abc123"}`,
			"abc123",
			false,
		},
		{
			"alphanumeric",
			`{"name":"Call-ID","value":"qweRTY123"}`,
			"qweRTY123",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got header.CallID
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

func TestCallID_RoundTripJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.CallID
	}{
		{"empty", ""},
		{"simple", "f81d4fae-7dec-11d0-a765-00a0c91e6bf6"},
		{"with host", "f81d4fae-7dec-11d0-a765-00a0c91e6bf6@foo.bar.com"},
		{"alphanumeric", "qweRTY123"},
		{"complex", "a84b4c76e66710@pc33.atlanta.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Marshal
			data, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			// Unmarshal
			var got header.CallID
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			// Compare
			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Fatalf("Round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}
