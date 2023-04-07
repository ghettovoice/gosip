package uri

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/constraints"
	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

// Tel implements "tel" URI for Telephone Numbers (RFC 3966).
type Tel struct {
	// Telephone number matching telephone-subscriber ABNF. Required.
	Number string
	// URI's parameters. Optional when telephone number is global. And mandatory to have
	// at least "phone-context" parameter when telephone number is local.
	Params Values
}

func (u *Tel) URIScheme() string { return "tel" }

// IsGlob checks whether the telephone number is global or not.
// RFC 3966 Section 5.1.4.
func (u *Tel) IsGlob() bool { return u != nil && grammar.IsGlobTelNum(u.number()) }

func (u *Tel) Clone() URI {
	if u == nil {
		return nil
	}
	u2 := *u
	u2.Params = u.Params.Clone()
	return &u2
}

func (u *Tel) RenderURITo(w io.Writer) error {
	if u == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, "tel:", u.number()); err != nil {
		return err
	}
	if len(u.Params) > 0 {
		var kvs [][]string
		for k := range u.Params {
			kvs = append(kvs, []string{utils.LCase(k), u.Params.Last(k)})
		}
		// RFC 3966 Section 3.
		// The 'isub' or 'ext' MUST appear first, if present,
		// followed by the 'phone-context' parameter, if present,
		// followed by any other parameters in lexicographical order.
		slices.SortFunc(kvs, func(a, b []string) int {
			if (a[0] == "isub" || a[0] == "ext") && b[0] != "isub" && b[0] != "ext" {
				return -1
			} else if (b[0] == "isub" || b[0] == "ext") && a[0] != "isub" && a[0] != "ext" {
				return 1
			}
			if a[0] == "phone-context" && b[0] != "phone-context" {
				return -1
			} else if a[0] != "phone-context" && b[0] == "phone-context" {
				return 1
			}
			return utils.CmpKVs(a, b)
		})
		for _, kv := range kvs {
			if _, err := fmt.Fprint(w, ";", kv[0]); err != nil {
				return err
			}
			if kv[1] != "" {
				if _, err := fmt.Fprint(w, "=", grammar.Escape(kv[1], shouldEscapeURIParamChar)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (u *Tel) number() string { return strings.ReplaceAll(u.Number, " ", "") }

func (u *Tel) RenderURI() string {
	if u == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	u.RenderURITo(sb)
	return sb.String()
}

func (u *Tel) String() string {
	if u == nil {
		return "<nil>"
	}
	return u.RenderURI()
}

func (u *Tel) Equal(val any) bool {
	var other *Tel
	switch v := val.(type) {
	case Tel:
		other = &v
	case *Tel:
		other = v
	default:
		return false
	}

	if u == other {
		return true
	} else if u == nil || other == nil {
		return false
	}

	// URI comparison is case-insensitive.
	// Both must be either a local or a global number, i.e., start with a '+'.
	// The numbers must be equal, after removing all visual separators.
	return utils.LCase(grammar.CleanTelNum(u.number())) == utils.LCase(grammar.CleanTelNum(other.number())) &&
		u.compareParams(other.Params)
}

// compareParams compares tel URI parameters according to rules from RFC 3966 Section 4.
func (u *Tel) compareParams(params Values) bool {
	// Parameters are compared according to name, regardless of the order they appeared in the URI.
	// If one URI has a parameter name not found in the other, the two URIs are not equal.
	// 'ext', 'phone-context' (if its value is phone number) are compared after removing all visual separators.
	// URI parameter comparisons are case-insensitive.
	if len(u.Params) != len(params) {
		return false
	}

	for k := range u.Params {
		if !params.Has(k) {
			return false
		}
		v1, v2 := utils.LCase(u.Params.Last(k)), utils.LCase(params.Last(k))
		switch utils.LCase(k) {
		case "ext", "phone-context":
			if grammar.IsTelNum(v1) {
				v1 = grammar.CleanTelNum(v1)
			}
			if grammar.IsTelNum(v2) {
				v2 = grammar.CleanTelNum(v2)
			}
		}
		if v1 != v2 {
			return false
		}
	}
	return true
}

// IsValid checks whether the u is syntactically valid tel URI.
func (u *Tel) IsValid() bool {
	if u == nil {
		return false
	}
	if u.number() == "" {
		return false
	}
	if !u.IsGlob() && grammar.CleanTelNum(u.Params.Last("phone-context")) == "" {
		return false
	}
	for k := range u.Params {
		if !grammar.IsTelURIParamName(k) {
			return false
		}
	}
	return true
}

func (u *Tel) ToSIP() *SIP {
	if u == nil {
		return nil
	}

	u2 := u.Clone().(*Tel)
	u2.Number = grammar.CleanTelNum(u2.Number)

	var host string
	if !u2.IsGlob() {
		if ctx := u2.Params.Last("phone-context"); grammar.IsHost(ctx) {
			host = ctx
			u2.Params.Del("phone-context")
		} else if ctx != "" {
			u2.Params.Set("phone-context", grammar.CleanTelNum(ctx))
		}
	}
	if ext := u2.Params.Last("ext"); ext != "" {
		u2.Params.Set("ext", grammar.CleanTelNum(ext))
	}
	// RFC 3966 Section 4.
	// All parameter names and values SHOULD use lower-case characters, as
	// tel URIs may be used within contexts where comparisons are case-sensitive.
	return &SIP{
		User:   User(utils.LCase(u2.RenderURI()[4:])),
		Addr:   Host(host),
		Params: make(Values).Set("user", "phone"),
	}
}

func ParseTel[T constraints.Byteseq](src T) (*Tel, error) {
	n, err := grammar.ParseTelURI(src)
	if err != nil {
		return nil, err
	}
	return buildFromTelURINode(n), nil
}

func buildFromTelURINode(node *abnf.Node) *Tel {
	var num string
	if node.Contains("global-number") {
		num = string(utils.MustGetNode(node, "global-number-digits").Value)
	} else if node.Contains("local-number") {
		num = string(utils.MustGetNode(node, "local-number-digits").Value)
	}

	return &Tel{
		Number: num,
		Params: buildTelURIParams(node),
	}
}

func buildTelURIParams(node *abnf.Node) Values {
	ns := node.GetNodes("par")
	if n := node.GetNode("context"); n != nil {
		ns = append(abnf.Nodes{n}, ns...)
	}
	if len(ns) == 0 {
		return nil
	}

	ps := make(Values, len(ns))
	for _, n := range ns {
		switch n.Key {
		case "context":
			ps.Append(
				strings.Trim(n.Children[0].String(), ";="),
				grammar.Unescape(n.Children[1].String()),
			)
		default:
			switch n.Children[0].Key {
			case "extension", "isdn-subaddress":
				ps.Append(
					strings.Trim(n.Children[0].Children[0].String(), ";="),
					grammar.Unescape(n.Children[0].Children[1].String()),
				)
			default:
				ps.Append(
					utils.MustGetNode(n, "pname").String(),
					grammar.Unescape(n.GetNode("pvalue").String()),
				)
			}
		}
	}
	return ps
}
