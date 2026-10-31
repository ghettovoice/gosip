package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestWarning_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Warning
		want string
	}{
		{"nil", header.Warning(nil), ""},
		{"empty", header.Warning{}, "Warning: "},
		{"empty elem", header.Warning{{}}, `Warning: 0  ""`},
		{"full", header.Warning{
			{
				Code:  307,
				Agent: "isi.edu",
				Text:  "Session parameter 'foo' not understood",
			},
			{
				Code:  301,
				Agent: "isi.edu",
				Text:  "Incompatible network address type 'E.164'",
			},
		}, "Warning: 307 isi.edu \"Session parameter 'foo' not understood\", " +
			"301 isi.edu \"Incompatible network address type 'E.164'\""},
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

func TestWarning_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.Warning
		wantRes string
		wantErr error
	}{
		{"nil", header.Warning(nil), "", nil},
		{"empty", header.Warning{}, "Warning: ", nil},
		{
			"full",
			header.Warning{
				{
					Code:  307,
					Agent: "isi.edu",
					Text:  "Session parameter 'foo' not understood",
				},
				{
					Code:  301,
					Agent: "isi.edu",
					Text:  "Incompatible network address type 'E.164'",
				},
			},
			"Warning: 307 isi.edu \"Session parameter 'foo' not understood\", " +
				"301 isi.edu \"Incompatible network address type 'E.164'\"",
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

func TestWarning_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Warning
		want string
	}{
		{"nil", header.Warning(nil), ""},
		{"empty", header.Warning{}, ""},
		{
			"full",
			header.Warning{
				{
					Code:  307,
					Agent: "isi.edu",
					Text:  "Session parameter 'foo' not understood",
				},
				{
					Code:  301,
					Agent: "isi.edu",
					Text:  "Incompatible network address type 'E.164'",
				},
			},
			"307 isi.edu \"Session parameter 'foo' not understood\", " +
				"301 isi.edu \"Incompatible network address type 'E.164'\"",
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

func TestWarning_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Warning
		val  any
		want bool
	}{
		{"nil ptr to nil", header.Warning(nil), nil, false},
		{"nil ptr to nil ptr", header.Warning(nil), header.Warning(nil), true},
		{"zero ptr to nil ptr", header.Warning{}, header.Warning(nil), true},
		{"zero to zero", header.Warning{}, header.Warning{}, true},
		{"zero to zero ptr", header.Warning{}, &header.Warning{}, true},
		{"zero to nil ptr", header.Warning{}, (*header.Warning)(nil), false},
		{
			"not match",
			header.Warning{
				{
					Code:  307,
					Agent: "isi.edu",
					Text:  "Session parameter 'foo' not understood",
				},
				{
					Code:  301,
					Agent: "isi.edu",
					Text:  "Incompatible network address type 'E.164'",
				},
			},
			header.Warning{
				{
					Code:  301,
					Agent: "isi.edu",
					Text:  "Incompatible network address type 'E.164'",
				},
				{
					Code:  307,
					Agent: "isi.edu",
					Text:  "Session parameter 'foo' not understood",
				},
			},
			false,
		},
		{
			"match",
			header.Warning{
				{
					Code:  307,
					Agent: "isi.edu",
					Text:  "Session parameter 'foo' not understood",
				},
				{
					Code:  301,
					Agent: "isi.edu",
					Text:  "Incompatible network address type 'E.164'",
				},
			},
			header.Warning{
				{
					Code:  307,
					Agent: "ISI.EDU",
					Text:  "Session parameter 'foo' not understood",
				},
				{
					Code:  301,
					Agent: "ISI.EDU",
					Text:  "Incompatible network address type 'E.164'",
				},
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

func TestWarning_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Warning
		want bool
	}{
		{"nil", header.Warning(nil), false},
		{"empty", header.Warning{}, false},
		{"valid", header.Warning{
			{
				Code:  307,
				Agent: "isi.edu",
				Text:  "Session parameter 'foo' not understood",
			},
			{
				Code:  301,
				Agent: "isi.edu",
				Text:  "Incompatible network address type 'E.164'",
			},
		}, true},
		{"invalid", header.Warning{{Code: 307}}, false},
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

func TestWarning_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Warning
	}{
		{"nil", header.Warning(nil)},
		{"empty", header.Warning{}},
		{
			"full",
			header.Warning{
				{
					Code:  307,
					Agent: "isi.edu",
					Text:  "Session parameter 'foo' not understood",
				},
				{
					Code:  301,
					Agent: "isi.edu",
					Text:  "Incompatible network address type 'E.164'",
				},
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

func TestWarning_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Warning
		want string
	}{
		{"nil", nil, "null"},
		{"empty", header.Warning{}, `{"name":"Warning","value":""}`},
		{
			"full",
			header.Warning{
				{
					Code:  307,
					Agent: "isi.edu",
					Text:  "Session parameter 'foo' not understood",
				},
				{
					Code:  301,
					Agent: "isi.edu",
					Text:  "Incompatible network address type 'E.164'",
				},
			},
			`{"name":"Warning","value":"307 isi.edu \"Session parameter 'foo' not understood\", 301 isi.edu \"Incompatible network address type 'E.164'\""}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			if diff := cmp.Diff(string(got), tt.want); diff != "" {
				t.Fatalf("json.Marshal(hdr) = %q, want %q\ndiff (-got +want):\n%v", got, tt.want, diff)
			}
		})
	}
}

func TestWarning_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    header.Warning
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"307 isi.edu \"Session parameter 'foo' not understood\""}`, nil, true},
		{"empty value", `{"name":"Warning","value":""}`, header.Warning{}, false},
		{"invalid json", `{"name":"Warning","value":`, nil, true},
		{"wrong header", `{"name":"From","value":"\"Alice\" <sip:alice@example.com>"}`, nil, true},
		{
			"full",
			`{"name":"Warning","value":"307 isi.edu \"Session parameter 'foo' not understood\", 301 isi.edu \"Incompatible network address type 'E.164'\""}`,
			header.Warning{
				{
					Code:  307,
					Agent: "isi.edu",
					Text:  "Session parameter 'foo' not understood",
				},
				{
					Code:  301,
					Agent: "isi.edu",
					Text:  "Incompatible network address type 'E.164'",
				},
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got header.Warning
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

func TestWarningEntry_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		item header.WarningEntry
		want string
	}{
		{"zero", header.WarningEntry{}, `0  ""`},
		{
			"full",
			header.WarningEntry{
				Code:  307,
				Agent: "isi.edu",
				Text:  "Session parameter 'foo' not understood",
			},
			`307 isi.edu "Session parameter 'foo' not understood"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.item.String(); got != tt.want {
				t.Errorf("item.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWarningEntry_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		item header.WarningEntry
		val  any
		want bool
	}{
		{"zero to nil", header.WarningEntry{}, nil, false},
		{"zero to zero", header.WarningEntry{}, header.WarningEntry{}, true},
		{"zero to zero ptr", header.WarningEntry{}, &header.WarningEntry{}, true},
		{"zero to nil ptr", header.WarningEntry{}, (*header.WarningEntry)(nil), false},
		{"not match 1", header.WarningEntry{Code: 100}, header.WarningEntry{Code: 307}, false},
		{
			"not match 2",
			header.WarningEntry{Code: 100, Agent: "isi.edu"},
			header.WarningEntry{Code: 100, Agent: "qwe.abc"},
			false,
		},
		{
			"match",
			header.WarningEntry{
				Code:  307,
				Agent: "isi.edu",
				Text:  "Session parameter 'foo' not understood",
			},
			header.WarningEntry{
				Code:  307,
				Agent: "ISI.EDU",
				Text:  "Session parameter 'foo' not understood",
			},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.item.Equal(tt.val); got != tt.want {
				t.Errorf("item.Equal(val) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWarningEntry_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		item header.WarningEntry
		want bool
	}{
		{"zero", header.WarningEntry{}, false},
		{
			"valid",
			header.WarningEntry{Code: 307, Agent: "isi.edu", Text: "Session parameter 'foo' not understood"},
			true,
		},
		{"invalid 1", header.WarningEntry{Code: 0}, false},
		{"invalid 2", header.WarningEntry{Agent: "isi.edu"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.item.IsValid(); got != tt.want {
				t.Errorf("item.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWarningEntry_IsZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		item header.WarningEntry
		want bool
	}{
		{"zero", header.WarningEntry{}, true},
		{"not zero", header.WarningEntry{Code: 307}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.item.IsZero(); got != tt.want {
				t.Errorf("item.IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWarningEntry_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		item header.WarningEntry
		want any
	}{
		{"zero", header.WarningEntry{}, header.WarningEntry{}},
		{
			"full",
			header.WarningEntry{
				Code:  307,
				Agent: "isi.edu",
				Text:  "Session parameter 'foo' not understood",
			},
			header.WarningEntry{
				Code:  307,
				Agent: "isi.edu",
				Text:  "Session parameter 'foo' not understood",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.item.Clone()
			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Errorf("item.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.want, diff)
			}
		})
	}
}

func TestWarningEntry_MarshalText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		wrn  header.WarningEntry
		want string
	}{
		{"zero", header.WarningEntry{}, `0  ""`},
		{
			"full",
			header.WarningEntry{
				Code:  307,
				Agent: "isi.edu",
				Text:  "Session parameter 'foo' not understood",
			},
			`307 isi.edu "Session parameter 'foo' not understood"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.wrn.MarshalText()
			if err != nil {
				t.Fatalf("wrn.MarshalText() error = %v, want nil", err)
			}

			if string(got) != tt.want {
				t.Fatalf("wrn.MarshalText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWarningEntry_UnmarshalText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    header.WarningEntry
		wantErr bool
	}{
		{"empty", "", header.WarningEntry{}, false},
		{"invalid", "not a valid hop", header.WarningEntry{}, true},
		{"zero", `0  ""`, header.WarningEntry{}, false},
		{
			"full",
			`307 isi.edu "Session parameter 'foo' not understood"`,
			header.WarningEntry{
				Code:  307,
				Agent: "isi.edu",
				Text:  "Session parameter 'foo' not understood",
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got header.WarningEntry
			if err := got.UnmarshalText([]byte(tt.data)); err != nil {
				if !tt.wantErr {
					t.Fatalf("warningEntry.UnmarshalText(data) error = %v, want nil", err)
				}
				return
			}

			if tt.wantErr {
				t.Fatal("warningEntry.UnmarshalText(data) error = nil, want error")
			}

			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.want, diff)
			}
		})
	}
}
