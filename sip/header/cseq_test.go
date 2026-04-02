package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestCSeq_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.CSeq
		want string
	}{
		{"nil", (*header.CSeq)(nil), ""},
		{"zero", &header.CSeq{}, "CSeq: 0 "},
		{"full", &header.CSeq{SeqNum: 4711, Method: "INVITE"}, "CSeq: 4711 INVITE"},
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

func TestCSeq_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.CSeq
		wantRes string
		wantErr error
	}{
		{"nil", (*header.CSeq)(nil), "", nil},
		{"zero", &header.CSeq{}, "CSeq: 0 ", nil},
		{"full", &header.CSeq{SeqNum: 4711, Method: "INVITE"}, "CSeq: 4711 INVITE", nil},
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

func TestCSeq_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.CSeq
		want string
	}{
		{"nil", (*header.CSeq)(nil), ""},
		{"zero", &header.CSeq{}, "0 "},
		{"full", &header.CSeq{SeqNum: 4711, Method: "INVITE"}, "4711 INVITE"},
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

func TestCSeq_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.CSeq
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.CSeq)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.CSeq)(nil), (*header.CSeq)(nil), true},
		{"zero ptr to nil ptr", &header.CSeq{}, (*header.CSeq)(nil), false},
		{"zero ptr to zero val", &header.CSeq{}, header.CSeq{}, true},
		{
			"not match 1",
			&header.CSeq{},
			header.CSeq{SeqNum: 4711, Method: "INVITE"},
			false,
		},
		{
			"not match 2",
			&header.CSeq{SeqNum: 4711, Method: "INVITE"},
			header.CSeq{SeqNum: 4711, Method: "BYE"},
			false,
		},
		{
			"not match 3",
			&header.CSeq{SeqNum: 4711, Method: "INVITE"},
			header.CSeq{SeqNum: 123, Method: "INVITE"},
			false,
		},
		{
			"match",
			&header.CSeq{SeqNum: 4711, Method: "INVITE"},
			header.CSeq{SeqNum: 4711, Method: "invite"},
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

func TestCSeq_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.CSeq
		want bool
	}{
		{"nil", (*header.CSeq)(nil), false},
		{"zero", &header.CSeq{}, false},
		{"invalid 1", &header.CSeq{Method: "INVITE"}, false},
		{"invalid 2", &header.CSeq{SeqNum: 4711, Method: "a c k"}, false},
		{"valid", &header.CSeq{SeqNum: 4711, Method: "INVITE"}, true},
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

func TestCSeq_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.CSeq
	}{
		{"nil", nil},
		{"zero", &header.CSeq{}},
		{"full", &header.CSeq{SeqNum: 4711, Method: "INVITE"}},
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

func TestCSeq_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.CSeq
		want string
	}{
		{"nil", nil, "null"},
		{"zero", &header.CSeq{}, `{"name":"CSeq","value":"0 "}`},
		{"full", &header.CSeq{SeqNum: 4711, Method: "INVITE"}, `{"name":"CSeq","value":"4711 INVITE"}`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v, want nil", err)
			}

			if got := string(got); got != c.want {
				t.Fatalf("json.Marshal() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCSeq_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    *header.CSeq
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"4711 INVITE"}`, nil, true},
		{"empty value", `{"name":"CSeq","value":""}`, &header.CSeq{}, false},
		{"wrong header", `{"name":"From","value":"<sip:alice@example.com>"}`, nil, true},
		{"invalid json", `{"name":"CSeq","value":`, nil, true},
		{"invalid seq", `{"name":"CSeq","value":"abc INVITE"}`, nil, true},
		{"invalid method", `{"name":"CSeq","value":"4711 in vite"}`, nil, true},
		{"zero", `{"name":"CSeq","value":"0 "}`, &header.CSeq{}, false},
		{"full", `{"name":"CSeq","value":"4711 INVITE"}`, &header.CSeq{SeqNum: 4711, Method: "INVITE"}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got *header.CSeq
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

func TestCSeq_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.CSeq
	}{
		{"nil", nil},
		{"zero", &header.CSeq{}},
		{"full", &header.CSeq{SeqNum: 4711, Method: "INVITE"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v, want nil", err)
			}

			var got *header.CSeq
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
