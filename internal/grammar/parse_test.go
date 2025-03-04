package grammar_test

import (
	"errors"
	"testing"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/grammar"
)

func TestParseSIPURI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		input  any
		expect string
		err    error
	}{
		{"", "", "", grammar.ErrEmptyInput},
		{"", "abc", "", grammar.ErrMalformedInput},
		{"", "sip:", "", grammar.ErrMalformedInput},
		{"", "qwe:abc", "", grammar.ErrMalformedInput},
		{"", "sip:abc", "sip:abc", nil},
		{"", []byte("sip:abc"), "sip:abc", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var (
				node *abnf.Node
				err  error
			)
			switch in := c.input.(type) {
			case string:
				node, err = grammar.ParseSIPURI(in)
			case []byte:
				node, err = grammar.ParseSIPURI(in)
			}
			if c.err == nil {
				if got, want := node.String(), c.expect; got != want {
					t.Errorf("grammar.ParseSIPURI(%q) = %q, want %q", c.input, got, want)
				}
				if err != nil {
					t.Errorf("grammar.ParseSIPURI(%q) error = %v, want nil", c.input, err)
				}
			} else {
				if got, want := err, c.err; !errors.Is(got, want) {
					t.Errorf("grammar.ParseSIPURI(%q) error = %v, want %v", c.input, got, want)
				}
			}
		})
	}
}
