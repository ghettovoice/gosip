package grammar_test

import (
	"bytes"
	"testing"

	"github.com/ghettovoice/gosip/internal/grammar"
)

func TestEscape(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := grammar.Escape(c.str, c.cb), c.want; got != want {
				t.Errorf("grammar.Escape(%q, %p) = %q, want %q", c.str, c.cb, got, want)
			}
		})
	}
}

func TestUnescape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		str  string
		want string
	}{
		{"empty", "", ""},
		{"no unescape", "abc%ax%", "abc%ax%"},
		{"unescape all", "abc%E4%b8%96", "abc世"}, //nolint:gosmopolitan
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := grammar.Unescape(c.str), c.want; got != want {
				t.Errorf("grammar.Unescape(%q) = %q, want %q", c.str, got, want)
			}
		})
	}
}

func BenchmarkEscape(b *testing.B) {
	cases := []struct {
		name    string
		in, out any
	}{
		{"string", "abc++qwe!", "abc%2B%2Bqwe!"},
		{"bytes", []byte("abc++qwe!"), []byte("abc%2B%2Bqwe!")},
	}

	b.ResetTimer()
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ResetTimer()
			for b.Loop() {
				switch in := c.in.(type) {
				case string:
					want, _ := c.out.(string)
					if got := grammar.Escape(in, nil); got != want {
						b.Errorf("grammar.Escape(%q, nil) = %q, want %q", in, got, want)
					}
				case []byte:
					want, _ := c.out.([]byte)
					if got := grammar.Escape(in, nil); !bytes.Equal(got, want) {
						b.Errorf("grammar.Escape(%q, nil) = %q, want %q", in, got, want)
					}
				}
			}
		})
	}
}
