package sip

import (
	"fmt"

	"github.com/ghettovoice/gosip/internal/constraints"
	"github.com/ghettovoice/gosip/sip/header"
)

// Header represents a generic SIP header.
// See [header.Header].
type Header = header.Header

type HeaderName = header.Name

// HeaderParser represents a custom SIP header parser.
// See [header.Parser].
type HeaderParser = header.Parser

// ParseHeader parses a generic SIP header.
// See [header.Parse].
func ParseHeader[T constraints.Byteseq](s T, hdrPrs map[string]HeaderParser) (Header, error) {
	return header.Parse(s, hdrPrs)
}

// CanonicHeaderName returns a canonicalized header name.
// See [header.CanonicName].
func CanonicHeaderName[T ~string](name T) HeaderName { return header.CanonicName(name) }

type missingHeaderError struct {
	Header string
}

func (err *missingHeaderError) Error() string {
	return fmt.Sprintf("missing %q header", CanonicHeaderName(err.Header))
}

func (*missingHeaderError) Grammar() bool { return true }
