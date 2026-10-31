package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestContentDisposition_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.ContentDisposition
		want string
	}{
		{"nil", (*header.ContentDisposition)(nil), ""},
		{"zero", &header.ContentDisposition{}, "Content-Disposition: "},
		{
			"full",
			&header.ContentDisposition{
				Type: "Session",
				Params: make(header.Values).
					Set("handling", "optional").
					Set("param", `"Hello world!"`),
			},
			"Content-Disposition: Session;handling=optional;param=\"Hello world!\"",
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

func TestContentDisposition_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     *header.ContentDisposition
		wantRes string
		wantErr error
	}{
		{"nil", (*header.ContentDisposition)(nil), "", nil},
		{"zero", &header.ContentDisposition{}, "Content-Disposition: ", nil},
		{
			"full",
			&header.ContentDisposition{
				Type: "Session",
				Params: make(header.Values).
					Set("handling", "optional").
					Set("param", `"Hello world!"`),
			},
			"Content-Disposition: Session;handling=optional;param=\"Hello world!\"",
			nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder

			_, err := tt.hdr.RenderTo(&sb)
			if diff := cmp.Diff(err, tt.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo(&sb) error = %v, want %v\ndiff (-got +want):\n%v", tt.hdr, tt.wantErr, diff)
			}

			if got := sb.String(); got != tt.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, tt.wantRes)
			}
		})
	}
}

func TestContentDisposition_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.ContentDisposition
		want string
	}{
		{"nil", (*header.ContentDisposition)(nil), ""},
		{"zero", &header.ContentDisposition{}, ""},
		{
			"full",
			&header.ContentDisposition{
				Type: "Session",
				Params: make(header.Values).
					Set("handling", "optional").
					Set("param", `"Hello world!"`),
			},
			"Session;handling=optional;param=\"Hello world!\"",
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

func TestContentDisposition_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.ContentDisposition
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.ContentDisposition)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.ContentDisposition)(nil), (*header.ContentDisposition)(nil), true},
		{"zero ptr to nil ptr", &header.ContentDisposition{}, (*header.ContentDisposition)(nil), false},
		{"zero ptr to zero val", &header.ContentDisposition{}, header.ContentDisposition{}, true},
		{
			"not match 1",
			&header.ContentDisposition{Type: "session"},
			&header.ContentDisposition{Type: "qwerty"},
			false,
		},
		{
			"not match 2",
			&header.ContentDisposition{
				Type: "session",
			},
			&header.ContentDisposition{
				Type:   "session",
				Params: make(header.Values).Set("handling", "required"),
			},
			false,
		},
		{
			"not match 3",
			&header.ContentDisposition{
				Type:   "session",
				Params: make(header.Values).Set("handling", "optional"),
			},
			&header.ContentDisposition{
				Type:   "session",
				Params: make(header.Values).Set("handling", "required"),
			},
			false,
		},
		{
			"match",
			&header.ContentDisposition{
				Type:   "SESSION",
				Params: make(header.Values).Set("handling", "optional"),
			},
			header.ContentDisposition{
				Type:   "session",
				Params: make(header.Values).Set("handling", "optional").Set("param", `"Hello world!"`),
			},
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

func TestContentDisposition_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.ContentDisposition
		want bool
	}{
		{"nil", (*header.ContentDisposition)(nil), false},
		{"zero", &header.ContentDisposition{}, false},
		{"invalid", &header.ContentDisposition{Type: "i c o n"}, false},
		{"valid", &header.ContentDisposition{Type: "icon"}, true},
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

func TestContentDisposition_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.ContentDisposition
	}{
		{"nil", nil},
		{"zero", &header.ContentDisposition{}},
		{
			"full",
			&header.ContentDisposition{
				Type:   "icon",
				Params: make(header.Values).Set("handling", "optional"),
			},
		},
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

func TestContentDisposition_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.ContentDisposition
		want string
	}{
		{"nil", nil, "null"},
		{"zero", &header.ContentDisposition{}, `{"name":"Content-Disposition","value":""}`},
		{
			"full",
			&header.ContentDisposition{
				Type: "Session",
				Params: make(header.Values).
					Set("handling", "optional").
					Set("param", `"Hello world!"`),
			},
			`{"name":"Content-Disposition","value":"Session;handling=optional;param=\"Hello world!\""}`,
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

func TestContentDisposition_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    *header.ContentDisposition
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"Session;handling=optional;param=\"Hello world!\""}`, nil, true},
		{"empty value", `{"name":"Content-Disposition","value":""}`, &header.ContentDisposition{}, false},
		{"wrong header", `{"name":"From","value":"<sip:alice@example.com>"}`, nil, true},
		{"invalid json", `{"name":"Content-Disposition","value":`, nil, true},
		{"invalid value", `{"name":"Content-Disposition","value":"i c o n"}`, nil, true},
		{
			"full",
			`{"name":"Content-Disposition","value":"Session;handling=optional;param=\"Hello world!\""}`,
			&header.ContentDisposition{
				Type: "Session",
				Params: make(header.Values).
					Set("handling", "optional").
					Set("param", `"Hello world!"`),
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got *header.ContentDisposition
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

func TestContentDisposition_RoundTripJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.ContentDisposition
	}{
		{"nil", nil},
		{"zero", &header.ContentDisposition{}},
		{
			"full",
			&header.ContentDisposition{
				Type: "Session",
				Params: make(header.Values).
					Set("handling", "optional").
					Set("param", `"Hello world!"`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v, want nil", err)
			}

			var got *header.ContentDisposition
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}
