package header

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/uri"
)

type AuthChallenge interface {
	shared.Renderer
	shared.ValidFlag
	shared.Equalable
	shared.Cloneable[AuthChallenge]
}

type WWWAuthenticate struct {
	AuthChallenge
}

func (*WWWAuthenticate) CanonicName() Name { return "WWW-Authenticate" }

func (*WWWAuthenticate) CompactName() Name { return "WWW-Authenticate" }

func (hdr *WWWAuthenticate) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr *WWWAuthenticate) renderValTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderTo(w, hdr.AuthChallenge, opts...))
}

func (hdr *WWWAuthenticate) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr *WWWAuthenticate) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderHdrVal(hdr)
}

func (hdr *WWWAuthenticate) String() string {
	return hdr.RenderValue()
}

func (hdr *WWWAuthenticate) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods     WWWAuthenticate
		WWWAuthenticate hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*WWWAuthenticate)(hdr))
}

func (hdr *WWWAuthenticate) Clone() Header {
	if hdr == nil {
		return nil
	}

	clone := *hdr
	clone.AuthChallenge = shared.Clone[AuthChallenge](hdr.AuthChallenge)
	return &clone
}

func (hdr *WWWAuthenticate) Equal(val any) bool {
	return util.EqualPointer(hdr, val, func(other *WWWAuthenticate) bool {
		return shared.IsEqual(hdr.AuthChallenge, other.AuthChallenge)
	})
}

func (hdr *WWWAuthenticate) IsValid() bool {
	return hdr != nil && shared.IsValid(hdr.AuthChallenge)
}

func (hdr *WWWAuthenticate) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *WWWAuthenticate) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromWWWAuthenticateNode(node *abnf.Node) *WWWAuthenticate {
	return &WWWAuthenticate{AuthChallenge: buildAuthChallengeFromNode(node)}
}

//nolint:gocognit // per-scheme challenge building per spec
func buildAuthChallengeFromNode(node *abnf.Node) AuthChallenge {
	var challenge AuthChallenge
	node = grammar.MustGetNode(node, "challenge")
	switch scheme := node.Children[0].Children[0].String(); util.ToLower(scheme) {
	case "digest":
		cln := &DigestChallenge{}
		challenge = cln
		for _, paramNode := range node.GetNodes("digest-cln") {
			paramNode = paramNode.Children[0]
			switch paramNode.Key {
			case "realm":
				cln.Realm = grammar.Unquote(paramNode.Children[2].String())
			case "domain":
				for _, n := range paramNode.GetNodes("URI") {
					cln.Domain = append(cln.Domain, util.Must2(uri.FromABNF(n.Children[0])))
				}
			case "nonce":
				cln.Nonce = grammar.Unquote(paramNode.Children[2].String())
			case "opaque":
				cln.Opaque = grammar.Unquote(paramNode.Children[2].String())
			case "stale":
				cln.Stale = util.EqualFold(paramNode.Children[2].String(), "true")
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
		challenge = cln
		for _, paramNode := range node.GetNodes("bearer-cln") {
			paramNode = paramNode.Children[0]
			switch paramNode.Key {
			case "realm":
				cln.Realm = grammar.Unquote(paramNode.Children[2].String())
			case "scope-param":
				cln.Scope = paramNode.Children[3].String()
			case "authz-server-param":
				cln.AuthzServer = util.Must2(uri.FromABNF(paramNode.Children[3]))
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
		challenge = cln
		for _, paramNode := range node.GetNodes("auth-param") {
			if cln.Params == nil {
				cln.Params = make(Values)
			}
			cln.Params.Set(paramNode.Children[0].String(), paramNode.Children[2].String())
		}
	}
	return challenge
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

	clone := *cln
	clone.QOP = slices.Clone(cln.QOP)
	if cln.Domain != nil {
		clone.Domain = make([]uri.URI, len(cln.Domain))
		for i := range cln.Domain {
			clone.Domain[i] = shared.Clone[uri.URI](cln.Domain[i])
		}
	}
	clone.Params = cln.Params.Clone()
	return &clone
}

//nolint:gocognit // per-field rendering per spec
func (cln *DigestChallenge) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if cln == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint("Digest ")

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
		slices.SortFunc(kvs, util.CompareKeys)
		for i, kv := range kvs {
			if i > 0 {
				cw.Fprint(", ")
			}
			cw.Fprint(kv[0], "=", kv[1])
		}
	}

	if len(cln.Domain) > 0 {
		if len(kvs) > 0 {
			cw.Fprint(", ")
		}

		cw.Fprint("domain=\"")

		var j int
		for i := range cln.Domain {
			if cln.Domain[i] == nil {
				continue
			}

			if j > 0 {
				cw.Fprint(" ")
			}
			cw.Call(func(w io.Writer) (int, error) {
				return errors.Wrap2(cln.Domain[i].RenderTo(w, opts...))
			})
			j++
		}

		cw.Fprint("\"")
	}

	// append custom parameters if present
	if len(cln.Params) > 0 {
		clear(kvs)
		kvs = kvs[:0]
		for k := range cln.Params {
			v, _ := cln.Params.Last(k)
			kvs = append(kvs, []string{util.ToLower(k), v})
		}
		slices.SortFunc(kvs, util.CompareKeys)
		if len(kvs) > 0 || len(cln.Domain) > 0 {
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

func (cln *DigestChallenge) Render(opts ...RenderOptions) string {
	return render(cln, opts...)
}

func (cln *DigestChallenge) String() string {
	if cln == nil {
		return ""
	}
	return cln.Render()
}

func (cln *DigestChallenge) Format(f fmt.State, verb rune) {
	if formatRndr(f, verb, cln) {
		return
	}
	type (
		hideMethods     DigestChallenge
		DigestChallenge hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*DigestChallenge)(cln))
}

func (cln *DigestChallenge) Equal(val any) bool {
	return util.EqualPointer(cln, val, func(other *DigestChallenge) bool {
		return util.EqualFold(cln.Realm, other.Realm) &&
			cln.Nonce == other.Nonce &&
			cln.Opaque == other.Opaque &&
			util.EqualFold(cln.Algorithm, other.Algorithm) &&
			slices.EqualFunc(cln.Domain, other.Domain, func(v1, v2 uri.URI) bool {
				return shared.IsEqual(v1, v2)
			}) &&
			slices.EqualFunc(cln.QOP, other.QOP, util.EqualFold) &&
			cln.Stale == other.Stale &&
			compareHdrParams(cln.Params, other.Params, nil)
	})
}

func (cln *DigestChallenge) IsValid() bool {
	return cln != nil &&
		cln.Realm != "" &&
		cln.Nonce != "" &&
		(cln.Algorithm == "" || grammar.IsToken(cln.Algorithm)) &&
		!slices.ContainsFunc(cln.QOP, func(v string) bool {
			return !grammar.IsToken(v)
		}) &&
		!slices.ContainsFunc(cln.Domain, func(v uri.URI) bool {
			return !shared.IsValid(v)
		}) &&
		validateHdrParams(cln.Params)
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

	clone := *cln
	clone.AuthzServer = shared.Clone[uri.URI](cln.AuthzServer)
	clone.Params = cln.Params.Clone()
	return &clone
}

//nolint:gocognit // per-field rendering per spec
func (cln *BearerChallenge) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if cln == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint("Bearer ")

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
		slices.SortFunc(kvs, util.CompareKeys)
		for i, kv := range kvs {
			if i > 0 {
				cw.Fprint(", ")
			}
			cw.Fprint(kv[0], "=", kv[1])
		}
	}

	if cln.AuthzServer != nil {
		if len(kvs) > 0 {
			cw.Fprint(", ")
		}
		cw.Fprint("authz_server=\"")
		cw.Call(func(w io.Writer) (int, error) {
			return errors.Wrap2(cln.AuthzServer.RenderTo(w, opts...))
		})
		cw.Fprint("\"")
	}

	// append custom parameters if present
	if len(cln.Params) > 0 {
		clear(kvs)
		kvs = kvs[:0]
		for k := range cln.Params {
			v, _ := cln.Params.Last(k)
			kvs = append(kvs, []string{util.ToLower(k), v})
		}
		slices.SortFunc(kvs, util.CompareKeys)
		if len(kvs) > 0 || cln.AuthzServer != nil {
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

func (cln *BearerChallenge) Render(opts ...RenderOptions) string {
	return render(cln, opts...)
}

func (cln *BearerChallenge) String() string {
	if cln == nil {
		return ""
	}
	return cln.Render()
}

func (cln *BearerChallenge) Format(f fmt.State, verb rune) {
	if formatRndr(f, verb, cln) {
		return
	}
	type (
		hideMethods     BearerChallenge
		BearerChallenge hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*BearerChallenge)(cln))
}

func (cln *BearerChallenge) Equal(val any) bool {
	return util.EqualPointer(cln, val, func(other *BearerChallenge) bool {
		return util.EqualFold(cln.Realm, other.Realm) &&
			cln.Scope == other.Scope &&
			cln.Error == other.Error &&
			shared.IsEqual(cln.AuthzServer, other.AuthzServer) &&
			compareHdrParams(cln.Params, other.Params, nil)
	})
}

func (cln *BearerChallenge) IsValid() bool {
	return cln != nil &&
		shared.IsValid(cln.AuthzServer) &&
		validateHdrParams(cln.Params)
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

	clone := *cln
	clone.Params = cln.Params.Clone()
	return &clone
}

func (cln *AnyChallenge) RenderTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	if cln == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(cln.Scheme, " ")

	kvs := make([][]string, 0, len(cln.Params))
	for k := range cln.Params {
		v, _ := cln.Params.Last(k)
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

func (cln *AnyChallenge) Render(opts ...RenderOptions) string {
	return render(cln, opts...)
}

func (cln *AnyChallenge) String() string {
	if cln == nil {
		return ""
	}
	return cln.Render()
}

func (cln *AnyChallenge) Format(f fmt.State, verb rune) {
	if formatRndr(f, verb, cln) {
		return
	}
	type (
		hideMethods  AnyChallenge
		AnyChallenge hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*AnyChallenge)(cln))
}

func (cln *AnyChallenge) Equal(val any) bool {
	return util.EqualPointer(cln, val, func(other *AnyChallenge) bool {
		return util.EqualFold(cln.Scheme, other.Scheme) &&
			compareHdrParams(cln.Params, other.Params, nil)
	})
}

func (cln *AnyChallenge) IsValid() bool {
	return cln != nil &&
		grammar.IsToken(cln.Scheme) &&
		len(cln.Params) > 0 &&
		validateHdrParams(cln.Params)
}
