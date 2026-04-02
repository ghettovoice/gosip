package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestContentLength_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLength
		opts *header.RenderOptions
		want string
	}{
		{"zero", header.ContentLength(0), nil, "Content-Length: 0"},
		{"full", header.ContentLength(123), nil, "Content-Length: 123"},
		{"compact", header.ContentLength(123), &header.RenderOptions{Compact: true}, "l: 123"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Render(c.opts); got != c.want {
				t.Errorf("hdr.Render(opts) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestContentLength_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.ContentLength
		wantRes string
		wantErr error
	}{
		{"zero", header.ContentLength(0), "Content-Length: 0", nil},
		{"full", header.ContentLength(123), "Content-Length: 123", nil},
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

func TestContentLength_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLength
		val  any
		want bool
	}{
		{"zero to nil", header.ContentLength(0), nil, false},
		{"zero to nil ptr", header.ContentLength(0), (*header.ContentLength)(nil), false},
		{"zero to zero", header.ContentLength(0), header.ContentLength(0), true},
		{"not match 1", header.ContentLength(123), header.ContentLength(0), false},
		{"not match 2", header.ContentLength(123), header.ContentLength(456), false},
		{"match", header.ContentLength(123), header.ContentLength(123), true},
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

func TestContentLength_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLength
		want bool
	}{
		{"zero", header.ContentLength(0), true},
		{"full", header.ContentLength(123), true},
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

func TestContentLength_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLength
	}{
		{"zero", header.ContentLength(0)},
		{"full", header.ContentLength(123)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.hdr.Clone()
			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}

func TestContentLength_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLength
		want string
	}{
		{"zero", 0, `{"name":"Content-Length","value":"0"}`},
		{"full", 123, `{"name":"Content-Length","value":"123"}`},
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

func TestContentLength_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    header.ContentLength
		wantErr bool
	}{
		{"null", "null", 0, false},
		{"empty object", "{}", 0, true},
		{"empty name", `{"value":"123"}`, 0, true},
		{"empty value", `{"name":"Content-Length","value":""}`, 0, false},
		{"wrong header", `{"name":"From","value":"<sip:alice@example.com>"}`, 0, true},
		{"invalid json", `{"name":"Content-Length","value":`, 0, true},
		{"invalid value", `{"name":"Content-Length","value":"abc"}`, 0, true},
		{"zero", `{"name":"Content-Length","value":"0"}`, 0, false},
		{"full", `{"name":"Content-Length","value":"3600"}`, 3600, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.ContentLength
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

func TestContentLength_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLength
	}{
		{"zero", 0},
		{"full", 3600},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			var got header.ContentLength
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
