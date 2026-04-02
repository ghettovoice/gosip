package sip

import (
	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/uri"
)

// URI represents generic URI (SIP, SIPS, Tel, ...etc).
// See [uri.URI].
type URI = uri.URI

// ParseURI parses any URI from a given input s (string or []byte).
// See [uri.Parse].
func ParseURI[T ~string | ~[]byte](s T) (URI, error) { return errors.Wrap2(uri.Parse(s)) }
