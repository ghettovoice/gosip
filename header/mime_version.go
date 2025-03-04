package header

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type MIMEVersion string

func (MIMEVersion) CanonicName() Name { return "MIME-Version" }

func (MIMEVersion) CompactName() Name { return "MIME-Version" }

func (hdr MIMEVersion) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	return errtrace.Wrap2(fmt.Fprint(w, hdr.CanonicName(), ": ", hdr.RenderValue()))
}

func (hdr MIMEVersion) Render(opts *RenderOptions) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr MIMEVersion) RenderValue() string { return string(hdr) }

func (hdr MIMEVersion) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			hdr.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, string(hdr))
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(hdr.Render(nil)))
			return
		}
		fmt.Fprint(f, strconv.Quote(string(hdr)))
		return
	default:
		type hideMethods MIMEVersion
		type MIMEVersion hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), MIMEVersion(hdr))
		return
	}
}

func (hdr MIMEVersion) Clone() Header { return hdr }

func (hdr MIMEVersion) Equal(val any) bool {
	var other MIMEVersion
	switch v := val.(type) {
	case MIMEVersion:
		other = v
	case *MIMEVersion:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return util.EqFold(hdr, other)
}

var mimeVerRe = regexp.MustCompile(`^\d+\.\d+$`)

func (hdr MIMEVersion) IsValid() bool { return len(hdr) > 0 && mimeVerRe.MatchString(string(hdr)) }

func (hdr MIMEVersion) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *MIMEVersion) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = ""
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(MIMEVersion)
	if !ok {
		*hdr = ""
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromMIMEVersionNode(node *abnf.Node) MIMEVersion {
	var s []byte
	for _, n := range node.Children[2:] {
		if n.IsEmpty() {
			continue
		}
		s = append(s, n.Value...)
	}
	return MIMEVersion(s)
}
