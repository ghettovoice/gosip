package header

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/uri"
)

type AuthChallenge interface {
	types.Renderer
	types.ValidFlag
	types.Equalable
	types.Cloneable[AuthChallenge]
}

type WWWAuthenticate struct {
	AuthChallenge
}

func (*WWWAuthenticate) CanonicName() Name { return "WWW-Authenticate" }

func (*WWWAuthenticate) CompactName() Name { return "WWW-Authenticate" }

func (hdr *WWWAuthenticate) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(func(w io.Writer) (int, error) {
		return errtrace.Wrap2(hdr.renderValueTo(w, opts))
	})
	return errtrace.Wrap2(cw.Result())
}

func (hdr *WWWAuthenticate) renderValueTo(w io.Writer, opts *RenderOptions) (num int, err error) {
	if hdr.AuthChallenge == nil {
		return 0, nil
	}
	return errtrace.Wrap2(hdr.AuthChallenge.RenderTo(w, opts))
}

func (hdr *WWWAuthenticate) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (hdr *WWWAuthenticate) RenderValue() string {
	if hdr == nil || hdr.AuthChallenge == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb, nil) //nolint:errcheck
	return sb.String()
}

func (hdr *WWWAuthenticate) String() string { return hdr.RenderValue() }

func (hdr *WWWAuthenticate) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			hdr.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, hdr.String())
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(hdr.Render(nil)))
			return
		}
		fmt.Fprint(f, strconv.Quote(hdr.String()))
		return
	default:
		type hideMethods WWWAuthenticate
		type WWWAuthenticate hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*WWWAuthenticate)(hdr))
		return
	}
}

func (hdr *WWWAuthenticate) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := *hdr
	hdr2.AuthChallenge = types.Clone[AuthChallenge](hdr.AuthChallenge)
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

	return types.IsEqual(hdr.AuthChallenge, other.AuthChallenge)
}

func (hdr *WWWAuthenticate) IsValid() bool {
	return hdr != nil && types.IsValid(hdr.AuthChallenge)
}

func (hdr *WWWAuthenticate) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

var zeroWWWAuthenticate WWWAuthenticate

func (hdr *WWWAuthenticate) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = zeroWWWAuthenticate
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(*WWWAuthenticate)
	if !ok {
		*hdr = zeroWWWAuthenticate
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, hdr))
	}

	*hdr = *h
	return nil
}

//nolint:gocognit
func buildFromWWWAuthenticateNode(node *abnf.Node) *WWWAuthenticate {
	var hdr WWWAuthenticate
	node = grammar.MustGetNode(node, "challenge")
	switch scheme := node.Children[0].Children[0].String(); util.LCase(scheme) {
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
				cln.Stale = util.EqFold(paramNode.Children[2].String(), "true")
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
			cln2.Domain[i] = types.Clone[uri.URI](cln.Domain[i])
		}
	}
	cln2.Params = cln.Params.Clone()
	return &cln2
}

//nolint:gocognit
func (cln *DigestChallenge) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
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
		slices.SortFunc(kvs, util.CmpKVs)
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
				return errtrace.Wrap2(cln.Domain[i].RenderTo(w, opts))
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
			kvs = append(kvs, []string{util.LCase(k), v})
		}
		slices.SortFunc(kvs, util.CmpKVs)

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

	return errtrace.Wrap2(cw.Result())
}

func (cln *DigestChallenge) Render(opts *RenderOptions) string {
	if cln == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	cln.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (cln *DigestChallenge) String() string {
	if cln == nil {
		return ""
	}
	return cln.Render(nil)
}

func (cln *DigestChallenge) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			cln.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, cln.String())
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(cln.Render(nil)))
			return
		}
		fmt.Fprint(f, strconv.Quote(cln.String()))
		return
	default:
		type hideMethods DigestChallenge
		type DigestChallenge hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*DigestChallenge)(cln))
		return
	}
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

	return util.EqFold(cln.Realm, other.Realm) &&
		cln.Nonce == other.Nonce &&
		cln.Opaque == other.Opaque &&
		util.EqFold(cln.Algorithm, other.Algorithm) &&
		slices.EqualFunc(cln.Domain, other.Domain, func(v1, v2 uri.URI) bool { return types.IsEqual(v1, v2) }) &&
		slices.EqualFunc(cln.QOP, other.QOP, util.EqFold) &&
		cln.Stale == other.Stale &&
		compareHdrParams(cln.Params, other.Params, nil)
}

func (cln *DigestChallenge) IsValid() bool {
	return cln != nil &&
		cln.Realm != "" && cln.Nonce != "" &&
		(cln.Algorithm == "" || grammar.IsToken(cln.Algorithm)) &&
		!slices.ContainsFunc(cln.QOP, func(v string) bool { return !grammar.IsToken(v) }) &&
		!slices.ContainsFunc(cln.Domain, func(v uri.URI) bool { return !types.IsValid(v) }) &&
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
	cln2 := *cln
	cln2.AuthzServer = types.Clone[uri.URI](cln.AuthzServer)
	cln2.Params = cln.Params.Clone()
	return &cln2
}

func (cln *BearerChallenge) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
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
		slices.SortFunc(kvs, util.CmpKVs)
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
			return errtrace.Wrap2(cln.AuthzServer.RenderTo(w, opts))
		})

		cw.Fprint("\"")
	}

	// append custom parameters if present
	if len(cln.Params) > 0 {
		clear(kvs)
		kvs = kvs[:0]
		for k := range cln.Params {
			v, _ := cln.Params.Last(k)
			kvs = append(kvs, []string{util.LCase(k), v})
		}
		slices.SortFunc(kvs, util.CmpKVs)

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

	return errtrace.Wrap2(cw.Result())
}

func (cln *BearerChallenge) Render(opts *RenderOptions) string {
	if cln == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	cln.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (cln *BearerChallenge) String() string {
	if cln == nil {
		return ""
	}
	return cln.Render(nil)
}

func (cln *BearerChallenge) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			cln.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, cln.String())
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(cln.Render(nil)))
			return
		}
		fmt.Fprint(f, strconv.Quote(cln.String()))
		return
	default:
		type hideMethods BearerChallenge
		type BearerChallenge hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*BearerChallenge)(cln))
		return
	}
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

	return util.EqFold(cln.Realm, other.Realm) &&
		cln.Scope == other.Scope &&
		cln.Error == other.Error &&
		types.IsEqual(cln.AuthzServer, other.AuthzServer) &&
		compareHdrParams(cln.Params, other.Params, nil)
}

func (cln *BearerChallenge) IsValid() bool {
	return cln != nil && types.IsValid(cln.AuthzServer) && validateHdrParams(cln.Params)
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

func (cln *AnyChallenge) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if cln == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(cln.Scheme, " ")

	kvs := make([][]string, 0, len(cln.Params))
	for k := range cln.Params {
		v, _ := cln.Params.Last(k)
		kvs = append(kvs, []string{util.LCase(k), v})
	}
	if len(kvs) > 0 {
		slices.SortFunc(kvs, util.CmpKVs)
		for i, kv := range kvs {
			if i > 0 {
				cw.Fprint(", ")
			}
			cw.Fprint(kv[0], "=", kv[1])
		}
	}
	return errtrace.Wrap2(cw.Result())
}

func (cln *AnyChallenge) Render(opts *RenderOptions) string {
	if cln == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	cln.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (cln *AnyChallenge) String() string {
	if cln == nil {
		return ""
	}
	return cln.Render(nil)
}

func (cln *AnyChallenge) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			cln.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, cln.String())
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(cln.Render(nil)))
			return
		}
		fmt.Fprint(f, strconv.Quote(cln.String()))
		return
	default:
		type hideMethods AnyChallenge
		type AnyChallenge hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*AnyChallenge)(cln))
		return
	}
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

	return util.EqFold(cln.Scheme, other.Scheme) &&
		compareHdrParams(cln.Params, other.Params, nil)
}

func (cln *AnyChallenge) IsValid() bool {
	return cln != nil &&
		grammar.IsToken(cln.Scheme) &&
		len(cln.Params) > 0 &&
		validateHdrParams(cln.Params)
}
