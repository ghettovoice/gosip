package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestInReplyTo_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.InReplyTo
		want string
	}{
		{"nil", header.InReplyTo(nil), ""},
		{"empty", header.InReplyTo{}, "In-Reply-To: "},
		{"empty elem", header.InReplyTo{""}, "In-Reply-To: "},
		{
			"full",
			header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"},
			"In-Reply-To: 70710@saturn.bell-tel.com, 17320@saturn.bell-tel.com",
		},
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

func TestInReplyTo_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.InReplyTo
		wantRes string
		wantErr error
	}{
		{"nil", header.InReplyTo(nil), "", nil},
		{"empty", header.InReplyTo{}, "In-Reply-To: ", nil},
		{
			"full",
			header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"},
			"In-Reply-To: 70710@saturn.bell-tel.com, 17320@saturn.bell-tel.com",
			nil,
		},
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

func TestInReplyTo_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.InReplyTo
		want string
	}{
		{"nil", header.InReplyTo(nil), ""},
		{"empty", header.InReplyTo{}, ""},
		{
			"full",
			header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"},
			"70710@saturn.bell-tel.com, 17320@saturn.bell-tel.com",
		},
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

func TestInReplyTo_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.InReplyTo
		val  any
		want bool
	}{
		{"nil ptr to nil", header.InReplyTo(nil), nil, false},
		{"nil ptr to nil ptr", header.InReplyTo(nil), header.InReplyTo(nil), true},
		{"zero ptr to nil ptr", header.InReplyTo{}, header.InReplyTo(nil), true},
		{"zero to zero", header.InReplyTo{}, header.InReplyTo{}, true},
		{"zero to zero ptr", header.InReplyTo{}, &header.InReplyTo{}, true},
		{"zero to nil ptr", header.InReplyTo{}, (*header.InReplyTo)(nil), false},
		{
			"not match 1",
			header.InReplyTo{"70710@saturn.bell-tel.com"},
			header.InReplyTo{"70710@SATURN.bell-tel.com"},
			false,
		},
		{
			"not match 2",
			header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"},
			header.InReplyTo{"17320@saturn.bell-tel.com", "70710@saturn.bell-tel.com"},
			false,
		},
		{
			"match",
			header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"},
			header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"},
			true,
		},
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

func TestInReplyTo_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.InReplyTo
		want bool
	}{
		{"nil", header.InReplyTo(nil), false},
		{"empty", header.InReplyTo{}, false},
		{"valid", header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"}, true},
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

func TestInReplyTo_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.InReplyTo
	}{
		{"nil", nil},
		{"empty", header.InReplyTo{}},
		{"full", header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"}},
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

func TestInReplyTo_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.InReplyTo
		want string
	}{
		{"nil", nil, "null"},
		{"empty", header.InReplyTo{}, `{"name":"In-Reply-To","value":""}`},
		{
			"single",
			header.InReplyTo{"70710@saturn.bell-tel.com"},
			`{"name":"In-Reply-To","value":"70710@saturn.bell-tel.com"}`,
		},
		{
			"multiple",
			header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"},
			`{"name":"In-Reply-To","value":"70710@saturn.bell-tel.com, 17320@saturn.bell-tel.com"}`,
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
				t.Fatalf("json.Marshal() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInReplyTo_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    header.InReplyTo
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"70710@saturn.bell-tel.com"}`, nil, true},
		{"empty value", `{"name":"In-Reply-To","value":""}`, header.InReplyTo{}, false},
		{"wrong header", `{"name":"From","value":"<sip:alice@example.com>"}`, nil, true},
		{"invalid json", `{"name":"In-Reply-To","value":`, nil, true},
		{
			"single",
			`{"name":"In-Reply-To","value":"70710@saturn.bell-tel.com"}`,
			header.InReplyTo{"70710@saturn.bell-tel.com"},
			false,
		},
		{
			"multiple",
			`{"name":"In-Reply-To","value":"70710@saturn.bell-tel.com, 17320@saturn.bell-tel.com"}`,
			header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got header.InReplyTo
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

func TestInReplyTo_RoundTripJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.InReplyTo
	}{
		{"nil", nil},
		{"empty", header.InReplyTo{}},
		{"single", header.InReplyTo{"70710@saturn.bell-tel.com"}},
		{"multiple", header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v, want nil", err)
			}

			var got header.InReplyTo
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}
