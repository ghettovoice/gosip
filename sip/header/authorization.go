package header

import (
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
	"github.com/ghettovoice/gosip/sip/uri"
)

type AuthCredentials interface {
	AuthCredentialsScheme() string
	RenderAuthCredentials() string
	RenderAuthCredentialsTo(io.Writer) error
	Clone() AuthCredentials
}

// Authorization is an implementation of the Authorization header.
type Authorization struct {
	AuthCredentials
}

func (hdr *Authorization) HeaderName() string { return "Authorization" }

func (hdr *Authorization) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *Authorization) renderValue(w io.Writer) error {
	if hdr.AuthCredentials != nil {
		if err := hdr.AuthCredentials.RenderAuthCredentialsTo(w); err != nil {
			return err
		}
	}
	return nil
}

func (hdr *Authorization) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr *Authorization) String() string {
	if hdr == nil {
		return "<nil>"
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.renderValue(sb)
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
	node = utils.MustGetNode(node, "credentials")
	switch scheme := node.Children[0].Children[0].String(); utils.LCase(scheme) {
	case "digest":
		crd := &DigestAuthCredentials{}
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
		hdr.AuthCredentials = &BearerAuthCredentials{
			Token: utils.MustGetNode(node, "bearer-response").String(),
		}
	default:
		crd := &GenericAuthCredentials{
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

// DigestAuthCredentials represents the digest authentication credentials.
type DigestAuthCredentials struct {
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

func (crd *DigestAuthCredentials) AuthCredentialsScheme() string { return "Digest" }

func (crd *DigestAuthCredentials) Clone() AuthCredentials {
	if crd == nil {
		return nil
	}
	crd2 := *crd
	crd2.URI = utils.Clone[uri.URI](crd.URI)
	crd2.Params = crd.Params.Clone()
	return &crd2
}

func (crd *DigestAuthCredentials) RenderAuthCredentialsTo(w io.Writer) error {
	if crd == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, crd.AuthCredentialsScheme(), " "); err != nil {
		return err
	}

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
		slices.SortFunc(kvs, utils.CmpKVs)
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
		if err := utils.RenderTo(w, "uri=\"", crd.URI, "\""); err != nil {
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
			kvs = append(kvs, []string{utils.LCase(k), crd.Params.Last(k)})
		}
		slices.SortFunc(kvs, utils.CmpKVs)
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

func (crd *DigestAuthCredentials) RenderAuthCredentials() string {
	if crd == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	crd.RenderAuthCredentialsTo(sb)
	return sb.String()
}

func (crd *DigestAuthCredentials) String() string {
	if crd == nil {
		return "<nil>"
	}
	return crd.RenderAuthCredentials()
}

func (crd *DigestAuthCredentials) Equal(val any) bool {
	var other *DigestAuthCredentials
	switch v := val.(type) {
	case DigestAuthCredentials:
		other = &v
	case *DigestAuthCredentials:
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
		utils.LCase(crd.Realm) == utils.LCase(other.Realm) &&
		crd.Nonce == other.Nonce &&
		crd.Response == other.Response &&
		utils.LCase(crd.Algorithm) == utils.LCase(other.Algorithm) &&
		crd.CNonce == other.CNonce &&
		crd.Opaque == other.Opaque &&
		utils.LCase(crd.QOP) == utils.LCase(other.QOP) &&
		crd.NonceCount == other.NonceCount &&
		utils.IsEqual(crd.URI, other.URI) &&
		compareHeaderParams(crd.Params, other.Params, nil)
}

func (crd *DigestAuthCredentials) IsValid() bool {
	return crd != nil &&
		crd.Username != "" && crd.Realm != "" && crd.Nonce != "" &&
		len(crd.Response) == 32 &&
		(crd.Algorithm == "" || grammar.IsToken(crd.Algorithm)) &&
		(crd.QOP == "" || grammar.IsToken(crd.QOP)) &&
		utils.IsValid(crd.URI) && validateHeaderParams(crd.Params)
}

// BearerAuthCredentials represents the bearer authentication credentials.
type BearerAuthCredentials struct {
	Token string
}

func (crd *BearerAuthCredentials) AuthCredentialsScheme() string { return "Bearer" }

func (crd *BearerAuthCredentials) Clone() AuthCredentials {
	if crd == nil {
		return nil
	}
	crd2 := *crd
	return &crd2
}

func (crd *BearerAuthCredentials) RenderAuthCredentialsTo(w io.Writer) error {
	if crd == nil {
		return nil
	}
	_, err := fmt.Fprint(w, crd.AuthCredentialsScheme(), " ", crd.Token)
	return err
}

func (crd *BearerAuthCredentials) RenderAuthCredentials() string {
	if crd == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	crd.RenderAuthCredentialsTo(sb)
	return sb.String()
}

func (crd *BearerAuthCredentials) String() string {
	if crd == nil {
		return "<nil>"
	}
	return crd.RenderAuthCredentials()
}

func (crd *BearerAuthCredentials) Equal(val any) bool {
	var other *BearerAuthCredentials
	switch v := val.(type) {
	case BearerAuthCredentials:
		other = &v
	case *BearerAuthCredentials:
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

func (crd *BearerAuthCredentials) IsValid() bool { return crd != nil && crd.Token != "" }

// GenericAuthCredentials represents generic authentication credentials.
type GenericAuthCredentials struct {
	Scheme string
	Params Values
}

func (crd *GenericAuthCredentials) AuthCredentialsScheme() string { return crd.Scheme }

func (crd *GenericAuthCredentials) Clone() AuthCredentials {
	if crd == nil {
		return nil
	}
	crd2 := *crd
	crd2.Params = crd.Params.Clone()
	return &crd2
}

func (crd *GenericAuthCredentials) RenderAuthCredentialsTo(w io.Writer) error {
	if crd == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, crd.Scheme, " "); err != nil {
		return err
	}

	kvs := make([][]string, 0, len(crd.Params))
	for k := range crd.Params {
		kvs = append(kvs, []string{utils.LCase(k), crd.Params.Last(k)})
	}
	if len(kvs) > 0 {
		slices.SortFunc(kvs, utils.CmpKVs)
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

func (crd *GenericAuthCredentials) RenderAuthCredentials() string {
	if crd == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	crd.RenderAuthCredentialsTo(sb)
	return sb.String()
}

func (crd *GenericAuthCredentials) String() string {
	if crd == nil {
		return "<nil>"
	}
	return crd.RenderAuthCredentials()
}

func (crd *GenericAuthCredentials) Equal(val any) bool {
	var other *GenericAuthCredentials
	switch v := val.(type) {
	case GenericAuthCredentials:
		other = &v
	case *GenericAuthCredentials:
		other = v
	default:
		return false
	}

	if crd == other {
		return true
	} else if crd == nil || other == nil {
		return false
	}

	return utils.LCase(crd.Scheme) == utils.LCase(other.Scheme) &&
		compareHeaderParams(crd.Params, other.Params, nil)
}

func (crd *GenericAuthCredentials) IsValid() bool {
	return crd != nil &&
		grammar.IsToken(crd.Scheme) &&
		len(crd.Params) > 0 &&
		validateHeaderParams(crd.Params)
}
