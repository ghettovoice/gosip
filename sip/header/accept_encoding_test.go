package header_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestAcceptEncoding_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Render(); got != tt.want {
				t.Errorf("hdr.Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAcceptEncoding_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

func TestAcceptEncoding_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.AcceptEncoding
		want string
	}{
		{"nil", header.AcceptEncoding(nil), ""},
		{"empty", header.AcceptEncoding{}, ""},
		{"full", header.AcceptEncoding{{Encoding: "gzip"}, {Encoding: "compress"}}, "gzip, compress"},
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

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Equal(tt.val); got != tt.want {
				t.Errorf("hdr.Equal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAcceptEncoding_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.IsValid(); got != tt.want {
				t.Errorf("hdr.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAcceptEncoding_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

func TestAcceptEncoding_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.AcceptEncoding
		want string
	}{
		{"nil", nil, "null"},
		{"empty", header.AcceptEncoding{}, `{"name":"Accept-Encoding","value":""}`},
		{
			"single",
			header.AcceptEncoding{
				{Encoding: "gzip", Params: make(header.Values).Set("q", "0.8")},
			},
			`{"name":"Accept-Encoding","value":"gzip;q=0.8"}`,
		},
		{
			"multiple",
			header.AcceptEncoding{
				{Encoding: "gzip"},
				{Encoding: "compress", Params: make(header.Values).Set("q", "0.5")},
			},
			`{"name":"Accept-Encoding","value":"gzip, compress;q=0.5"}`,
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

func TestAcceptEncoding_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    header.AcceptEncoding
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"name":"","value":"gzip"}`, nil, true},
		{"empty value", `{"name":"Accept-Encoding","value":""}`, header.AcceptEncoding{}, false},
		{"invalid json", `{"name":"Accept-Encoding","value":`, nil, true},
		{"wrong header", `{"name":"From","value":"<sip:alice@example.com>"}`, nil, true},
		{
			name: "single",
			data: `{"name":"Accept-Encoding","value":"gzip;q=0.8"}`,
			want: header.AcceptEncoding{
				{Encoding: "gzip", Params: make(header.Values).Set("q", "0.8")},
			},
		},
		{
			name: "multiple",
			data: `{"name":"Accept-Encoding","value":"gzip, compress;q=0.5"}`,
			want: header.AcceptEncoding{
				{Encoding: "gzip"},
				{Encoding: "compress", Params: make(header.Values).Set("q", "0.5")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got header.AcceptEncoding
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

func TestAcceptEncoding_RoundTripJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.AcceptEncoding
	}{
		{"nil", nil},
		{"empty", header.AcceptEncoding{}},
		{
			"single",
			header.AcceptEncoding{
				{Encoding: "gzip", Params: make(header.Values).Set("q", "0.8")},
			},
		},
		{
			"multiple",
			header.AcceptEncoding{
				{Encoding: "gzip"},
				{Encoding: "compress", Params: make(header.Values).Set("q", "0.5")},
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

			var got header.AcceptEncoding
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}

func TestEncodingRange_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.EncodingRange
		want string
	}{
		{"zero", header.EncodingRange{}, ""},
		{"full", header.EncodingRange{Encoding: "gzip", Params: header.Values{"q": {"0.7"}}}, "gzip;q=0.7"},
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

func TestEncodingRange_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.rng.Equal(tt.val); got != tt.want {
				t.Errorf("rng.Equal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEncodingRange_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.EncodingRange
		want bool
	}{
		{"zero", header.EncodingRange{}, false},
		{"valid", header.EncodingRange{Encoding: "gzip", Params: header.Values{"q": {"0.7"}}}, true},
		{"invalid 1", header.EncodingRange{Encoding: "gzip", Params: header.Values{"f i e l d": {"123"}}}, false},
		{"invalid 2", header.EncodingRange{Encoding: "gzip", Params: header.Values{"field": {" a b c "}}}, false},
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

func TestEncodingRange_IsZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.EncodingRange
		want bool
	}{
		{"zero", header.EncodingRange{}, true},
		{"not zero", header.EncodingRange{Encoding: "gzip"}, false},
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

func TestEncodingRange_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rng  header.EncodingRange
	}{
		{"zero", header.EncodingRange{}},
		{"full", header.EncodingRange{Encoding: "gzip", Params: header.Values{"q": {"0.7"}}}},
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
