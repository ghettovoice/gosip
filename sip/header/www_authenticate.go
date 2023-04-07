package header

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
	"github.com/ghettovoice/gosip/sip/uri"
)

type AuthChallenge interface {
	AuthChallengeScheme() string
	Clone() AuthChallenge
	RenderAuthChallenge() string
	RenderAuthChallengeTo(io.Writer) error
}

type WWWAuthenticate struct {
	AuthChallenge
}

func (hdr *WWWAuthenticate) HeaderName() string { return "WWW-Authenticate" }

func (hdr *WWWAuthenticate) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *WWWAuthenticate) renderValue(w io.Writer) error {
	if hdr.AuthChallenge != nil {
		if err := hdr.AuthChallenge.RenderAuthChallengeTo(w); err != nil {
			return err
		}
	}
	return nil
}

func (hdr *WWWAuthenticate) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr *WWWAuthenticate) String() string {
	if hdr == nil {
		return "<nil>"
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.renderValue(sb)
	return sb.String()
}

func (hdr *WWWAuthenticate) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := *hdr
	hdr2.AuthChallenge = utils.Clone[AuthChallenge](hdr.AuthChallenge)
	return &hdr2
}

func (hdr *WWWAuthenticate) Equal(val any) bool {
	var other *WWWAuthenticate
	switch v := val.(type) {
	case WWWAuthenticate:
		other = &v
	case *WWWAuthenticate:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return utils.IsEqual(hdr.AuthChallenge, other.AuthChallenge)
}

func (hdr *WWWAuthenticate) IsValid() bool {
	return hdr != nil && utils.IsValid(hdr.AuthChallenge)
}

func buildFromWWWAuthenticateNode(node *abnf.Node) *WWWAuthenticate {
	var hdr WWWAuthenticate
	node = utils.MustGetNode(node, "challenge")
	switch scheme := node.Children[0].Children[0].String(); utils.LCase(scheme) {
	case "digest":
		cln := &DigestAuthChallenge{}
		hdr.AuthChallenge = cln
		for _, paramNode := range node.GetNodes("digest-cln") {
			paramNode = paramNode.Children[0]
			switch paramNode.Key {
			case "realm":
				cln.Realm = grammar.Unquote(paramNode.Children[2].String())
			case "domain":
				for _, n := range paramNode.GetNodes("URI") {
					cln.Domain = append(cln.Domain, uri.FromABNF(n.Children[0]))
				}
			case "nonce":
				cln.Nonce = grammar.Unquote(paramNode.Children[2].String())
			case "opaque":
				cln.Opaque = grammar.Unquote(paramNode.Children[2].String())
			case "stale":
				cln.Stale = utils.LCase(paramNode.Children[2].String()) == "true"
			case "algorithm":
				cln.Algorithm = paramNode.Children[2].String()
			case "qop-options":
				for _, n := range paramNode.GetNodes("qop-value") {
					cln.QOP = append(cln.QOP, n.String())
				}
			default:
				if cln.Params == nil {
					cln.Params = make(Values)
				}
				cln.Params.Set(paramNode.Children[0].String(), paramNode.Children[2].String())
			}
		}
	case "bearer":
		cln := &BearerAuthChallenge{}
		hdr.AuthChallenge = cln
		for _, paramNode := range node.GetNodes("bearer-cln") {
			paramNode = paramNode.Children[0]
			switch paramNode.Key {
			case "realm":
				cln.Realm = grammar.Unquote(paramNode.Children[2].String())
			case "scope-param":
				cln.Scope = paramNode.Children[3].String()
			case "authz-server-param":
				cln.AuthzServer = uri.FromABNF(paramNode.Children[3])
			case "error-param":
				cln.Error = paramNode.Children[3].String()
			default:
				if cln.Params == nil {
					cln.Params = make(Values)
				}
				cln.Params.Set(paramNode.Children[0].String(), paramNode.Children[2].String())
			}
		}
	default:
		cln := &GenericAuthChallenge{
			Scheme: node.Children[0].Children[0].String(),
		}
		hdr.AuthChallenge = cln
		for _, paramNode := range node.GetNodes("auth-param") {
			if cln.Params == nil {
				cln.Params = make(Values)
			}
			cln.Params.Set(paramNode.Children[0].String(), paramNode.Children[2].String())
		}
	}
	return &hdr
}

type DigestAuthChallenge struct {
	Realm,
	Nonce,
	Opaque,
	Algorithm string
	Domain []uri.URI
	QOP    []string
	Stale  bool
	Params Values
}

func (cln *DigestAuthChallenge) AuthChallengeScheme() string { return "Digest" }

func (cln *DigestAuthChallenge) Clone() AuthChallenge {
	if cln == nil {
		return nil
	}
	cln2 := *cln
	cln2.QOP = slices.Clone(cln.QOP)
	if cln.Domain != nil {
		cln2.Domain = make([]uri.URI, len(cln.Domain))
		for i := range cln.Domain {
			cln2.Domain[i] = utils.Clone[uri.URI](cln.Domain[i])
		}
	}
	cln2.Params = cln.Params.Clone()
	return &cln2
}

func (cln *DigestAuthChallenge) RenderAuthChallengeTo(w io.Writer) error {
	if cln == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, cln.AuthChallengeScheme(), " "); err != nil {
		return err
	}

	var kvs [][]string
	// resolve and write all non-empty std scalar parameters in alphabet order
	for k, v := range map[string]string{
		"realm":     cln.Realm,
		"nonce":     cln.Nonce,
		"opaque":    cln.Opaque,
		"algorithm": cln.Algorithm,
		"qop":       strings.Join(cln.QOP, ","),
	} {
		if v == "" {
			continue
		}
		switch k {
		case "realm", "nonce", "opaque", "qop":
			v = grammar.Quote(v)
		}
		kvs = append(kvs, []string{k, v})
	}
	if cln.Stale {
		kvs = append(kvs, []string{"stale", "true"})
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

	if len(cln.Domain) > 0 {
		if len(kvs) > 0 {
			if _, err := fmt.Fprint(w, ", "); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprint(w, "domain=\""); err != nil {
			return err
		}
		var j int
		for i := range cln.Domain {
			if cln.Domain[i] == nil {
				continue
			}
			if j > 0 {
				if _, err := fmt.Fprint(w, " "); err != nil {
					return err
				}
			}
			if err := utils.RenderTo(w, cln.Domain[i]); err != nil {
				return err
			}
			j++
		}
		if _, err := fmt.Fprint(w, "\""); err != nil {
			return err
		}
	}

	// append custom parameters if present
	if len(cln.Params) > 0 {
		if len(kvs) > 0 || len(cln.Domain) > 0 {
			if _, err := fmt.Fprint(w, ", "); err != nil {
				return err
			}
		}

		clear(kvs)
		kvs = kvs[:0]
		for k := range cln.Params {
			kvs = append(kvs, []string{utils.LCase(k), cln.Params.Last(k)})
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

func (cln *DigestAuthChallenge) RenderAuthChallenge() string {
	if cln == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	cln.RenderAuthChallengeTo(sb)
	return sb.String()
}

func (cln *DigestAuthChallenge) String() string {
	if cln == nil {
		return "<nil>"
	}
	return cln.RenderAuthChallenge()
}

func (cln *DigestAuthChallenge) Equal(val any) bool {
	var other *DigestAuthChallenge
	switch v := val.(type) {
	case DigestAuthChallenge:
		other = &v
	case *DigestAuthChallenge:
		other = v
	default:
		return false
	}

	if cln == other {
		return true
	} else if cln == nil || other == nil {
		return false
	}

	return utils.LCase(cln.Realm) == utils.LCase(other.Realm) &&
		cln.Nonce == other.Nonce &&
		cln.Opaque == other.Opaque &&
		utils.LCase(cln.Algorithm) == utils.LCase(other.Algorithm) &&
		slices.EqualFunc(cln.Domain, other.Domain, func(v1, v2 uri.URI) bool { return utils.IsEqual(v1, v2) }) &&
		slices.EqualFunc(cln.QOP, other.QOP, func(v1, v2 string) bool { return utils.LCase(v1) == utils.LCase(v2) }) &&
		cln.Stale == other.Stale &&
		compareHeaderParams(cln.Params, other.Params, nil)
}

func (cln *DigestAuthChallenge) IsValid() bool {
	return cln != nil &&
		cln.Realm != "" && cln.Nonce != "" &&
		(cln.Algorithm == "" || grammar.IsToken(cln.Algorithm)) &&
		!slices.ContainsFunc(cln.QOP, func(v string) bool { return !grammar.IsToken(v) }) &&
		!slices.ContainsFunc(cln.Domain, func(v uri.URI) bool { return !utils.IsValid(v) }) &&
		validateHeaderParams(cln.Params)
}

// BearerAuthChallenge represents a bearer authentication challenge.
type BearerAuthChallenge struct {
	Realm,
	Scope,
	Error string
	AuthzServer uri.URI
	Params      Values
}

func (cln *BearerAuthChallenge) AuthChallengeScheme() string { return "Bearer" }

func (cln *BearerAuthChallenge) Clone() AuthChallenge {
	if cln == nil {
		return nil
	}
	cln2 := *cln
	cln2.AuthzServer = utils.Clone[uri.URI](cln.AuthzServer)
	cln2.Params = cln.Params.Clone()
	return &cln2
}

func (cln *BearerAuthChallenge) RenderAuthChallengeTo(w io.Writer) error {
	if cln == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, cln.AuthChallengeScheme(), " "); err != nil {
		return err
	}

	// write std parameters
	var kvs [][]string
	for k, v := range map[string]string{
		"realm": cln.Realm,
		"scope": cln.Scope,
		"error": cln.Error,
	} {
		if v == "" {
			continue
		}
		switch k {
		case "realm", "scope", "error":
			v = grammar.Quote(v)
		}
		kvs = append(kvs, []string{k, v})
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

	if cln.AuthzServer != nil {
		if len(kvs) > 0 {
			if _, err := fmt.Fprint(w, ", "); err != nil {
				return err
			}
		}
		if err := utils.RenderTo(w, "authz_server=\"", cln.AuthzServer, "\""); err != nil {
			return err
		}
	}

	// append custom parameters if present
	if len(cln.Params) > 0 {
		if len(kvs) > 0 || cln.AuthzServer != nil {
			if _, err := fmt.Fprint(w, ", "); err != nil {
				return err
			}
		}

		clear(kvs)
		kvs = kvs[:0]
		for k := range cln.Params {
			kvs = append(kvs, []string{utils.LCase(k), cln.Params.Last(k)})
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

func (cln *BearerAuthChallenge) RenderAuthChallenge() string {
	if cln == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	cln.RenderAuthChallengeTo(sb)
	return sb.String()
}

func (cln *BearerAuthChallenge) String() string {
	if cln == nil {
		return "<nil>"
	}
	return cln.RenderAuthChallenge()
}

func (cln *BearerAuthChallenge) Equal(val any) bool {
	var other *BearerAuthChallenge
	switch v := val.(type) {
	case BearerAuthChallenge:
		other = &v
	case *BearerAuthChallenge:
		other = v
	default:
		return false
	}

	if cln == other {
		return true
	} else if cln == nil || other == nil {
		return false
	}

	return utils.LCase(cln.Realm) == utils.LCase(other.Realm) &&
		cln.Scope == other.Scope &&
		cln.Error == other.Error &&
		utils.IsEqual(cln.AuthzServer, other.AuthzServer) &&
		compareHeaderParams(cln.Params, other.Params, nil)
}

func (cln *BearerAuthChallenge) IsValid() bool {
	return cln != nil && utils.IsValid(cln.AuthzServer) && validateHeaderParams(cln.Params)
}

// GenericAuthChallenge represents a miscellaneous authentication challenge.
type GenericAuthChallenge struct {
	Scheme string
	Params Values
}

func (cln *GenericAuthChallenge) AuthChallengeScheme() string { return cln.Scheme }

func (cln *GenericAuthChallenge) Clone() AuthChallenge {
	if cln == nil {
		return nil
	}
	cln2 := *cln
	cln2.Params = cln.Params.Clone()
	return &cln2
}

func (cln *GenericAuthChallenge) RenderAuthChallengeTo(w io.Writer) error {
	if cln == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, cln.Scheme, " "); err != nil {
		return err
	}

	kvs := make([][]string, 0, len(cln.Params))
	for k := range cln.Params {
		kvs = append(kvs, []string{utils.LCase(k), cln.Params.Last(k)})
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

func (cln *GenericAuthChallenge) RenderAuthChallenge() string {
	if cln == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	cln.RenderAuthChallengeTo(sb)
	return sb.String()
}

func (cln *GenericAuthChallenge) String() string {
	if cln == nil {
		return "<nil>"
	}
	return cln.RenderAuthChallenge()
}

func (cln *GenericAuthChallenge) Equal(val any) bool {
	var other *GenericAuthChallenge
	switch v := val.(type) {
	case GenericAuthChallenge:
		other = &v
	case *GenericAuthChallenge:
		other = v
	default:
		return false
	}

	if cln == other {
		return true
	} else if cln == nil || other == nil {
		return false
	}

	return utils.LCase(cln.Scheme) == utils.LCase(other.Scheme) &&
		compareHeaderParams(cln.Params, other.Params, nil)
}

func (cln *GenericAuthChallenge) IsValid() bool {
	return cln != nil &&
		grammar.IsToken(cln.Scheme) &&
		len(cln.Params) > 0 &&
		validateHeaderParams(cln.Params)
}
