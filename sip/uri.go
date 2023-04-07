package sip

import (
	"github.com/ghettovoice/gosip/internal/constraints"
	"github.com/ghettovoice/gosip/sip/uri"
)

// URI represents generic URI (SIP, SIPS, Tel, ...etc).
// See [uri.URI].
type URI = uri.URI

// ParseURI parses any URI from a given input s (string or []byte).
// See [uri.Parse].
func ParseURI[T constraints.Byteseq](s T) (URI, error) { return uri.Parse(s) }
