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
	"github.com/ghettovoice/gosip/internal/util"
)

type AuthenticationInfo struct {
	NextNonce,
	QOP,
	RspAuth,
	CNonce string
	NonceCount uint
}

func (*AuthenticationInfo) CanonicName() Name { return "Authentication-Info" }

func (*AuthenticationInfo) CompactName() Name { return "Authentication-Info" }

func (hdr *AuthenticationInfo) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)

	return errors.Wrap2(cw.Result())
}

func (hdr *AuthenticationInfo) renderValueTo(w io.Writer) (num int, err error) {
	var kvs [][]string
	for k, v := range map[string]string{
		"nextnonce": hdr.NextNonce,
		"qop":       hdr.QOP,
		"rspauth":   hdr.RspAuth,
		"cnonce":    hdr.CNonce,
	} {
		if v == "" {
			continue
		}

		switch k {
		case "nextnonce", "rspauth", "cnonce":
			v = grammar.Quote(v)
		}

		kvs = append(kvs, []string{k, v})
	}

	if hdr.NonceCount > 0 {
		kvs = append(kvs, []string{"nc", fmt.Sprintf("%08x", hdr.NonceCount)})
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	if len(kvs) > 0 {
		slices.SortFunc(kvs, util.CmpKVs)

		for i, kv := range kvs {
			if i > 0 {
				cw.Fprint(", ")
			}

			cw.Fprint(kv[0], "=", kv[1])
		}
	}

	return errors.Wrap2(cw.Result())
}

func (hdr *AuthenticationInfo) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.RenderTo(sb, opts) //nolint:errcheck

	return sb.String()
}

func (hdr *AuthenticationInfo) RenderValue() string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.renderValueTo(sb) //nolint:errcheck

	return sb.String()
}

func (hdr *AuthenticationInfo) String() string { return hdr.RenderValue() }

func (hdr *AuthenticationInfo) Format(f fmt.State, verb rune) {
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
		type (
			hideMethods        AuthenticationInfo
			AuthenticationInfo hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), (*AuthenticationInfo)(hdr))

		return
	}
}

func (hdr *AuthenticationInfo) Clone() Header {
	if hdr == nil {
		return nil
	}

	hdr2 := *hdr

	return &hdr2
}

func (hdr *AuthenticationInfo) Equal(val any) bool {
	var other *AuthenticationInfo
	switch v := val.(type) {
	case AuthenticationInfo:
		other = &v
	case *AuthenticationInfo:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return hdr.NextNonce == other.NextNonce &&
		util.EqFold(hdr.QOP, other.QOP) &&
		hdr.RspAuth == other.RspAuth &&
		hdr.CNonce == other.CNonce &&
		hdr.NonceCount == other.NonceCount
}

func (hdr *AuthenticationInfo) IsValid() bool {
	return hdr != nil && hdr.NextNonce != "" && (hdr.QOP == "" || grammar.IsToken(hdr.QOP))
}

func (hdr *AuthenticationInfo) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *AuthenticationInfo) UnmarshalJSON(data []byte) error {
	if hdr == nil {
		return errors.NewInvalidArgumentErrorWrap("nil header")
	}

	gh, err := FromJSON(data)
	if gh == nil {
		*hdr = AuthenticationInfo{}
		return errors.Wrap(err)
	}

	h, ok := gh.(*AuthenticationInfo)
	if !ok {
		*hdr = AuthenticationInfo{}

		ah, ok := gh.(*Any)
		if ok && ah.CanonicName().Equal(hdr.CanonicName()) && len(ah.Value) == 0 {
			return nil
		}

		return newUnexpectHdrTypeErrWrap(gh)
	}

	*hdr = *h

	return nil
}

func buildFromAuthenticationInfoNode(node *abnf.Node) *AuthenticationInfo {
	var hdr AuthenticationInfo

	paramNodes := node.GetNodes("ainfo")
	for _, paramNode := range paramNodes {
		paramNode = paramNode.Children[0]
		switch paramNode.Key {
		case "nextnonce":
			hdr.NextNonce = grammar.Unquote(paramNode.Children[2].String())
		case "message-qop":
			hdr.QOP = paramNode.Children[2].String()
		case "response-auth":
			hdr.RspAuth = grammar.Unquote(paramNode.Children[2].String())
		case "cnonce":
			hdr.CNonce = grammar.Unquote(paramNode.Children[2].String())
		case "nonce-count":
			if v, err := strconv.ParseUint(paramNode.Children[2].String(), 16, 64); err == nil {
				hdr.NonceCount = uint(v)
			}
		}
	}

	return &hdr
}
