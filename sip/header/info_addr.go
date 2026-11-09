package header

import (
	"bytes"
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/shared"
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
	return renderVal(addr)
}

func (addr InfoAddr) renderTo(w io.Writer) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint("<")
	if addr.URI != nil {
		cw.Call(func(w io.Writer) (int, error) {
			return errors.Wrap2(addr.URI.RenderTo(w))
		})
	}
	cw.Fprint(">")

	cw.Call(func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrParamsTo(w, addr.Params, false))
	})
	return errors.Wrap2(cw.Result())
}

// Format implements fmt.Formatter for custom formatting of the InfoAddr.
func (addr InfoAddr) Format(f fmt.State, verb rune) {
	if formatVal(f, verb, addr) {
		return
	}
	type (
		hideMethods  InfoAddr
		ResourceAddr hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), ResourceAddr(addr))
}

// Equal compares this InfoAddr with another for equality.
func (addr InfoAddr) Equal(val any) bool {
	return util.EqualValue(val, func(other InfoAddr) bool {
		return shared.IsEqual(addr.URI, other.URI) &&
			compareHdrParams(addr.Params, other.Params, map[string]bool{
				"purpose": true,
			})
	})
}

// IsValid checks whether the InfoAddr is syntactically valid.
func (addr InfoAddr) IsValid() bool {
	return shared.IsValid(addr.URI) && validateHdrParams(addr.Params)
}

// IsZero checks whether the InfoAddr is empty.
func (addr InfoAddr) IsZero() bool {
	return addr.URI == nil && len(addr.Params) == 0
}

// Clone returns a copy of the InfoAddr.
func (addr InfoAddr) Clone() InfoAddr {
	addr.URI = shared.Clone[uri.URI](addr.URI)
	addr.Params = addr.Params.Clone()
	return addr
}

func (addr InfoAddr) MarshalText() ([]byte, error) {
	return []byte(addr.String()), nil
}

func (addr InfoAddr) AppendText(data []byte) ([]byte, error) {
	return append(data, addr.String()...), nil
}

func (addr *InfoAddr) UnmarshalText(data []byte) (finErr error) {
	if len(data) == 0 || bytes.Equal(data, []byte("<>")) {
		*addr = InfoAddr{}
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

	node, err := grammar.ParseInfo(data)
	if err != nil {
		return errors.Wrap(err)
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
		URI:    util.Must2(uri.FromABNF(grammar.MustGetNode(node, "absoluteURI"))),
		Params: buildFromHdrParamNodes(node.GetNodes(psKey), nil),
	}
}
