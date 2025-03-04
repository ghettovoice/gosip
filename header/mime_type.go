package header

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

// MIMEType holds media type information.
type MIMEType struct {
	Type    string
	Subtype string
	Params  Values
}

func (mt MIMEType) String() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	fmt.Fprint(sb, mt.Type, "/", mt.Subtype)

	if len(mt.Params) > 0 {
		kvs := make([][]string, 0, len(mt.Params))
		for k := range mt.Params {
			v, _ := mt.Params.Last(k)
			kvs = append(kvs, []string{util.LCase(k), v})
		}
		slices.SortFunc(kvs, util.CmpKVs)
		for _, kv := range kvs {
			fmt.Fprint(sb, ";", kv[0], "=", kv[1])
		}
	}

	return sb.String()
}

func (mt MIMEType) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		fmt.Fprint(f, mt.String())
		return
	case 'q':
		fmt.Fprint(f, strconv.Quote(mt.String()))
		return
	default:
		if !f.Flag('+') && !f.Flag('#') {
			fmt.Fprint(f, mt.String())
			return
		}

		type hideMethods MIMEType
		type MIMEType hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), MIMEType(mt))
		return
	}
}

func (mt MIMEType) Equal(val any) bool {
	var other MIMEType
	switch v := val.(type) {
	case MIMEType:
		other = v
	case *MIMEType:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}

	return util.EqFold(mt.Type, other.Type) &&
		util.EqFold(mt.Subtype, other.Subtype) &&
		compareHdrParams(mt.Params, other.Params, map[string]bool{"charset": true})
}

func (mt MIMEType) IsValid() bool {
	return grammar.IsToken(mt.Type) &&
		grammar.IsToken(mt.Subtype) &&
		validateHdrParams(mt.Params)
}

func (mt MIMEType) IsZero() bool {
	return mt.Type == "" &&
		mt.Subtype == "" &&
		len(mt.Params) == 0
}

func (mt MIMEType) Clone() MIMEType {
	mt.Params = mt.Params.Clone()
	return mt
}

func (mt MIMEType) MarshalText() ([]byte, error) {
	return []byte(mt.String()), nil
}

func (mt *MIMEType) UnmarshalText(data []byte) error {
	node, err := grammar.ParseMediaRange(data)
	if err != nil {
		*mt = MIMEType{}
		if errors.Is(err, grammar.ErrEmptyInput) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	var ps [][2]string
	*mt, ps = buildFromMIMETypeNode(node)
	if len(ps) > 0 {
		if mt.Params == nil {
			mt.Params = make(Values, len(ps))
		}
		for _, kv := range ps {
			mt.Params.Append(kv[0], kv[1])
		}
	}

	return nil
}

func buildFromMIMETypeNode(node *abnf.Node) (MIMEType, [][2]string) {
	var mt MIMEType
	if n, ok := node.GetNode("m-type"); ok {
		mt.Type = n.String()
	} else if node.Key == "media-range" {
		mt.Type = "*"
	}
	if n, ok := node.GetNode("m-subtype"); ok {
		mt.Subtype = n.String()
	} else if node.Key == "media-range" {
		mt.Subtype = "*"
	}

	var (
		otherParams        [][2]string
		otherParamsStarted bool
	)
	if paramNodes := node.GetNodes("m-parameter"); len(paramNodes) > 0 {
		for _, paramNode := range paramNodes {
			valNode := grammar.MustGetNode(paramNode, "m-value")
			kv := [2]string{paramNode.Children[0].String(), valNode.String()}

			if otherParamsStarted || util.LCase(kv[0]) == "q" {
				// media-range usually used as part of accept-range
				// we interpret 'q' param as a separator between media-range and accept-range params
				// RFC 2616 Section 14.1.
				otherParams = append(otherParams, kv)
				otherParamsStarted = true
				continue
			}

			if mt.Params == nil {
				mt.Params = make(Values)
			}
			mt.Params.Append(kv[0], kv[1])
		}
	}
	return mt, otherParams
}
