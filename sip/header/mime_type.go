package header

import (
	"fmt"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

// MIMEType holds media type information.
type MIMEType struct {
	Type    string
	Subtype string
	Params  Values
}

func (mt MIMEType) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_, _ = fmt.Fprint(sb, mt.Type, "/", mt.Subtype)
	if len(mt.Params) > 0 {
		kvs := make([][]string, 0, len(mt.Params))
		for k := range mt.Params {
			kvs = append(kvs, []string{stringutils.LCase(k), mt.Params.Last(k)})
		}
		slices.SortFunc(kvs, stringutils.CmpKVs)
		for _, kv := range kvs {
			_, _ = fmt.Fprint(sb, ";", kv[0], "=", kv[1])
		}
	}
	return sb.String()
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
	return stringutils.LCase(mt.Type) == stringutils.LCase(other.Type) &&
		stringutils.LCase(mt.Subtype) == stringutils.LCase(other.Subtype) &&
		compareHeaderParams(mt.Params, other.Params, map[string]bool{"charset": true})
}

func (mt MIMEType) IsValid() bool {
	return grammar.IsToken(mt.Type) &&
		grammar.IsToken(mt.Subtype) &&
		validateHeaderParams(mt.Params)
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

func buildFromMIMETypeNode(node *abnf.Node) (MIMEType, [][2]string) {
	var mt MIMEType
	if n := node.GetNode("m-type"); n != nil {
		mt.Type = n.String()
	} else if node.Key == "media-range" {
		mt.Type = "*"
	}
	if n := node.GetNode("m-subtype"); n != nil {
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
			valNode := abnfutils.MustGetNode(paramNode, "m-value")
			kv := [2]string{paramNode.Children[0].String(), valNode.String()}

			if otherParamsStarted || stringutils.LCase(kv[0]) == "q" {
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
