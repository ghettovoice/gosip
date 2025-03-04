package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/types"
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
					Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.HostPort("first.example.com", 4000),
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
					Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.HostPort("first.example.com", 4000),
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
					Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.HostPort("first.example.com", 4000),
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
					Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.HostPort("first.example.com", 4000),
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
					Addr:      header.HostPort("first.example.com", 4000),
					Params: make(header.Values).
						Set("ttl", "16").
						Set("maddr", "224.2.0.1").
						Set("branch", "z9hG4bKa7c6a8dlze.1"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "UDP",
					Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
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
					Addr:      header.HostPort("ERLANG.BELL-TELEPHONE.COM", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.HostPort("first.example.com", 4000),
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
					Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.HostPort("first.example.com", 4000),
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
					Addr:      header.HostPort("ERLANG.BELL-TELEPHONE.COM", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.HostPort("first.example.com", 4000),
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
					Addr:      header.HostPort("ERLANG.BELL-TELEPHONE.COM", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "TCP",
					Addr:      header.HostPort("first.example.com", 4000),
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
				Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
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
				Addr:      header.HostPort("first.example.com", 4000),
			},
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.HostPort("first.example.com", 4000),
			},
			false,
		},
		{
			"not match 2",
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.HostPort("first.example.com", 4000),
			},
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "UDP",
				Addr:      header.HostPort("first.example.com", 4000),
			},
			false,
		},
		{
			"not match 3",
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.HostPort("example.com", 5060),
			},
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.HostPort("first.example.com", 4000),
			},
			false,
		},
		{
			"not match 4",
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.HostPort("example.com", 5060),
				Params:    make(types.Values).Set("branch", "z9hG4bKa7c6a8dlze.1"),
			},
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.HostPort("example.com", 5060),
				Params:    make(types.Values).Set("branch", "qwerty"),
			},
			false,
		},
		{
			"match",
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TCP",
				Addr:      header.HostPort("first.example.com", 4000),
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
				Addr:      header.HostPort("first.example.com", 4000),
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
				Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
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
				Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
				Params:    make(header.Values).Set("branch", "z9hG4bK87asdks7"),
			},
			header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "TLS",
				Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
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
