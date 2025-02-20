package header

import (
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
	"github.com/ghettovoice/gosip/sip/uri"
)

type AuthCredentials interface {
	Render() string
	RenderTo(w io.Writer) error
	Clone() AuthCredentials
}

// Authorization is an implementation of the Authorization header.
type Authorization struct {
	AuthCredentials
}

func (*Authorization) CanonicName() Name { return "Authorization" }

func (hdr *Authorization) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *Authorization) renderValue(w io.Writer) error {
	if hdr.AuthCredentials != nil {
		if err := hdr.AuthCredentials.RenderTo(w); err != nil {
			return err
		}
	}
	return nil
}

func (hdr *Authorization) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr *Authorization) String() string {
	if hdr == nil {
		return nilTag
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.renderValue(sb)
	return sb.String()
}

func (hdr *Authorization) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := *hdr
	hdr2.AuthCredentials = utils.Clone[AuthCredentials](hdr.AuthCredentials)
	return &hdr2
}

func (hdr *Authorization) Equal(val any) bool {
	var other *Authorization
	switch v := val.(type) {
	case Authorization:
		other = &v
	case *Authorization:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return utils.IsEqual(hdr.AuthCredentials, other.AuthCredentials)
}

func (hdr *Authorization) IsValid() bool {
	return hdr != nil && utils.IsValid(hdr.AuthCredentials)
}

func buildFromAuthorizationNode(node *abnf.Node) *Authorization {
	var hdr Authorization
	node = abnfutils.MustGetNode(node, "credentials")
	switch scheme := node.Children[0].Children[0].String(); stringutils.LCase(scheme) {
	case "digest":
		crd := &DigestCredentials{}
		hdr.AuthCredentials = crd
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
				crd.URI = uri.FromABNF(paramNode.Children[3].Children[0])
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
		hdr.AuthCredentials = &BearerCredentials{
			Token: abnfutils.MustGetNode(node, "bearer-response").String(),
		}
	default:
		crd := &AnyCredentials{
			Scheme: node.Children[0].Children[0].String(),
		}
		hdr.AuthCredentials = crd
		for _, paramNode := range node.GetNodes("auth-param") {
			if crd.Params == nil {
				crd.Params = make(Values)
			}
			crd.Params.Set(paramNode.Children[0].String(), paramNode.Children[2].String())
		}
	}
	return &hdr
}

// DigestCredentials represents the digest authentication credentials.
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
}

func (crd *DigestCredentials) Clone() AuthCredentials {
	if crd == nil {
		return nil
	}
	crd2 := *crd
	crd2.URI = utils.Clone[uri.URI](crd.URI)
	crd2.Params = crd.Params.Clone()
	return &crd2
}

//nolint:gocognit
func (crd *DigestCredentials) RenderTo(w io.Writer) error {
	if crd == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, "Digest "); err != nil {
		return err
	}

	var kvs [][]string //nolint:prealloc
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
		slices.SortFunc(kvs, stringutils.CmpKVs)
		for i, kv := range kvs {
			if i > 0 {
				if _, err := fmt.Fprint(w, ", "); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprint(w, kv[0], "=", kv[1]); err != nil {
				return err
			}
		}
	}

	if crd.URI != nil {
		if len(kvs) > 0 {
			if _, err := fmt.Fprint(w, ", "); err != nil {
				return err
			}
		}
		if err := stringutils.RenderTo(w, "uri=\"", crd.URI, "\""); err != nil {
			return err
		}
	}

	// append custom parameters if present
	if len(crd.Params) > 0 {
		if len(kvs) > 0 || crd.URI != nil {
			if _, err := fmt.Fprint(w, ", "); err != nil {
				return err
			}
		}

		clear(kvs)
		kvs = kvs[:0]
		for k := range crd.Params {
			kvs = append(kvs, []string{stringutils.LCase(k), crd.Params.Last(k)})
		}
		slices.SortFunc(kvs, stringutils.CmpKVs)
		for i, kv := range kvs {
			if i > 0 {
				if _, err := fmt.Fprint(w, ", "); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprint(w, kv[0], "=", kv[1]); err != nil {
				return err
			}
		}
	}

	return nil
}

func (crd *DigestCredentials) Render() string {
	if crd == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = crd.RenderTo(sb)
	return sb.String()
}

func (crd *DigestCredentials) String() string {
	if crd == nil {
		return nilTag
	}
	return crd.Render()
}

func (crd *DigestCredentials) Equal(val any) bool {
	var other *DigestCredentials
	switch v := val.(type) {
	case DigestCredentials:
		other = &v
	case *DigestCredentials:
		other = v
	default:
		return false
	}

	if crd == other {
		return true
	} else if crd == nil || other == nil {
		return false
	}

	return crd.Username == other.Username &&
		stringutils.LCase(crd.Realm) == stringutils.LCase(other.Realm) &&
		crd.Nonce == other.Nonce &&
		crd.Response == other.Response &&
		stringutils.LCase(crd.Algorithm) == stringutils.LCase(other.Algorithm) &&
		crd.CNonce == other.CNonce &&
		crd.Opaque == other.Opaque &&
		stringutils.LCase(crd.QOP) == stringutils.LCase(other.QOP) &&
		crd.NonceCount == other.NonceCount &&
		utils.IsEqual(crd.URI, other.URI) &&
		compareHeaderParams(crd.Params, other.Params, nil)
}

func (crd *DigestCredentials) IsValid() bool {
	return crd != nil &&
		crd.Username != "" && crd.Realm != "" && crd.Nonce != "" &&
		len(crd.Response) == 32 &&
		(crd.Algorithm == "" || grammar.IsToken(crd.Algorithm)) &&
		(crd.QOP == "" || grammar.IsToken(crd.QOP)) &&
		utils.IsValid(crd.URI) && validateHeaderParams(crd.Params)
}

// BearerCredentials represents the bearer authentication credentials.
type BearerCredentials struct {
	Token string
}

func (crd *BearerCredentials) Clone() AuthCredentials {
	if crd == nil {
		return nil
	}
	crd2 := *crd
	return &crd2
}

func (crd *BearerCredentials) RenderTo(w io.Writer) error {
	if crd == nil {
		return nil
	}
	_, err := fmt.Fprint(w, "Bearer ", crd.Token)
	return err
}

func (crd *BearerCredentials) Render() string {
	if crd == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = crd.RenderTo(sb)
	return sb.String()
}

func (crd *BearerCredentials) String() string {
	if crd == nil {
		return nilTag
	}
	return crd.Render()
}

func (crd *BearerCredentials) Equal(val any) bool {
	var other *BearerCredentials
	switch v := val.(type) {
	case BearerCredentials:
		other = &v
	case *BearerCredentials:
		other = v
	default:
		return false
	}

	if crd == other {
		return true
	} else if crd == nil || other == nil {
		return false
	}

	return crd.Token == other.Token
}

func (crd *BearerCredentials) IsValid() bool { return crd != nil && crd.Token != "" }

// AnyCredentials represents generic authentication credentials.
type AnyCredentials struct {
	Scheme string
	Params Values
}

func (crd *AnyCredentials) Clone() AuthCredentials {
	if crd == nil {
		return nil
	}
	crd2 := *crd
	crd2.Params = crd.Params.Clone()
	return &crd2
}

func (crd *AnyCredentials) RenderTo(w io.Writer) error {
	if crd == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, crd.Scheme, " "); err != nil {
		return err
	}

	kvs := make([][]string, 0, len(crd.Params))
	for k := range crd.Params {
		kvs = append(kvs, []string{stringutils.LCase(k), crd.Params.Last(k)})
	}
	if len(kvs) > 0 {
		slices.SortFunc(kvs, stringutils.CmpKVs)
		for i, kv := range kvs {
			if i > 0 {
				if _, err := fmt.Fprint(w, ", "); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprint(w, kv[0], "=", kv[1]); err != nil {
				return err
			}
		}
	}

	return nil
}

func (crd *AnyCredentials) Render() string {
	if crd == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = crd.RenderTo(sb)
	return sb.String()
}

func (crd *AnyCredentials) String() string {
	if crd == nil {
		return nilTag
	}
	return crd.Render()
}

func (crd *AnyCredentials) Equal(val any) bool {
	var other *AnyCredentials
	switch v := val.(type) {
	case AnyCredentials:
		other = &v
	case *AnyCredentials:
		other = v
	default:
		return false
	}

	if crd == other {
		return true
	} else if crd == nil || other == nil {
		return false
	}

	return stringutils.LCase(crd.Scheme) == stringutils.LCase(other.Scheme) &&
		compareHeaderParams(crd.Params, other.Params, nil)
}

func (crd *AnyCredentials) IsValid() bool {
	return crd != nil && grammar.IsToken(crd.Scheme) && len(crd.Params) > 0 &&
		validateHeaderParams(crd.Params)
}
