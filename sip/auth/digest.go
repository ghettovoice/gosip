package auth

import (
	"crypto/md5" //nolint:gosec // Legacy MD5 is explicitly enabled for SIP Digest compatibility.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strings"

	"github.com/ghettovoice/gosip/internal/errors"
)

const (
	digestAlgoMD5    = "MD5"
	digestAlgoSHA256 = "SHA-256"
	digestQOPAuth    = "auth"
)

// normalizeDigestAlgo resolves a wire or configured algorithm name to
// its canonical spelling. An empty name means MD5 at the protocol boundary;
// session variants and unsupported algorithms are rejected.
func normalizeDigestAlgo(name string) (string, error) {
	switch strings.ToUpper(name) {
	case "", digestAlgoMD5:
		return digestAlgoMD5, nil
	case digestAlgoSHA256:
		return digestAlgoSHA256, nil
	default:
		return "", errors.ErrorfWrap("unsupported digest algorithm %q", name)
	}
}

func newDigestHash(algo string) (hash.Hash, error) {
	switch algo {
	case digestAlgoMD5:
		return md5.New(), nil //nolint:gosec // Legacy MD5 is explicitly enabled for SIP Digest compatibility.
	case digestAlgoSHA256:
		return sha256.New(), nil
	default:
		return nil, errors.ErrorfWrap("unsupported digest algorithm %q", algo)
	}
}

func digestHex(h hash.Hash, parts ...string) string {
	for _, p := range parts {
		_, _ = io.WriteString(h, p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// DigestHA1 calculates the RFC 7616 HA1 value for the account. An empty
// algorithm means MD5; only MD5 and SHA-256 are supported. An empty
// password is valid input.
func DigestHA1(username, realm, password, algorithm string) (string, error) {
	algo, err := normalizeDigestAlgo(algorithm)
	if err != nil {
		return "", errors.Wrap(err)
	}
	h, _ := newDigestHash(algo)
	return digestHex(h, username, ":", realm, ":", password), nil
}

// digestHA2 calculates the RFC 7616 qop=auth HA2 value. Method and URI text
// are hashed byte-for-byte; they are not canonicalized.
func digestHA2(method, uriText, algorithm string) (string, error) {
	algo, err := normalizeDigestAlgo(algorithm)
	if err != nil {
		return "", errors.Wrap(err)
	}
	h, _ := newDigestHash(algo)
	return digestHex(h, method, ":", uriText), nil
}

// digestRes calculates the RFC 7616 qop=auth response over an
// algorithm-sized hexadecimal HA1.
func digestRes(ha1, method, uriText, nonce, cnonce string, count uint32, algorithm string) (string, error) {
	algo, err := normalizeDigestAlgo(algorithm)
	if err != nil {
		return "", errors.Wrap(err)
	}
	h, _ := newDigestHash(algo)
	if len(ha1) != h.Size()*2 || !isLowerHex(ha1) {
		return "", errors.ErrorWrap("malformed HA1")
	}
	if count == 0 {
		return "", errors.ErrorWrap("nonce count required")
	}
	if cnonce == "" {
		return "", errors.ErrorWrap("cnonce required")
	}
	ha2, err := digestHA2(method, uriText, algo)
	if err != nil {
		return "", errors.Wrap(err)
	}
	return digestHex(h, ha1, ":", nonce, ":", fmt.Sprintf("%08x", count), ":", cnonce, ":auth:", ha2), nil
}

// matchDigestRes compares a received proof with the expected digest in
// constant time. The received value must be lowercase hexadecimal of the
// algorithm's digest size; anything else is a malformed proof, not a match.
func matchDigestRes(got, want, algorithm string) bool {
	algo, err := normalizeDigestAlgo(algorithm)
	if err != nil {
		return false
	}
	h, _ := newDigestHash(algo)
	wantBytes, err := hex.DecodeString(want)
	if err != nil || len(wantBytes) != h.Size() {
		return false
	}
	if len(got) != len(want) || !isLowerHex(got) {
		return false
	}
	gotBytes, err := hex.DecodeString(got)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(gotBytes, wantBytes) == 1
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
