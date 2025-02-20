package grammar

import (
	"bytes"

	"github.com/ghettovoice/gosip/internal/constraints"
)

// Unescape unescapes s by converting each 3-byte encoded substring of the form "% HEXDIG HEXDIG" into the hex-decoded byte.
func Unescape[T constraints.Byteseq](s T) T {
	if len(s) == 0 {
		return s
	}

	var b bytes.Buffer
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 <= len(s) && ishex(s[i+1]) && ishex(s[i+2]) {
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
		} else {
			b.WriteByte(s[i])
		}
	}
	return T(b.Bytes())
}

// Escape escapes s by replacing each char matched by shouldEscape callback to the hex form "% HEXDIG HEXDIG".
func Escape[T constraints.Byteseq](s T, shouldEscape func(c byte) bool) T {
	if len(s) == 0 {
		return s
	}

	if shouldEscape == nil {
		shouldEscape = func(c byte) bool { return !IsCharUnreserved(c) }
	}

	var b bytes.Buffer
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '%' && i+2 <= len(s) && ishex(s[i+1]) && ishex(s[i+2]):
			b.WriteByte(s[i])
			b.WriteByte(s[i+1])
			b.WriteByte(s[i+2])
			i += 2
		case shouldEscape(s[i]):
			b.WriteByte('%')
			b.WriteByte(upperhex[s[i]>>4])
			b.WriteByte(upperhex[s[i]&15])
		default:
			b.WriteByte(s[i])
		}
	}
	return T(b.Bytes())
}

const upperhex = "0123456789ABCDEF"

func ishex(c byte) bool {
	switch {
	case '0' <= c && c <= '9':
		return true
	case 'a' <= c && c <= 'f':
		return true
	case 'A' <= c && c <= 'F':
		return true
	}
	return false
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

// IsAlphanumChar checks alphanum rule.
func IsAlphanumChar(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

var unreservedChars = map[byte]bool{
	'-':  true,
	'_':  true,
	'.':  true,
	'!':  true,
	'~':  true,
	'*':  true,
	'\'': true,
	'(':  true,
	')':  true,
}

// IsCharUnreserved checks on unreserved rule.
func IsCharUnreserved(c byte) bool {
	return unreservedChars[c] || IsAlphanumChar(c)
}

var uriUserUnreservedChars = map[byte]bool{
	'&': true,
	'=': true,
	'+': true,
	'$': true,
	',': true,
	';': true,
	'?': true,
	'/': true,
}

// IsURIUserCharUnreserved checks on user-unreserved rule.
func IsURIUserCharUnreserved(c byte) bool {
	return uriUserUnreservedChars[c] || IsCharUnreserved(c)
}

var uriPasswdUnreservedChar = map[byte]bool{
	'&': true,
	'=': true,
	'+': true,
	'$': true,
	',': true,
}

// IsURIPasswdCharUnreserved checks on password-unreserved rule.
func IsURIPasswdCharUnreserved(c byte) bool {
	return uriPasswdUnreservedChar[c] || IsCharUnreserved(c)
}

var uriParamUnreservedChar = map[byte]bool{
	'[': true,
	']': true,
	'/': true,
	':': true,
	'&': true,
	'+': true,
	'$': true,
}

// IsURIParamCharUnreserved checks on param-unreserved rule.
func IsURIParamCharUnreserved(c byte) bool {
	return uriParamUnreservedChar[c] || IsCharUnreserved(c)
}

var uriHeaderUnreservedChars = map[byte]bool{
	'[': true,
	']': true,
	'/': true,
	'?': true,
	':': true,
	'+': true,
	'$': true,
}

// IsURIHeaderCharUnreserved checks on hnv-unreserved rule.
func IsURIHeaderCharUnreserved(c byte) bool {
	return uriHeaderUnreservedChars[c] || IsCharUnreserved(c)
}
