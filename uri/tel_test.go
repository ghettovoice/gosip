package uri_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/uri"
)

func TestTel_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  *uri.Tel
		want string
	}{
		{"nil", (*uri.Tel)(nil), ""},
		{"zero", &uri.Tel{}, "tel:"},
		{"params only", &uri.Tel{Params: uri.Values{"ext": []string{"123"}}}, "tel:;ext=123"},
		{"number only", &uri.Tel{Number: "+1(222)333-44-55"}, "tel:+1(222)333-44-55"},
		{"number with spaces", &uri.Tel{Number: "+1  (222)  333-44-55   "}, "tel:+1(222)333-44-55"},
		{
			"number and params",
			&uri.Tel{
				Number: "+1  (222)  333-44-55",
				Params: uri.Values{
					"isub":          []string{"abc"},
					"Foo-Bar":       []string{""},
					"ext":           []string{"111", "222"},
					"z":             []string{"2@;"},
					"phone-context": []string{"+333", "+1(23)"},
					"a":             []string{"1"},
				},
			},
			"tel:+1(222)333-44-55;ext=222;isub=abc;phone-context=+1(23);a=1;foo-bar;z=2%40%3B",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.uri.Render(nil); got != c.want {
				t.Errorf("uri.Render(nil) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTel_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		uri     *uri.Tel
		wantRes string
		wantErr error
	}{
		{"nil", (*uri.Tel)(nil), "", nil},
		{"zero", &uri.Tel{}, "tel:", nil},
		{"number only", &uri.Tel{Number: "+1(222)333-44-55"}, "tel:+1(222)333-44-55", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := c.uri.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("uri.RenderTo(sb, nil) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}
			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestTel_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  *uri.Tel
		want string
	}{
		{"nil", (*uri.Tel)(nil), ""},
		{"zero", &uri.Tel{}, "tel:"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.uri.String(); got != c.want {
				t.Errorf("uri.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTel_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  *uri.Tel
		val  any
		want bool
	}{
		{"nil ptr to nil", (*uri.Tel)(nil), nil, false},
		{"nil ptr to nil ptr", (*uri.Tel)(nil), (*uri.Tel)(nil), true},
		{"zero ptr to nil ptr", &uri.Tel{}, (*uri.Tel)(nil), false},
		{"nil ptr to zero ptr", (*uri.Tel)(nil), &uri.Tel{}, false},
		{"zero ptr to zero ptr", &uri.Tel{}, &uri.Tel{}, true},
		{"zero ptr to zero val", &uri.Tel{}, uri.Tel{}, true},
		{"type mismatch", &uri.Tel{Number: "+123"}, "tel:+123", false},
		{"phone match", &uri.Tel{Number: "+1(123)33-55"}, &uri.Tel{Number: "+1 123 33 - 55 "}, true},
		{"phone not match 1", &uri.Tel{Number: "+1(123)33-55"}, &uri.Tel{}, false},
		{"phone not match 2", &uri.Tel{Number: "+1(123)33-55"}, &uri.Tel{Number: "1(123)33-55"}, false},
		{
			"params match",
			&uri.Tel{
				Number: "+1(123)33-55",
				Params: make(uri.Values).
					Set("BAR", "QWE").
					Set("foo", "").
					Set("ext", "12344"),
			},
			&uri.Tel{
				Number: "+1(123)33-55",
				Params: make(uri.Values).
					Set("ext", "123-44").
					Set("bar", "qwe").
					Set("FOO", ""),
			},
			true,
		},
		{
			"params not match 1",
			&uri.Tel{
				Number: "+1(123)33-55",
				Params: make(uri.Values).
					Set("foo", "").
					Set("ext", "12344"),
			},
			&uri.Tel{
				Number: "+1(123)33-55",
				Params: make(uri.Values).
					Set("ext", "12344").
					Set("bar", "qwe").
					Set("foo", ""),
			},
			false,
		},
		{
			"params not match 2",
			&uri.Tel{
				Number: "+1(123)33-55",
				Params: make(uri.Values).
					Set("a", "qwe").
					Set("foo", "").
					Set("ext", "12344"),
			},
			&uri.Tel{
				Number: "+1(123)33-55",
				Params: make(uri.Values).
					Set("b", "qwe").
					Set("foo", "").
					Set("ext", "12344"),
			},
			false,
		},
		{
			"params not match 3",
			&uri.Tel{
				Number: "+1(123)33-55",
				Params: make(uri.Values).
					Set("a", "qwe").
					Set("foo", "").
					Set("ext", "12344"),
			},
			&uri.Tel{
				Number: "+1(123)33-55",
				Params: make(uri.Values).
					Set("a", "abc").
					Set("foo", "").
					Set("ext", "12344"),
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.uri.Equal(c.val); got != c.want {
				t.Errorf("uri.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestTel_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  *uri.Tel
		want bool
	}{
		{"nil", (*uri.Tel)(nil), false},
		{"zero", &uri.Tel{}, false},
		{"valid glob phone", &uri.Tel{Number: "+123"}, true},
		{"invalid local phone", &uri.Tel{Number: "123"}, false},
		{
			"valid local phone with params",
			&uri.Tel{Number: "123", Params: make(uri.Values).Set("phone-context", "+11")},
			true,
		},
		{"invalid params", &uri.Tel{Number: "+123", Params: make(uri.Values).Set("a b c", "111")}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.uri.IsValid(); got != c.want {
				t.Errorf("uri.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestTel_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  *uri.Tel
	}{
		{"nil", nil},
		{"zero", &uri.Tel{}},
		{
			"full",
			&uri.Tel{Number: "+123", Params: make(uri.Values).Set("phone-context", "+11")},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.uri.Clone()
			if c.uri == nil {
				if got != nil {
					t.Errorf("uri.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, c.uri); diff != "" {
				t.Errorf("uri.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.uri, diff)
			}
		})
	}
}

func TestTel_IsGlob(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  *uri.Tel
		want bool
	}{
		{"nil", (*uri.Tel)(nil), false},
		{"zero", &uri.Tel{}, false},
		{"glob", &uri.Tel{Number: "+123"}, true},
		{"local", &uri.Tel{Number: "123"}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.uri.IsGlob(); got != c.want {
				t.Errorf("uri.IsGlob() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestTel_ToSIP(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  *uri.Tel
		want *uri.SIP
	}{
		{"nil", (*uri.Tel)(nil), nil},
		{"zero", &uri.Tel{}, &uri.SIP{
			User:   uri.User(""),
			Addr:   uri.Host(""),
			Params: make(uri.Values).Set("user", "phone"),
		}},
		{
			"local with phone context as number",
			&uri.Tel{
				Number: "123",
				Params: make(uri.Values).
					Append("FOO", "BAR").
					Append("ext", "5-5-5").
					Append("baz", "").
					Append("phone-context", "+11"),
			},
			&uri.SIP{
				User:   uri.User("123;ext=555;phone-context=+11;baz;foo=bar"),
				Addr:   uri.Host(""),
				Params: make(uri.Values).Set("user", "phone"),
			},
		},
		{
			"local with phone context as host",
			&uri.Tel{
				Number: "123",
				Params: make(uri.Values).
					Append("FOO", "BAR").
					Append("ext", "5-5-5").
					Append("baz", "").
					Append("phone-context", "example.com"),
			},
			&uri.SIP{
				User:   uri.User("123;ext=555;baz;foo=bar"),
				Addr:   uri.Host("example.com"),
				Params: make(uri.Values).Set("user", "phone"),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.uri.ToSIP()
			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Errorf("uri.ToSIP() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestTel_MarshalUnmarshalText_RoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		uri     *uri.Tel
		wantErr bool
	}{
		{"nil", nil, true},
		{"zero", &uri.Tel{}, true},
		{"number", &uri.Tel{Number: "+123"}, false},
		{
			"params",
			&uri.Tel{
				Number: "+123",
				Params: make(uri.Values).
					Set("ext", "55").
					Set("phone-context", "+1"),
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			text, err := c.uri.MarshalText()
			if err != nil {
				t.Fatalf("uri.MarshalText() error = %v, want nil", err)
			}

			var got uri.Tel
			err = got.UnmarshalText(text)
			if c.wantErr {
				if err == nil {
					t.Fatalf("got.UnmarshalText(text) error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("got.UnmarshalText(text) error = %v, want nil", err)
			}

			if diff := cmp.Diff(&got, c.uri); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%s", &got, c.uri, diff)
			}
		})
	}
}
