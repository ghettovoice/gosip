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

func TestFrom_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.From
		opts *header.RenderOptions
		want string
	}{
		{"nil", (*header.From)(nil), nil, ""},
		{"zero", &header.From{}, nil, "From: <>"},
		{
			"full",
			&header.From{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			}, &header.RenderOptions{Compact: true}, "f: \"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>;tag=a48s",
		},
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

func TestFrom_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.From
		wantRes string
		wantErr error
	}{
		{"nil", (*header.From)(nil), "", nil},
		{"zero", &header.From{}, "From: <>", nil},
		{
			"full",
			&header.From{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			"From: \"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>;tag=a48s",
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

func TestFrom_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.From
		want string
	}{
		{"nil", (*header.From)(nil), ""},
		{"zero", &header.From{}, "<>"},
		{
			"full",
			&header.From{
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

func TestFrom_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.From
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.From)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.From)(nil), (*header.From)(nil), true},
		{"zero ptr to nil ptr", &header.From{}, (*header.From)(nil), false},
		{"zero ptr to zero val", &header.From{}, header.From{}, true},
		{
			"not match 1",
			&header.From{},
			header.From{
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
			&header.From{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			&header.From{
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
			&header.From{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s").Set("x", "def"),
			},
			&header.From{
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
			&header.From{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			header.From{
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

func TestFrom_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.From
		want bool
	}{
		{"nil", (*header.From)(nil), false},
		{"zero", &header.From{}, false},
		{"invalid", &header.From{URI: (*uri.SIP)(nil)}, false},
		{
			"valid",
			&header.From{
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

func TestFrom_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.From
	}{
		{"nil", nil},
		{"zero", &header.From{}},
		{
			"full",
			&header.From{
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

func TestFrom_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.From
		want string
	}{
		{"nil", nil, "null"},
		{"zero", &header.From{}, `{"name":"From","value":"\u003c\u003e"}`},
		{
			"simple",
			&header.From{
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
			},
			`{"name":"From","value":"\u003csip:alice@example.com\u003e"}`,
		},
		{
			"full",
			&header.From{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.AddrFromHost("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			`{"name":"From","value":"\"A. G. Bell\" \u003csip:agb@bell-telephone.com;transport=udp\u003e;tag=a48s"}`,
		},
		{
			"with multiple params",
			&header.From{
				DisplayName: "Alice",
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
				Params: make(header.Values).Set("tag", "xyz123").Set("expires", "3600"),
			},
			`{"name":"From","value":"\"Alice\" \u003csip:alice@example.com\u003e;expires=3600;tag=xyz123"}`,
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

func TestFrom_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    *header.From
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"<sip:alice@example.com>"}`, nil, true},
		{"empty value", `{"name":"From","value":""}`, &header.From{}, false},
		{"invalid json", `{"name":"From","value":`, nil, true},
		{"wrong header type", `{"name":"To","value":"<sip:alice@example.com>"}`, nil, true},
		{"zero", `{"name":"From","value":"<>"}`, &header.From{}, false},
		{
			"simple",
			`{"name":"From","value":"<sip:alice@example.com>"}`,
			&header.From{
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
			},
			false,
		},
		{
			"full",
			`{"name":"From","value":"\"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>;tag=a48s"}`,
			&header.From{
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
			`{"name":"From","value":"\"Alice\" <sip:alice@example.com>;tag=xyz123;expires=3600"}`,
			&header.From{
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
			`{"name":"f","value":"<sip:bob@biloxi.com>;tag=1234"}`,
			&header.From{
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

			var got *header.From
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

func TestFrom_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.From
	}{
		{"nil", nil},
		{"zero", &header.From{}},
		{
			"simple",
			&header.From{
				URI: &uri.SIP{
					User: uri.User("alice"),
					Addr: uri.AddrFromHost("example.com"),
				},
			},
		},
		{
			"with display name",
			&header.From{
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
			&header.From{
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

			// Marshal
			data, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			// Unmarshal
			var got *header.From
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
