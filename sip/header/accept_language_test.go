package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestAcceptLanguage_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.AcceptLanguage
		want string
	}{
		{"nil", header.AcceptLanguage(nil), ""},
		{"empty", header.AcceptLanguage{}, "Accept-Language: "},
		{"empty elem", header.AcceptLanguage{{}}, "Accept-Language: "},
		{"any", header.AcceptLanguage{{Lang: "*"}}, "Accept-Language: *"},
		{
			"multi",
			header.AcceptLanguage{
				{
					Lang: "en",
					Params: make(header.Values).
						Set("a", "123").
						Set("q", "0.9"),
				},
				{Lang: "de"},
			},
			"Accept-Language: en;q=0.9;a=123, de",
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

func TestAcceptLanguage_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.AcceptLanguage
		wantRes string
		wantErr error
	}{
		{"nil", header.AcceptLanguage(nil), "", nil},
		{"empty", header.AcceptLanguage{}, "Accept-Language: ", nil},
		{
			"full",
			header.AcceptLanguage{{Lang: "en"}, {Lang: "fr"}},
			"Accept-Language: en, fr",
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

func TestAcceptLanguage_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.AcceptLanguage
		want string
	}{
		{"nil", header.AcceptLanguage(nil), ""},
		{"empty", header.AcceptLanguage{}, ""},
		{"full", header.AcceptLanguage{{Lang: "en"}, {Lang: "fr"}}, "en, fr"},
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

func TestAcceptLanguage_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.AcceptLanguage
		val  any
		want bool
	}{
		{"nil ptr to nil", header.AcceptLanguage(nil), nil, false},
		{"nil ptr to nil ptr", header.AcceptLanguage(nil), header.AcceptLanguage(nil), true},
		{"zero ptr to nil ptr", header.AcceptLanguage{}, header.AcceptLanguage(nil), true},
		{"zero to zero", header.AcceptLanguage{}, header.AcceptLanguage{}, true},
		{"zero to zero ptr", header.AcceptLanguage{}, &header.AcceptLanguage{}, true},
		{"zero to nil ptr", header.AcceptLanguage{}, (*header.AcceptLanguage)(nil), false},
		{"not match 1", header.AcceptLanguage{}, header.AcceptLanguage{{Lang: "ru"}}, false},
		{"not match 2", header.AcceptLanguage{{Lang: "ru"}}, header.AcceptLanguage{{Lang: "en"}}, false},
		{
			"not match 3",
			header.AcceptLanguage{{Lang: "ru"}, {Lang: "en"}},
			header.AcceptLanguage{{Lang: "en"}, {Lang: "ru"}},
			false,
		},
		{
			"not match 4",
			header.AcceptLanguage{{Lang: "ru", Params: header.Values{"foo": {"bar"}}}},
			header.AcceptLanguage{{Lang: "ru", Params: header.Values{"foo": {"qwe"}}}},
			false,
		},
		{
			"not match 5",
			header.AcceptLanguage{{Lang: "ru", Params: header.Values{"foo": {`"bar"`}}}},
			header.AcceptLanguage{{Lang: "ru", Params: header.Values{"foo": {`"BAR"`}}}},
			false,
		},
		{"match 1", header.AcceptLanguage{{Lang: "ru"}}, header.AcceptLanguage{{Lang: "RU"}}, true},
		{
			"match 2",
			header.AcceptLanguage{{Lang: "ru", Params: header.Values{"foo": {"bar"}}}},
			header.AcceptLanguage{{Lang: "ru"}},
			true,
		},
		{
			"match 3",
			header.AcceptLanguage{
				{Lang: "ru", Params: header.Values{"foo": {"bar"}}},
				{Lang: "en", Params: header.Values{"q": {"0.9"}}},
			},
			header.AcceptLanguage{
				{Lang: "ru", Params: header.Values{"foo": {"BAR"}}},
				{Lang: "en", Params: header.Values{"q": {"0.9"}}},
			},
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

func TestAcceptLanguage_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.AcceptLanguage
		want bool
	}{
		{"nil", header.AcceptLanguage(nil), false},
		{"empty", header.AcceptLanguage{}, true},
		{
			"valid",
			header.AcceptLanguage{
				{
					Lang: "ru",
					Params: header.Values{
						"q":   {"0.7"},
						"foo": {"a_b-c"},
						"bar": {`"A B C"`},
					},
				},
				{
					Lang:   "*",
					Params: header.Values{"q": {"0.5"}},
				},
			},
			true,
		},
		{"invalid 1", header.AcceptLanguage{{Lang: "en", Params: header.Values{"f i e l d": {"123"}}}}, false},
		{"invalid 2", header.AcceptLanguage{{Lang: "ru", Params: header.Values{"field": {" a b c "}}}}, false},
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

func TestAcceptLanguage_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.AcceptLanguage
	}{
		{"nil", header.AcceptLanguage(nil)},
		{"empty", header.AcceptLanguage{}},
		{
			"full",
			header.AcceptLanguage{
				{Lang: "ru", Params: header.Values{"q": {"0.7"}}},
				{Lang: "en", Params: header.Values{"q": {"0.5"}}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.hdr.Clone()
			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}

func TestAcceptLanguage_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.AcceptLanguage
		want string
	}{
		{"nil", nil, "null"},
		{"empty", header.AcceptLanguage{}, `{"name":"Accept-Language","value":""}`},
		{
			"single",
			header.AcceptLanguage{
				{Lang: "en", Params: make(header.Values).Set("q", "0.8")},
			},
			`{"name":"Accept-Language","value":"en;q=0.8"}`,
		},
		{
			"multiple",
			header.AcceptLanguage{
				{Lang: "en"},
				{Lang: "de", Params: make(header.Values).Set("q", "0.5")},
			},
			`{"name":"Accept-Language","value":"en, de;q=0.5"}`,
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

func TestAcceptLanguage_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    header.AcceptLanguage
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"en"}`, nil, true},
		{"empty value", `{"name":"Accept-Language","value":""}`, header.AcceptLanguage{}, false},
		{"invalid json", `{"name":"Accept-Language","value":`, nil, true},
		{"wrong header", `{"name":"From","value":"<sip:alice@example.com>"}`, nil, true},
		{
			name: "single",
			data: `{"name":"Accept-Language","value":"en;q=0.8"}`,
			want: header.AcceptLanguage{
				{Lang: "en", Params: make(header.Values).Set("q", "0.8")},
			},
		},
		{
			name: "multiple",
			data: `{"name":"Accept-Language","value":"en, de;q=0.5"}`,
			want: header.AcceptLanguage{
				{Lang: "en"},
				{Lang: "de", Params: make(header.Values).Set("q", "0.5")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got header.AcceptLanguage
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

func TestAcceptLanguage_RoundTripJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.AcceptLanguage
	}{
		{"nil", nil},
		{"empty", header.AcceptLanguage{}},
		{
			"single",
			header.AcceptLanguage{
				{Lang: "en", Params: make(header.Values).Set("q", "0.8")},
			},
		},
		{
			"multiple",
			header.AcceptLanguage{
				{Lang: "en"},
				{Lang: "de", Params: make(header.Values).Set("q", "0.5")},
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

			var got header.AcceptLanguage
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Errorf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}

func TestLanguageRange_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.LanguageRange
		want string
	}{
		{"zero", header.LanguageRange{}, ""},
		{"full", header.LanguageRange{Lang: "ru", Params: header.Values{"q": {"0.7"}}}, "ru;q=0.7"},
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

func TestLanguageRange_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.LanguageRange
		val  any
		want bool
	}{
		{"zero to nil", header.LanguageRange{}, nil, false},
		{"zero to zero", header.LanguageRange{}, header.LanguageRange{}, true},
		{"zero to zero ptr", header.LanguageRange{}, &header.LanguageRange{}, true},
		{"zero to nil ptr", header.LanguageRange{}, (*header.LanguageRange)(nil), false},
		{"not match 1", header.LanguageRange{}, header.LanguageRange{Lang: "ru"}, false},
		{"not match 2", header.LanguageRange{Lang: "*"}, header.LanguageRange{Lang: "ru"}, false},
		{"not match 3", header.LanguageRange{Lang: "ru"}, header.LanguageRange{Lang: "en"}, false},
		{
			"not match 4",
			header.LanguageRange{Lang: "ru", Params: header.Values{"foo": {"bar"}}},
			header.LanguageRange{Lang: "ru", Params: header.Values{"foo": {"qwe"}}},
			false,
		},
		{
			"not match 5",
			header.LanguageRange{Lang: "ru", Params: header.Values{"foo": {`"bar"`}}},
			header.LanguageRange{Lang: "ru", Params: header.Values{"foo": {`"BAR"`}}},
			false,
		},
		{"match 1", header.LanguageRange{Lang: "ru"}, header.LanguageRange{Lang: "RU"}, true},
		{
			"match 2",
			header.LanguageRange{Lang: "ru", Params: header.Values{"foo": {"bar"}}},
			header.LanguageRange{Lang: "ru"},
			true,
		},
		{
			"match 3",
			header.LanguageRange{Lang: "ru", Params: header.Values{"foo": {"bar"}}},
			header.LanguageRange{Lang: "ru"},
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

func TestLanguageRange_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.LanguageRange
		want bool
	}{
		{"zero", header.LanguageRange{}, false},
		{"valid", header.LanguageRange{Lang: "ru", Params: header.Values{"q": {"0.7"}}}, true},
		{"invalid 1", header.LanguageRange{Lang: "ru", Params: header.Values{"f i e l d": {"123"}}}, false},
		{"invalid 2", header.LanguageRange{Lang: "ru", Params: header.Values{"field": {" a b c "}}}, false},
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

func TestLanguageRange_IsZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.LanguageRange
		want bool
	}{
		{"zero", header.LanguageRange{}, true},
		{"not zero", header.LanguageRange{Lang: "en"}, false},
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

func TestLanguageRange_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.LanguageRange
	}{
		{"zero", header.LanguageRange{}},
		{"full", header.LanguageRange{Lang: "ru", Params: header.Values{"q": {"0.7"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.rng.Clone()
			if diff := cmp.Diff(got, tt.rng); diff != "" {
				t.Errorf("rng.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.rng, diff)
			}
		})
	}
}

func TestLanguageRange_MarshalText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.LanguageRange
		want string
	}{
		{"zero", header.LanguageRange{}, ""},
		{"simple", header.LanguageRange{Lang: "en"}, "en"},
		{
			"with params",
			header.LanguageRange{
				Lang:   "en",
				Params: make(header.Values).Set("q", "0.7").Set("dialect", "us"),
			},
			"en;q=0.7;dialect=us",
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

func TestLanguageRange_UnmarshalText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    header.LanguageRange
		wantErr bool
	}{
		{"empty", "", header.LanguageRange{}, false},
		{"simple", "en", header.LanguageRange{Lang: "en"}, false},
		{
			"with params",
			"en;q=0.7;dialect=us",
			header.LanguageRange{
				Lang:   "en",
				Params: make(header.Values).Set("q", "0.7").Set("dialect", "us"),
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got header.LanguageRange
			if err := got.UnmarshalText([]byte(tt.data)); err != nil {
				if !tt.wantErr {
					t.Errorf("rng.UnmarshalText(data) error = %v, want nil", err)
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

func TestLanguageRange_RoundTripText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.LanguageRange
	}{
		{"zero", header.LanguageRange{}},
		{"simple", header.LanguageRange{Lang: "en"}},
		{
			"with params",
			header.LanguageRange{
				Lang:   "en",
				Params: make(header.Values).Set("q", "0.7").Set("dialect", "us"),
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

			var got header.LanguageRange
			if err := got.UnmarshalText(data); err != nil {
				t.Fatalf("rng.UnmarshalText(data) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, tt.rng); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.rng, diff)
			}
		})
	}
}
