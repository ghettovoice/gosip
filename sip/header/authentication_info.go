package header

import (
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type AuthenticationInfo struct {
	NextNonce,
	QOP,
	RspAuth,
	CNonce string
	NonceCount uint
}

func (*AuthenticationInfo) CanonicName() Name { return "Authentication-Info" }

func (hdr *AuthenticationInfo) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *AuthenticationInfo) renderValue(w io.Writer) error {
	var kvs [][]string //nolint:prealloc
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

func (hdr *AuthenticationInfo) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr *AuthenticationInfo) String() string {
	if hdr == nil {
		return nilTag
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.renderValue(sb)
	return sb.String()
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
		stringutils.LCase(hdr.QOP) == stringutils.LCase(other.QOP) &&
		hdr.RspAuth == other.RspAuth &&
		hdr.CNonce == other.CNonce &&
		hdr.NonceCount == other.NonceCount
}

func (hdr *AuthenticationInfo) IsValid() bool {
	return hdr != nil && hdr.NextNonce != "" && (hdr.QOP == "" || grammar.IsToken(hdr.QOP))
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
