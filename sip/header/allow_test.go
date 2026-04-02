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

	cases := []struct {
		name string
		hdr  header.Allow
		want string
	}{
		{"nil", header.Allow(nil), ""},
		{"empty", header.Allow{}, "Allow: "},
		{"empty elem", header.Allow{""}, "Allow: "},
		{"full", header.Allow{"INVITE", "ACK", "ABC"}, "Allow: INVITE, ACK, ABC"},
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

func TestAllow_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.Allow
		wantRes string
		wantErr error
	}{
		{"nil", header.Allow(nil), "", nil},
		{"empty", header.Allow{}, "Allow: ", nil},
		{"full", header.Allow{"INVITE", "ACK", "ABC"}, "Allow: INVITE, ACK, ABC", nil},
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

func TestAllow_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Allow
		want string
	}{
		{"nil", header.Allow(nil), ""},
		{"empty", header.Allow{}, ""},
		{"full", header.Allow{"INVITE", "ACK", "ABC"}, "INVITE, ACK, ABC"},
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

func TestAllow_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Equal(c.val); got != c.want {
				t.Errorf("hdr.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAllow_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Allow
		want bool
	}{
		{"nil", header.Allow(nil), false},
		{"empty", header.Allow{}, true},
		{"valid", header.Allow{"INVITE", "abc"}, true},
		{"invalid", header.Allow{"a b c"}, false},
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

func TestAllow_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Allow
	}{
		{"nil", header.Allow(nil)},
		{"empty", header.Allow{}},
		{"full", header.Allow{"INVITE", "ACK", "ABC"}},
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

func TestAllow_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Allow
		want string
	}{
		{"nil", nil, "null"},
		{"empty", header.Allow{}, `{"name":"Allow","value":""}`},
		{"single", header.Allow{"INVITE"}, `{"name":"Allow","value":"INVITE"}`},
		{"multiple", header.Allow{"INVITE", "ACK"}, `{"name":"Allow","value":"INVITE, ACK"}`},
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

func TestAllow_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.Allow
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

func TestAllow_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Allow
	}{
		{"nil", nil},
		{"empty", header.Allow{}},
		{"single", header.Allow{"INVITE"}},
		{"multiple", header.Allow{"INVITE", "ACK"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v, want nil", err)
			}

			var got header.Allow
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
