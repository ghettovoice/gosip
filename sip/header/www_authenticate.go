package header

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
	"github.com/ghettovoice/gosip/sip/uri"
)

type AuthChallenge interface {
	Render() string
	RenderTo(w io.Writer) error
	Clone() AuthChallenge
}

type WWWAuthenticate struct {
	AuthChallenge
}

func (*WWWAuthenticate) CanonicName() Name { return "WWW-Authenticate" }

func (hdr *WWWAuthenticate) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *WWWAuthenticate) renderValue(w io.Writer) error {
	if hdr.AuthChallenge != nil {
		if err := hdr.AuthChallenge.RenderTo(w); err != nil {
			return err
		}
	}
	return nil
}

func (hdr *WWWAuthenticate) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr *WWWAuthenticate) String() string {
	if hdr == nil {
		return nilTag
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.renderValue(sb)
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

//nolint:gocognit
func buildFromWWWAuthenticateNode(node *abnf.Node) *WWWAuthenticate {
	var hdr WWWAuthenticate
	node = abnfutils.MustGetNode(node, "challenge")
	switch scheme := node.Children[0].Children[0].String(); stringutils.LCase(scheme) {
	case "digest":
		cln := &DigestChallenge{}
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
				cln.Stale = stringutils.LCase(paramNode.Children[2].String()) == "true"
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
		cln := &BearerChallenge{}
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
		cln := &AnyChallenge{
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

type DigestChallenge struct {
	Realm,
	Nonce,
	Opaque,
	Algorithm string
	Domain []uri.URI
	QOP    []string
	Stale  bool
	Params Values
}

func (cln *DigestChallenge) Clone() AuthChallenge {
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

//nolint:gocognit
func (cln *DigestChallenge) RenderTo(w io.Writer) error {
	if cln == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, "Digest "); err != nil {
		return err
	}

	var kvs [][]string //nolint:prealloc
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
			if err := stringutils.RenderTo(w, cln.Domain[i]); err != nil {
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
			kvs = append(kvs, []string{stringutils.LCase(k), cln.Params.Last(k)})
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

func (cln *DigestChallenge) Render() string {
	if cln == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = cln.RenderTo(sb)
	return sb.String()
}

func (cln *DigestChallenge) String() string {
	if cln == nil {
		return nilTag
	}
	return cln.Render()
}

func (cln *DigestChallenge) Equal(val any) bool {
	var other *DigestChallenge
	switch v := val.(type) {
	case DigestChallenge:
		other = &v
	case *DigestChallenge:
		other = v
	default:
		return false
	}

	if cln == other {
		return true
	} else if cln == nil || other == nil {
		return false
	}

	return stringutils.LCase(cln.Realm) == stringutils.LCase(other.Realm) &&
		cln.Nonce == other.Nonce &&
		cln.Opaque == other.Opaque &&
		stringutils.LCase(cln.Algorithm) == stringutils.LCase(other.Algorithm) &&
		slices.EqualFunc(cln.Domain, other.Domain, func(v1, v2 uri.URI) bool { return utils.IsEqual(v1, v2) }) &&
		slices.EqualFunc(cln.QOP, other.QOP, func(v1, v2 string) bool { return stringutils.LCase(v1) == stringutils.LCase(v2) }) &&
		cln.Stale == other.Stale &&
		compareHeaderParams(cln.Params, other.Params, nil)
}

func (cln *DigestChallenge) IsValid() bool {
	return cln != nil &&
		cln.Realm != "" && cln.Nonce != "" &&
		(cln.Algorithm == "" || grammar.IsToken(cln.Algorithm)) &&
		!slices.ContainsFunc(cln.QOP, func(v string) bool { return !grammar.IsToken(v) }) &&
		!slices.ContainsFunc(cln.Domain, func(v uri.URI) bool { return !utils.IsValid(v) }) &&
		validateHeaderParams(cln.Params)
}

// BearerChallenge represents a bearer authentication challenge.
type BearerChallenge struct {
	Realm,
	Scope,
	Error string
	AuthzServer uri.URI
	Params      Values
}

func (cln *BearerChallenge) Clone() AuthChallenge {
	if cln == nil {
		return nil
	}
	cln2 := *cln
	cln2.AuthzServer = utils.Clone[uri.URI](cln.AuthzServer)
	cln2.Params = cln.Params.Clone()
	return &cln2
}

//nolint:gocognit
func (cln *BearerChallenge) RenderTo(w io.Writer) error {
	if cln == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, "Bearer "); err != nil {
		return err
	}

	// write std parameters
	var kvs [][]string //nolint:prealloc
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

	if cln.AuthzServer != nil {
		if len(kvs) > 0 {
			if _, err := fmt.Fprint(w, ", "); err != nil {
				return err
			}
		}
		if err := stringutils.RenderTo(w, "authz_server=\"", cln.AuthzServer, "\""); err != nil {
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
			kvs = append(kvs, []string{stringutils.LCase(k), cln.Params.Last(k)})
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

func (cln *BearerChallenge) Render() string {
	if cln == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = cln.RenderTo(sb)
	return sb.String()
}

func (cln *BearerChallenge) String() string {
	if cln == nil {
		return nilTag
	}
	return cln.Render()
}

func (cln *BearerChallenge) Equal(val any) bool {
	var other *BearerChallenge
	switch v := val.(type) {
	case BearerChallenge:
		other = &v
	case *BearerChallenge:
		other = v
	default:
		return false
	}

	if cln == other {
		return true
	} else if cln == nil || other == nil {
		return false
	}

	return stringutils.LCase(cln.Realm) == stringutils.LCase(other.Realm) &&
		cln.Scope == other.Scope &&
		cln.Error == other.Error &&
		utils.IsEqual(cln.AuthzServer, other.AuthzServer) &&
		compareHeaderParams(cln.Params, other.Params, nil)
}

func (cln *BearerChallenge) IsValid() bool {
	return cln != nil && utils.IsValid(cln.AuthzServer) && validateHeaderParams(cln.Params)
}

// AnyChallenge represents a generic authentication challenge.
type AnyChallenge struct {
	Scheme string
	Params Values
}

func (cln *AnyChallenge) Clone() AuthChallenge {
	if cln == nil {
		return nil
	}
	cln2 := *cln
	cln2.Params = cln.Params.Clone()
	return &cln2
}

func (cln *AnyChallenge) RenderTo(w io.Writer) error {
	if cln == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, cln.Scheme, " "); err != nil {
		return err
	}

	kvs := make([][]string, 0, len(cln.Params))
	for k := range cln.Params {
		kvs = append(kvs, []string{stringutils.LCase(k), cln.Params.Last(k)})
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

func (cln *AnyChallenge) Render() string {
	if cln == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = cln.RenderTo(sb)
	return sb.String()
}

func (cln *AnyChallenge) String() string {
	if cln == nil {
		return nilTag
	}
	return cln.Render()
}

func (cln *AnyChallenge) Equal(val any) bool {
	var other *AnyChallenge
	switch v := val.(type) {
	case AnyChallenge:
		other = &v
	case *AnyChallenge:
		other = v
	default:
		return false
	}

	if cln == other {
		return true
	} else if cln == nil || other == nil {
		return false
	}

	return stringutils.LCase(cln.Scheme) == stringutils.LCase(other.Scheme) &&
		compareHeaderParams(cln.Params, other.Params, nil)
}

func (cln *AnyChallenge) IsValid() bool {
	return cln != nil &&
		grammar.IsToken(cln.Scheme) &&
		len(cln.Params) > 0 &&
		validateHeaderParams(cln.Params)
}
