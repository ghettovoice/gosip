package grammar_test

import (
	"testing"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
)

func TestParseSIPURI(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				node *abnf.Node
				err  error
			)
			switch in := tt.input.(type) {
			case string:
				node, err = grammar.ParseSIPURI(in)
			case []byte:
				node, err = grammar.ParseSIPURI(in)
			}

			if tt.err == nil {
				if got, want := node.String(), tt.expect; got != want {
					t.Errorf("grammar.ParseSIPURI(%q) = %q, want %q", tt.input, got, want)
				}

				if err != nil {
					t.Errorf("grammar.ParseSIPURI(%q) error = %v, want nil", tt.input, err)
				}
			} else {
				if got, want := err, tt.err; !errors.Is(got, want) {
					t.Errorf("grammar.ParseSIPURI(%q) error = %v, want %v", tt.input, got, want)
				}
			}
		})
	}
}
