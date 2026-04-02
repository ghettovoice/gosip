package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestAccept_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Accept
		want string
	}{
		{"nil", header.Accept(nil), ""},
		{"empty", header.Accept{}, "Accept: "},
		{"empty elem", header.Accept{{}}, "Accept: /"},
		{"any", header.Accept{{MIMEType: header.MIMEType{Type: "*", Subtype: "*"}}}, "Accept: */*"},
		{
			"single elem",
			header.Accept{{MIMEType: header.MIMEType{Type: "text", Subtype: "plain"}}},
			"Accept: text/plain",
		},
		{
			"multiple elems 1",
			header.Accept{
				{MIMEType: header.MIMEType{Type: "text", Subtype: "plain"}},
				{MIMEType: header.MIMEType{Type: "text", Subtype: "csv"}},
			},
			"Accept: text/plain, text/csv",
		},
		{
			"multiple elems 2",
			header.Accept{
				{
					MIMEType: header.MIMEType{Type: "text", Subtype: "plain"},
					Params:   make(header.Values).Set("a", "123").Set("q", "0.9"),
				},
				{MIMEType: header.MIMEType{Type: "text", Subtype: "csv"}},
			},
			"Accept: text/plain;q=0.9;a=123, text/csv",
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

func TestAccept_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.Accept
		wantRes string
		wantErr error
	}{
		{"nil", header.Accept(nil), "", nil},
		{"empty", header.Accept{}, "Accept: ", nil},
		{
			"full",
			header.Accept{
				{MIMEType: header.MIMEType{Type: "text", Subtype: "*"}},
				{MIMEType: header.MIMEType{Type: "application", Subtype: "*"}},
			},
			"Accept: text/*, application/*",
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder

			_, err := c.hdr.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo() error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}

			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestAccept_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Accept
		want string
	}{
		{"nil", header.Accept(nil), ""},
		{"empty", header.Accept{}, ""},
		{
			"full",
			header.Accept{
				{MIMEType: header.MIMEType{Type: "text", Subtype: "*"}},
				{MIMEType: header.MIMEType{Type: "application", Subtype: "*"}},
			},
			"text/*, application/*",
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

func TestAccept_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Accept
		val  any
		want bool
	}{
		{"nil ptr to nil", header.Accept(nil), nil, false},
		{"nil ptr to nil ptr", header.Accept(nil), header.Accept(nil), true},
		{"zero ptr to nil ptr", header.Accept{}, header.Accept(nil), true},
		{"zero to zero", header.Accept{}, header.Accept{}, true},
		{"zero to zero ptr", header.Accept{}, &header.Accept{}, true},
		{"zero to nil ptr", header.Accept{}, (*header.Accept)(nil), false},
		{"not match 1", header.Accept{}, header.Accept{{MIMEType: header.MIMEType{Type: "*", Subtype: "*"}}}, false},
		{
			"not match 2",
			header.Accept{{MIMEType: header.MIMEType{Type: "text", Subtype: "*"}}},
			header.Accept{{MIMEType: header.MIMEType{Type: "text"}}},
			false,
		},
		{
			"not match 3",
			header.Accept{{MIMEType: header.MIMEType{Type: "text", Subtype: "*"}}},
			header.Accept{{MIMEType: header.MIMEType{Type: "html", Subtype: "*"}}},
			false,
		},
		{
			"not match 4",
			header.Accept{{MIMEType: header.MIMEType{Type: "text", Subtype: "plain"}}},
			header.Accept{{MIMEType: header.MIMEType{Type: "text", Subtype: "*"}}},
			false,
		},
		{
			"not match 5",
			header.Accept{{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "*",
				},
			}},
			header.Accept{{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "*",
					Params:  make(header.Values).Set("charset", "UTF-8"),
				},
			}},
			false,
		},
		{
			"not match 6",
			header.Accept{{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "*",
					Params:  make(header.Values).Set("charset", "UTF-8"),
				},
			}},
			header.Accept{{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "*",
					Params:  make(header.Values).Set("charset", "UTF-16"),
				},
			}},
			false,
		},
		{
			"not match 7",
			header.Accept{{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "*",
					Params:  make(header.Values).Set("charset", "UTF-8"),
				},
				Params: make(header.Values).Set("FOO", "BAR").Set("field", `"QwertY"`),
			}},
			header.Accept{{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "*",
					Params:  make(header.Values).Set("charset", "UTF-8"),
				},
				Params: make(header.Values).Set("FOO", "BAR").Set("field", `"qwerty"`),
			}},
			false,
		},
		{
			"not match 8",
			header.Accept{{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "*",
					Params:  make(header.Values).Set("charset", "UTF-8"),
				},
				Params: make(header.Values).Set("FOO", "BAR").Set("field1", `"QwertY"`),
			}},
			header.Accept{{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "*",
					Params:  make(header.Values).Set("charset", "UTF-8"),
				},
				Params: make(header.Values).Set("q", "0.7").Set("FOO", "BAR").Set("field1", `"QwertY"`),
			}},
			false,
		},
		{
			"not match 9",
			header.Accept{{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "*",
					Params:  make(header.Values).Set("charset", "UTF-8"),
				},
				Params: make(header.Values).Set("FOO", "BAR").Set("field1", `"QwertY"`).Set("q", "0.7"),
			}},
			header.Accept{{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "*",
					Params:  make(header.Values).Set("charset", "UTF-8"),
				},
				Params: make(header.Values).Set("FOO", "BAR").Set("field1", `"QwertY"`),
			}},
			false,
		},
		{
			"match 1",
			header.Accept{{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "*",
					Params:  make(header.Values).Set("charset", "UTF-8"),
				},
				Params: make(header.Values).Set("FOO", "BAR").Set("q", "0.7"),
			}},
			header.Accept{{
				MIMEType: header.MIMEType{
					Type:    "TEXT",
					Subtype: "*",
					Params:  make(header.Values).Set("CHARSET", "utf-8"),
				},
				Params: make(header.Values).Set("foo", "bar").Set("field", `"QwertY"`).Set("q", "0.7"),
			}},
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

func TestAccept_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Accept
		want bool
	}{
		{"nil", header.Accept(nil), false},
		{"empty", header.Accept{}, true},
		{"invalid 1", header.Accept{{}}, false},
		{
			"invalid 2",
			header.Accept{{
				MIMEType: header.MIMEType{Type: "*", Subtype: "*"},
				Params:   make(header.Values).Set(" f o o ", "bar"),
			}},
			false,
		},
		{
			"invalid 3",
			header.Accept{{
				MIMEType: header.MIMEType{Type: "*", Subtype: "*"},
				Params:   make(header.Values).Set("foo", " b a r "),
			}},
			false,
		},
		{
			"valid",
			header.Accept{
				{
					MIMEType: header.MIMEType{
						Type:    "*",
						Subtype: "*",
						Params:  make(header.Values).Set("charset", "utf-8"),
					},
					Params: make(header.Values).Set("foo", `" b a r "`),
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

func TestAccept_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Accept
		want any
	}{
		{"nil", header.Accept(nil), header.Accept(nil)},
		{"empty", header.Accept{}, header.Accept{}},
		{
			"full",
			header.Accept{
				{MIMEType: header.MIMEType{Type: "text", Subtype: "plain"}, Params: header.Values{"q": {"0.7"}}},
				{
					MIMEType: header.MIMEType{Type: "text", Subtype: "csv", Params: header.Values{"charset": {"utf-8"}}},
					Params:   header.Values{"q": {"0.5"}},
				},
			},
			header.Accept{
				{MIMEType: header.MIMEType{Type: "text", Subtype: "plain"}, Params: header.Values{"q": {"0.7"}}},
				{
					MIMEType: header.MIMEType{Type: "text", Subtype: "csv", Params: header.Values{"charset": {"utf-8"}}},
					Params:   header.Values{"q": {"0.5"}},
				},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.hdr.Clone()
			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestAccept_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Accept
		want string
	}{
		{"nil", nil, "null"},
		{"empty", header.Accept{}, `{"name":"Accept","value":""}`},
		{
			"single",
			header.Accept{
				{
					MIMEType: header.MIMEType{Type: "text", Subtype: "plain"},
					Params:   make(header.Values).Set("q", "0.8"),
				},
			},
			`{"name":"Accept","value":"text/plain;q=0.8"}`,
		},
		{
			"multiple",
			header.Accept{
				{MIMEType: header.MIMEType{Type: "text", Subtype: "plain"}},
				{
					MIMEType: header.MIMEType{Type: "application", Subtype: "json"},
					Params:   make(header.Values).Set("q", "0.5"),
				},
			},
			`{"name":"Accept","value":"text/plain, application/json;q=0.5"}`,
		},
		{
			"with mime params",
			header.Accept{
				{
					MIMEType: header.MIMEType{
						Type:    "text",
						Subtype: "html",
						Params:  make(header.Values).Set("charset", "utf-8"),
					},
				},
			},
			`{"name":"Accept","value":"text/html;charset=utf-8"}`,
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

func TestAccept_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    header.Accept
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"text/plain"}`, nil, true},
		{"empty value", `{"name":"Accept","value":""}`, header.Accept{}, false},
		{"invalid json", `{"name":"Accept","value":`, nil, true},
		{"wrong header", `{"name":"From","value":"<sip:alice@example.com>"}`, nil, true},
		{
			"single",
			`{"name":"Accept","value":"text/plain;q=0.8"}`,
			header.Accept{
				{
					MIMEType: header.MIMEType{Type: "text", Subtype: "plain"},
					Params:   make(header.Values).Set("q", "0.8"),
				},
			},
			false,
		},
		{
			"multiple",
			`{"name":"Accept","value":"text/plain, application/json;q=0.5"}`,
			header.Accept{
				{MIMEType: header.MIMEType{Type: "text", Subtype: "plain"}},
				{
					MIMEType: header.MIMEType{Type: "application", Subtype: "json"},
					Params:   make(header.Values).Set("q", "0.5"),
				},
			},
			false,
		},
		{
			"with mime params",
			`{"name":"Accept","value":"text/html;charset=utf-8"}`,
			header.Accept{
				{
					MIMEType: header.MIMEType{
						Type:    "text",
						Subtype: "html",
						Params:  make(header.Values).Set("charset", "utf-8"),
					},
				},
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.Accept
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

func TestAccept_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Accept
	}{
		{"empty", header.Accept{}},
		{
			"single",
			header.Accept{
				{
					MIMEType: header.MIMEType{Type: "text", Subtype: "plain"},
					Params:   make(header.Values).Set("q", "0.8"),
				},
			},
		},
		{
			"multiple",
			header.Accept{
				{MIMEType: header.MIMEType{Type: "text", Subtype: "plain"}},
				{
					MIMEType: header.MIMEType{Type: "application", Subtype: "json"},
					Params:   make(header.Values).Set("q", "0.5"),
				},
			},
		},
		{
			"with_mime_params",
			header.Accept{
				{
					MIMEType: header.MIMEType{
						Type:    "text",
						Subtype: "html",
						Params:  make(header.Values).Set("charset", "utf-8"),
					},
					Params: make(header.Values).Set("q", "0.7").Set("level", "1"),
				},
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

			var got header.Accept
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}

func TestMIMERange_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.MIMERange
		want string
	}{
		{"zero", header.MIMERange{}, "/"},
		{
			"full",
			header.MIMERange{
				MIMEType: header.MIMEType{Type: "text", Subtype: "*", Params: header.Values{"charset": {"utf-8"}}},
				Params:   header.Values{"q": {"0.7"}},
			},
			"text/*;charset=utf-8;q=0.7",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.rng.String(); got != c.want {
				t.Errorf("rng.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMIMERange_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.MIMERange
		val  any
		want bool
	}{
		{"zero to nil", header.MIMERange{}, nil, false},
		{"zero to zero", header.MIMERange{}, header.MIMERange{}, true},
		{"zero to zero ptr", header.MIMERange{}, &header.MIMERange{}, true},
		{"zero to nil ptr", header.MIMERange{}, (*header.MIMERange)(nil), false},
		{"not match 1", header.MIMERange{}, header.MIMERange{MIMEType: header.MIMEType{Type: "*", Subtype: "*"}}, false},
		{
			"not match 2",
			header.MIMERange{MIMEType: header.MIMEType{Type: "text", Subtype: "*"}},
			header.MIMERange{MIMEType: header.MIMEType{Type: "text", Subtype: "csv"}},
			false,
		},
		{
			"not match 3",
			header.MIMERange{MIMEType: header.MIMEType{Type: "text", Subtype: "*"}, Params: header.Values{"q": {"0.7"}}},
			header.MIMERange{MIMEType: header.MIMEType{Type: "text", Subtype: "*"}, Params: header.Values{"q": {"0.5"}}},
			false,
		},
		{
			"match 1",
			header.MIMERange{
				MIMEType: header.MIMEType{Type: "text", Subtype: "*", Params: header.Values{"charset": {"utf-8"}}},
				Params:   header.Values{"q": {"0.7"}},
			},
			header.MIMERange{
				MIMEType: header.MIMEType{Type: "text", Subtype: "*", Params: header.Values{"charset": {"UTF-8"}}},
				Params:   header.Values{"q": {"0.7"}, "foo": {"bar"}},
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.rng.Equal(c.val); got != c.want {
				t.Errorf("rng.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestMIMERange_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.MIMERange
		want bool
	}{
		{"zero", header.MIMERange{}, false},
		{
			"valid",
			header.MIMERange{
				MIMEType: header.MIMEType{Type: "*", Subtype: "*"},
				Params:   header.Values{"q": {"0.7"}},
			},
			true,
		},
		{
			"invalid 1",
			header.MIMERange{
				MIMEType: header.MIMEType{Type: "text", Subtype: "*"},
				Params:   header.Values{"f i e l d": {"123"}},
			},
			false,
		},
		{
			"invalid 2",
			header.MIMERange{
				MIMEType: header.MIMEType{Type: "gzip", Subtype: "*"},
				Params:   header.Values{"field": {" a b c "}},
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.rng.IsValid(); got != c.want {
				t.Errorf("rng.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestMIMERange_IsZero(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.MIMERange
		want bool
	}{
		{"zero", header.MIMERange{}, true},
		{"not zero", header.MIMERange{MIMEType: header.MIMEType{Type: "text", Subtype: "*"}}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.rng.IsZero(); got != c.want {
				t.Errorf("rng.IsZero() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestMIMERange_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.MIMERange
		want any
	}{
		{"zero", header.MIMERange{}, header.MIMERange{}},
		{
			"full",
			header.MIMERange{
				MIMEType: header.MIMEType{
					Type:    "application",
					Subtype: "*",
					Params:  header.Values{"charset": {"utf-8"}},
				},
				Params: header.Values{"q": {"0.7"}},
			},
			header.MIMERange{
				MIMEType: header.MIMEType{
					Type:    "application",
					Subtype: "*",
					Params:  header.Values{"charset": {"utf-8"}},
				},
				Params: header.Values{"q": {"0.7"}},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.rng.Clone()
			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Errorf("rng.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestMIMERange_MarshalText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.MIMERange
		want string
	}{
		{"zero", header.MIMERange{}, "/"},
		{
			"simple",
			header.MIMERange{
				MIMEType: header.MIMEType{Type: "text", Subtype: "plain"},
			},
			"text/plain",
		},
		{
			"with mime and range params",
			header.MIMERange{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "html",
					Params:  make(header.Values).Set("charset", "utf-8"),
				},
				Params: make(header.Values).
					Set("q", "0.7").
					Set("level", "1"),
			},
			"text/html;charset=utf-8;q=0.7;level=1",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := c.rng.MarshalText()
			if err != nil {
				t.Fatalf("rng.MarshalText() error = %v, want nil", err)
			}

			if string(got) != c.want {
				t.Fatalf("rng.MarshalText() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMIMERange_UnmarshalText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    header.MIMERange
		wantErr bool
	}{
		{"empty", "", header.MIMERange{}, false},
		{"slash", "/", header.MIMERange{}, false},
		{"invalid", "text", header.MIMERange{}, true},
		{
			"simple",
			"text/plain;q=0.7",
			header.MIMERange{
				MIMEType: header.MIMEType{Type: "text", Subtype: "plain"},
				Params:   make(header.Values).Set("q", "0.7"),
			},
			false,
		},
		{
			"with mime and range params",
			"text/html;charset=utf-8;level=1;q=0.5",
			header.MIMERange{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "html",
					Params:  make(header.Values).Set("charset", "utf-8"),
				},
				Params: make(header.Values).
					Set("level", "1").
					Set("q", "0.5"),
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.MIMERange
			if err := got.UnmarshalText([]byte(c.data)); err != nil {
				if !c.wantErr {
					t.Fatalf("rng.UnmarshalText(data) error = %v, want nil", err)
				}
				return
			}

			if c.wantErr {
				t.Fatal("rng.UnmarshalText(data) error = nil, want error")
			}

			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestMIMERange_RoundTripText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.MIMERange
	}{
		{"empty", header.MIMERange{}},
		{"simple", header.MIMERange{MIMEType: header.MIMEType{Type: "text", Subtype: "plain"}}},
		{
			"with mime and range params",
			header.MIMERange{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "html",
					Params:  make(header.Values).Set("charset", "utf-8"),
				},
				Params: make(header.Values).
					Set("q", "0.7").
					Set("level", "1"),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := c.rng.MarshalText()
			if err != nil {
				t.Fatalf("rng.MarshalText() error = %v, want nil", err)
			}

			var got header.MIMERange
			if err := got.UnmarshalText(data); err != nil {
				t.Fatalf("rng.UnmarshalText(data) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.rng); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.rng, diff)
			}
		})
	}
}
