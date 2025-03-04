package uri

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

// Tel implements "tel" URI for Telephone Numbers (RFC 3966).
type Tel struct {
	// Telephone number matching telephone-subscriber ABNF. Required.
	Number string
	// URI's parameters.
	// Optional when the telephone number is global.
	// And mandatory to have at least a "phone-context" parameter when the telephone number is local.
	Params Values
}

// IsGlob checks whether the telephone number is global or not.
// RFC 3966 Section 5.1.4.
func (u *Tel) IsGlob() bool { return u != nil && grammar.IsGlobTelNum(u.number()) }

// Clone returns a deep copy of the Tel URI.
func (u *Tel) Clone() URI {
	if u == nil {
		return nil
	}
	u2 := *u
	u2.Params = u.Params.Clone()
	return &u2
}

// RenderToOptions writes the Tel URI to the provided writer.
func (u *Tel) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if u == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint("tel:", u.number())

	if len(u.Params) > 0 {
		var kvs [][]string
		for k := range u.Params {
			v, _ := u.Params.Last(k)
			kvs = append(kvs, []string{util.LCase(k), v})
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
			return util.CmpKVs(a, b)
		})

		for _, kv := range kvs {
			cw.Fprint(";", kv[0])
			if kv[1] != "" {
				cw.Fprint("=", grammar.Escape(kv[1], shouldEscapeURIParamChar))
			}
		}
	}

	return errtrace.Wrap2(cw.Result())
}

func (u *Tel) number() string { return strings.ReplaceAll(u.Number, " ", "") }

// Render returns the string representation of the Tel URI.
func (u *Tel) Render(opts *RenderOptions) string {
	if u == nil {
		return ""
	}
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	u.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// String returns the string representation of the Tel URI.
func (u *Tel) String() string {
	if u == nil {
		return ""
	}
	return u.Render(nil)
}

// Format implements fmt.Formatter for custom formatting of the Tel URI.
func (u *Tel) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			u.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, u.String())
		return
	case 'q':
		fmt.Fprint(f, strconv.Quote(u.String()))
		return
	default:
		type hideMethods Tel
		type Tel hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*Tel)(u))
		return
	}
}

// Equal compares this Tel URI with another for equality according to RFC 3966 Section 4.
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
	// The numbers must be equal after removing all visual separators.
	return util.EqFold(grammar.CleanTelNum(u.number()), grammar.CleanTelNum(other.number())) &&
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

		v1, _ := u.Params.Last(k)
		v2, _ := params.Last(k)
		switch util.LCase(k) {
		case "ext", "phone-context":
			if grammar.IsTelNum(v1) {
				v1 = grammar.CleanTelNum(v1)
			}
			if grammar.IsTelNum(v2) {
				v2 = grammar.CleanTelNum(v2)
			}
		}
		if !util.EqFold(v1, v2) {
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
	if !u.IsGlob() {
		if ctx, ok := u.Params.Last("phone-context"); !ok || grammar.CleanTelNum(ctx) == "" {
			return false
		}
	}
	for k := range u.Params {
		if !grammar.IsTelURIParamName(k) {
			return false
		}
	}
	return true
}

// ToSIP converts the Tel URI to a SIP URI according to RFC 3966 Section 5.1.7.
func (u *Tel) ToSIP() *SIP {
	if u == nil {
		return nil
	}

	u2, _ := u.Clone().(*Tel)
	u2.Number = grammar.CleanTelNum(u2.Number)

	var host string
	if !u2.IsGlob() {
		if ctx, _ := u2.Params.Last("phone-context"); grammar.IsHost(ctx) {
			host = ctx
			u2.Params.Del("phone-context")
		} else if ctx != "" {
			u2.Params.Set("phone-context", grammar.CleanTelNum(ctx))
		}
	}
	if ext, _ := u2.Params.Last("ext"); ext != "" {
		u2.Params.Set("ext", grammar.CleanTelNum(ext))
	}
	// RFC 3966 Section 4.
	// All parameter names and values SHOULD use lower-case characters, as
	// tel URIs may be used within contexts where comparisons are case-sensitive.
	return &SIP{
		User:   User(util.LCase(u2.Render(nil)[4:])),
		Addr:   Host(host),
		Params: make(Values).Set("user", "phone"),
	}
}

// MarshalText implements [encoding.TextMarshaler].
func (u *Tel) MarshalText() ([]byte, error) {
	return []byte(u.String()), nil
}

// UnmarshalText implements [encoding.TextUnmarshaler].
func (u *Tel) UnmarshalText(text []byte) error {
	u1, err := ParseTel(string(text))
	if err != nil {
		*u = Tel{}
		return errtrace.Wrap(err)
	}
	*u = *u1
	return nil
}

func (u *Tel) PhoneContext() (string, bool) {
	return u.Params.Last("phone-context")
}

func (u *Tel) Extension() (string, bool) {
	return u.Params.Last("ext")
}

func (u *Tel) ISDNSubAddr() (string, bool) {
	return u.Params.Last("isub")
}

// ParseTel parses a Tel URI from the given input src (string or []byte).
func ParseTel[T ~string | ~[]byte](src T) (*Tel, error) {
	n, err := grammar.ParseTelURI(src)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	return buildFromTelURINode(n), nil
}

func buildFromTelURINode(node *abnf.Node) *Tel {
	var num string
	if node.Contains("global-number") {
		num = string(grammar.MustGetNode(node, "global-number-digits").Value)
	} else if node.Contains("local-number") {
		num = string(grammar.MustGetNode(node, "local-number-digits").Value)
	}

	return &Tel{
		Number: num,
		Params: buildTelURIParams(node),
	}
}

func buildTelURIParams(node *abnf.Node) Values {
	ns := node.GetNodes("par")
	if n, ok := node.GetNode("context"); ok {
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
				var pval string
				if vn, ok := n.GetNode("pvalue"); ok {
					pval = grammar.Unescape(vn.String())
				}
				ps.Append(
					grammar.MustGetNode(n, "pname").String(),
					grammar.Unescape(pval),
				)
			}
		}
	}
	return ps
}
