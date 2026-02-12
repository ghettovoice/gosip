package header

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/uri"
)

type AuthCredentials interface {
	types.Renderer
	types.Equalable
	types.ValidFlag
	types.Cloneable[AuthCredentials]
}

// Authorization is an implementation of the Authorization header.
type Authorization struct {
	AuthCredentials
}

func (*Authorization) CanonicName() Name { return "Authorization" }

func (*Authorization) CompactName() Name { return "Authorization" }

func (hdr *Authorization) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
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

func (hdr *Authorization) renderValueTo(w io.Writer, opts *RenderOptions) (num int, err error) {
	if hdr.AuthCredentials == nil {
		return 0, nil
	}
	return errtrace.Wrap2(hdr.AuthCredentials.RenderTo(w, opts))
}

func (hdr *Authorization) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (hdr *Authorization) RenderValue() string {
	if hdr == nil || hdr.AuthCredentials == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb, nil) //nolint:errcheck
	return sb.String()
}

func (hdr *Authorization) String() string { return hdr.RenderValue() }

func (hdr *Authorization) Format(f fmt.State, verb rune) {
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
		type hideMethods Authorization
		type Authorization hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*Authorization)(hdr))
		return
	}
}

func (hdr *Authorization) Clone() Header {
	if hdr == nil {
		return nil
	}

	hdr2 := *hdr
	hdr2.AuthCredentials = types.Clone[AuthCredentials](hdr.AuthCredentials)
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

	return types.IsEqual(hdr.AuthCredentials, other.AuthCredentials)
}

func (hdr *Authorization) IsValid() bool {
	return hdr != nil && types.IsValid(hdr.AuthCredentials)
}

func (hdr *Authorization) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

var zeroAuthorization Authorization

func (hdr *Authorization) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = zeroAuthorization
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(*Authorization)
	if !ok {
		*hdr = zeroAuthorization
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, hdr))
	}

	*hdr = *h
	return nil
}

func buildFromAuthorizationNode(node *abnf.Node) *Authorization {
	var hdr Authorization
	node = grammar.MustGetNode(node, "credentials")
	switch scheme := node.Children[0].Children[0].String(); util.LCase(scheme) {
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
				crd.URI = uri.FromABNF(grammar.MustGetNode(paramNode, "Request-URI").Children[0])
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
			Token: grammar.MustGetNode(node, "bearer-response").String(),
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
	crd2.URI = types.Clone[uri.URI](crd.URI)
	crd2.Params = crd.Params.Clone()
	return &crd2
}

func (crd *DigestCredentials) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
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
		slices.SortFunc(kvs, util.CmpKVs)
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
			return errtrace.Wrap2(crd.URI.RenderTo(w, opts))
		})
		cw.Fprint("\"")
	}

	// append custom parameters if present
	if len(crd.Params) > 0 {
		clear(kvs)
		kvs = kvs[:0]
		for k := range crd.Params {
			v, _ := crd.Params.Last(k)
			kvs = append(kvs, []string{util.LCase(k), v})
		}
		slices.SortFunc(kvs, util.CmpKVs)

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

	return errtrace.Wrap2(cw.Result())
}

func (crd *DigestCredentials) Render(opts *RenderOptions) string {
	if crd == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	crd.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (crd *DigestCredentials) String() string { return crd.Render(nil) }

func (crd *DigestCredentials) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			crd.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, crd.String())
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(crd.Render(nil)))
			return
		}
		fmt.Fprint(f, strconv.Quote(crd.String()))
		return
	default:
		type hideMethods DigestCredentials
		type DigestCredentials hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*DigestCredentials)(crd))
		return
	}
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
		util.EqFold(crd.Realm, other.Realm) &&
		crd.Nonce == other.Nonce &&
		crd.Response == other.Response &&
		util.EqFold(crd.Algorithm, other.Algorithm) &&
		crd.CNonce == other.CNonce &&
		crd.Opaque == other.Opaque &&
		util.EqFold(crd.QOP, other.QOP) &&
		crd.NonceCount == other.NonceCount &&
		types.IsEqual(crd.URI, other.URI) &&
		compareHdrParams(crd.Params, other.Params, nil)
}

func (crd *DigestCredentials) IsValid() bool {
	return crd != nil &&
		crd.Username != "" && crd.Realm != "" && crd.Nonce != "" &&
		len(crd.Response) == 32 &&
		(crd.Algorithm == "" || grammar.IsToken(crd.Algorithm)) &&
		(crd.QOP == "" || grammar.IsToken(crd.QOP)) &&
		types.IsValid(crd.URI) && validateHdrParams(crd.Params)
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

func (crd *BearerCredentials) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if crd == nil {
		return 0, nil
	}
	return errtrace.Wrap2(fmt.Fprint(w, "Bearer ", crd.Token))
}

func (crd *BearerCredentials) Render(opts *RenderOptions) string {
	if crd == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	crd.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (crd *BearerCredentials) String() string { return crd.Render(nil) }

func (crd *BearerCredentials) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			crd.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, crd.String())
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(crd.Render(nil)))
			return
		}
		fmt.Fprint(f, strconv.Quote(crd.String()))
		return
	default:
		type hideMethods BearerCredentials
		type BearerCredentials hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*BearerCredentials)(crd))
		return
	}
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

func (crd *AnyCredentials) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if crd == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(crd.Scheme, " ")

	kvs := make([][]string, 0, len(crd.Params))
	for k := range crd.Params {
		v, _ := crd.Params.Last(k)
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

func (crd *AnyCredentials) Render(opts *RenderOptions) string {
	if crd == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	crd.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (crd *AnyCredentials) String() string { return crd.Render(nil) }

func (crd *AnyCredentials) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			crd.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, crd.String())
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(crd.Render(nil)))
			return
		}
		fmt.Fprint(f, strconv.Quote(crd.String()))
		return
	default:
		type hideMethods AnyCredentials
		type AnyCredentials hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*AnyCredentials)(crd))
		return
	}
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

	return util.EqFold(crd.Scheme, other.Scheme) &&
		compareHdrParams(crd.Params, other.Params, nil)
}

func (crd *AnyCredentials) IsValid() bool {
	return crd != nil &&
		grammar.IsToken(crd.Scheme) &&
		len(crd.Params) > 0 &&
		validateHdrParams(crd.Params)
}
