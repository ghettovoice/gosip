package header

import (
	"errors"
	"fmt"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/uri"
)

// InfoAddr represents a single element in Alert-Info, Call-Info, Error-Info headers.
type InfoAddr struct {
	URI    uri.URI
	Params Values
}

// String returns the string representation of the InfoAddr.
func (addr InfoAddr) String() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	fmt.Fprint(sb, "<")
	if addr.URI != nil {
		addr.URI.RenderTo(sb, nil) //nolint:errcheck
	}
	fmt.Fprint(sb, ">")

	renderHdrParams(sb, addr.Params, false) //nolint:errcheck

	return sb.String()
}

// Format implements fmt.Formatter for custom formatting of the InfoAddr.
func (addr InfoAddr) Format(f fmt.State, verb rune) {
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

		type hideMethods InfoAddr
		type ResourceAddr hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), ResourceAddr(addr))
		return
	}
}

// Equal compares this InfoAddr with another for equality.
func (addr InfoAddr) Equal(val any) bool {
	var other InfoAddr
	switch v := val.(type) {
	case InfoAddr:
		other = v
	case *InfoAddr:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}

	return types.IsEqual(addr.URI, other.URI) &&
		compareHdrParams(addr.Params, other.Params, map[string]bool{"purpose": true})
}

// IsValid checks whether the InfoAddr is syntactically valid.
func (addr InfoAddr) IsValid() bool {
	return types.IsValid(addr.URI) && validateHdrParams(addr.Params)
}

// IsZero checks whether the InfoAddr is empty.
func (addr InfoAddr) IsZero() bool { return addr.URI == nil && len(addr.Params) == 0 }

// Clone returns a copy of the InfoAddr.
func (addr InfoAddr) Clone() InfoAddr {
	addr.URI = types.Clone[uri.URI](addr.URI)
	addr.Params = addr.Params.Clone()
	return addr
}

func (addr InfoAddr) MarshalText() ([]byte, error) {
	return []byte(addr.String()), nil
}

func (addr *InfoAddr) UnmarshalText(data []byte) error {
	node, err := grammar.ParseInfo(data)
	if err != nil {
		*addr = InfoAddr{}
		if errors.Is(err, grammar.ErrEmptyInput) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	*addr = buildFromInfoAddrNode(node)
	return nil
}

func buildFromInfoAddrNode(node *abnf.Node) InfoAddr {
	psKey := "generic-param"
	if node.Key == "info" {
		psKey = "info-param"
	}
	return InfoAddr{
		URI:    uri.FromABNF(grammar.MustGetNode(node, "absoluteURI")),
		Params: buildFromHeaderParamNodes(node.GetNodes(psKey), nil),
	}
}
