package uri

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/constraints"
	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

// SIP represents a SIP or SIPS URI.
type SIP struct {
	User    UserInfo // username and passwd
	Addr    Addr     // host and port
	Params  Values   // parameters
	Headers Values   // headers
	Secured bool
}

func (u *SIP) URIScheme() string {
	if u.Secured {
		return "sips"
	}
	return "sip"
}

func (u *SIP) Clone() URI {
	if u == nil {
		return nil
	}
	u2 := *u
	u2.Addr = u.Addr.Clone()
	u2.Params = u.Params.Clone()
	u2.Headers = u.Headers.Clone()
	return &u2
}

func (u *SIP) RenderURITo(w io.Writer) error {
	if u == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, u.URIScheme(), ":"); err != nil {
		return err
	}
	if !u.User.IsZero() {
		if _, err := fmt.Fprint(w, u.User, "@"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(w, u.Addr); err != nil {
		return err
	}
	if err := u.renderParams(w); err != nil {
		return err
	}
	if err := u.renderHeaders(w); err != nil {
		return err
	}
	return nil
}

func (u *SIP) renderParams(w io.Writer) error {
	if len(u.Params) == 0 {
		return nil
	}

	kvs := make([][]string, 0, len(u.Params))
	for k := range u.Params {
		kvs = append(kvs, []string{utils.LCase(k), u.Params.Last(k)})
	}
	slices.SortFunc(kvs, utils.CmpKVs)
	for _, kv := range kvs {
		if _, err := fmt.Fprint(w, ";", grammar.Escape(kv[0], shouldEscapeURIParamChar)); err != nil {
			return err
		}
		if kv[1] != "" {
			if _, err := fmt.Fprint(w, "=", grammar.Escape(kv[1], shouldEscapeURIParamChar)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (u *SIP) renderHeaders(w io.Writer) error {
	if len(u.Headers) == 0 {
		return nil
	}

	kvs := make([][]string, 0, len(u.Headers))
	for k := range u.Headers {
		kvs = append(kvs, append([]string{utils.LCase(k)}, u.Headers.Get(k)...))
	}
	slices.SortFunc(kvs, utils.CmpKVs)
	if _, err := fmt.Fprint(w, "?"); err != nil {
		return err
	}
	var i int
	for _, kv := range kvs {
		for _, v := range kv[1:] {
			if i > 0 {
				if _, err := fmt.Fprint(w, "&"); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprint(w, grammar.Escape(kv[0], shouldEscapeURIHeaderChar), "=", grammar.Escape(v, shouldEscapeURIHeaderChar)); err != nil {
				return err
			}
			i++
		}
	}
	return nil
}

func (u *SIP) RenderURI() string {
	if u == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	u.RenderURITo(sb)
	return sb.String()
}

func (u *SIP) String() string {
	if u == nil {
		return "<nil>"
	}
	return u.RenderURI()
}

func (u *SIP) Equal(val any) bool {
	var other *SIP
	switch v := val.(type) {
	case SIP:
		other = &v
	case *SIP:
		other = v
	default:
		return false
	}

	if u == other {
		return true
	} else if u == nil || other == nil {
		return false
	}

	return u.Secured == other.Secured &&
		u.User.Equal(other.User) &&
		u.Addr.Equal(other.Addr) &&
		u.compareParams(other.Params) &&
		u.compareHeaders(other.Headers)
}

func (u *SIP) compareParams(params Values) bool {
	if len(u.Params) == 0 && len(params) == 0 {
		return true
	} else if len(u.Params) == 0 {
		return !hasSIPURISpecParam(params)
	} else if len(params) == 0 {
		return !hasSIPURISpecParam(u.Params)
	}

	checked := map[string]bool{}
	// Any non-special parameters appearing in only one list are ignored.
	// First traverse over self-parameters, compare values appearing in both lists,
	// check on speciality and save checked param names.
	for k := range u.Params {
		if params.Has(k) {
			// Any parameter appearing in both URIs must match.
			v1, v2 := utils.LCase(u.Params.Last(k)), utils.LCase(params.Last(k))
			if v1 != v2 {
				return false
			}
		} else if sipURISpecParams[utils.LCase(k)] {
			// Any special SIP URI parameter appearing in one URI must appear in the other.
			return false
		}
		checked[utils.LCase(k)] = true
	}
	// Then need only check that there are no non-checked special parameters in the other list.
	for k := range sipURISpecParams {
		if checked[k] {
			continue
		}
		if params.Has(k) {
			return false
		}
	}
	return true
}

var sipURISpecParams = map[string]bool{
	"transport": true,
	"user":      true,
	"method":    true,
	"maddr":     true,
	"ttl":       true,
	"lr":        true,
}

func hasSIPURISpecParam(ps Values) bool {
	for k := range sipURISpecParams {
		if _, ok := ps[k]; ok {
			return true
		}
	}
	return false
}

func (u *SIP) compareHeaders(hdrs Values) bool {
	// URI header components are never ignored. Any present header component MUST be present
	// in both URIs and match for the URIs to match.
	if len(u.Headers) != len(hdrs) {
		return false
	}

	for k := range u.Headers {
		if !hdrs.Has(k) {
			return false
		}
		// very simplified comparison, but probably not worth to make it fully spec compatible
		// take all header values as lower-cased string
		v1, v2 := utils.LCase(strings.Join(u.Headers.Get(k), ", ")), utils.LCase(strings.Join(hdrs.Get(k), ", "))
		if v1 != v2 {
			return false
		}
	}
	return true
}

func (u *SIP) IsValid() bool {
	return u != nil && u.Addr.IsValid() && (u.User.IsZero() || u.User.IsValid())
}

func ParseSIP[T constraints.Byteseq](src T) (*SIP, error) {
	var (
		n   *abnf.Node
		err error
	)
	if len(src) >= 4 && utils.LCase(string(src[:4])) == "sips" {
		n, err = grammar.ParseSIPSURI(src)
	} else {
		n, err = grammar.ParseSIPURI(src)
	}
	if err != nil {
		return nil, err
	}
	return buildFromSIPURINode(n), nil
}

func buildFromSIPURINode(node *abnf.Node) *SIP {
	return &SIP{
		User:    buildFromUserinfoNode(node.GetNode("userinfo")),
		Addr:    buildFromHostportNode(utils.MustGetNode(node, "hostport")),
		Params:  buildFromURIParamsNode(utils.MustGetNode(node, "uri-parameters")),
		Headers: buildFromURIHeadersNode(node.GetNode("headers")),
		Secured: node.Key == "SIPS-URI",
	}
}

func buildFromHostportNode(node *abnf.Node) Addr {
	host := utils.MustGetNode(node, "host").String()
	if portNode := node.GetNode("port"); portNode != nil {
		port, _ := strconv.Atoi(portNode.String())
		return HostPort(host, uint16(port))
	}
	return Host(host)
}

func buildFromUserinfoNode(node *abnf.Node) UserInfo {
	if node.IsEmpty() {
		return UserInfo{}
	}
	usrname := grammar.Unescape(utils.MustGetNode(node, "user").String())
	if passwdNode := node.GetNode("password"); passwdNode != nil {
		return UserPassword(usrname, grammar.Unescape(passwdNode.String()))
	}
	return User(usrname)
}

func buildFromURIParamsNode(node *abnf.Node) Values {
	if node.IsEmpty() {
		return nil
	}

	paramNodes := node.GetNodes("uri-parameter")
	params := make(Values, len(paramNodes))
	for _, paramNode := range paramNodes {
		paramNode = paramNode.Children[0]
		switch paramNode.Key {
		case "transport-param", "user-param", "method-param", "maddr-param", "ttl-param", "lr-param":
			var k, v string
			if len(paramNode.Children) == 0 { // like lr-param
				k = string(paramNode.Value)
			} else {
				k = string(paramNode.Children[0].Value[:len(paramNode.Children[0].Value)-1])
				v = string(paramNode.Children[1].Value)
			}
			params.Append(k, v)
		default: // other-param
			if nameNode := paramNode.GetNode("pname"); !nameNode.IsEmpty() {
				k := grammar.Unescape(nameNode.String())
				var v string
				if valueNode := paramNode.GetNode("pvalue"); !valueNode.IsEmpty() {
					v = grammar.Unescape(valueNode.String())
				}
				params.Append(k, v)
			}
		}
	}
	return params
}

func buildFromURIHeadersNode(node *abnf.Node) Values {
	if node.IsEmpty() {
		return nil
	}

	hdrNodes := node.GetNodes("header")
	hdrs := make(Values, len(hdrNodes))
	for _, n := range hdrNodes {
		hdrs.Append(
			grammar.Unescape(utils.MustGetNode(n, "hname").String()),
			grammar.Unescape(utils.MustGetNode(n, "hvalue").String()),
		)
	}
	return hdrs
}

// UserInfo is a container for user credentials.
// It is typically used in [SIP] to store userinfo part.
type UserInfo struct {
	usrname, passwd string
	hasPasswd       bool
}

// User returns a [UserInfo] containing the provided username and no password.
func User(usrname string) UserInfo {
	return UserInfo{usrname: usrname}
}

// UserPassword returns a [UserInfo] containing the provided username and password.
func UserPassword(usrname, passwd string) UserInfo {
	return UserInfo{usrname: usrname, passwd: passwd, hasPasswd: true}
}

func (ui UserInfo) Username() string { return ui.usrname }

// Password returns the password, in case it is set, and bool flag indicating whether it is set.
func (ui UserInfo) Password() (string, bool) { return ui.passwd, ui.hasPasswd }

func shouldEscapeUserChar(c byte) bool { return !grammar.IsURIUserCharUnreserved(c) }

func shouldEscapePasswdChar(c byte) bool { return !grammar.IsURIPasswdCharUnreserved(c) }

func (ui UserInfo) String() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	if ui.usrname != "" {
		sb.WriteString(grammar.Escape(ui.usrname, shouldEscapeUserChar))
	}
	if ui.hasPasswd {
		sb.WriteString(":")
		sb.WriteString(grammar.Escape(ui.passwd, shouldEscapePasswdChar))
	}
	return sb.String()
}

func (ui UserInfo) Equal(val any) bool {
	var other UserInfo
	switch v := val.(type) {
	case UserInfo:
		other = v
	case *UserInfo:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return ui.usrname == other.usrname && ui.passwd == other.passwd && ui.hasPasswd == other.hasPasswd
}

func (ui UserInfo) IsValid() bool { return ui.usrname != "" }

func (ui UserInfo) IsZero() bool { return ui.usrname == "" && ui.passwd == "" && !ui.hasPasswd }
