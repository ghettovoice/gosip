package uri

import (
	"net"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
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

// Values represents URI parameters or headers as a multi-value map.
type Values = types.Values

// RenderOptions contains options for rendering URIs and headers.
type RenderOptions = types.RenderOptions

type TransportProto = types.TransportProto

type RequestMethod = types.RequestMethod

// URI represents generic URI (SIP, SIPS, Tel, ...etc).
type URI interface {
	Scheme() string
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
			return errors.Wrap2(ParseSIP(s))
		case "tel":
			return errors.Wrap2(ParseTel(s))
		}
	}

	return errors.Wrap2(ParseAny(s))
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
func FromABNF(node *abnf.Node) (u URI, err error) {
	defer func() {
		if rv := recover(); rv != nil {
			u = nil

			if e, ok := rv.(error); ok {
				err = errors.Wrap(e)
			} else {
				err = errors.ErrorfWrap("%v", rv)
			}
		}
	}()

	switch node.Key {
	case "SIP-URI", "SIPS-URI":
		return buildFromSIPURINode(node), nil
	case "telephone-uri":
		return buildFromTelURINode(node), nil
	case "absoluteURI":
		switch util.LCase(grammar.MustGetNode(node, "scheme").String()) {
		case "sip", "sips":
			return errors.Wrap2(Parse(node.Value))
		case "tel":
			return errors.Wrap2(ParseTel(node.Value))
		}

		fallthrough
	default:
		return buildFromAnyNode(node), nil
	}
}
