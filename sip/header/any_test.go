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

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Render(nil); got != c.want {
				t.Errorf("hdr.Render(nil) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAny_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.Any
		wantRes string
		wantErr error
	}{
		{"nil", (*header.Any)(nil), "", nil},
		{"zero", &header.Any{}, ": ", nil},
		{"full", &header.Any{Name: "x-custom", Value: "abc"}, "X-Custom: abc", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder

			_, err := c.hdr.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo(sb, nil) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}

			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestAny_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Any
		want string
	}{
		{"nil", (*header.Any)(nil), ""},
		{"zero", &header.Any{}, ""},
		{"full", &header.Any{Name: "x-custom", Value: "abc"}, "abc"},
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

func TestAny_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Equal(c.val); got != c.want {
				t.Errorf("hdr.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAny_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.IsValid(); got != c.want {
				t.Errorf("hdr.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAny_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Any
	}{
		{"nil", nil},
		{"zero", &header.Any{}},
		{"full", &header.Any{Name: "X-Custom", Value: "ABC"}},
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

func TestAny_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			if got := string(got); got != c.want {
				t.Fatalf("json.Marshal(hdr) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAny_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got *header.Any
			if err := json.Unmarshal([]byte(c.data), &got); err != nil {
				if !c.wantErr {
					t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
				}
				return
			}

			if c.wantErr {
				t.Fatalf("json.Unmarshal(data, got) error = nil, want error")
			}

			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestAny_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Any
	}{
		{"nil", nil},
		{"zero", &header.Any{}},
		{"empty name", &header.Any{Name: "", Value: "abc"}},
		{"empty value", &header.Any{Name: "X-Custom", Value: ""}},
		{"full", &header.Any{Name: "X-Custom", Value: "abc"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			var got *header.Any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
