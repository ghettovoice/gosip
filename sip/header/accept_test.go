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

	tests := []struct {
		name string
		hdr  header.Accept
		want string
	}{
		{"nil", header.Accept(nil), ""},
		{"empty", header.Accept{}, "Accept: "},
		{"empty elem", header.Accept{{}}, "Accept: /"},
		{"any", header.Accept{{Type: "*", Subtype: "*"}}, "Accept: */*"},
		{
			"single elem",
			header.Accept{{Type: "text", Subtype: "plain"}},
			"Accept: text/plain",
		},
		{
			"multiple elems 1",
			header.Accept{
				{Type: "text", Subtype: "plain"},
				{Type: "text", Subtype: "csv"},
			},
			"Accept: text/plain, text/csv",
		},
		{
			"multiple elems 2",
			header.Accept{
				{
					Type: "text", Subtype: "plain",
					Params: make(header.Values).Set("a", "123").Set("q", "0.9"),
				},
				{Type: "text", Subtype: "csv"},
			},
			"Accept: text/plain;q=0.9;a=123, text/csv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Render(); got != tt.want {
				t.Errorf("hdr.Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAccept_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
				{Type: "text", Subtype: "*"},
				{Type: "application", Subtype: "*"},
			},
			"Accept: text/*, application/*",
			nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder

			_, err := tt.hdr.RenderTo(&sb)
			if diff := cmp.Diff(err, tt.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo(&sb) error = %v, want %v\ndiff (-got +want):\n%v", err, tt.wantErr, diff)
			}

			if got := sb.String(); got != tt.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, tt.wantRes)
			}
		})
	}
}

func TestAccept_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Accept
		want string
	}{
		{"nil", header.Accept(nil), ""},
		{"empty", header.Accept{}, ""},
		{
			"full",
			header.Accept{
				{Type: "text", Subtype: "*"},
				{Type: "application", Subtype: "*"},
			},
			"text/*, application/*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.String(); got != tt.want {
				t.Errorf("hdr.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAccept_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
		{"not match 1", header.Accept{}, header.Accept{{Type: "*", Subtype: "*"}}, false},
		{
			"not match 2",
			header.Accept{{Type: "text", Subtype: "*"}},
			header.Accept{{Type: "text"}},
			false,
		},
		{
			"not match 3",
			header.Accept{{Type: "text", Subtype: "*"}},
			header.Accept{{Type: "html", Subtype: "*"}},
			false,
		},
		{
			"not match 4",
			header.Accept{{Type: "text", Subtype: "plain"}},
			header.Accept{{Type: "text", Subtype: "*"}},
			false,
		},
		{
			"not match 5",
			header.Accept{{
				Type:    "text",
				Subtype: "*",
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Equal(tt.val); got != tt.want {
				t.Errorf("hdr.Equal(val) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccept_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
				Type: "*", Subtype: "*",
				Params: make(header.Values).Set(" f o o ", "bar"),
			}},
			false,
		},
		{
			"invalid 3",
			header.Accept{{
				Type: "*", Subtype: "*",
				Params: make(header.Values).Set("foo", " b a r "),
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.IsValid(); got != tt.want {
				t.Errorf("hdr.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccept_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Accept
		want any
	}{
		{"nil", header.Accept(nil), header.Accept(nil)},
		{"empty", header.Accept{}, header.Accept{}},
		{
			"full",
			header.Accept{
				{Type: "text", Subtype: "plain", Params: header.Values{"q": {"0.7"}}},
				{
					MIMEType: header.MIMEType{Type: "text", Subtype: "csv", Params: header.Values{"charset": {"utf-8"}}},
					Params:   header.Values{"q": {"0.5"}},
				},
			},
			header.Accept{
				{Type: "text", Subtype: "plain", Params: header.Values{"q": {"0.7"}}},
				{
					MIMEType: header.MIMEType{Type: "text", Subtype: "csv", Params: header.Values{"charset": {"utf-8"}}},
					Params:   header.Values{"q": {"0.5"}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.hdr.Clone()
			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.want, diff)
			}
		})
	}
}

func TestAccept_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
					Type: "text", Subtype: "plain",
					Params: make(header.Values).Set("q", "0.8"),
				},
			},
			`{"name":"Accept","value":"text/plain;q=0.8"}`,
		},
		{
			"multiple",
			header.Accept{
				{Type: "text", Subtype: "plain"},
				{
					Type: "application", Subtype: "json",
					Params: make(header.Values).Set("q", "0.5"),
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			if got := string(got); got != tt.want {
				t.Fatalf("json.Marshal(hdr) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAccept_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
					Type: "text", Subtype: "plain",
					Params: make(header.Values).Set("q", "0.8"),
				},
			},
			false,
		},
		{
			"multiple",
			`{"name":"Accept","value":"text/plain, application/json;q=0.5"}`,
			header.Accept{
				{Type: "text", Subtype: "plain"},
				{
					Type: "application", Subtype: "json",
					Params: make(header.Values).Set("q", "0.5"),
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got header.Accept
			if err := json.Unmarshal([]byte(tt.data), &got); err != nil {
				if !tt.wantErr {
					t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
				}
				return
			}

			if tt.wantErr {
				t.Fatal("json.Unmarshal(data, got) error = nil, want error")
			}

			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.want, diff)
			}
		})
	}
}

func TestAccept_RoundTripJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Accept
	}{
		{"empty", header.Accept{}},
		{
			"single",
			header.Accept{
				{
					Type: "text", Subtype: "plain",
					Params: make(header.Values).Set("q", "0.8"),
				},
			},
		},
		{
			"multiple",
			header.Accept{
				{Type: "text", Subtype: "plain"},
				{
					Type: "application", Subtype: "json",
					Params: make(header.Values).Set("q", "0.5"),
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			var got header.Accept
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}

func TestMIMERange_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.rng.String(); got != tt.want {
				t.Errorf("rng.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMIMERange_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.MIMERange
		val  any
		want bool
	}{
		{"zero to nil", header.MIMERange{}, nil, false},
		{"zero to zero", header.MIMERange{}, header.MIMERange{}, true},
		{"zero to zero ptr", header.MIMERange{}, &header.MIMERange{}, true},
		{"zero to nil ptr", header.MIMERange{}, (*header.MIMERange)(nil), false},
		{"not match 1", header.MIMERange{}, header.MIMERange{Type: "*", Subtype: "*"}, false},
		{
			"not match 2",
			header.MIMERange{Type: "text", Subtype: "*"},
			header.MIMERange{Type: "text", Subtype: "csv"},
			false,
		},
		{
			"not match 3",
			header.MIMERange{Type: "text", Subtype: "*", Params: header.Values{"q": {"0.7"}}},
			header.MIMERange{Type: "text", Subtype: "*", Params: header.Values{"q": {"0.5"}}},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.rng.Equal(tt.val); got != tt.want {
				t.Errorf("rng.Equal(val) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMIMERange_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.MIMERange
		want bool
	}{
		{"zero", header.MIMERange{}, false},
		{
			"valid",
			header.MIMERange{
				Type: "*", Subtype: "*",
				Params: header.Values{"q": {"0.7"}},
			},
			true,
		},
		{
			"invalid 1",
			header.MIMERange{
				Type: "text", Subtype: "*",
				Params: header.Values{"f i e l d": {"123"}},
			},
			false,
		},
		{
			"invalid 2",
			header.MIMERange{
				Type: "gzip", Subtype: "*",
				Params: header.Values{"field": {" a b c "}},
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.rng.IsValid(); got != tt.want {
				t.Errorf("rng.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMIMERange_IsZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.MIMERange
		want bool
	}{
		{"zero", header.MIMERange{}, true},
		{"not zero", header.MIMERange{Type: "text", Subtype: "*"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.rng.IsZero(); got != tt.want {
				t.Errorf("rng.IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMIMERange_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.rng.Clone()
			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Errorf("rng.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.want, diff)
			}
		})
	}
}

func TestMIMERange_MarshalText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.MIMERange
		want string
	}{
		{"zero", header.MIMERange{}, "/"},
		{
			"simple",
			header.MIMERange{
				Type: "text", Subtype: "plain",
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.rng.MarshalText()
			if err != nil {
				t.Fatalf("rng.MarshalText() error = %v, want nil", err)
			}

			if string(got) != tt.want {
				t.Fatalf("rng.MarshalText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMIMERange_UnmarshalText(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
				Type: "text", Subtype: "plain",
				Params: make(header.Values).Set("q", "0.7"),
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got header.MIMERange
			if err := got.UnmarshalText([]byte(tt.data)); err != nil {
				if !tt.wantErr {
					t.Fatalf("rng.UnmarshalText(data) error = %v, want nil", err)
				}
				return
			}

			if tt.wantErr {
				t.Fatal("rng.UnmarshalText(data) error = nil, want error")
			}

			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.want, diff)
			}
		})
	}
}

func TestMIMERange_RoundTripText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.MIMERange
	}{
		{"empty", header.MIMERange{}},
		{"simple", header.MIMERange{Type: "text", Subtype: "plain"}},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := tt.rng.MarshalText()
			if err != nil {
				t.Fatalf("rng.MarshalText() error = %v, want nil", err)
			}

			var got header.MIMERange
			if err := got.UnmarshalText(data); err != nil {
				t.Fatalf("rng.UnmarshalText(data) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, tt.rng); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.rng, diff)
			}
		})
	}
}
