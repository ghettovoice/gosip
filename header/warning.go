package header

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type Warning []WarningEntry

func (Warning) CanonicName() Name { return "Warning" }

func (Warning) CompactName() Name { return "Warning" }

func (hdr Warning) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

func (hdr Warning) renderValueTo(w io.Writer) (num int, err error) {
	return errtrace.Wrap2(renderHdrEntries(w, hdr))
}

func (hdr Warning) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (hdr Warning) String() string {
	return hdr.RenderValue()
}

func (hdr Warning) RenderValue() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb) //nolint:errcheck
	return sb.String()
}

func (hdr Warning) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			hdr.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, hdr.String())
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(hdr.Render(nil)))
			return
		}
		fmt.Fprint(f, strconv.Quote(hdr.String()))
		return
	default:
		type hideMethods Warning
		type Warning hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), Warning(hdr))
		return
	}
}

func (hdr Warning) Clone() Header { return cloneHdrEntries(hdr) }

func (hdr Warning) Equal(val any) bool {
	var other Warning
	switch v := val.(type) {
	case Warning:
		other = v
	case *Warning:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(wrn1, wrn2 WarningEntry) bool { return wrn1.Equal(wrn2) })
}

func (hdr Warning) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(wrn WarningEntry) bool { return !wrn.IsValid() })
}

func (hdr Warning) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *Warning) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = nil
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(Warning)
	if !ok {
		*hdr = nil
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromWarningNode(node *abnf.Node) Warning {
	warnNodes := node.GetNodes("warning-value")
	h := make(Warning, len(warnNodes))
	for i, warnNode := range warnNodes {
		h[i] = buildFromWarningEntryNode(warnNode)
	}
	return h
}

type WarningEntry struct {
	Code  uint
	Agent string
	Text  string
}

func (wrn WarningEntry) String() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	fmt.Fprintf(sb, "%d %s %q", wrn.Code, wrn.Agent, wrn.Text)
	return sb.String()
}

func (wrn WarningEntry) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		fmt.Fprint(f, wrn.String())
		return
	case 'q':
		fmt.Fprint(f, strconv.Quote(wrn.String()))
		return
	default:
		if !f.Flag('+') && !f.Flag('#') {
			fmt.Fprint(f, wrn.String())
			return
		}

		type hideMethods WarningEntry
		type WarningItem hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), WarningItem(wrn))
		return
	}
}

func (wrn WarningEntry) Equal(val any) bool {
	var other WarningEntry
	switch v := val.(type) {
	case WarningEntry:
		other = v
	case *WarningEntry:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return wrn.Code == other.Code &&
		util.EqFold(wrn.Agent, other.Agent) &&
		wrn.Text == other.Text
}

func (wrn WarningEntry) IsValid() bool { return wrn.Code > 0 && grammar.IsToken(wrn.Agent) }

func (wrn WarningEntry) IsZero() bool { return wrn.Code == 0 && wrn.Agent == "" && wrn.Text == "" }

func (wrn WarningEntry) Clone() WarningEntry { return wrn }

func (wrn WarningEntry) MarshalText() ([]byte, error) {
	return []byte(wrn.String()), nil
}

func (wrn *WarningEntry) UnmarshalText(data []byte) error {
	node, err := grammar.ParseWarningValue(data)
	if err != nil {
		*wrn = WarningEntry{}
		if errors.Is(err, grammar.ErrEmptyInput) {
			return nil
		}
		return errtrace.Wrap(err)
	}
	*wrn = buildFromWarningEntryNode(node)
	return nil
}

func buildFromWarningEntryNode(node *abnf.Node) WarningEntry {
	codeNode := grammar.MustGetNode(node, "warn-code")
	c, _ := strconv.ParseUint(codeNode.String(), 10, 64)
	return WarningEntry{
		Code:  uint(c),
		Agent: grammar.MustGetNode(node, "warn-agent").String(),
		Text:  grammar.Unquote(node.Children[4].String()),
	}
}
