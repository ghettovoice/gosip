package header

import (
	"encoding/json"
	"io"
	"net"
	"net/textproto"
	"slices"
	"sync"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
)

// Addr represents a network address consisting of a host and optional port.
type Addr = types.Addr

// AddrFromHost creates an Addr from a hostname without a port.
func AddrFromHost(host string) Addr { return types.AddrFromHost(host) }

// AddrFromHostPort creates an Addr from a hostname and port.
func AddrFromHostPort(host string, port uint16) Addr { return types.AddrFromHostPort(host, port) }

func AddrFromIP(ip net.IP) Addr { return types.AddrFromIP(ip) }

func AddrFromIPPort(ip net.IP, port uint16) Addr { return types.AddrFromIPPort(ip, port) }

// ParseAddr parses a network address from the given input s (string or []byte).
func ParseAddr[T ~string | ~[]byte](s T) (Addr, error) { return errors.Wrap2(types.ParseAddr(s)) }

// Values represents header parameters as a multi-value map.
type Values = types.Values

// ProtoInfo represents SIP protocol information (name and version).
type ProtoInfo = types.ProtoInfo

// TransportProto represents a transport protocol (UDP, TCP, TLS, SCTP, WS, WSS).
type TransportProto = types.TransportProto

// RequestMethod represents a SIP request method (INVITE, ACK, BYE, etc.).
type RequestMethod = types.RequestMethod

// RenderOptions contains options for rendering headers and URIs.
type RenderOptions = types.RenderOptions

// Header represents a generic SIP header.
type Header interface {
	types.Renderer
	types.Cloneable[Header]
	types.ValidFlag
	types.Equalable
	CanonicName() Name
	CompactName() Name
	RenderValue() string
}

// Name represents a SIP header name.
type Name string

// Canonic converts the Name to its canonical form.
func (n Name) Canonic() Name { return CanonicName(n) }

// IsValid checks whether the Name is syntactically valid.
func (n Name) IsValid() bool { return grammar.IsToken(n) }

// Equal compares this Name with another for equality.
func (n Name) Equal(val any) bool {
	var other Name
	switch v := val.(type) {
	case Name:
		other = v
	case *Name:
		if v == nil {
			return false
		}

		other = *v
	default:
		return false
	}

	return CanonicName(n) == CanonicName(other)
}

var hdrNames = map[string]Name{
	"c":                "Content-Type",
	"e":                "Content-Encoding",
	"f":                "From",
	"i":                "Call-ID",
	"k":                "Supported",
	"l":                "Content-Length",
	"m":                "Contact",
	"s":                "Subject",
	"t":                "To",
	"v":                "Via",
	"Call-Id":          "Call-ID",
	"Cseq":             "CSeq",
	"Mime-Version":     "MIME-Version",
	"Www-Authenticate": "WWW-Authenticate",
}

// CanonicName converts name to the canonical form.
// The canonicalization converts the first letter and any letter following a hyphen to upper case;
// the rest are converted to lowercase. For example, the canonical name for "accept-encoding" is "Accept-Encoding".
// Also, any compact name is converted to its full canonical form. For example, "c" converts to "Content-Type".
func CanonicName[T ~string](name T) Name {
	name = util.TrimSP(name)
	if n, ok := hdrNames[string(name)]; ok {
		return n
	}

	name = T(textproto.CanonicalMIMEHeaderKey(string(name)))
	if n, ok := hdrNames[string(name)]; ok {
		return n
	}

	return Name(name)
}

func renderHdrEntries[H ~[]E, E any](w io.Writer, hdr H) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	for i := range hdr {
		if i > 0 {
			cw.Fprint(", ")
		}

		cw.Fprint(hdr[i])
	}

	return errors.Wrap2(cw.Result())
}

func renderHdrParams(w io.Writer, params Values, addQParam bool) (num int, err error) {
	if len(params) == 0 {
		return 0, nil
	}

	// Sort parameters in alphabet order, but with "q" parameter always the first place.
	// If missing the "q" param, then dump it with the default value.
	// RFC 2616 Section 14.1.
	var kvs [][]string
	if addQParam && !params.Has("q") {
		kvs = append(kvs, []string{"q", "1"})
	}

	for k := range params {
		v, _ := params.Last(k)
		kvs = append(kvs, []string{util.LCase(k), v})
	}

	slices.SortFunc(kvs, func(a, b []string) int {
		if a[0] == "q" && b[0] != "q" {
			return -1
		} else if a[0] != "q" && b[0] == "q" {
			return 1
		}

		return util.CmpKVs(a, b)
	})

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	for _, kv := range kvs {
		cw.Fprint(";", kv[0])

		if kv[1] != "" {
			cw.Fprint("=", kv[1])
		}
	}

	return errors.Wrap2(cw.Result())
}

func compareHdrParams(params1, params2 Values, specParams map[string]bool) bool {
	switch {
	case len(params1) == 0 && len(params2) == 0:
		return true
	case len(params1) == 0:
		return !hasSpecHdrParam(params2, specParams)
	case len(params2) == 0:
		return !hasSpecHdrParam(params1, specParams)
	}

	checked := map[string]bool{}
	// Any non-special parameters appearing in only one list are ignored.
	// First, traverse over self-parameters, compare values appearing in both lists,
	// check on speciality and save checked param names.
	for k := range params1 {
		if params2.Has(k) {
			// Any parameter appearing in both URIs must match.
			v1, _ := params1.Last(k)
			v2, _ := params2.Last(k)

			if !grammar.IsQuoted(v1) {
				v1 = util.LCase(v1)
			}

			if !grammar.IsQuoted(v1) {
				v2 = util.LCase(v2)
			}

			if v1 != v2 {
				return false
			}
		} else if specParams[util.LCase(k)] {
			// Any special SIP URI parameter appearing in one URI must appear in the other.
			return false
		}

		checked[util.LCase(k)] = true
	}
	// Then need only check that there are no non-checked special parameters in the other list.
	for k := range specParams {
		if checked[k] {
			continue
		}

		if params2.Has(k) {
			return false
		}
	}

	return true
}

func hasSpecHdrParam(params Values, specParams map[string]bool) bool {
	for k := range specParams {
		if params.Has(k) {
			return true
		}
	}

	return false
}

func validateHdrParams(params Values) bool {
	for k := range params {
		if !grammar.IsToken(k) {
			return false
		}

		v, _ := params.Last(k)
		if v != "" && (!grammar.IsToken(v) && !grammar.IsHost(v) && !grammar.IsQuoted(v)) {
			return false
		}
	}

	return true
}

func cloneHdrEntries[H ~[]E, E interface{ Clone() E }](hdr H) H {
	var hdr2 H
	if hdr == nil {
		return hdr2
	}

	hdr2 = make(H, len(hdr))
	for i := range hdr {
		hdr2[i] = hdr[i].Clone()
	}

	return hdr2
}

// Parser is a function type for parsing a custom SIP header.
type Parser func(name string, value []byte) (Header, error)

var customParsers sync.Map // map[string]Parser

// RegisterParser registers a custom SIP header parser.
func RegisterParser(name string, parser Parser) {
	customParsers.Store(util.LCase(name), parser)
}

// UnregisterParser unregisters a custom SIP header parser.
func UnregisterParser(name string) {
	customParsers.Delete(util.LCase(name))
}

// Parse parses a SIP header from the given input s (string or []byte) and
// returns the parsed header as an instance of [Header].
// If the parsing fails, an error is returned along with nil as the header value.
//
// Example usage:
//
//	hdr, err := header.Parse("From: <sip:alice@example.com;foo>;tag=qwerty")
func Parse[T ~string | ~[]byte](s T) (Header, error) {
	node, err := grammar.ParseMessageHeader(s)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return errors.Wrap2(FromABNF(node.Children[0].Children[0]))
}

// FromABNF creates a Header from an ABNF node.
// This is typically used during parsing and most users should use [Parse] instead.
func FromABNF(node *abnf.Node) (hdr Header, err error) {
	defer func() {
		if rv := recover(); rv != nil {
			hdr = nil

			if e, ok := rv.(error); ok {
				err = errors.Wrap(e)
			} else {
				err = errors.ErrorfWrap("%v", rv)
			}
		}
	}()

	switch node.Key {
	case "Accept":
		return buildFromAcceptNode(node), nil
	case "Accept-Encoding":
		return buildFromAcceptEncodingNode(node), nil
	case "Accept-Language":
		return buildFromAcceptLanguageNode(node), nil
	case "Alert-Info":
		return buildFromAlertInfoNode(node), nil
	case "Allow":
		return buildFromAllowNode(node), nil
	case "Authentication-Info":
		return buildFromAuthenticationInfoNode(node), nil
	case "Authorization":
		return buildFromAuthorizationNode(node), nil
	case "Call-ID":
		return buildFromCallIDNode(node), nil
	case "Call-Info":
		return buildFromCallInfoNode(node), nil
	case "Contact":
		return buildFromContactNode(node), nil
	case "Content-Disposition":
		return buildFromContentDispositionNode(node), nil
	case "Content-Encoding":
		return buildFromContentEncodingNode(node), nil
	case "Content-Language":
		return buildFromContentLanguageNode(node), nil
	case "Content-Length":
		return buildFromContentLengthNode(node), nil
	case "Content-Type":
		return buildFromContentTypeNode(node), nil
	case "CSeq":
		return buildFromCSeqNode(node), nil
	case "Date":
		return buildFromDateNode(node), nil
	case "Error-Info":
		return buildFromErrorInfoNode(node), nil
	case "Expires":
		return buildFromExpiresNode(node), nil
	case "From":
		return buildFromFromNode(node), nil
	case "In-Reply-To":
		return buildFromInReplyToNode(node), nil
	case "Max-Forwards":
		return buildFromMaxForwardsNode(node), nil
	case "MIME-Version":
		return buildFromMIMEVersionNode(node), nil
	case "Min-Expires":
		return buildFromMinExpiresNode(node), nil
	case "Organization":
		return buildFromOrganizationNode(node), nil
	case "Priority":
		return buildFromPriorityNode(node), nil
	case "Proxy-Authenticate":
		return buildFromProxyAuthenticateNode(node), nil
	case "Proxy-Authorization":
		return buildFromProxyAuthorizationNode(node), nil
	case "Proxy-Require":
		return buildFromProxyRequireNode(node), nil
	case "Record-Route":
		return buildFromRecordRouteNode(node), nil
	case "Reply-To":
		return buildFromReplyToNode(node), nil
	case "Require":
		return buildFromRequireNode(node), nil
	case "Retry-After":
		return buildFromRetryAfterNode(node), nil
	case "Route":
		return buildFromRouteNode(node), nil
	case "Server":
		return buildFromServerNode(node), nil
	case "Subject":
		return buildFromSubjectNode(node), nil
	case "Supported":
		return buildFromSupportedNode(node), nil
	case "Timestamp":
		return buildFromTimestampNode(node), nil
	case "To":
		return buildFromToNode(node), nil
	case "Unsupported":
		return buildFromUnsupportedNode(node), nil
	case "User-Agent":
		return buildFromUserAgentNode(node), nil
	case "Via":
		return buildFromViaNode(node), nil
	case "Warning":
		return buildFromWarningNode(node), nil
	case "WWW-Authenticate":
		return buildFromWWWAuthenticateNode(node), nil
	case "extension-header":
		name := util.LCase(string(node.Children[0].Value))
		if prs, ok := customParsers.Load(name); ok && prs != nil {
			//nolint:forcetypeassert
			return errors.Wrap2(prs.(Parser)(
				node.Children[0].String(),
				grammar.MustGetNode(node, "header-value").Value,
			))
		}

		return buildFromExtensionHeaderNode(node), nil
	default:
		return nil, errors.Wrap(grammar.ErrUnexpectNode)
	}
}

func buildFromHeaderParamNodes(nodes abnf.Nodes, params Values) Values {
	if len(nodes) == 0 {
		return params
	}

	if params == nil {
		params = make(Values, len(nodes))
	}

	for _, node := range nodes {
		if n, ok := node.GetNode("generic-param"); ok {
			kv := buildFromGenericParamNode(n)
			params.Append(kv[0], kv[1])
			continue
		}

		if n, ok := node.GetNode("response-port"); ok {
			digits := ""
			if d, ok := n.GetNode("1*DIGIT"); ok {
				digits = d.String()
			}

			params.Append(n.Children[0].String(), digits)

			continue
		}

		node = node.Children[0]

		var val []byte

		for _, n := range node.Children[2:] {
			if n.IsEmpty() {
				continue
			}

			val = append(val, n.Value...)
		}

		params.Append(node.Children[0].String(), string(val))
	}

	return params
}

func buildFromGenericParamNode(node *abnf.Node) [2]string {
	var kv [2]string

	kv[0] = node.Children[0].String()
	if valNode, ok := node.GetNode("gen-value"); ok {
		kv[1] = valNode.String()
	}

	return kv
}

type headerData struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

var jsonNull = []byte("null")

// ToJSON encodes a header to JSON.
func ToJSON(hdr Header) ([]byte, error) {
	if util.IsNil(hdr) {
		return jsonNull, nil
	}

	return errors.Wrap2(json.Marshal(headerData{
		Name:  string(hdr.CanonicName()),
		Value: hdr.RenderValue(),
	}))
}

// FromJSON decodes a header from JSON.
// It returns:
//   - nil header and nil error if the JSON is null
//   - &Any{Value: hd.Value} and nil error if the JSON is '{}' or '{"value":"..."}'
//   - the parsed header and nil error if the JSON is '{"name":"...","value":"..."}'
//   - nil header and error in case of any error
func FromJSON[T ~string | ~[]byte](data T) (Header, error) {
	var hd *headerData
	if err := json.Unmarshal([]byte(data), &hd); err != nil {
		return nil, errors.Wrap(err)
	}

	if hd == nil {
		// json null
		return nil, nil //nolint:nilnil
	}

	if hd.Name == "" {
		// json {} or {"value":"..."}
		return &Any{Value: hd.Value}, nil
	}

	hdr, err := Parse(hd.Name + ":" + hd.Value)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return hdr, nil
}

func newUnexpectHdrErrType(h Header) error {
	return errors.Errorf("unexpected header type %T", h)
}

func newUnexpectHdrTypeErrWrap(h Header) error {
	return errors.GetCaller().Wrap(newUnexpectHdrErrType(h))
}
