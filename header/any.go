package header

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

// Any implements a generic header.
// It can be used to parse/append any headers that aren't natively supported by the lib.
type Any struct {
	Name  string
	Value string
}

func (hdr *Any) CanonicName() Name { return CanonicName(hdr.Name) }

func (hdr *Any) CompactName() Name { return CanonicName(hdr.Name) }

func (hdr *Any) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errtrace.Wrap2(fmt.Fprint(w, hdr.CanonicName(), ": ", hdr.RenderValue()))
}

func (hdr *Any) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (hdr *Any) String() string {
	return hdr.RenderValue()
}

// RenderValue returns the header value without the name prefix.
func (hdr *Any) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return hdr.Value
}

func (hdr *Any) Format(f fmt.State, verb rune) {
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
		type hideMethods Any
		type Any hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*Any)(hdr))
		return
	}
}

func (hdr *Any) Clone() Header {
	if hdr == nil {
		return nil
	}

	hdr2 := *hdr
	return &hdr2
}

func (hdr *Any) Equal(val any) bool {
	var other *Any
	switch v := val.(type) {
	case Any:
		other = &v
	case *Any:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return util.EqFold(hdr.Name, other.Name) && hdr.Value == other.Value
}

func (hdr *Any) IsValid() bool { return hdr != nil && grammar.IsToken(hdr.Name) }

func (hdr *Any) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

var zeroAny Any

func (hdr *Any) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = zeroAny
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(*Any)
	if !ok {
		*hdr = zeroAny
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, hdr))
	}

	*hdr = *h
	return nil
}

func buildFromExtensionHeaderNode(node *abnf.Node) *Any {
	return &Any{node.Children[0].String(), grammar.MustGetNode(node, "header-value").String()}
}
