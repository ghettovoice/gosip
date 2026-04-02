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

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Render(nil); got != c.want {
				t.Errorf("hdr.Render(nil) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestContentDisposition_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder

			_, err := c.hdr.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo(sb, nil) error = %v, want %v\ndiff (-got +want):\n%v", c.hdr, c.wantErr, diff)
			}

			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestContentDisposition_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.String(); got != c.want {
				t.Errorf("hdr.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestContentDisposition_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Equal(c.val); got != c.want {
				t.Errorf("hdr.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestContentDisposition_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ContentDisposition
		want bool
	}{
		{"nil", (*header.ContentDisposition)(nil), false},
		{"zero", &header.ContentDisposition{}, false},
		{"invalid", &header.ContentDisposition{Type: "i c o n"}, false},
		{"valid", &header.ContentDisposition{Type: "icon"}, true},
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

func TestContentDisposition_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.hdr.Clone()
			if c.hdr == nil {
				if got != nil {
					t.Errorf("hdr.Clone() = %+v, want nil", got)
				}
				return
			}

			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}

func TestContentDisposition_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			if got := string(got); got != c.want {
				t.Fatalf("json.Marshal() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestContentDisposition_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got *header.ContentDisposition
			if err := json.Unmarshal([]byte(c.data), &got); err != nil {
				if !c.wantErr {
					t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
				}
				return
			}

			if c.wantErr {
				t.Fatal("json.Unmarshal(data, got) error = nil, want error")
			}

			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestContentDisposition_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v, want nil", err)
			}

			var got *header.ContentDisposition
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
