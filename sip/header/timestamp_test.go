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

func TestTimestamp_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Timestamp
		want string
	}{
		{"nil", nil, ""},
		{"zero", &header.Timestamp{}, "Timestamp: 0"},
		{
			"full",
			&header.Timestamp{RequestTime: time.Unix(0, 543*1e6).UTC(), ResponseDelay: 5325750 * time.Microsecond},
			"Timestamp: 0.543 5.326",
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

func TestTimestamp_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.Timestamp
		wantRes string
		wantErr error
	}{
		{"nil", nil, "", nil},
		{"zero", &header.Timestamp{}, "Timestamp: 0", nil},
		{
			"full",
			&header.Timestamp{RequestTime: time.Unix(0, 543*1e6).UTC(), ResponseDelay: 5325750 * time.Microsecond},
			"Timestamp: 0.543 5.326",
			nil,
		},
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

func TestTimestamp_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Timestamp
		want string
	}{
		{"nil", nil, ""},
		{"zero", &header.Timestamp{}, "0"},
		{
			"full",
			&header.Timestamp{RequestTime: time.Unix(0, 543*1e6).UTC(), ResponseDelay: 5325750 * time.Microsecond},
			"0.543 5.326",
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

func TestTimestamp_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Timestamp
		val  any
		want bool
	}{
		{"nil ptr to nil", nil, nil, false},
		{"nil ptr to nil ptr", nil, (*header.Timestamp)(nil), true},
		{"zero ptr to nil ptr", &header.Timestamp{}, (*header.Timestamp)(nil), false},
		{"zero ptr to zero val", &header.Timestamp{}, header.Timestamp{}, true},
		{
			"not match 1",
			&header.Timestamp{RequestTime: time.Now(), ResponseDelay: 5325750 * time.Microsecond},
			&header.Timestamp{RequestTime: time.Unix(0, 543*1e6).UTC(), ResponseDelay: 5325750 * time.Microsecond},
			false,
		},
		{
			"not match 2",
			&header.Timestamp{RequestTime: time.Unix(0, 543*1e6).UTC(), ResponseDelay: 10 * time.Millisecond},
			&header.Timestamp{RequestTime: time.Unix(0, 543*1e6).UTC(), ResponseDelay: 5325750 * time.Microsecond},
			false,
		},
		{
			"match",
			&header.Timestamp{RequestTime: time.Unix(0, 543*1e6).UTC(), ResponseDelay: 5325750 * time.Microsecond},
			header.Timestamp{RequestTime: time.Unix(0, 543*1e6).UTC(), ResponseDelay: 5325750 * time.Microsecond},
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

func TestTimestamp_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Timestamp
		want bool
	}{
		{"nil", nil, false},
		{"zero", &header.Timestamp{}, false},
		{"invalid 1", &header.Timestamp{RequestTime: time.Time{}}, false},
		{"invalid 2", &header.Timestamp{RequestTime: time.Unix(0, 543*1e6).UTC(), ResponseDelay: -time.Nanosecond}, false},
		{"valid", &header.Timestamp{RequestTime: time.Unix(0, 543*1e6).UTC(), ResponseDelay: 5325750 * time.Microsecond}, true},
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

func TestTimestamp_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Timestamp
	}{
		{"nil", nil},
		{"zero", &header.Timestamp{}},
		{
			"full",
			&header.Timestamp{RequestTime: time.Unix(0, 543*1e6).UTC(), ResponseDelay: 5325750 * time.Microsecond},
		},
	}

	cmpOpts := []cmp.Option{
		cmp.AllowUnexported(header.Timestamp{}, time.Time{}),
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

			if diff := cmp.Diff(got, c.hdr, cmpOpts...); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}

func TestTimestamp_MarshalJSON(t *testing.T) {
	t.Parallel()

	request := time.Unix(0, 543*1e6).UTC()
	delay := 5326 * time.Millisecond

	cases := []struct {
		name string
		hdr  *header.Timestamp
		want string
	}{
		{"nil", nil, "null"},
		{"zero", &header.Timestamp{}, `{"name":"Timestamp","value":"0"}`},
		{"request", &header.Timestamp{RequestTime: request}, `{"name":"Timestamp","value":"0.543"}`},
		{
			"request delay",
			&header.Timestamp{RequestTime: request, ResponseDelay: delay},
			`{"name":"Timestamp","value":"0.543 5.326"}`,
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
				t.Fatalf("json.Marshal(hdr) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTimestamp_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	request := time.Unix(0, 543*1e6).UTC()
	delay := 5326 * time.Millisecond

	cases := []struct {
		name    string
		data    string
		want    *header.Timestamp
		wantErr bool
	}{
		{"null", "null", &header.Timestamp{}, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"0"}`, nil, true},
		{"empty value", `{"name":"Timestamp","value":""}`, &header.Timestamp{}, false},
		{"wrong header", `{"name":"Retry-After","value":"0"}`, nil, true},
		{"invalid json", `{"name":"Timestamp","value":`, nil, true},
		{"zero", `{"name":"Timestamp","value":"0"}`, &header.Timestamp{RequestTime: time.Unix(0, 0).UTC()}, false},
		{"request", `{"name":"Timestamp","value":"0.543"}`, &header.Timestamp{RequestTime: request}, false},
		{
			"request+delay",
			`{"name":"Timestamp","value":"0.543 5.326"}`,
			&header.Timestamp{RequestTime: request, ResponseDelay: delay},
			false,
		},
	}

	cmpOpts := []cmp.Option{cmp.AllowUnexported(header.Timestamp{}, time.Time{})}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.Timestamp
			if err := json.Unmarshal([]byte(c.data), &got); err != nil {
				if !c.wantErr {
					t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
				}
				return
			}

			if c.wantErr {
				t.Fatal("json.Unmarshal(data, got) error = nil, want error")
			}

			if diff := cmp.Diff(&got, c.want, cmpOpts...); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", &got, c.want, diff)
			}
		})
	}
}

func TestTimestamp_RoundTripJSON(t *testing.T) {
	t.Parallel()

	request := time.Unix(0, 543*1e6).UTC()
	delay := 5326 * time.Millisecond

	cases := []struct {
		name string
		hdr  *header.Timestamp
		want *header.Timestamp
	}{
		{"nil", nil, nil},
		{"zero", &header.Timestamp{}, &header.Timestamp{RequestTime: time.Unix(0, 0).UTC()}},
		{"request", &header.Timestamp{RequestTime: request}, &header.Timestamp{RequestTime: request}},
		{
			"request delay",
			&header.Timestamp{RequestTime: request, ResponseDelay: delay},
			&header.Timestamp{RequestTime: request, ResponseDelay: delay},
		},
	}

	cmpOpts := []cmp.Option{cmp.AllowUnexported(header.Timestamp{}, time.Time{})}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			var got *header.Timestamp
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.want, cmpOpts...); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}
