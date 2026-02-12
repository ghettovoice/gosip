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

// SIP represents a SIP or SIPS URI.
type SIP struct {
	User    UserInfo // username and passwd
	Addr    Addr     // host and port
	Params  Values   // parameters
	Headers Values   // headers
	Secured bool
}

// Clone returns a deep copy of the SIP URI.
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

// Scheme returns the URI scheme.
func (u *SIP) Scheme() string {
	if u == nil {
		return ""
	}
	return u.scheme()
}

// RenderToOptions writes the SIP URI to the provided writer.
func (u *SIP) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if u == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(u.scheme(), ":")
	if !u.User.IsZero() {
		cw.Fprint(u.User, "@")
	}
	cw.Fprint(u.Addr)
	cw.Call(u.renderParams)
	cw.Call(u.renderHeaders)
	return errtrace.Wrap2(cw.Result())
}

func (u *SIP) scheme() string {
	if u.Secured {
		return "sips"
	}
	return "sip"
}

func (u *SIP) renderParams(w io.Writer) (num int, err error) {
	if len(u.Params) == 0 {
		return 0, nil
	}

	kvs := make([][]string, 0, len(u.Params))
	for k := range u.Params {
		v, _ := u.Params.Last(k)
		kvs = append(kvs, []string{util.LCase(k), v})
	}
	slices.SortFunc(kvs, util.CmpKVs)

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	for _, kv := range kvs {
		cw.Fprint(";", grammar.Escape(kv[0], shouldEscapeURIParamChar))
		if kv[1] != "" {
			cw.Fprint("=", grammar.Escape(kv[1], shouldEscapeURIParamChar))
		}
	}
	return errtrace.Wrap2(cw.Result())
}

func (u *SIP) renderHeaders(w io.Writer) (num int, err error) {
	if len(u.Headers) == 0 {
		return 0, nil
	}

	kvs := make([][]string, 0, len(u.Headers))
	for k := range u.Headers {
		kvs = append(kvs, append([]string{util.LCase(k)}, u.Headers.Get(k)...))
	}
	slices.SortFunc(kvs, util.CmpKVs)

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint("?")

	var i int
	for _, kv := range kvs {
		for _, v := range kv[1:] {
			if i > 0 {
				cw.Fprint("&")
			}
			cw.Fprint(grammar.Escape(kv[0], shouldEscapeURIHeaderChar), "=", grammar.Escape(v, shouldEscapeURIHeaderChar))
			i++
		}
	}
	return errtrace.Wrap2(cw.Result())
}

// Render returns the string representation of the SIP URI.
func (u *SIP) Render(opts *RenderOptions) string {
	if u == nil {
		return ""
	}
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	u.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// String returns the string representation of the SIP URI.
func (u *SIP) String() string {
	if u == nil {
		return ""
	}
	return u.Render(nil)
}

// Format implements fmt.Formatter for custom formatting of the SIP URI.
func (u *SIP) Format(f fmt.State, verb rune) {
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
		type hideMethods SIP
		type SIP hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*SIP)(u))
		return
	}
}

// Equal compares this SIP URI with another for equality according to RFC 3261 Section 19.1.4.
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
	switch {
	case len(u.Params) == 0 && len(params) == 0:
		return true
	case len(u.Params) == 0:
		return !hasSIPURISpecParam(params)
	case len(params) == 0:
		return !hasSIPURISpecParam(u.Params)
	}

	checked := map[string]bool{}
	// Any non-special parameters appearing in only one list are ignored.
	// First, traverse over self-parameters, compare values appearing in both lists,
	// check on speciality and save checked param names.
	for k := range u.Params {
		if params.Has(k) {
			// Any parameter appearing in both URIs must match.
			v1, _ := u.Params.Last(k)
			v2, _ := params.Last(k)
			if !util.EqFold(v1, v2) {
				return false
			}
		} else if sipURISpecParams[util.LCase(k)] {
			// Any special SIP URI parameter appearing in one URI must appear in the other.
			return false
		}
		checked[util.LCase(k)] = true
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
		v1, v2 := util.LCase(strings.Join(u.Headers.Get(k), ", ")), util.LCase(strings.Join(hdrs.Get(k), ", "))
		if v1 != v2 {
			return false
		}
	}
	return true
}

// IsValid checks whether the SIP URI is syntactically valid.
func (u *SIP) IsValid() bool {
	return u != nil && u.Addr.IsValid() && (u.User.IsZero() || u.User.IsValid())
}

// MarshalText implements [encoding.TextMarshaler].
func (u *SIP) MarshalText() ([]byte, error) {
	return []byte(u.String()), nil
}

// UnmarshalText implements [encoding.TextUnmarshaler].
func (u *SIP) UnmarshalText(text []byte) error {
	u1, err := ParseSIP(string(text))
	if err != nil {
		*u = SIP{}
		return errtrace.Wrap(err)
	}
	*u = *u1
	return nil
}

func (u *SIP) Transport() (TransportProto, bool) {
	tp, ok := u.Params.Last("transport")
	return TransportProto(tp), ok
}

func (u *SIP) UserType() (string, bool) {
	return u.Params.Last("user")
}

func (u *SIP) Method() (RequestMethod, bool) {
	mtd, ok := u.Params.Last("method")
	return RequestMethod(mtd), ok
}

func (u *SIP) MAddr() (string, bool) {
	return u.Params.Last("maddr")
}

func (u *SIP) TTL() (uint8, bool) {
	val, ok := u.Params.Last("ttl")
	if !ok {
		return 0, false
	}
	tts, err := strconv.ParseUint(val, 10, 8)
	if err != nil {
		return 0, false
	}
	return uint8(tts), true
}

func (u *SIP) LR() bool {
	return u.Params.Has("lr")
}

// ParseSIP parses a SIP or SIPS URI from the given input src (string or []byte).
func ParseSIP[T ~string | ~[]byte](src T) (*SIP, error) {
	var (
		n   *abnf.Node
		err error
	)
	if len(src) >= 4 && util.EqFold(string(src[:4]), "sips") {
		n, err = grammar.ParseSIPSURI(src)
	} else {
		n, err = grammar.ParseSIPURI(src)
	}
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	return buildFromSIPURINode(n), nil
}

func buildFromSIPURINode(node *abnf.Node) *SIP {
	u := &SIP{
		Addr:    buildFromHostportNode(grammar.MustGetNode(node, "hostport")),
		Params:  buildFromURIParamsNode(grammar.MustGetNode(node, "uri-parameters")),
		Secured: node.Key == "SIPS-URI",
	}
	if n, ok := node.GetNode("userinfo"); ok {
		u.User = buildFromUserinfoNode(n)
	}
	if n, ok := node.GetNode("headers"); ok {
		u.Headers = buildFromURIHeadersNode(n)
	}
	return u
}

func buildFromHostportNode(node *abnf.Node) Addr {
	host := grammar.MustGetNode(node, "host").String()
	if portNode, ok := node.GetNode("port"); ok {
		port, _ := strconv.Atoi(portNode.String())
		return HostPort(host, uint16(port))
	}
	return Host(host)
}

func buildFromUserinfoNode(node *abnf.Node) UserInfo {
	if node.IsEmpty() {
		return UserInfo{}
	}
	usrname := grammar.Unescape(grammar.MustGetNode(node, "user").String())
	if passwdNode, ok := node.GetNode("password"); ok {
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
			if nameNode, ok := paramNode.GetNode("pname"); ok {
				k := grammar.Unescape(nameNode.String())
				var v string
				if valueNode, ok := paramNode.GetNode("pvalue"); ok {
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
			grammar.Unescape(grammar.MustGetNode(n, "hname").String()),
			grammar.Unescape(grammar.MustGetNode(n, "hvalue").String()),
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

// Username returns the username from the UserInfo.
func (ui UserInfo) Username() string { return ui.usrname }

// Password returns the password, in case it is set, and a bool flag indicating whether it is set.
func (ui UserInfo) Password() (string, bool) { return ui.passwd, ui.hasPasswd }

func shouldEscapeUserChar(c byte) bool { return !grammar.IsURIUserCharUnreserved(c) }

func shouldEscapePasswdChar(c byte) bool { return !grammar.IsURIPasswdCharUnreserved(c) }

// String returns the string representation of the UserInfo.
func (ui UserInfo) String() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	if ui.usrname != "" {
		sb.WriteString(grammar.Escape(ui.usrname, shouldEscapeUserChar))
	}
	if ui.hasPasswd {
		sb.WriteString(":")
		sb.WriteString(grammar.Escape(ui.passwd, shouldEscapePasswdChar))
	}
	return sb.String()
}

// Equal compares this UserInfo with another for equality.
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

// IsValid checks whether the UserInfo is syntactically valid.
func (ui UserInfo) IsValid() bool { return ui.usrname != "" }

// IsZero checks whether the UserInfo is empty.
func (ui UserInfo) IsZero() bool { return ui.usrname == "" && ui.passwd == "" && !ui.hasPasswd }
