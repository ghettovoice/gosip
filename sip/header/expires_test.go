package header_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestExpires_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Expires
		want string
	}{
		{"nil", nil, ""},
		{"zero", &header.Expires{}, "Expires: 0"},
		{"full", &header.Expires{Duration: 123 * time.Second}, "Expires: 123"},
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

func TestExpires_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.Expires
		wantRes string
		wantErr error
	}{
		{"nil", nil, "", nil},
		{"zero", &header.Expires{}, "Expires: 0", nil},
		{"full", &header.Expires{Duration: 3600 * time.Second}, "Expires: 3600", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder

			_, err := c.hdr.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo(sb, nil) error = %v, want %q\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}

			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestExpires_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Expires
		val  any
		want bool
	}{
		{"nil to nil", nil, nil, false},
		{"nil to zero", nil, &header.Expires{}, false},
		{"nil to nil ptr", nil, (*header.Expires)(nil), true},
		{"zero to nil", &header.Expires{}, nil, false},
		{"zero to nil ptr", &header.Expires{}, (*header.Expires)(nil), false},
		{"zero to zero", &header.Expires{}, &header.Expires{}, true},
		{"not match 1", &header.Expires{Duration: 123}, &header.Expires{}, false},
		{"not match 2", &header.Expires{Duration: 123}, &header.Expires{Duration: 456}, false},
		{"match", &header.Expires{Duration: 123}, &header.Expires{Duration: 123}, true},
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

func TestExpires_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Expires
		want bool
	}{
		{"nil", nil, false},
		{"zero", &header.Expires{}, true},
		{"full", &header.Expires{Duration: 123 * time.Second}, true},
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

func TestExpires_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Expires
	}{
		{"nil", nil},
		{"zero", &header.Expires{}},
		{"full", &header.Expires{Duration: 123 * time.Second}},
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

func TestExpires_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Expires
		want string
	}{
		{"nil", nil, "null"},
		{"zero", &header.Expires{}, `{"name":"Expires","value":"0"}`},
		{"full", &header.Expires{Duration: 123 * time.Second}, `{"name":"Expires","value":"123"}`},
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

func TestExpires_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    *header.Expires
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", "{}", nil, true},
		{"empty name", `{"value":"0"}`, nil, true},
		{"empty value", `{"name":"Expires","value":""}`, &header.Expires{}, false},
		{"wrong header", `{"name":"From","value":"<sip:alice@example.com>"}`, nil, true},
		{"invalid json", `{"name":"Expires","value":`, nil, true},
		{"invalid value", `{"name":"Expires","value":"abc"}`, nil, true},
		{"zero", `{"name":"Expires","value":"0"}`, &header.Expires{}, false},
		{"full", `{"name":"Expires","value":"3600"}`, &header.Expires{Duration: time.Hour}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got *header.Expires
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

func TestExpires_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Expires
	}{
		{"nil", nil},
		{"zero", &header.Expires{}},
		{"full", &header.Expires{Duration: 123 * time.Second}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			var got *header.Expires
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
