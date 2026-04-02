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

func TestRetryAfter_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.RetryAfter
		want string
	}{
		{"nil", (*header.RetryAfter)(nil), ""},
		{"zero", &header.RetryAfter{}, "Retry-After: 0"},
		{
			"no comment",
			&header.RetryAfter{
				Delay:  120 * time.Second,
				Params: make(header.Values).Set("duration", "60"),
			},
			"Retry-After: 120;duration=60",
		},
		{
			"full",
			&header.RetryAfter{
				Delay:   120 * time.Second,
				Comment: "I'm in a meeting",
				Params:  make(header.Values).Set("duration", "60"),
			},
			"Retry-After: 120 (I'm in a meeting);duration=60",
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

func TestRetryAfter_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.RetryAfter
		wantRes string
		wantErr error
	}{
		{"nil", (*header.RetryAfter)(nil), "", nil},
		{"zero", &header.RetryAfter{}, "Retry-After: 0", nil},
		{
			"full",
			&header.RetryAfter{
				Delay:   120 * time.Second,
				Comment: "I'm in a meeting",
				Params:  make(header.Values).Set("duration", "60"),
			},
			"Retry-After: 120 (I'm in a meeting);duration=60",
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

func TestRetryAfter_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.RetryAfter
		want string
	}{
		{"nil", nil, ""},
		{"zero", &header.RetryAfter{}, "0"},
		{
			"full",
			&header.RetryAfter{
				Delay:   120 * time.Second,
				Comment: "I'm in a meeting",
				Params:  make(header.Values).Set("duration", "60"),
			},
			"120 (I'm in a meeting);duration=60",
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

func TestRetryAfter_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.RetryAfter
		val  any
		want bool
	}{
		{"nil ptr to nil", nil, nil, false},
		{"nil ptr to nil ptr", nil, (*header.RetryAfter)(nil), true},
		{"zero ptr to nil ptr", &header.RetryAfter{}, (*header.RetryAfter)(nil), false},
		{"zero ptr to zero val", &header.RetryAfter{}, header.RetryAfter{}, true},
		{
			"not match 1",
			&header.RetryAfter{
				Delay:   60 * time.Second,
				Comment: "I'm in a meeting",
			},
			header.RetryAfter{
				Delay:   120 * time.Second,
				Comment: "I'm in a meeting",
			},
			false,
		},
		{
			"not match 2",
			&header.RetryAfter{
				Delay:   60 * time.Second,
				Comment: "I'm in a meeting",
			},
			header.RetryAfter{
				Delay:   120 * time.Second,
				Comment: "I'm in a meeting",
			},
			false,
		},
		{
			"match",
			&header.RetryAfter{
				Delay:   60 * time.Second,
				Comment: "I'm in a meeting",
				Params:  make(header.Values).Set("duration", "60").Set("x", "abc"),
			},
			header.RetryAfter{
				Delay:   60 * time.Second,
				Comment: "I'm in a meeting",
				Params:  make(header.Values).Set("duration", "60"),
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

func TestRetryAfter_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.RetryAfter
		want bool
	}{
		{"nil", nil, false},
		{"zero", &header.RetryAfter{}, true},
		{"valid", &header.RetryAfter{Delay: 60 * time.Second}, true},
		{"invalid", &header.RetryAfter{Delay: 60 * time.Second, Params: make(header.Values).Set("d u r", "60")}, false},
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

func TestRetryAfter_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.RetryAfter
	}{
		{"nil", nil},
		{"zero", &header.RetryAfter{}},
		{
			"full",
			&header.RetryAfter{
				Delay:   60 * time.Second,
				Comment: "I'm in a meeting",
				Params:  make(header.Values).Set("duration", "60"),
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

func TestRetryAfter_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.RetryAfter
		want string
	}{
		{"nil", nil, "null"},
		{"zero", &header.RetryAfter{}, `{"name":"Retry-After","value":"0"}`},
		{
			"full",
			&header.RetryAfter{
				Delay:   60 * time.Second,
				Comment: "I'm in a meeting",
				Params: make(header.Values).
					Set("duration", "60"),
			},
			`{"name":"Retry-After","value":"60 (I'm in a meeting);duration=60"}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			if diff := cmp.Diff(string(got), c.want); diff != "" {
				t.Fatalf("json.Marshal() = %q, want %q\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestRetryAfter_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    *header.RetryAfter
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"60 (I'm in a meeting);duration=60"}`, nil, true},
		{"empty value", `{"name":"Retry-After","value":""}`, &header.RetryAfter{}, false},
		{"wrong header", `{"name":"From","value":"<sip:alice@example.com>"}`, nil, true},
		{"invalid json", `{"name":"Retry-After","value":`, nil, true},
		{"invalid value", `{"name":"Retry-After","value":"i c o n"}`, nil, true},
		{"zero", `{"name":"Retry-After","value":"0"}`, &header.RetryAfter{}, false},
		{
			"full",
			`{"name":"Retry-After","value":"60 (I'm in a meeting);duration=60"}`,
			&header.RetryAfter{
				Delay:   60 * time.Second,
				Comment: "I'm in a meeting",
				Params: make(header.Values).
					Set("duration", "60"),
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got *header.RetryAfter
			if err := json.Unmarshal([]byte(c.data), &got); err != nil {
				if !c.wantErr {
					t.Fatalf("json.Unmarshal(data, got) error = %+v, want nil", err)
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

func TestRetryAfter_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.RetryAfter
	}{
		{"nil", nil},
		{"zero", &header.RetryAfter{}},
		{
			"full",
			&header.RetryAfter{
				Delay:   60 * time.Second,
				Comment: "I'm in a meeting",
				Params: make(header.Values).
					Set("duration", "60"),
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

			var got *header.RetryAfter
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
