package header

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/uri"
)

// NameAddr represents a single element in From, To, Contact, Reply-To headers.
// It contains a display name, URI, and parameters.
type NameAddr struct {
	DisplayName string
	URI         uri.URI
	Params      Values
}

// String returns the string representation of the NameAddr.
func (addr NameAddr) String() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	if addr.DisplayName != "" {
		fmt.Fprint(sb, grammar.Quote(addr.DisplayName), " ")
	}

	fmt.Fprint(sb, "<")
	if addr.URI != nil {
		addr.URI.RenderTo(sb, nil) //nolint:errcheck
	}
	fmt.Fprint(sb, ">")

	renderHdrParams(sb, addr.Params, false) //nolint:errcheck

	return sb.String()
}

// Format implements fmt.Formatter for custom formatting of the NameAddr.
func (addr NameAddr) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		fmt.Fprint(f, addr.String())
		return
	case 'q':
		fmt.Fprint(f, strconv.Quote(addr.String()))
		return
	default:
		if !f.Flag('+') && !f.Flag('#') {
			fmt.Fprint(f, addr.String())
			return
		}

		type hideMethods NameAddr
		type NameAddr hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), NameAddr(addr))
		return
	}
}

// Equal compares this NameAddr with another for equality.
func (addr NameAddr) Equal(val any) bool {
	var other NameAddr
	switch v := val.(type) {
	case NameAddr:
		other = v
	case *NameAddr:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}

	return types.IsEqual(addr.URI, other.URI) &&
		compareHdrParams(addr.Params, other.Params, map[string]bool{
			"q":       true,
			"tag":     true,
			"expires": true,
		})
}

// IsValid checks whether the NameAddr is syntactically valid.
func (addr NameAddr) IsValid() bool {
	return types.IsValid(addr.URI) && validateHdrParams(addr.Params)
}

// IsZero checks whether the NameAddr is empty.
func (addr NameAddr) IsZero() bool {
	return addr.DisplayName == "" && addr.URI == nil && len(addr.Params) == 0
}

// Clone returns a copy of the NameAddr.
func (addr NameAddr) Clone() NameAddr {
	addr.URI = types.Clone[uri.URI](addr.URI)
	addr.Params = addr.Params.Clone()
	return addr
}

func (addr NameAddr) MarshalText() ([]byte, error) {
	return []byte(addr.String()), nil
}

func (addr *NameAddr) UnmarshalText(data []byte) error {
	node, err := grammar.ParseContactParam(data)
	if err != nil {
		*addr = NameAddr{}
		if errors.Is(err, grammar.ErrEmptyInput) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	*addr = buildFromNameAddrNode(node, "contact-params")
	return nil
}

func (addr NameAddr) Tag() (string, bool) {
	return addr.Params.Last("tag")
}

func (addr NameAddr) Expires() (time.Duration, bool) {
	v, ok := addr.Params.Last("expires")
	if !ok {
		return 0, false
	}
	sec, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(sec) * time.Second, true
}

func buildFromNameAddrNode(node *abnf.Node, psNodeKey string) NameAddr {
	addr := NameAddr{
		URI:    uri.FromABNF(grammar.MustGetNode(node, "addr-spec").Children[0]),
		Params: buildFromHeaderParamNodes(node.GetNodes(psNodeKey), nil),
	}

	// https://datatracker.ietf.org/doc/rfc8217/
	if !node.Contains("name-addr") && strings.ContainsAny(node.String(), ",;?") {
		switch v := addr.URI.(type) {
		case *uri.SIP:
			addr.Params = v.Params
			v.Params = nil
		case *uri.Tel:
			addr.Params = v.Params
			v.Params = nil
		case *uri.Any:
			p, _ := url.ParseQuery(v.RawQuery)
			v.RawQuery = ""
			addr.Params = Values(p)
		}
	}

	if dnameNode, ok := node.GetNode("display-name"); ok {
		addr.DisplayName = grammar.Unquote(strings.TrimSpace(dnameNode.String()))
	}
	return addr
}
