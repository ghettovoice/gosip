package uri

//go:generate go tool errtrace -w .

import (
	"net/url"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
)

// Addr represents a network address consisting of a host and optional port.
type Addr = types.Addr

// Host creates an Addr from a hostname without a port.
func Host(host string) Addr { return types.Host(host) }

// HostPort creates an Addr from a hostname and port.
func HostPort(host string, port uint16) Addr { return types.HostPort(host, port) }

// ParseAddr parses a network address from the given input s (string or []byte).
func ParseAddr[T ~string | ~[]byte](s T) (Addr, error) { return errtrace.Wrap2(types.ParseAddr(s)) }

// Values represents URI parameters or headers as a multi-value map.
type Values = types.Values

// RenderOptions contains options for rendering URIs and headers.
type RenderOptions = types.RenderOptions

type TransportProto = types.TransportProto

type RequestMethod = types.RequestMethod

// URI represents generic URI (SIP, SIPS, Tel, ...etc).
type URI interface {
	types.Renderer
	types.Cloneable[URI]
	types.ValidFlag
	types.Equalable
}

// Parse parses any URI (sip, sips, tel, ...etc.) from a given input s (string or []byte).
//
// Parsing of:
//   - sip/sips returns [SIP];
//   - tel URI returns [Tel];
//   - any other URI returns [Any].
//
// See [ParseSIP], [ParseTel], [ParseAny].
func Parse[T ~string | ~[]byte](s T) (URI, error) {
	if len(s) >= 3 {
		switch util.LCase(string(s[:3])) {
		case "sip":
			return errtrace.Wrap2(ParseSIP(s))
		case "tel":
			return errtrace.Wrap2(ParseTel(s))
		}
	}
	return errtrace.Wrap2(ParseAny(s))
}

// FromABNF creates a URI from an ABNF node.
//
//   - node "SIP-URI" or "SIPS-URI" returns [SIP];
//   - node "telephone-uri" returns [Tel];
//   - node "absoluteURI" with scheme "sip" or "sips" returns [SIP];
//   - node "absoluteURI" with scheme "tel" returns [Tel];
//   - any other node returns [Any].
//
// End users usually don't need to use this function directly and should use [Parse] instead.
func FromABNF(node *abnf.Node) URI {
	switch node.Key {
	case "SIP-URI", "SIPS-URI":
		return buildFromSIPURINode(node)
	case "telephone-uri":
		return buildFromTelURINode(node)
	case "absoluteURI":
		if sn, ok := node.GetNode("scheme"); ok {
			switch util.LCase(sn.String()) {
			case "sip", "sips":
				if u, err := ParseSIP(node.Value); err == nil {
					return u
				}
			case "tel":
				if u, err := ParseTel(node.Value); err == nil {
					return u
				}
			}
		}
		fallthrough
	default:
		return buildFromAnyNode(node)
	}
}

// GetScheme returns the scheme of the URI.
//
// SIP and SIPS URIs return "sip" or "sips" respectively,
// Tel URI returns "tel",
// Any URI returns the value of [URI.Scheme] field.
// If the URI is nil, an empty string is returned.
// If the URI is of unknown type, a panic is raised.
func GetScheme(u URI) string {
	if u == nil {
		return ""
	}

	switch u := u.(type) {
	case *SIP:
		return u.scheme()
	case *Tel:
		return "tel"
	case *Any:
		return u.Scheme
	default:
		panic(newUnexpectURITypeErr(u))
	}
}

// GetAddr returns the address of the URI.
//
// SIP and SIPS URIs returns the value of [SIP.Addr] field,
// Tel URI returns the value of [Tel.Number] field,
// Any URI returns the value of concatenated [net/url.URL.Host] and [net/url.URL.Path] fields.
// If the URI is nil, an empty string is returned.
// If the URI is of unknown type, a panic is raised.
func GetAddr(u URI) string {
	if u == nil {
		return ""
	}

	switch u := u.(type) {
	case *SIP:
		return u.Addr.String()
	case *Tel:
		return u.Number
	case *Any:
		return u.Host + u.Path
	default:
		panic(newUnexpectURITypeErr(u))
	}
}

// GetParams returns the parameters of the URI.
//
// SIP and SIPS URIs return the value of [SIP.Params] field,
// Tel URI returns the value of [Tel.Params] field,
// Any URI returns the value of [net/url.URL.RawQuery] field parsed into [Values].
// If the URI is nil, nil is returned.
// If the URI is of unknown type, a panic is raised.
func GetParams(u URI) Values {
	if u == nil {
		return nil
	}

	switch u := u.(type) {
	case *SIP:
		return u.Params
	case *Tel:
		return u.Params
	case *Any:
		p, _ := url.ParseQuery(u.RawQuery)
		return Values(p)
	default:
		panic(newUnexpectURITypeErr(u))
	}
}

func newUnexpectURITypeErr(u URI) error {
	return errorutil.Errorf("unexpected URI type %T", u) //errtrace:skip
}
