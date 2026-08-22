package header_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestMIMEType_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mt   header.MIMEType
		want string
	}{
		{"zero", header.MIMEType{}, "/"},
		{
			"full",
			header.MIMEType{
				Type:    "TEXT",
				Subtype: "PLAIN",
				Params: make(header.Values).
					Append("foo", "123").
					Append("Charset", "UTF-8").
					Append("bar", `"QwertY"`),
			},
			`TEXT/PLAIN;bar="QwertY";charset=UTF-8;foo=123`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.mt.String(); got != c.want {
				t.Errorf("mt.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMIMEType_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mt   header.MIMEType
		val  any
		want bool
	}{
		{"zero to nil", header.MIMEType{}, nil, false},
		{"zero to zero", header.MIMEType{}, header.MIMEType{}, true},
		{"zero to zero ptr", header.MIMEType{}, &header.MIMEType{}, true},
		{"zero to nil ptr", header.MIMEType{}, (*header.MIMEType)(nil), false},
		{
			"not match 1",
			header.MIMEType{Type: "text"},
			header.MIMEType{},
			false,
		},
		{
			"not match 2",
			header.MIMEType{Type: "text"},
			header.MIMEType{Type: "text", Subtype: "*"},
			false,
		},
		{
			"not match 3",
			header.MIMEType{
				Type:    "text",
				Subtype: "plain",
				Params:  make(header.Values).Set("charset", "utf-8"),
			},
			header.MIMEType{
				Type:    "text",
				Subtype: "plain",
				Params:  make(header.Values).Set("charset", "cp1251"),
			},
			false,
		},
		{
			"not match 4",
			header.MIMEType{
				Type:    "text",
				Subtype: "plain",
				Params:  make(header.Values).Set("foo", "bar"),
			},
			header.MIMEType{
				Type:    "text",
				Subtype: "plain",
				Params:  make(header.Values).Set("charset", "cp1251"),
			},
			false,
		},
		{
			"match",
			header.MIMEType{
				Type:    "text",
				Subtype: "plain",
				Params:  make(header.Values).Set("charset", "utf-8"),
			},
			header.MIMEType{
				Type:    "TEXT",
				Subtype: "PLAIN",
				Params:  make(header.Values).Set("CHARSET", "UTF-8").Set("foo", "bar"),
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.mt.Equal(c.val); got != c.want {
				t.Errorf("mt.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestMIMEType_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mt   header.MIMEType
		want bool
	}{
		{"zero", header.MIMEType{}, false},
		{"valid 1", header.MIMEType{Type: "*", Subtype: "*"}, true},
		{
			"valid 2",
			header.MIMEType{
				Type:    "text",
				Subtype: "*",
				Params:  make(header.Values).Append("Foo", `" B a R "`),
			},
			true,
		},
		{"invalid 1", header.MIMEType{Type: "text"}, false},
		{"invalid 2", header.MIMEType{Subtype: "plain"}, false},
		{
			"invalid 3",
			header.MIMEType{
				Type:    "text",
				Subtype: "plain",
				Params:  make(header.Values).Append(" F - O_O ", "bar"),
			},
			false,
		},
		{
			"invalid 4",
			header.MIMEType{
				Type:    "text",
				Subtype: "*",
				Params:  make(header.Values).Append("Foo", " B a R "),
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.mt.IsValid(); got != c.want {
				t.Errorf("mt.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestMIMEType_IsZero(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mt   header.MIMEType
		want bool
	}{
		{"zero", header.MIMEType{}, true},
		{"not zero 1", header.MIMEType{Type: "*"}, false},
		{"not zero 2", header.MIMEType{Subtype: "*"}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.mt.IsZero(); got != c.want {
				t.Errorf("mt.IsZero() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestMIMEType_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mt   header.MIMEType
	}{
		{"zero", header.MIMEType{}},
		{
			"full",
			header.MIMEType{
				Type:    "text",
				Subtype: "*",
				Params:  header.Values{"charset": {"utf-8"}},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.mt.Clone()
			if diff := cmp.Diff(got, c.mt); diff != "" {
				t.Errorf("mt.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.mt, diff)
			}
		})
	}
}

func TestMIMEType_MarshalText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mt   header.MIMEType
		want string
	}{
		{"zero", header.MIMEType{}, "/"},
		{
			"with params",
			header.MIMEType{
				Type:    "text",
				Subtype: "html",
				Params: make(header.Values).
					Append("charset", "utf-8").
					Append("level", "1"),
			},
			"text/html;charset=utf-8;level=1",
		},
		{
			"wildcard with q",
			header.MIMEType{
				Type:    "*",
				Subtype: "*",
				Params: make(header.Values).
					Append("level", "1").
					Append("q", "0.5"),
			},
			"*/*;level=1;q=0.5",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := c.mt.MarshalText()
			if err != nil {
				t.Fatalf("mt.MarshalText() error = %v, want nil", err)
			}

			if string(got) != c.want {
				t.Fatalf("mt.MarshalText() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMIMEType_UnmarshalText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    header.MIMEType
		wantErr bool
	}{
		{"empty", "", header.MIMEType{}, false},
		{"slash", "/", header.MIMEType{}, false},
		{"invalid", "text", header.MIMEType{}, true},
		{
			"simple",
			"text/plain;charset=utf-8",
			header.MIMEType{
				Type:    "text",
				Subtype: "plain",
				Params: make(header.Values).
					Append("charset", "utf-8"),
			},
			false,
		},
		{
			"wildcard with q",
			"*/*;q=0.5;level=1",
			header.MIMEType{
				Type:    "*",
				Subtype: "*",
				Params: make(header.Values).
					Append("q", "0.5").
					Append("level", "1"),
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.MIMEType
			if err := got.UnmarshalText([]byte(c.data)); err != nil {
				if !c.wantErr {
					t.Fatalf("mt.UnmarshalText() error = %v, want nil", err)
				}
				return
			}

			if c.wantErr {
				t.Fatal("mt.UnmarshalText() error = nil, want error")
			}

			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestMIMEType_RoundTripText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mt   header.MIMEType
	}{
		{"zero", header.MIMEType{}},
		{"simple", header.MIMEType{Type: "text", Subtype: "plain"}},
		{
			"with params",
			header.MIMEType{
				Type:    "text",
				Subtype: "html",
				Params: make(header.Values).
					Append("charset", "utf-8").
					Append("level", "1"),
			},
		},
		{
			"wildcard with q",
			header.MIMEType{
				Type:    "*",
				Subtype: "*",
				Params: make(header.Values).
					Append("q", "0.5"),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := c.mt.MarshalText()
			if err != nil {
				t.Fatalf("mt.MarshalText() error = %v, want nil", err)
			}

			var got header.MIMEType
			if err := got.UnmarshalText(data); err != nil {
				t.Fatalf("mt.UnmarshalText(data) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.mt); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.mt, diff)
			}
		})
	}
}
