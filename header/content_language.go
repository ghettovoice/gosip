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

type ContentLanguage []Language

func (ContentLanguage) CanonicName() Name { return "Content-Language" }

func (ContentLanguage) CompactName() Name { return "Content-Language" }

func (hdr ContentLanguage) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

func (hdr ContentLanguage) renderValueTo(w io.Writer) (num int, err error) {
	return errtrace.Wrap2(renderHdrEntries(w, hdr))
}

func (hdr ContentLanguage) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (hdr ContentLanguage) String() string { return hdr.RenderValue() }

func (hdr ContentLanguage) RenderValue() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb) //nolint:errcheck
	return sb.String()
}

func (hdr ContentLanguage) Format(f fmt.State, verb rune) {
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
		type hideMethods ContentLanguage
		type ContentLanguage hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), ContentLanguage(hdr))
		return
	}
}

func (hdr ContentLanguage) Clone() Header { return slices.Clone(hdr) }

func (hdr ContentLanguage) Equal(val any) bool {
	var other ContentLanguage
	switch v := val.(type) {
	case ContentLanguage:
		other = v
	case *ContentLanguage:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(lang1, lang2 Language) bool { return lang1.Equal(lang2) })
}

func (hdr ContentLanguage) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(lang Language) bool { return !lang.IsValid() })
}

func (hdr ContentLanguage) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *ContentLanguage) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = nil
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(ContentLanguage)
	if !ok {
		*hdr = nil
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromContentLanguageNode(node *abnf.Node) ContentLanguage {
	langNodes := node.GetNodes("language-tag")
	h := make(ContentLanguage, len(langNodes))
	for i, langNode := range langNodes {
		h[i] = Language(langNode.String())
	}
	return h
}

type Language string

func (lng Language) IsValid() bool { return grammar.IsToken(lng) }

func (lng Language) Equal(val any) bool {
	var other Language
	switch v := val.(type) {
	case Language:
		other = v
	case *Language:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return util.EqFold(lng, other)
}
