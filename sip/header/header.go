// Package header implements various SIP headers defined in the RFC 3261.
package header

import (
	"fmt"
	"io"
	"net/textproto"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/constraints"
	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
	"github.com/ghettovoice/gosip/sip/internal/shared"
)

// Header represents a generic SIP header.
type Header interface {
	CanonicName() Name
	Render() string
	RenderTo(w io.Writer) error
	Clone() Header
}

type Name string

func (n Name) ToCanonic() Name { return CanonicName(n) }

func (n Name) IsValid() bool { return grammar.IsToken(n) }

func (n Name) IsEqual(val any) bool {
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

var headerNames = map[string]Name{
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
	name = stringutils.TrimSP(name)
	if n, ok := headerNames[string(name)]; ok {
		return n
	}

	name = T(textproto.CanonicalMIMEHeaderKey(string(name)))
	if n, ok := headerNames[string(name)]; ok {
		return n
	}
	return Name(name)
}

func renderHeaderEntries[H ~[]E, E any](w io.Writer, hdr H) error {
	for i := range hdr {
		if i > 0 {
			if _, err := fmt.Fprint(w, ", "); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprint(w, hdr[i]); err != nil {
			return err
		}
	}
	return nil
}

func renderHeaderParams(w io.Writer, params Values, addQParam bool) error {
	if len(params) == 0 {
		return nil
	}

	// Sort parameters in alphabet order, but with "q" parameter always the first place.
	// If missing the "q" param, then dump it with the default value.
	// RFC 2616 Section 14.1.
	var kvs [][]string //nolint:prealloc
	if addQParam && !params.Has("q") {
		kvs = append(kvs, []string{"q", "1"})
	}
	for k := range params {
		kvs = append(kvs, []string{stringutils.LCase(k), params.Last(k)})
	}
	slices.SortFunc(kvs, func(a, b []string) int {
		if a[0] == "q" && b[0] != "q" {
			return -1
		} else if a[0] != "q" && b[0] == "q" {
			return 1
		}
		return stringutils.CmpKVs(a, b)
	})
	for _, kv := range kvs {
		if _, err := fmt.Fprint(w, ";", kv[0]); err != nil {
			return err
		}
		if kv[1] != "" {
			if _, err := fmt.Fprint(w, "=", kv[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

func compareHeaderParams(params1, params2 Values, specParams map[string]bool) bool {
	switch {
	case len(params1) == 0 && len(params2) == 0:
		return true
	case len(params1) == 0:
		return !hasSpecHeaderParam(params2, specParams)
	case len(params2) == 0:
		return !hasSpecHeaderParam(params1, specParams)
	}

	checked := map[string]bool{}
	// Any non-special parameters appearing in only one list are ignored.
	// First, traverse over self-parameters, compare values appearing in both lists,
	// check on speciality and save checked param names.
	for k := range params1 {
		if params2.Has(k) {
			// Any parameter appearing in both URIs must match.
			v1, v2 := params1.Last(k), params2.Last(k)
			if !grammar.IsQuoted(v1) {
				v1 = stringutils.LCase(v1)
			}
			if !grammar.IsQuoted(v1) {
				v2 = stringutils.LCase(v2)
			}
			if v1 != v2 {
				return false
			}
		} else if specParams[stringutils.LCase(k)] {
			// Any special SIP URI parameter appearing in one URI must appear in the other.
			return false
		}
		checked[stringutils.LCase(k)] = true
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

func hasSpecHeaderParam(params Values, specParams map[string]bool) bool {
	for k := range specParams {
		if params.Has(k) {
			return true
		}
	}
	return false
}

func validateHeaderParams(params Values) bool {
	for k := range params {
		if !grammar.IsToken(k) {
			return false
		}
		v := params.Last(k)
		if !(grammar.IsToken(v) || grammar.IsHost(v) || grammar.IsQuoted(v)) {
			return false
		}
	}
	return true
}

func cloneHeaderEntries[H ~[]E, E interface{ Clone() E }](hdr H) H {
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
type Parser func(name string, value []byte) Header

// Parse parses the header from the given input s (string or a []byte) and
// returns the parsed header as an instance of [Header].
// If the parsing fails, an error is returned along with nil as the header value.
//
// Example usage:
//
//	hdr, err := header.Parse("From: <sip:alice@example.com;foo>;tag=qwerty", nil)
func Parse[T constraints.Byteseq](s T, hdrPrs map[string]Parser) (Header, error) {
	node, err := grammar.ParseMessageHeader(s)
	if err != nil {
		return nil, err
	}
	return FromABNF(node.Children[0].Children[0], hdrPrs), nil
}

func FromABNF(node *abnf.Node, hdrPrs map[string]Parser) Header {
	switch node.Key {
	case "Accept":
		return buildFromAcceptNode(node)
	case "Accept-Encoding":
		return buildFromAcceptEncodingNode(node)
	case "Accept-Language":
		return buildFromAcceptLanguageNode(node)
	case "Alert-Info":
		return buildFromAlertInfoNode(node)
	case "Allow":
		return buildFromAllowNode(node)
	case "Authentication-Info":
		return buildFromAuthenticationInfoNode(node)
	case "Authorization":
		return buildFromAuthorizationNode(node)
	case "Call-ID":
		return buildFromCallIDNode(node)
	case "Call-Info":
		return buildFromCallInfoNode(node)
	case "Contact":
		return buildFromContactNode(node)
	case "Content-Disposition":
		return buildFromContentDispositionNode(node)
	case "Content-Encoding":
		return buildFromContentEncodingNode(node)
	case "Content-Language":
		return buildFromContentLanguageNode(node)
	case "Content-Length":
		return buildFromContentLengthNode(node)
	case "Content-Type":
		return buildFromContentTypeNode(node)
	case "CSeq":
		return buildFromCSeqNode(node)
	case "Date":
		return buildFromDateNode(node)
	case "Error-Info":
		return buildFromErrorInfoNode(node)
	case "Expires":
		return buildFromExpiresNode(node)
	case "From":
		return buildFromFromNode(node)
	case "In-Reply-To":
		return buildFromInReplyToNode(node)
	case "Max-Forwards":
		return buildFromMaxForwardsNode(node)
	case "MIME-Version":
		return buildFromMIMEVersionNode(node)
	case "Min-Expires":
		return buildFromMinExpiresNode(node)
	case "Organization":
		return buildFromOrganizationNode(node)
	case "Priority":
		return buildFromPriorityNode(node)
	case "Proxy-Authenticate":
		return buildFromProxyAuthenticateNode(node)
	case "Proxy-Authorization":
		return buildFromProxyAuthorizationNode(node)
	case "Proxy-Require":
		return buildFromProxyRequireNode(node)
	case "Record-Route":
		return buildFromRecordRouteNode(node)
	case "Reply-To":
		return buildFromReplyToNode(node)
	case "Require":
		return buildFromRequireNode(node)
	case "Retry-After":
		return buildFromRetryAfterNode(node)
	case "Route":
		return buildFromRouteNode(node)
	case "Server":
		return buildFromServerNode(node)
	case "Subject":
		return buildFromSubjectNode(node)
	case "Supported":
		return buildFromSupportedNode(node)
	case "Timestamp":
		return buildFromTimestampNode(node)
	case "To":
		return buildFromToNode(node)
	case "Unsupported":
		return buildFromUnsupportedNode(node)
	case "User-Agent":
		return buildFromUserAgentNode(node)
	case "Via":
		return buildFromViaNode(node)
	case "Warning":
		return buildFromWarningNode(node)
	case "WWW-Authenticate":
		return buildFromWWWAuthenticateNode(node)
	case "extension-header":
		if prs, ok := hdrPrs[stringutils.LCase(string(node.Children[0].Value))]; ok && prs != nil {
			if hdr := prs(node.Children[0].String(), abnfutils.MustGetNode(node, "header-value").Value); hdr != nil {
				return hdr
			}
		}
		return buildFromExtensionHeaderNode(node)
	default:
		return nil
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
		if n := node.GetNode("generic-param"); n != nil {
			kv := buildFromGenericParamNode(n)
			params.Append(kv[0], kv[1])
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
	if valNode := node.GetNode("gen-value"); valNode != nil {
		kv[1] = valNode.String()
	}
	return kv
}

type Addr = shared.Addr

func Host(host string) Addr { return shared.Host(host) }

func HostPort(host string, port uint16) Addr { return shared.HostPort(host, port) }

type Values = shared.Values

type ProtoInfo = shared.ProtoInfo

type TransportProto = shared.TransportProto

type RequestMethod = shared.RequestMethod

var nilTag = "<nil>"
