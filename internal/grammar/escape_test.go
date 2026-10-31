package grammar_test

import (
	"bytes"
	"testing"

	"github.com/ghettovoice/gosip/internal/grammar"
)

func TestEscape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		str  string
		cb   func(byte) bool
		want string
	}{
		{"empty", "", nil, ""},
		{"no escape", "abc-%2Bqwe!", nil, "abc-%2Bqwe!"},
		{"escape all", "abc++qwe!", nil, "abc%2B%2Bqwe!"},
		{"escape some", "abc+?qwe!", func(c byte) bool { return c != '+' && !grammar.IsCharUnreserved(c) }, "abc+%3Fqwe!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, want := grammar.Escape(tt.str, tt.cb), tt.want; got != want {
				t.Errorf("grammar.Escape(%q, %p) = %q, want %q", tt.str, tt.cb, got, want)
			}
		})
	}
}

func TestUnescape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		str  string
		want string
	}{
		{"empty", "", ""},
		{"no unescape", "abc%ax%", "abc%ax%"},
		{"unescape all", "abc%E4%b8%96", "abc世"}, //nolint:gosmopolitan
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, want := grammar.Unescape(tt.str), tt.want; got != want {
				t.Errorf("grammar.Unescape(%q) = %q, want %q", tt.str, got, want)
			}
		})
	}
}

func BenchmarkEscape(b *testing.B) {
	tests := []struct {
		name    string
		in, out any
	}{
		{"string", "abc++qwe!", "abc%2B%2Bqwe!"},
		{"bytes", []byte("abc++qwe!"), []byte("abc%2B%2Bqwe!")},
	}

	b.ResetTimer()

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ResetTimer()

			for b.Loop() {
				switch in := tt.in.(type) {
				case string:
					want, _ := tt.out.(string)
					if got := grammar.Escape(in, nil); got != want {
						b.Errorf("grammar.Escape(%q, nil) = %q, want %q", in, got, want)
					}
				case []byte:
					want, _ := tt.out.([]byte)
					if got := grammar.Escape(in, nil); !bytes.Equal(got, want) {
						b.Errorf("grammar.Escape(%q, nil) = %q, want %q", in, got, want)
					}
				}
			}
		})
	}
}
