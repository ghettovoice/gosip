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

func (hdr *AuthenticationInfo) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr *AuthenticationInfo) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
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

func (hdr *AuthenticationInfo) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr *AuthenticationInfo) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderHdrVal(hdr)
}

func (hdr *AuthenticationInfo) String() string {
	return hdr.RenderValue()
}

func (hdr *AuthenticationInfo) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods        AuthenticationInfo
		AuthenticationInfo hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*AuthenticationInfo)(hdr))
}

func (hdr *AuthenticationInfo) Clone() Header {
	if hdr == nil {
		return nil
	}

	clone := *hdr
	return &clone
}

func (hdr *AuthenticationInfo) Equal(val any) bool {
	return util.EqualPointer(hdr, val, func(other *AuthenticationInfo) bool {
		return hdr.NextNonce == other.NextNonce &&
			util.EqualFold(hdr.QOP, other.QOP) &&
			hdr.RspAuth == other.RspAuth &&
			hdr.CNonce == other.CNonce &&
			hdr.NonceCount == other.NonceCount
	})
}

func (hdr *AuthenticationInfo) IsValid() bool {
	return hdr != nil &&
		hdr.NextNonce != "" &&
		(hdr.QOP == "" || grammar.IsToken(hdr.QOP))
}

func (hdr *AuthenticationInfo) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *AuthenticationInfo) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
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
