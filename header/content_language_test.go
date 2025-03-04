package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestContentLanguage_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLanguage
		want string
	}{
		{"nil", header.ContentLanguage(nil), ""},
		{"empty", header.ContentLanguage{}, "Content-Language: "},
		{"empty elem", header.ContentLanguage{""}, "Content-Language: "},
		{"full", header.ContentLanguage{"en", "ru-RU"}, "Content-Language: en, ru-RU"},
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

func TestContentLanguage_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.ContentLanguage
		wantRes string
		wantErr error
	}{
		{"nil", header.ContentLanguage(nil), "", nil},
		{"empty", header.ContentLanguage{}, "Content-Language: ", nil},
		{"full", header.ContentLanguage{"en", "ru-RU"}, "Content-Language: en, ru-RU", nil},
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

func TestContentLanguage_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLanguage
		want string
	}{
		{"nil", header.ContentLanguage(nil), ""},
		{"empty", header.ContentLanguage{}, ""},
		{"full", header.ContentLanguage{"en", "ru-RU"}, "en, ru-RU"},
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

func TestContentLanguage_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLanguage
		val  any
		want bool
	}{
		{"nil ptr to nil", header.ContentLanguage(nil), nil, false},
		{"nil ptr to nil ptr", header.ContentLanguage(nil), header.ContentLanguage(nil), true},
		{"zero ptr to nil ptr", header.ContentLanguage{}, header.ContentLanguage(nil), true},
		{"zero to zero", header.ContentLanguage{}, header.ContentLanguage{}, true},
		{"zero to zero ptr", header.ContentLanguage{}, &header.ContentLanguage{}, true},
		{"zero to nil ptr", header.ContentLanguage{}, (*header.ContentLanguage)(nil), false},
		{"not match 1", header.ContentLanguage{"en"}, header.ContentLanguage{}, false},
		{"not match 2", header.ContentLanguage{"en", "ru-RU"}, header.ContentLanguage{"ru-RU", "en"}, false},
		{"match", header.ContentLanguage{"en", "ru-RU"}, header.ContentLanguage{"EN", "ru-RU"}, true},
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

func TestContentLanguage_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLanguage
		want bool
	}{
		{"nil", header.ContentLanguage(nil), false},
		{"empty", header.ContentLanguage{}, false},
		{"valid", header.ContentLanguage{"en", "ru-RU"}, true},
		{"invalid", header.ContentLanguage{"en", "ru-RU", " "}, false},
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

func TestContentLanguage_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLanguage
	}{
		{"nil", nil},
		{"empty", header.ContentLanguage{}},
		{"full", header.ContentLanguage{"en", "ru-RU"}},
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
