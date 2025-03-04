package header_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestAcceptEncoding_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.AcceptEncoding
		want string
	}{
		{"nil", header.AcceptEncoding(nil), ""},
		{"empty", header.AcceptEncoding{}, "Accept-Encoding: "},
		{"empty elem", header.AcceptEncoding{{}}, "Accept-Encoding: "},
		{"any", header.AcceptEncoding{{Encoding: "*"}}, "Accept-Encoding: *"},
		{"single elem", header.AcceptEncoding{{Encoding: "gzip"}}, "Accept-Encoding: gzip"},
		{
			"multiple elems 1",
			header.AcceptEncoding{{Encoding: "gzip"}, {Encoding: "compress"}},
			"Accept-Encoding: gzip, compress",
		},
		{
			"multiple elems 2",
			header.AcceptEncoding{
				{
					Encoding: "gzip",
					Params: make(header.Values).
						Set("a", "123").
						Set("q", "0.9"),
				},
				{Encoding: "deflate"},
			},
			"Accept-Encoding: gzip;q=0.9;a=123, deflate",
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

func TestAcceptEncoding_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.AcceptEncoding
		wantRes string
		wantErr error
	}{
		{"nil", header.AcceptEncoding(nil), "", nil},
		{"empty", header.AcceptEncoding{}, "Accept-Encoding: ", nil},
		{
			"full",
			header.AcceptEncoding{{Encoding: "gzip"}, {Encoding: "compress"}},
			"Accept-Encoding: gzip, compress",
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

func TestAcceptEncoding_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.AcceptEncoding
		want string
	}{
		{"nil", header.AcceptEncoding(nil), ""},
		{"empty", header.AcceptEncoding{}, ""},
		{"full", header.AcceptEncoding{{Encoding: "gzip"}, {Encoding: "compress"}}, "gzip, compress"},
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

func TestAcceptEncoding_Format(t *testing.T) {
	t.Parallel()

	hdr := header.AcceptEncoding{{Encoding: "gzip"}, {Encoding: "compress"}}
	if got, want := fmt.Sprintf("%s", hdr), "gzip, compress"; got != want {
		t.Errorf("fmt.Sprintf(\"%%s\", hdr) = %q, want %q", got, want)
	}
	if got, want := fmt.Sprintf("%q", hdr), "\"gzip, compress\""; got != want {
		t.Errorf("fmt.Sprintf(\"%%q\", hdr) = %q, want %q", got, want)
	}
	if got, want := fmt.Sprintf("%+s", hdr), "Accept-Encoding: gzip, compress"; got != want {
		t.Errorf("fmt.Sprintf(\"%%+s\", hdr) = %q, want %q", got, want)
	}
	if got, want := fmt.Sprintf("%+q", hdr), "\"Accept-Encoding: gzip, compress\""; got != want {
		t.Errorf("fmt.Sprintf(\"%%+q\", hdr) = %q, want %q", got, want)
	}
	if got, want := fmt.Sprintf("%v", hdr), "[gzip compress]"; got != want {
		t.Errorf("fmt.Sprintf(\"%%v\", hdr) = %q, want %q", got, want)
	}
	if got, want := fmt.Sprintf("%+v", hdr), "[{Encoding:gzip Params:map[]} {Encoding:compress Params:map[]}]"; got != want {
		t.Errorf("fmt.Sprintf(\"%%+v\", hdr) = %q, want %q", got, want)
	}
}

func TestAcceptEncoding_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.AcceptEncoding
		val  any
		want bool
	}{
		{"nil ptr to nil", header.AcceptEncoding(nil), nil, false},
		{"nil ptr to nil ptr", header.AcceptEncoding(nil), header.AcceptEncoding(nil), true},
		{"zero ptr to nil ptr", header.AcceptEncoding{}, header.AcceptEncoding(nil), true},
		{"zero to zero", header.AcceptEncoding{}, header.AcceptEncoding{}, true},
		{"zero to zero ptr", header.AcceptEncoding{}, &header.AcceptEncoding{}, true},
		{"zero to nil ptr", header.AcceptEncoding{}, (*header.AcceptEncoding)(nil), false},
		{"not match 1", header.AcceptEncoding{}, header.AcceptEncoding{{Encoding: "gzip"}}, false},
		{"not match 2", header.AcceptEncoding{{Encoding: "gzip"}}, header.AcceptEncoding{{Encoding: "compress"}}, false},
		{
			"not match 3",
			header.AcceptEncoding{{Encoding: "gzip"}, {Encoding: "deflate"}},
			header.AcceptEncoding{{Encoding: "deflate"}, {Encoding: "gzip"}},
			false,
		},
		{
			"not match 4",
			header.AcceptEncoding{{Encoding: "gzip", Params: header.Values{"foo": {"bar"}}}},
			header.AcceptEncoding{{Encoding: "gzip", Params: header.Values{"foo": {"qwe"}}}},
			false,
		},
		{
			"not match 5",
			header.AcceptEncoding{{Encoding: "gzip", Params: header.Values{"foo": {`"bar"`}}}},
			header.AcceptEncoding{{Encoding: "gzip", Params: header.Values{"foo": {`"BAR"`}}}},
			false,
		},
		{"match 1", header.AcceptEncoding{{Encoding: "gzip"}}, header.AcceptEncoding{{Encoding: "GZIP"}}, true},
		{
			"match 2",
			header.AcceptEncoding{{Encoding: "gzip", Params: header.Values{"foo": {"bar"}}}},
			header.AcceptEncoding{{Encoding: "gzip"}},
			true,
		},
		{
			"match 3",
			header.AcceptEncoding{
				{Encoding: "gzip", Params: header.Values{"foo": {"bar"}}},
				{Encoding: "deflate", Params: header.Values{"q": {"0.9"}}},
			},
			header.AcceptEncoding{
				{Encoding: "gzip", Params: header.Values{"foo": {"BAR"}}},
				{Encoding: "deflate", Params: header.Values{"q": {"0.9"}}},
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Equal(c.val); got != c.want {
				t.Errorf("hdr.Equal() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAcceptEncoding_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.AcceptEncoding
		want bool
	}{
		{"nil", header.AcceptEncoding(nil), false},
		{"empty", header.AcceptEncoding{}, true},
		{
			"valid",
			header.AcceptEncoding{
				{
					Encoding: "gzip",
					Params: header.Values{
						"q":   {"0.7"},
						"foo": {"a_b-c"},
						"bar": {`"A B C"`},
					},
				},
				{
					Encoding: "*",
					Params:   header.Values{"q": {"0.5"}},
				},
			},
			true,
		},
		{"invalid 1", header.AcceptEncoding{{Encoding: "gzip", Params: header.Values{"f i e l d": {"123"}}}}, false},
		{"invalid 2", header.AcceptEncoding{{Encoding: "gzip", Params: header.Values{"field": {" a b c "}}}}, false},
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

func TestAcceptEncoding_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.AcceptEncoding
	}{
		{"nil", header.AcceptEncoding(nil)},
		{"empty", header.AcceptEncoding{}},
		{
			"full",
			header.AcceptEncoding{
				{Encoding: "gzip", Params: header.Values{"q": {"0.7"}}},
				{Encoding: "compress", Params: header.Values{"q": {"0.5"}}},
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

func TestEncodingRange_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.EncodingRange
		want string
	}{
		{"zero", header.EncodingRange{}, ""},
		{"full", header.EncodingRange{Encoding: "gzip", Params: header.Values{"q": {"0.7"}}}, "gzip;q=0.7"},
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

func TestEncodingRange_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.EncodingRange
		val  any
		want bool
	}{
		{"zero to nil", header.EncodingRange{}, nil, false},
		{"zero to zero", header.EncodingRange{}, header.EncodingRange{}, true},
		{"zero to zero ptr", header.EncodingRange{}, &header.EncodingRange{}, true},
		{"zero to nil ptr", header.EncodingRange{}, (*header.EncodingRange)(nil), false},
		{"not match 1", header.EncodingRange{}, header.EncodingRange{Encoding: "gzip"}, false},
		{"not match 2", header.EncodingRange{Encoding: "*"}, header.EncodingRange{Encoding: "gzip"}, false},
		{"not match 3", header.EncodingRange{Encoding: "gzip"}, header.EncodingRange{Encoding: "compress"}, false},
		{
			"not match 4",
			header.EncodingRange{Encoding: "gzip", Params: header.Values{"foo": {"bar"}}},
			header.EncodingRange{Encoding: "gzip", Params: header.Values{"foo": {"qwe"}}},
			false,
		},
		{
			"not match 5",
			header.EncodingRange{Encoding: "gzip", Params: header.Values{"foo": {`"bar"`}}},
			header.EncodingRange{Encoding: "gzip", Params: header.Values{"foo": {`"BAR"`}}},
			false,
		},
		{"match 1", header.EncodingRange{Encoding: "gzip"}, header.EncodingRange{Encoding: "GZIP"}, true},
		{
			"match 2",
			header.EncodingRange{Encoding: "gzip", Params: header.Values{"foo": {"bar"}}},
			header.EncodingRange{Encoding: "gzip"},
			true,
		},
		{
			"match 3",
			header.EncodingRange{Encoding: "gzip", Params: header.Values{"foo": {"bar"}}},
			header.EncodingRange{Encoding: "gzip", Params: header.Values{"foo": {"BAR"}}},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.rng.Equal(c.val); got != c.want {
				t.Errorf("rng.Equal() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestEncodingRange_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.EncodingRange
		want bool
	}{
		{"zero", header.EncodingRange{}, false},
		{"valid", header.EncodingRange{Encoding: "gzip", Params: header.Values{"q": {"0.7"}}}, true},
		{"invalid 1", header.EncodingRange{Encoding: "gzip", Params: header.Values{"f i e l d": {"123"}}}, false},
		{"invalid 2", header.EncodingRange{Encoding: "gzip", Params: header.Values{"field": {" a b c "}}}, false},
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

func TestEncodingRange_IsZero(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.EncodingRange
		want bool
	}{
		{"zero", header.EncodingRange{}, true},
		{"not zero", header.EncodingRange{Encoding: "gzip"}, false},
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

func TestEncodingRange_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rng  header.EncodingRange
	}{
		{"zero", header.EncodingRange{}},
		{"full", header.EncodingRange{Encoding: "gzip", Params: header.Values{"q": {"0.7"}}}},
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
