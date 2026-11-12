package header

import (
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/uri"
)

type AuthCredentials interface {
	shared.Renderer
	shared.Equalable
	shared.ValidFlag
	shared.Cloneable[AuthCredentials]
}

// Authorization is an implementation of the Authorization header.
type Authorization struct {
	AuthCredentials
}

func (*Authorization) CanonicName() Name { return "Authorization" }

func (*Authorization) CompactName() Name { return "Authorization" }

func (hdr *Authorization) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr *Authorization) renderValTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderTo(w, hdr.AuthCredentials, opts...))
}

func (hdr *Authorization) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr *Authorization) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderHdrVal(hdr)
}

func (hdr *Authorization) String() string {
	return hdr.RenderValue()
}

func (hdr *Authorization) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods   Authorization
		Authorization hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*Authorization)(hdr))
}

func (hdr *Authorization) Clone() Header {
	if hdr == nil {
		return nil
	}

	clone := *hdr
	clone.AuthCredentials = shared.Clone[AuthCredentials](hdr.AuthCredentials)
	return &clone
}

func (hdr *Authorization) Equal(val any) bool {
	return util.EqualPointer(hdr, val, func(other *Authorization) bool {
		return shared.IsEqual(hdr.AuthCredentials, other.AuthCredentials)
	})
}

func (hdr *Authorization) IsValid() bool {
	return hdr != nil && shared.IsValid(hdr.AuthCredentials)
}

func (hdr *Authorization) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Authorization) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromAuthorizationNode(node *abnf.Node) *Authorization {
	return &Authorization{AuthCredentials: buildAuthCredentialsFromNode(node)}
}

func buildAuthCredentialsFromNode(node *abnf.Node) AuthCredentials {
	var credentials AuthCredentials
	node = grammar.MustGetNode(node, "credentials")
	switch scheme := node.Children[0].Children[0].String(); util.ToLower(scheme) {
	case "digest":
		crd := &DigestCredentials{}
		credentials = crd
		for _, paramNode := range node.GetNodes("dig-resp") {
			paramNode = paramNode.Children[0]
			switch paramNode.Key {
			case "username":
				crd.Username = grammar.Unquote(paramNode.Children[2].String())
			case "realm":
				crd.Realm = grammar.Unquote(paramNode.Children[2].String())
			case "nonce":
				crd.Nonce = grammar.Unquote(paramNode.Children[2].String())
			case "digest-uri":
				uriNode := grammar.MustGetNode(paramNode, "Request-URI")
				crd.URI = util.Must2(uri.FromABNF(uriNode.Children[0]))
				crd.uriText = uriNode.String()
				crd.uriRender = shared.Render(crd.URI)
			case "dresponse":
				crd.Response = grammar.Unquote(paramNode.Children[2].String())
			case "algorithm":
				crd.Algorithm = paramNode.Children[2].String()
			case "cnonce":
				crd.CNonce = grammar.Unquote(paramNode.Children[2].String())
			case "opaque":
				crd.Opaque = grammar.Unquote(paramNode.Children[2].String())
			case "message-qop":
				crd.QOP = paramNode.Children[2].String()
			case "nonce-count":
				if v, err := strconv.ParseUint(paramNode.Children[2].String(), 16, 64); err == nil {
					crd.NonceCount = uint(v)
				}
			default:
				if crd.Params == nil {
					crd.Params = make(Values)
				}
				crd.Params.Set(paramNode.Children[0].String(), paramNode.Children[2].String())
			}
		}
	case "bearer":
		credentials = &BearerCredentials{
			Token: grammar.MustGetNode(node, "bearer-response").String(),
		}
	default:
		crd := &AnyCredentials{
			Scheme: node.Children[0].Children[0].String(),
		}
		credentials = crd
		for _, paramNode := range node.GetNodes("auth-param") {
			if crd.Params == nil {
				crd.Params = make(Values)
			}
			crd.Params.Set(paramNode.Children[0].String(), paramNode.Children[2].String())
		}
	}
	return credentials
}

// DigestCredentials represents the digest authentication credentials.
//
// IsValid checks header structure only: Response must be ASCII hexadecimal
// sized for the declared algorithm (an omitted algorithm means MD5 per the
// protocol), while unknown algorithm tokens keep even-length hexadecimal
// syntax of at least 32 digits. The empty RFC 8760 pre-challenge placeholder
// can parse but is never a valid proof, and structurally valid syntax for an
// unknown algorithm does not imply a mechanism can compute it.
//
// Digest proofs sign the Request-URI as it appeared on the wire; URIText
// preserves that spelling across clone, render, and JSON round trips while
// the typed URI is unchanged, and falls back to the typed rendering once
// URI is edited. Callers must recalculate the response after changing any
// signed field. The typed URI and its Equal semantics stay authoritative
// for target-acceptance policy.
type DigestCredentials struct {
	Username,
	Realm,
	Nonce,
	Response,
	Algorithm,
	CNonce,
	Opaque,
	QOP string
	NonceCount uint
	URI        uri.URI
	Params     Values

	// uriText keeps the Request-URI exactly as it appeared in the parsed
	// header and uriRender its typed rendering at parse time. Digest proofs
	// sign the wire spelling; URIText returns it while URI still renders
	// identically to uriRender.
	uriText,
	uriRender string
}

func (crd *DigestCredentials) Clone() AuthCredentials {
	if crd == nil {
		return nil
	}

	clone := *crd
	clone.URI = shared.Clone[uri.URI](crd.URI)
	clone.Params = crd.Params.Clone()
	return &clone
}

// URIText returns the Digest Request-URI text for signing and rendering.
// Parsed credentials keep the original wire spelling while the typed URI
// still renders identically to its parse-time snapshot; once URI is
// mutated or replaced, the current typed rendering is returned.
// Programmatically constructed credentials use the typed rendering.
// A nil receiver or nil URI returns an empty string.
func (crd *DigestCredentials) URIText() string {
	text, _ := crd.renderURIText()
	return text
}

// renderURIText renders the typed URI once with the given options and
// selects the original wire text only while the option-aware rendering
// still matches the parse-time snapshot.
func (crd *DigestCredentials) renderURIText(opts ...shared.RenderOptions) (string, error) {
	if crd == nil || crd.URI == nil {
		return "", nil
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	if _, err := crd.URI.RenderTo(sb, opts...); err != nil {
		return "", errors.Wrap(err)
	}

	rendered := sb.String()
	if crd.uriText == "" || rendered != crd.uriRender {
		return rendered, nil
	}
	return crd.uriText, nil
}

//nolint:gocognit // per-field rendering per RFC 7616
func (crd *DigestCredentials) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if crd == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint("Digest ")

	var kvs [][]string
	// resolve and write all non-empty std scalar parameters in alphabet order
	for k, v := range map[string]string{
		"username":  crd.Username,
		"realm":     crd.Realm,
		"nonce":     crd.Nonce,
		"response":  crd.Response,
		"algorithm": crd.Algorithm,
		"cnonce":    crd.CNonce,
		"opaque":    crd.Opaque,
		"qop":       crd.QOP,
	} {
		if v == "" {
			continue
		}

		switch k {
		case "username", "realm", "nonce", "response", "cnonce", "opaque":
			v = grammar.Quote(v)
		}
		kvs = append(kvs, []string{k, v})
	}

	if crd.NonceCount > 0 {
		kvs = append(kvs, []string{"nc", fmt.Sprintf("%08x", crd.NonceCount)})
	}

	if len(kvs) > 0 {
		slices.SortFunc(kvs, util.CompareKeys)
		for i, kv := range kvs {
			if i > 0 {
				cw.Fprint(", ")
			}
			cw.Fprint(kv[0], "=", kv[1])
		}
	}

	if crd.URI != nil {
		if len(kvs) > 0 {
			cw.Fprint(", ")
		}
		cw.Fprint("uri=\"")
		cw.Call(func(w io.Writer) (int, error) {
			text, err := crd.renderURIText(opts...)
			if err != nil {
				return 0, err
			}
			return errors.Wrap2(fmt.Fprint(w, text))
		})
		cw.Fprint("\"")
	}

	// append custom parameters if present
	if len(crd.Params) > 0 {
		clear(kvs)
		kvs = kvs[:0]
		for k := range crd.Params {
			v, _ := crd.Params.Last(k)
			kvs = append(kvs, []string{util.ToLower(k), v})
		}
		slices.SortFunc(kvs, util.CompareKeys)
		if len(kvs) > 0 || crd.URI != nil {
			cw.Fprint(", ")
		}
		for i, kv := range kvs {
			if i > 0 {
				cw.Fprint(", ")
			}
			cw.Fprint(kv[0], "=", kv[1])
		}
	}
	return errors.Wrap2(cw.Result())
}

func (crd *DigestCredentials) Render(opts ...RenderOptions) string {
	return render(crd, opts...)
}

func (crd *DigestCredentials) String() string {
	return crd.Render()
}

func (crd *DigestCredentials) Format(f fmt.State, verb rune) {
	if formatRndr(f, verb, crd) {
		return
	}
	type (
		hideMethods       DigestCredentials
		DigestCredentials hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*DigestCredentials)(crd))
}

func (crd *DigestCredentials) Equal(val any) bool {
	return util.EqualPointer(crd, val, func(other *DigestCredentials) bool {
		return crd.Username == other.Username &&
			util.EqualFold(crd.Realm, other.Realm) &&
			crd.Nonce == other.Nonce &&
			crd.Response == other.Response &&
			util.EqualFold(crd.Algorithm, other.Algorithm) &&
			crd.CNonce == other.CNonce &&
			crd.Opaque == other.Opaque &&
			util.EqualFold(crd.QOP, other.QOP) &&
			crd.NonceCount == other.NonceCount &&
			shared.IsEqual(crd.URI, other.URI) &&
			compareHdrParams(crd.Params, other.Params, nil)
	})
}

func (crd *DigestCredentials) IsValid() bool {
	return crd != nil &&
		crd.Username != "" &&
		crd.Realm != "" &&
		crd.Nonce != "" &&
		(crd.Algorithm == "" || grammar.IsToken(crd.Algorithm)) &&
		(crd.QOP == "" || grammar.IsToken(crd.QOP)) &&
		shared.IsValid(crd.URI) &&
		validateHdrParams(crd.Params) &&
		isValidDigestRes(crd.Algorithm, crd.Response)
}

// isValidDigestRes reports whether response has ASCII hexadecimal
// syntax of the size required for the declared algorithm.
func isValidDigestRes(algorithm, response string) bool {
	if len(response) == 0 || !isASCIIHex(response) {
		return false
	}

	const (
		md5ResponseLen    = 32
		sha256ResponseLen = 64
	)
	switch util.ToLower(algorithm) {
	case "", "md5", "md5-sess":
		return len(response) == md5ResponseLen
	case "sha-256", "sha-256-sess", "sha-512-256", "sha-512-256-sess":
		return len(response) == sha256ResponseLen
	default:
		return len(response) >= md5ResponseLen && len(response)%2 == 0
	}
}

// isASCIIHex reports whether s contains only ASCII hexadecimal digits.
func isASCIIHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// BearerCredentials represents the bearer authentication credentials.
type BearerCredentials struct {
	Token string
}

func (crd *BearerCredentials) Clone() AuthCredentials {
	if crd == nil {
		return nil
	}

	clone := *crd
	return &clone
}

func (crd *BearerCredentials) RenderTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	if crd == nil {
		return 0, nil
	}
	return errors.Wrap2(fmt.Fprint(w, "Bearer ", crd.Token))
}

func (crd *BearerCredentials) Render(opts ...RenderOptions) string {
	return render(crd, opts...)
}

func (crd *BearerCredentials) String() string {
	return crd.Render()
}

func (crd *BearerCredentials) Format(f fmt.State, verb rune) {
	if formatRndr(f, verb, crd) {
		return
	}
	type (
		hideMethods       BearerCredentials
		BearerCredentials hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*BearerCredentials)(crd))
}

func (crd *BearerCredentials) Equal(val any) bool {
	return util.EqualPointer(crd, val, func(other *BearerCredentials) bool {
		return crd.Token == other.Token
	})
}

func (crd *BearerCredentials) IsValid() bool {
	return crd != nil && crd.Token != ""
}

// AnyCredentials represents generic authentication credentials.
type AnyCredentials struct {
	Scheme string
	Params Values
}

func (crd *AnyCredentials) Clone() AuthCredentials {
	if crd == nil {
		return nil
	}

	clone := *crd
	clone.Params = crd.Params.Clone()
	return &clone
}

func (crd *AnyCredentials) RenderTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	if crd == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(crd.Scheme, " ")

	kvs := make([][]string, 0, len(crd.Params))
	for k := range crd.Params {
		v, _ := crd.Params.Last(k)
		kvs = append(kvs, []string{util.ToLower(k), v})
	}

	if len(kvs) > 0 {
		slices.SortFunc(kvs, util.CompareKeys)
		for i, kv := range kvs {
			if i > 0 {
				cw.Fprint(", ")
			}
			cw.Fprint(kv[0], "=", kv[1])
		}
	}
	return errors.Wrap2(cw.Result())
}

func (crd *AnyCredentials) Render(opts ...RenderOptions) string {
	return render(crd, opts...)
}

func (crd *AnyCredentials) String() string {
	return crd.Render()
}

func (crd *AnyCredentials) Format(f fmt.State, verb rune) {
	if formatRndr(f, verb, crd) {
		return
	}
	type (
		hideMethods    AnyCredentials
		AnyCredentials hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*AnyCredentials)(crd))
}

func (crd *AnyCredentials) Equal(val any) bool {
	return util.EqualPointer(crd, val, func(other *AnyCredentials) bool {
		return util.EqualFold(crd.Scheme, other.Scheme) &&
			compareHdrParams(crd.Params, other.Params, nil)
	})
}

func (crd *AnyCredentials) IsValid() bool {
	return crd != nil &&
		grammar.IsToken(crd.Scheme) &&
		len(crd.Params) > 0 &&
		validateHdrParams(crd.Params)
}
