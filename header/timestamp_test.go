package header_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
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
