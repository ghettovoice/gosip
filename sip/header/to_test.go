package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestTo_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.To
		want string
	}{
		{"nil", (*header.To)(nil), ""},
		{"zero", &header.To{}, "To: <>"},
		{
			"full",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			"To: \"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>;tag=a48s",
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

func TestTo_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.To
		wantRes string
		wantErr error
	}{
		{"nil", (*header.To)(nil), "", nil},
		{"zero", &header.To{}, "To: <>", nil},
		{
			"full",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			"To: \"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>;tag=a48s",
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

func TestTo_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.To
		want string
	}{
		{"nil", (*header.To)(nil), ""},
		{"zero", &header.To{}, "<>"},
		{
			"full",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			"\"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>;tag=a48s",
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

func TestTo_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.To
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.To)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.To)(nil), (*header.To)(nil), true},
		{"zero ptr to nil ptr", &header.To{}, (*header.To)(nil), false},
		{"zero ptr to zero val", &header.To{}, header.To{}, true},
		{
			"not match 1",
			&header.To{},
			header.To{
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
			},
			false,
		},
		{
			"not match 2",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User: uri.User("AGB"),
					Addr: uri.AddrFromHost("bell-telephone.com"),
				},
				Params: make(header.Values).Set("tag", "qwerty"),
			},
			false,
		},
		{
			"not match 3",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s").Set("x", "def"),
			},
			&header.To{
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s").Set("x", "abc"),
			},
			false,
		},
		{
			"match",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			header.To{
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s").Set("x", "abc"),
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

func TestTo_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.To
		want bool
	}{
		{"nil", (*header.To)(nil), false},
		{"zero", &header.To{}, false},
		{"invalid", &header.To{URI: (*uri.SIP)(nil)}, false},
		{
			"valid",
			&header.To{
				URI: &uri.SIP{Addr: uri.AddrFromHost("bell-telephone.com")},
			},
			true,
		},
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

func TestTo_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.To
	}{
		{"nil", (*header.To)(nil)},
		{"zero", &header.To{}},
		{
			"full",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s").Set("x", "def"),
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

func TestTo_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.To
		want string
	}{
		{"nil", nil, "null"},
		{"zero", &header.To{}, `{"name":"To","value":"\u003c\u003e"}`},
		{
			"simple",
			&header.To{
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
			},
			`{"name":"To","value":"\u003csip:alice@example.com\u003e"}`,
		},
		{
			"full",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			`{"name":"To","value":"\"A. G. Bell\" \u003csip:agb@bell-telephone.com;transport=udp\u003e;tag=a48s"}`,
		},
		{
			"with multiple params",
			&header.To{
				DisplayName: "Alice",
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
				Params: make(header.Values).Set("tag", "xyz123").Set("expires", "3600"),
			},
			`{"name":"To","value":"\"Alice\" \u003csip:alice@example.com\u003e;expires=3600;tag=xyz123"}`,
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

func TestTo_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    *header.To
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"<sip:alice@example.com>"}`, nil, true},
		{"empty value", `{"name":"To","value":""}`, &header.To{}, false},
		{"invalid json", `{"name":"To","value":`, nil, true},
		{"wrong header type", `{"name":"From","value":"<sip:alice@example.com>"}`, nil, true},
		{"zero", `{"name":"To","value":"<>"}`, &header.To{}, false},
		{
			"simple",
			`{"name":"To","value":"<sip:alice@example.com>"}`,
			&header.To{
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
			},
			false,
		},
		{
			"full",
			`{"name":"To","value":"\"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>;tag=a48s"}`,
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			false,
		},
		{
			"with multiple params",
			`{"name":"To","value":"\"Alice\" <sip:alice@example.com>;tag=xyz123;expires=3600"}`,
			&header.To{
				DisplayName: "Alice",
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
				Params: make(header.Values).Set("tag", "xyz123").Set("expires", "3600"),
			},
			false,
		},
		{
			"compact form",
			`{"name":"t","value":"<sip:bob@biloxi.com>;tag=1234"}`,
			&header.To{
				URI: &uri.SIP{
					User: uri.User("bob"),
					Addr: uri.AddrFromHost("biloxi.com"),
				},
				Params: make(header.Values).Set("tag", "1234"),
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got *header.To
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

func TestTo_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.To
	}{
		{"nil", nil},
		{"zero", &header.To{}},
		{
			"simple",
			&header.To{
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
			},
		},
		{
			"with display name",
			&header.To{
				DisplayName: "Alice Smith",
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
				Params: make(header.Values).Set("tag", "abc123"),
			},
		},
		{
			"full",
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp").Set("user", "phone"),
				},
				Params: make(header.Values).Set("tag", "a48s").Set("expires", "3600"),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			var got *header.To
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
