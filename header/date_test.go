package header_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestDate_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Date
		want string
	}{
		{"nil", (*header.Date)(nil), ""},
		{"zero", &header.Date{}, "Date: Mon, 01 Jan 0001 00:00:00 GMT"},
		{
			"full",
			&header.Date{Time: time.Date(2010, 11, 13, 23, 29, 0, 0, time.UTC)},
			"Date: Sat, 13 Nov 2010 23:29:00 GMT",
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

func TestDate_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.Date
		wantRes string
		wantErr error
	}{
		{"nil", (*header.Date)(nil), "", nil},
		{"zero", &header.Date{}, "Date: Mon, 01 Jan 0001 00:00:00 GMT", nil},
		{
			"full",
			&header.Date{Time: time.Date(2010, 11, 13, 23, 29, 0, 0, time.UTC)},
			"Date: Sat, 13 Nov 2010 23:29:00 GMT",
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

func TestDate_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Date
		want string
	}{
		{"nil", (*header.Date)(nil), ""},
		{"zero", &header.Date{}, "Mon, 01 Jan 0001 00:00:00 GMT"},
		{"full", &header.Date{Time: time.Date(2010, 11, 13, 23, 29, 0, 0, time.UTC)}, "Sat, 13 Nov 2010 23:29:00 GMT"},
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

func TestDate_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Date
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.Date)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.Date)(nil), (*header.Date)(nil), true},
		{"zero ptr to nil ptr", &header.Date{}, (*header.Date)(nil), false},
		{"zero ptr to zero val", &header.Date{}, header.Date{}, true},
		{
			"not match 1",
			&header.Date{Time: time.Date(2019, 4, 13, 23, 29, 0, 0, time.UTC)},
			&header.Date{},
			false,
		},
		{
			"not match 2",
			&header.Date{Time: time.Date(2019, 4, 13, 23, 29, 0, 0, time.UTC)},
			&header.Date{Time: time.Date(2010, 11, 13, 23, 29, 0, 0, time.UTC)},
			false,
		},
		{
			"match",
			&header.Date{Time: time.Date(2010, 11, 13, 23, 29, 0, 0, time.UTC)},
			header.Date{Time: time.Date(2010, 11, 13, 23, 29, 0, 0, time.UTC)},
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

func TestDate_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Date
		want bool
	}{
		{"nil", (*header.Date)(nil), false},
		{"zero", &header.Date{}, false},
		{"valid", &header.Date{Time: time.Now()}, true},
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

func TestDate_Clone(t *testing.T) {
	t.Parallel()

	now := time.Now()
	cases := []struct {
		name string
		hdr  *header.Date
	}{
		{"nil", nil},
		{"zero", &header.Date{}},
		{"full", &header.Date{Time: now}},
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

//nolint:gocognit
func TestDate_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.Date
		want    map[string]any
		wantErr error
	}{
		{name: "nil", hdr: (*header.Date)(nil)},
		{
			name: "zero",
			hdr:  &header.Date{},
			want: map[string]any{
				"name":  "Date",
				"value": "Mon, 01 Jan 0001 00:00:00 GMT",
			},
		},
		{
			name: "custom",
			hdr:  &header.Date{Time: time.Date(2010, 11, 13, 23, 29, 0, 0, time.UTC)},
			want: map[string]any{
				"name":  "Date",
				"value": "Sat, 13 Nov 2010 23:29:00 GMT",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(c.hdr)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("json.Marshal(hdr) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
				return
			}

			if c.hdr == nil {
				if string(got) != "null" {
					t.Errorf("json.Marshal(nil) = %s, want \"null\"", string(got))
				}
				return
			}

			var gotMap map[string]any
			if err := json.Unmarshal(got, &gotMap); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if c.want != nil {
				if name, ok := gotMap["name"].(string); !ok || name != c.want["name"] {
					t.Errorf("got[\"name\"] = %v, want %v", gotMap["name"], c.want["name"])
				}
				if wantValue, ok := c.want["value"]; ok {
					if value, ok := gotMap["value"].(string); !ok || value != wantValue {
						t.Errorf("got[\"value\"] = %v, want %v", gotMap["value"], wantValue)
					}
				}
			}
		})
	}
}

func TestDate_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    *header.Date
		wantErr bool
	}{
		{name: "null", data: "null", want: &header.Date{}},
		{
			name: "zero",
			data: `{"name":"Date","value":"Mon, 01 Jan 0001 00:00:00 GMT"}`,
			want: &header.Date{},
		},
		{
			name: "custom",
			data: `{"name":"Date","value":"Sat, 13 Nov 2010 23:29:00 GMT"}`,
			want: &header.Date{Time: time.Date(2010, 11, 13, 23, 29, 0, 0, time.UTC)},
		},
		{
			name:    "invalid_json",
			data:    `{"name":"Date","value":`,
			want:    &header.Date{},
			wantErr: true,
		},
		{
			name:    "wrong_header",
			data:    `{"name":"From","value":"\"Alice\" <sip:alice@example.com>"}`,
			want:    &header.Date{},
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.Date
			err := json.Unmarshal([]byte(c.data), &got)
			if (err != nil) != c.wantErr {
				t.Errorf("json.Unmarshal(data, got) error = %v, want %v", err, c.wantErr)
				return
			}
			if err != nil {
				return
			}

			if diff := cmp.Diff(&got, c.want); diff != "" {
				t.Errorf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", &got, c.want, diff)
			}
		})
	}
}

func TestDate_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Date
	}{
		{name: "nil", hdr: (*header.Date)(nil)},
		{name: "zero", hdr: &header.Date{}},
		{name: "custom", hdr: &header.Date{Time: time.Date(2010, 11, 13, 23, 29, 0, 0, time.UTC)}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			var got header.Date
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			var want *header.Date
			if c.hdr != nil {
				want = c.hdr
			} else {
				want = &header.Date{}
			}

			if diff := cmp.Diff(&got, want); diff != "" {
				t.Errorf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", &got, want, diff)
			}
		})
	}
}
