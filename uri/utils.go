package uri

import "github.com/ghettovoice/gosip/internal/grammar"

// shouldEscapeURIParamChar reports whether the given byte for URI parameters needs escaping.
func shouldEscapeURIParamChar(c byte) bool { return !grammar.IsURIParamCharUnreserved(c) }

// shouldEscapeURIHeaderChar reports whether the given byte for URI headers needs escaping.
func shouldEscapeURIHeaderChar(c byte) bool { return !grammar.IsURIHeaderCharUnreserved(c) }
