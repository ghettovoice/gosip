package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestAcceptLanguage_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Render(nil); got != c.want {
				t.Errorf("hdr.Render(nil) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAcceptLanguage_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

func TestAcceptLanguage_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.AcceptLanguage
		want string
	}{
		{"nil", header.AcceptLanguage(nil), ""},
		{"empty", header.AcceptLanguage{}, ""},
		{"full", header.AcceptLanguage{{Lang: "en"}, {Lang: "fr"}}, "en, fr"},
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

func TestAcceptLanguage_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Equal(c.val); got != c.want {
				t.Errorf("hdr.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAcceptLanguage_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.IsValid(); got != c.want {
				t.Errorf("hdr.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAcceptLanguage_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

func TestLanguageRange_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.LanguageRange
		want string
	}{
		{"zero", header.LanguageRange{}, ""},
		{"full", header.LanguageRange{Lang: "ru", Params: header.Values{"q": {"0.7"}}}, "ru;q=0.7"},
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

func TestLanguageRange_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.rng.Equal(c.val); got != c.want {
				t.Errorf("rng.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestLanguageRange_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.LanguageRange
		want bool
	}{
		{"zero", header.LanguageRange{}, false},
		{"valid", header.LanguageRange{Lang: "ru", Params: header.Values{"q": {"0.7"}}}, true},
		{"invalid 1", header.LanguageRange{Lang: "ru", Params: header.Values{"f i e l d": {"123"}}}, false},
		{"invalid 2", header.LanguageRange{Lang: "ru", Params: header.Values{"field": {" a b c "}}}, false},
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

func TestLanguageRange_IsZero(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.LanguageRange
		want bool
	}{
		{"zero", header.LanguageRange{}, true},
		{"not zero", header.LanguageRange{Lang: "en"}, false},
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

func TestLanguageRange_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.LanguageRange
	}{
		{"zero", header.LanguageRange{}},
		{"full", header.LanguageRange{Lang: "ru", Params: header.Values{"q": {"0.7"}}}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.rng.Clone()
			if diff := cmp.Diff(got, c.rng); diff != "" {
				t.Errorf("rng.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.rng, diff)
			}
		})
	}
}
