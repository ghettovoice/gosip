package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestAllow_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Allow
		want string
	}{
		{"nil", header.Allow(nil), ""},
		{"empty", header.Allow{}, "Allow: "},
		{"empty elem", header.Allow{""}, "Allow: "},
		{"full", header.Allow{"INVITE", "ACK", "ABC"}, "Allow: INVITE, ACK, ABC"},
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

func TestAllow_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.Allow
		wantRes string
		wantErr error
	}{
		{"nil", header.Allow(nil), "", nil},
		{"empty", header.Allow{}, "Allow: ", nil},
		{"full", header.Allow{"INVITE", "ACK", "ABC"}, "Allow: INVITE, ACK, ABC", nil},
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

func TestAllow_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Allow
		want string
	}{
		{"nil", header.Allow(nil), ""},
		{"empty", header.Allow{}, ""},
		{"full", header.Allow{"INVITE", "ACK", "ABC"}, "INVITE, ACK, ABC"},
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

func TestAllow_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Allow
		val  any
		want bool
	}{
		{"nil ptr to nil", header.Allow(nil), nil, false},
		{"nil ptr to nil ptr", header.Allow(nil), header.Allow(nil), true},
		{"zero ptr to nil ptr", header.Allow{}, header.Allow(nil), true},
		{"zero to zero", header.Allow{}, header.Allow{}, true},
		{"zero to zero ptr", header.Allow{}, &header.Allow{}, true},
		{"zero to nil ptr", header.Allow{}, (*header.Allow)(nil), false},
		{"not match 1", header.Allow{"INVITE"}, header.Allow{}, false},
		{"not match 2", header.Allow{"INVITE", "BYE"}, header.Allow{"BYE", "INVITE"}, false},
		{"match", header.Allow{"INVITE", "BYE"}, header.Allow{"invite", "bye"}, true},
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

func TestAllow_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Allow
		want bool
	}{
		{"nil", header.Allow(nil), false},
		{"empty", header.Allow{}, true},
		{"valid", header.Allow{"INVITE", "abc"}, true},
		{"invalid", header.Allow{"a b c"}, false},
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

func TestAllow_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Allow
	}{
		{"nil", header.Allow(nil)},
		{"empty", header.Allow{}},
		{"full", header.Allow{"INVITE", "ACK", "ABC"}},
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

func TestAllow_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Allow
		want string
	}{
		{"nil", nil, "null"},
		{"empty", header.Allow{}, `{"name":"Allow","value":""}`},
		{"single", header.Allow{"INVITE"}, `{"name":"Allow","value":"INVITE"}`},
		{"multiple", header.Allow{"INVITE", "ACK"}, `{"name":"Allow","value":"INVITE, ACK"}`},
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

func TestAllow_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    header.Allow
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"ACK"}`, nil, true},
		{"empty value", `{"name":"Allow","value":""}`, header.Allow{}, false},
		{"wrong header", `{"name":"From","value":"<sip:alice@example.com>"}`, nil, true},
		{"invalid json", `{"name":"Allow","value":`, nil, true},
		{"single", `{"name":"Allow","value":"INVITE"}`, header.Allow{"INVITE"}, false},
		{"multiple", `{"name":"Allow","value":"INVITE, ACK"}`, header.Allow{"INVITE", "ACK"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got header.Allow
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

func TestAllow_RoundTripJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Allow
	}{
		{"nil", nil},
		{"empty", header.Allow{}},
		{"single", header.Allow{"INVITE"}},
		{"multiple", header.Allow{"INVITE", "ACK"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v, want nil", err)
			}

			var got header.Allow
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}
