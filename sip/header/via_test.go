package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/sip/header"
)

func TestVia_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Via
		want string
	}{
		{"nil", header.Via(nil), ""},
		{"empty", header.Via{}, "Via: "},
		{"empty elem", header.Via{{}}, "Via: // "},
		{
			"full",
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "UDP",
					Addr:      header.AddrFromHostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.AddrFromHostPort("first.example.com", 4000),
					Params: make(header.Values).
						Set("ttl", "16").
						Set("maddr", "224.2.0.1").
						Set("branch", "z9hG4bKa7c6a8dlze.1"),
				},
			},
			"Via: SIP/2.0/UDP erlang.bell-telephone.com:5060;branch=z9hG4bK87asdks7;received=192.0.2.207, " +
				"SIP/2.0/TCP first.example.com:4000;branch=z9hG4bKa7c6a8dlze.1;maddr=224.2.0.1;ttl=16",
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

func TestVia_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.Via
		wantRes string
		wantErr error
	}{
		{"nil", header.Via(nil), "", nil},
		{"empty", header.Via{}, "Via: ", nil},
		{
			"full",
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "UDP",
					Addr:      header.AddrFromHostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.AddrFromHostPort("first.example.com", 4000),
					Params: make(header.Values).
						Set("ttl", "16").
						Set("maddr", "224.2.0.1").
						Set("branch", "z9hG4bKa7c6a8dlze.1"),
				},
			},
			"Via: SIP/2.0/UDP erlang.bell-telephone.com:5060;branch=z9hG4bK87asdks7;received=192.0.2.207, " +
				"SIP/2.0/TCP first.example.com:4000;branch=z9hG4bKa7c6a8dlze.1;maddr=224.2.0.1;ttl=16",
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

func TestVia_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Via
		want string
	}{
		{"nil", header.Via(nil), ""},
		{"empty", header.Via{}, ""},
		{
			"full",
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "UDP",
					Addr:      header.AddrFromHostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.AddrFromHostPort("first.example.com", 4000),
					Params: make(header.Values).
						Set("ttl", "16").
						Set("maddr", "224.2.0.1").
						Set("branch", "z9hG4bKa7c6a8dlze.1"),
				},
			},
			"SIP/2.0/UDP erlang.bell-telephone.com:5060;branch=z9hG4bK87asdks7;received=192.0.2.207, " +
				"SIP/2.0/TCP first.example.com:4000;branch=z9hG4bKa7c6a8dlze.1;maddr=224.2.0.1;ttl=16",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := c.hdr.String(), c.want; got != want {
				t.Errorf("hdr.String() = %q, want %q\nhdr = %#v", got, want, c.hdr)
			}
		})
	}
}

func TestVia_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Via
		val  any
		want bool
	}{
		{"nil ptr to nil", header.Via(nil), nil, false},
		{"nil ptr to nil ptr", header.Via(nil), header.Via(nil), true},
		{"zero ptr to nil ptr", header.Via{}, header.Via(nil), true},
		{"zero to zero", header.Via{}, header.Via{}, true},
		{"zero to zero ptr", header.Via{}, &header.Via{}, true},
		{"zero to nil ptr", header.Via{}, (*header.Via)(nil), false},
		{
			"not match",
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "UDP",
					Addr:      header.AddrFromHostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.AddrFromHostPort("first.example.com", 4000),
					Params: make(header.Values).
						Set("ttl", "16").
						Set("maddr", "224.2.0.1").
						Set("branch", "z9hG4bKa7c6a8dlze.1"),
				},
			},
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.AddrFromHostPort("first.example.com", 4000),
					Params: make(header.Values).
						Set("ttl", "16").
						Set("maddr", "224.2.0.1").
						Set("branch", "z9hG4bKa7c6a8dlze.1"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "UDP",
					Addr:      header.AddrFromHostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
			},
			false,
		},
		{
			"match",
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "sip", Version: "2.0"},
					Transport: "udp",
					Addr:      header.AddrFromHostPort("ERLANG.BELL-TELEPHONE.COM", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.AddrFromHostPort("first.example.com", 4000),
					Params: make(header.Values).
						Set("ttl", "16").
						Set("maddr", "224.2.0.1").
						Set("branch", "z9hG4bKa7c6a8dlze.1").
						Set("custom_1", "qwerty").
						Set("custom_2", `"Aaa BBB"`),
				},
			},
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "UDP",
					Addr:      header.AddrFromHostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.AddrFromHostPort("first.example.com", 4000),
					Params: make(header.Values).
						Set("ttl", "16").
						Set("maddr", "224.2.0.1").
						Set("branch", "z9hG4bKa7c6a8dlze.1").
						Set("custom_2", `"Aaa BBB"`),
				},
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

func TestVia_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Via
		want bool
	}{
		{"nil", header.Via(nil), false},
		{"empty", header.Via{}, false},
		{
			"invalid",
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "sip", Version: "2.0"},
					Transport: "UDP",
				},
			},
			false,
		},
		{
			"valid",
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "sip", Version: "2.0"},
					Transport: "udp",
					Addr:      header.AddrFromHostPort("ERLANG.BELL-TELEPHONE.COM", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.AddrFromHostPort("first.example.com", 4000),
					Params: make(header.Values).
						Set("ttl", "16").
						Set("maddr", "224.2.0.1").
						Set("branch", "z9hG4bKa7c6a8dlze.1").
						Set("custom_1", "qwerty").
						Set("custom_2", `"Aaa BBB"`),
				},
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

func TestVia_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Via
	}{
		{"nil", header.Via(nil)},
		{"empty", header.Via{}},
		{
			"full",
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "sip", Version: "2.0"},
					Transport: "udp",
					Addr:      header.AddrFromHostPort("ERLANG.BELL-TELEPHONE.COM", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.AddrFromHostPort("first.example.com", 4000),
					Params: make(header.Values).
						Set("ttl", "16").
						Set("maddr", "224.2.0.1").
						Set("branch", "z9hG4bKa7c6a8dlze.1").
						Set("custom_1", "qwerty").
						Set("custom_2", `"Aaa BBB"`),
				},
			},
		},
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

func TestVia_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Via
		want string
	}{
		{"nil", nil, "null"},
		{"empty", header.Via{}, `{"name":"Via","value":""}`},
		{
			"full",
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "UDP",
					Addr:      header.AddrFromHostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.AddrFromHostPort("first.example.com", 4000),
					Params: make(header.Values).
						Set("ttl", "16").
						Set("maddr", "224.2.0.1").
						Set("branch", "z9hG4bKa7c6a8dlze.1"),
				},
			},
			`{"name":"Via","value":"SIP/2.0/UDP erlang.bell-telephone.com:5060;branch=z9hG4bK87asdks7;received=192.0.2.207, SIP/2.0/TCP first.example.com:4000;branch=z9hG4bKa7c6a8dlze.1;maddr=224.2.0.1;ttl=16"}`,
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
				t.Fatalf("json.Marshal(hdr) = %q, want %q\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestVia_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    header.Via
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"*"}`, nil, true},
		{"empty value", `{"name":"Via","value":""}`, header.Via{}, false},
		{"invalid json", `{"name":"Via","value":`, nil, true},
		{"wrong header", `{"name":"From","value":"\"Alice\" <sip:alice@example.com>"}`, nil, true},
		{
			"full",
			`{"name":"Via","value":"SIP/2.0/UDP erlang.bell-telephone.com:5060;branch=z9hG4bK87asdks7;received=192.0.2.207, SIP/2.0/TCP first.example.com:4000;branch=z9hG4bKa7c6a8dlze.1;maddr=224.2.0.1;ttl=16"}`,
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "UDP",
					Addr:      header.AddrFromHostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.AddrFromHostPort("first.example.com", 4000),
					Params: make(header.Values).
						Set("ttl", "16").
						Set("maddr", "224.2.0.1").
						Set("branch", "z9hG4bKa7c6a8dlze.1"),
				},
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.Via
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

func TestViaHop_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hop  header.ViaHop
		want string
	}{
		{"zero", header.ViaHop{}, "// "},
		{
			"full",
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "UDP",
				Addr:      header.AddrFromHostPort("erlang.bell-telephone.com", 5060),
				Params: make(header.Values).
					Set("received", "192.0.2.207").
					Set("branch", "z9hG4bK87asdks7"),
			},
			"SIP/2.0/UDP erlang.bell-telephone.com:5060;branch=z9hG4bK87asdks7;received=192.0.2.207",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hop.String(); got != c.want {
				t.Errorf("hop.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestViaHop_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hop  header.ViaHop
		val  any
		want bool
	}{
		{"zero to nil", header.ViaHop{}, nil, false},
		{"zero to zero", header.ViaHop{}, header.ViaHop{}, true},
		{"zero to zero ptr", header.ViaHop{}, &header.ViaHop{}, true},
		{"zero to nil ptr", header.ViaHop{}, (*header.ViaHop)(nil), false},
		{
			"not match 1",
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "3.0"},
				Transport: "TCP",
				Addr:      header.AddrFromHostPort("first.example.com", 4000),
			},
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.AddrFromHostPort("first.example.com", 4000),
			},
			false,
		},
		{
			"not match 2",
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.AddrFromHostPort("first.example.com", 4000),
			},
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "UDP",
				Addr:      header.AddrFromHostPort("first.example.com", 4000),
			},
			false,
		},
		{
			"not match 3",
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.AddrFromHostPort("example.com", 5060),
			},
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.AddrFromHostPort("first.example.com", 4000),
			},
			false,
		},
		{
			"not match 4",
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.AddrFromHostPort("example.com", 5060),
				Params:    make(types.Values).Set("branch", "z9hG4bKa7c6a8dlze.1"),
			},
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.AddrFromHostPort("example.com", 5060),
				Params:    make(types.Values).Set("branch", "qwerty"),
			},
			false,
		},
		{
			"match",
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.AddrFromHostPort("first.example.com", 4000),
				Params: make(header.Values).
					Set("ttl", "16").
					Set("maddr", "224.2.0.1").
					Set("branch", "z9hG4bKa7c6a8dlze.1").
					Set("custom_1", "qwerty").
					Set("custom_2", `"Aaa BBB"`),
			},
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.AddrFromHostPort("first.example.com", 4000),
				Params: make(header.Values).
					Set("ttl", "16").
					Set("maddr", "224.2.0.1").
					Set("branch", "z9hG4bKa7c6a8dlze.1").
					Set("custom_2", `"Aaa BBB"`),
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hop.Equal(c.val); got != c.want {
				t.Errorf("hop.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestViaHop_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hop  header.ViaHop
		want bool
	}{
		{"zero", header.ViaHop{}, false},
		{
			"valid",
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TLS",
				Addr:      header.AddrFromHostPort("erlang.bell-telephone.com", 5060),
				Params:    make(header.Values).Set("branch", "z9hG4bK87asdks7"),
			},
			true,
		},
		{"invalid 1", header.ViaHop{Proto: header.ProtoInfo{Name: "SIP"}}, false},
		{"invalid 2", header.ViaHop{Proto: header.ProtoInfo{Name: "SIP", Version: "2.0"}}, false},
		{"invalid 3", header.ViaHop{Proto: header.ProtoInfo{Name: "SIP", Version: "2.0"}, Transport: "UDP"}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hop.IsValid(); got != c.want {
				t.Errorf("hop.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestViaHop_IsZero(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hop  header.ViaHop
		want bool
	}{
		{"zero", header.ViaHop{}, true},
		{"not zero", header.ViaHop{Transport: "UDP"}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hop.IsZero(); got != c.want {
				t.Errorf("hop.IsZero() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestViaHop_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hop  header.ViaHop
		want any
	}{
		{"zero", header.ViaHop{}, header.ViaHop{}},
		{
			"full",
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TLS",
				Addr:      header.AddrFromHostPort("erlang.bell-telephone.com", 5060),
				Params:    make(header.Values).Set("branch", "z9hG4bK87asdks7"),
			},
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TLS",
				Addr:      header.AddrFromHostPort("erlang.bell-telephone.com", 5060),
				Params:    make(header.Values).Set("branch", "z9hG4bK87asdks7"),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.hop.Clone()
			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Errorf("hop.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestViaHop_MarshalText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hop  header.ViaHop
		want string
	}{
		{"zero", header.ViaHop{}, "// "},
		{
			"full",
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "UDP",
				Addr:      header.AddrFromHostPort("erlang.bell-telephone.com", 5060),
				Params: make(header.Values).
					Set("received", "192.0.2.207").
					Set("branch", "z9hG4bK87asdks7"),
			},
			"SIP/2.0/UDP erlang.bell-telephone.com:5060;branch=z9hG4bK87asdks7;received=192.0.2.207",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := c.hop.MarshalText()
			if err != nil {
				t.Fatalf("hop.MarshalText() error = %v, want nil", err)
			}

			if string(got) != c.want {
				t.Fatalf("hop.MarshalText() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestViaHop_UnmarshalText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    header.ViaHop
		wantErr bool
	}{
		{"empty", "", header.ViaHop{}, false},
		{"invalid", "not a valid hop", header.ViaHop{}, true},
		{"zero", "// ", header.ViaHop{}, false},
		{
			"full",
			"SIP/2.0/UDP erlang.bell-telephone.com:5060;branch=z9hG4bK87asdks7;received=192.0.2.207",
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "UDP",
				Addr:      header.AddrFromHostPort("erlang.bell-telephone.com", 5060),
				Params: make(header.Values).
					Set("received", "192.0.2.207").
					Set("branch", "z9hG4bK87asdks7"),
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.ViaHop
			if err := got.UnmarshalText([]byte(c.data)); err != nil {
				if !c.wantErr {
					t.Fatalf("hop.UnmarshalText(data) error = %v, want nil", err)
				}
				return
			}

			if c.wantErr {
				t.Fatal("hop.UnmarshalText(data) error = nil, want error")
			}

			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}
