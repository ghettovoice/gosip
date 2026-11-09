package header

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

type Warning []WarningValue

func (Warning) CanonicName() Name { return "Warning" }

func (Warning) CompactName() Name { return "Warning" }

func (hdr Warning) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr Warning) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrValsTo(w, hdr))
}

func (hdr Warning) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr Warning) String() string {
	return hdr.RenderValue()
}

func (hdr Warning) RenderValue() string {
	return renderHdrVal(hdr)
}

func (hdr Warning) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Warning
		Warning     hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), Warning(hdr))
}

func (hdr Warning) Clone() Header {
	return cloneHdrVals(hdr)
}

func (hdr Warning) Equal(val any) bool {
	return util.EqualValue(val, func(other Warning) bool {
		return slices.EqualFunc(hdr, other, func(wrn1, wrn2 WarningValue) bool {
			return wrn1.Equal(wrn2)
		})
	})
}

func (hdr Warning) IsValid() bool {
	return len(hdr) > 0 &&
		!slices.ContainsFunc(hdr, func(wrn WarningValue) bool {
			return !wrn.IsValid()
		})
}

func (hdr Warning) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Warning) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromWarningNode(node *abnf.Node) Warning {
	warnNodes := node.GetNodes("warning-value")
	h := make(Warning, len(warnNodes))
	for i, warnNode := range warnNodes {
		h[i] = buildFromWarningValueNode(warnNode)
	}
	return h
}

type WarningValue struct {
	Code  uint
	Agent string
	Text  string
}

func (wrn WarningValue) String() string {
	return renderVal(wrn)
}

func (wrn WarningValue) renderTo(w io.Writer) (int, error) {
	return errors.Wrap2(fmt.Fprintf(w, "%d %s %q", wrn.Code, wrn.Agent, wrn.Text))
}

func (wrn WarningValue) Format(f fmt.State, verb rune) {
	if formatVal(f, verb, wrn) {
		return
	}
	type (
		hideMethods WarningValue
		WarningItem hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), WarningItem(wrn))
}

func (wrn WarningValue) Equal(val any) bool {
	return util.EqualValue(val, func(other WarningValue) bool {
		return wrn.Code == other.Code &&
			util.EqualFold(wrn.Agent, other.Agent) &&
			wrn.Text == other.Text
	})
}

func (wrn WarningValue) IsValid() bool {
	return wrn.Code > 0 && grammar.IsToken(wrn.Agent)
}

func (wrn WarningValue) IsZero() bool {
	return wrn.Code == 0 &&
		wrn.Agent == "" &&
		wrn.Text == ""
}

func (wrn WarningValue) Clone() WarningValue {
	return wrn
}

func (wrn WarningValue) MarshalText() ([]byte, error) {
	return []byte(wrn.String()), nil
}

func (wrn WarningValue) AppendText(b []byte) ([]byte, error) {
	return append(b, wrn.String()...), nil
}

func (wrn *WarningValue) UnmarshalText(data []byte) (finErr error) {
	if len(data) == 0 || bytes.Equal(data, []byte(`0  ""`)) {
		*wrn = WarningValue{}
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

	node, err := grammar.ParseWarningValue(data)
	if err != nil {
		return errors.Wrap(err)
	}

	*wrn = buildFromWarningValueNode(node)
	return nil
}

func buildFromWarningValueNode(node *abnf.Node) WarningValue {
	codeNode := grammar.MustGetNode(node, "warn-code")
	code, err := strconv.ParseUint(codeNode.String(), 10, 64)
	if err != nil {
		panic(errors.ErrorfWrap("invalid warning code: %w", err))
	}
	return WarningValue{
		Code:  uint(code),
		Agent: grammar.MustGetNode(node, "warn-agent").String(),
		Text:  grammar.Unquote(node.Children[4].String()),
	}
}
