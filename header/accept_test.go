package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
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
		name    string
		hdr     header.Accept
		want    map[string]any
		wantErr error
	}{
		{
			name: "nil",
			hdr:  header.Accept(nil),
			want: map[string]any{
				"name":  "Accept",
				"value": "",
			},
		},
		{
			name: "empty",
			hdr:  header.Accept{},
			want: map[string]any{
				"name":  "Accept",
				"value": "",
			},
		},
		{
			name: "single",
			hdr: header.Accept{
				{
					MIMEType: header.MIMEType{Type: "text", Subtype: "plain"},
					Params:   make(header.Values).Set("q", "0.8"),
				},
			},
			want: map[string]any{
				"name":  "Accept",
				"value": "text/plain;q=0.8",
			},
		},
		{
			name: "multiple",
			hdr: header.Accept{
				{MIMEType: header.MIMEType{Type: "text", Subtype: "plain"}},
				{
					MIMEType: header.MIMEType{Type: "application", Subtype: "json"},
					Params:   make(header.Values).Set("q", "0.5"),
				},
			},
			want: map[string]any{
				"name":  "Accept",
				"value": "text/plain, application/json;q=0.5",
			},
		},
		{
			name: "with_mime_params",
			hdr: header.Accept{
				{
					MIMEType: header.MIMEType{
						Type:    "text",
						Subtype: "html",
						Params:  make(header.Values).Set("charset", "utf-8"),
					},
				},
			},
			want: map[string]any{
				"name":  "Accept",
				"value": "text/html;charset=utf-8",
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

func TestAccept_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    header.Accept
		wantErr bool
	}{
		{
			name:    "null",
			data:    "null",
			want:    header.Accept(nil),
			wantErr: false,
		},
		{
			name: "empty",
			data: `{"name":"Accept","value":""}`,
			want: header.Accept{},
		},
		{
			name: "single",
			data: `{"name":"Accept","value":"text/plain;q=0.8"}`,
			want: header.Accept{
				{
					MIMEType: header.MIMEType{Type: "text", Subtype: "plain"},
					Params:   make(header.Values).Set("q", "0.8"),
				},
			},
		},
		{
			name: "multiple",
			data: `{"name":"Accept","value":"text/plain, application/json;q=0.5"}`,
			want: header.Accept{
				{MIMEType: header.MIMEType{Type: "text", Subtype: "plain"}},
				{
					MIMEType: header.MIMEType{Type: "application", Subtype: "json"},
					Params:   make(header.Values).Set("q", "0.5"),
				},
			},
		},
		{
			name: "with_mime_params",
			data: `{"name":"Accept","value":"text/html;charset=utf-8"}`,
			want: header.Accept{
				{
					MIMEType: header.MIMEType{
						Type:    "text",
						Subtype: "html",
						Params:  make(header.Values).Set("charset", "utf-8"),
					},
				},
			},
		},
		{
			name:    "invalid_json",
			data:    `{"name":"Accept","value":`,
			want:    header.Accept(nil),
			wantErr: true,
		},
		{
			name:    "wrong_header",
			data:    `{"name":"From","value":"<sip:alice@example.com>"}`,
			want:    header.Accept(nil),
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.Accept
			err := json.Unmarshal([]byte(c.data), &got)
			if (err != nil) != c.wantErr {
				t.Errorf("json.Unmarshal(data, got) error = %v, want %v", err, c.wantErr)
				return
			}
			if err != nil {
				return
			}

			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Errorf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
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
		{
			name: "empty",
			hdr:  header.Accept{},
		},
		{
			name: "single",
			hdr: header.Accept{
				{
					MIMEType: header.MIMEType{Type: "text", Subtype: "plain"},
					Params:   make(header.Values).Set("q", "0.8"),
				},
			},
		},
		{
			name: "multiple",
			hdr: header.Accept{
				{MIMEType: header.MIMEType{Type: "text", Subtype: "plain"}},
				{
					MIMEType: header.MIMEType{Type: "application", Subtype: "json"},
					Params:   make(header.Values).Set("q", "0.5"),
				},
			},
		},
		{
			name: "with_mime_params",
			hdr: header.Accept{
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
				t.Errorf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
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
		name    string
		rng     header.MIMERange
		want    string
		wantErr error
	}{
		{name: "zero", rng: header.MIMERange{}, want: "/"},
		{
			name: "simple",
			rng: header.MIMERange{
				MIMEType: header.MIMEType{Type: "text", Subtype: "plain"},
			},
			want: "text/plain",
		},
		{
			name: "with_mime_and_range_params",
			rng: header.MIMERange{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "html",
					Params:  make(header.Values).Set("charset", "utf-8"),
				},
				Params: make(header.Values).
					Set("q", "0.7").
					Set("level", "1"),
			},
			want: "text/html;charset=utf-8;q=0.7;level=1",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := c.rng.MarshalText()
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("rng.MarshalText() error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
				return
			}
			if string(got) != c.want {
				t.Errorf("rng.MarshalText() = %q, want %q", got, c.want)
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
		{name: "empty", data: "", want: header.MIMERange{}},
		{
			name: "simple",
			data: "text/plain;q=0.7",
			want: header.MIMERange{
				MIMEType: header.MIMEType{Type: "text", Subtype: "plain"},
				Params:   make(header.Values).Set("q", "0.7"),
			},
		},
		{
			name: "with_mime_and_range_params",
			data: "text/html;charset=utf-8;level=1;q=0.5",
			want: header.MIMERange{
				MIMEType: header.MIMEType{
					Type:    "text",
					Subtype: "html",
					Params:  make(header.Values).Set("charset", "utf-8"),
				},
				Params: make(header.Values).
					Set("level", "1").
					Set("q", "0.5"),
			},
		},
		{
			name:    "invalid",
			data:    "text",
			want:    header.MIMERange{},
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.MIMERange
			err := got.UnmarshalText([]byte(c.data))
			if (err != nil) != c.wantErr {
				t.Errorf("rng.UnmarshalText(data) error = %v, want %v", err, c.wantErr)
				return
			}
			if err != nil {
				return
			}

			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Errorf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
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
		{
			name: "simple",
			rng:  header.MIMERange{MIMEType: header.MIMEType{Type: "text", Subtype: "plain"}},
		},
		{
			name: "with_mime_and_range_params",
			rng: header.MIMERange{
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
				t.Errorf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.rng, diff)
			}
		})
	}
}
