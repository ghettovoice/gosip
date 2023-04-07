package uri

import (
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/constraints"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/common"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

// URI represents generic URI (SIP, SIPS, Tel, ...etc).
type URI interface {
	URIScheme() string
	RenderURI() string
	RenderURITo(w io.Writer) error
	Clone() URI
}

// Parse parses any URI (sip, sips, tel, ...etc.) from a given input s (string or []byte).
// Parsing of sip/sips returns [SIP], parsing of tel URI returns [Tel],
// parsing any other URI returns [Any].
func Parse[T constraints.Byteseq](s T) (URI, error) {
	if len(s) >= 3 {
		switch utils.LCase(string(s[:3])) {
		case "sip":
			return ParseSIP(s)
		case "tel":
			return ParseTel(s)
		}
	}
	return ParseAny(s)
}

func FromABNF(node *abnf.Node) URI {
	switch node.Key {
	case "SIP-URI", "SIPS-URI":
		return buildFromSIPURINode(node)
	case "telephone-uri":
		return buildFromTelURINode(node)
	case "absoluteURI":
		if sn := node.GetNode("scheme"); sn != nil {
			switch utils.LCase(sn.String()) {
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

func shouldEscapeURIParamChar(c byte) bool { return !grammar.IsURIParamCharUnreserved(c) }

func shouldEscapeURIHeaderChar(c byte) bool { return !grammar.IsURIHeaderCharUnreserved(c) }

type Addr = common.Addr

func Host(host string) Addr { return common.Host(host) }

func HostPort(host string, port uint16) Addr { return common.HostPort(host, port) }

type Values = common.Values
