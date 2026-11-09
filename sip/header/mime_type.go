package header

import (
	"bytes"
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

// MIMEType holds media type information.
type MIMEType struct {
	Type    string
	Subtype string
	Params  Values
}

func (mt MIMEType) String() string {
	return renderVal(mt)
}

func (mt MIMEType) renderTo(w io.Writer) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(mt.Type, "/", mt.Subtype)
	if len(mt.Params) > 0 {
		kvs := make([][]string, 0, len(mt.Params))
		for k := range mt.Params {
			v, _ := mt.Params.Last(k)
			kvs = append(kvs, []string{util.ToLower(k), v})
		}
		slices.SortFunc(kvs, util.CompareKeys)
		for _, kv := range kvs {
			cw.Fprint(";", kv[0], "=", kv[1])
		}
	}
	return errors.Wrap2(cw.Result())
}

func (mt MIMEType) Format(f fmt.State, verb rune) {
	if formatVal(f, verb, mt) {
		return
	}
	type (
		hideMethods MIMEType
		MIMEType    hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), MIMEType(mt))
}

func (mt MIMEType) Equal(val any) bool {
	return util.EqualValue(val, func(other MIMEType) bool {
		return util.EqualFold(mt.Type, other.Type) &&
			util.EqualFold(mt.Subtype, other.Subtype) &&
			compareHdrParams(mt.Params, other.Params, map[string]bool{
				"charset": true,
			})
	})
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

func (mt MIMEType) AppendText(data []byte) ([]byte, error) {
	return append(data, mt.String()...), nil
}

func (mt *MIMEType) UnmarshalText(data []byte) (finErr error) {
	if len(data) == 0 || bytes.Equal(data, []byte("/")) {
		*mt = MIMEType{}
		return nil
	}

	defer func() {
		if rv := recover(); rv != nil {
			if e, ok := rv.(error); ok {
				finErr = errors.Wrap(e)
			} else {
				finErr = errors.ErrorfWrap("%v", rv)
			}
		}
	}()

	node, err := grammar.ParseMediaRange(data)
	if err != nil {
		return errors.Wrap(err)
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

			if otherParamsStarted || util.ToLower(kv[0]) == "q" {
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
