package header

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type Timestamp struct {
	RequestTime   time.Time
	ResponseDelay time.Duration
}

func (*Timestamp) CanonicName() Name { return "Timestamp" }

func (*Timestamp) CompactName() Name { return "Timestamp" }

func (hdr *Timestamp) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

func (hdr *Timestamp) renderValueTo(w io.Writer) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	if !hdr.RequestTime.IsZero() {
		cw.Fprintf("%.3f", float64(hdr.RequestTime.UnixNano())/1e9)
	} else {
		cw.Fprint("0")
	}
	if hdr.ResponseDelay > 0 {
		cw.Fprintf(" %.3f", hdr.ResponseDelay.Seconds())
	}
	return errtrace.Wrap2(cw.Result())
}

func (hdr *Timestamp) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (hdr *Timestamp) RenderValue() string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb) //nolint:errcheck
	return sb.String()
}

func (hdr *Timestamp) String() string { return hdr.RenderValue() }

func (hdr *Timestamp) Format(f fmt.State, verb rune) {
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
		type hideMethods Timestamp
		type Timestamp hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*Timestamp)(hdr))
		return
	}
}

func (hdr *Timestamp) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := *hdr
	return &hdr2
}

func (hdr *Timestamp) Equal(val any) bool {
	var other *Timestamp
	switch v := val.(type) {
	case Timestamp:
		other = &v
	case *Timestamp:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return hdr.RequestTime.Equal(other.RequestTime) && hdr.ResponseDelay == other.ResponseDelay
}

func (hdr *Timestamp) IsValid() bool {
	return hdr != nil && !hdr.RequestTime.IsZero() && hdr.ResponseDelay >= 0
}

func (hdr *Timestamp) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

var zeroTimestamp Timestamp

func (hdr *Timestamp) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = zeroTimestamp
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(*Timestamp)
	if !ok {
		*hdr = zeroTimestamp
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, hdr))
	}

	*hdr = *h
	return nil
}

func buildFromTimestampNode(node *abnf.Node) *Timestamp {
	var hdr Timestamp
	sec, _ := strconv.ParseFloat(string(append(node.Children[2].Value, node.Children[3].Value...)), 64)
	hdr.RequestTime = time.UnixMilli(int64(sec * 1e3)).UTC()
	if delNode, ok := node.GetNode("delay"); ok && !delNode.IsEmpty() {
		sec, _ = strconv.ParseFloat(string(append(delNode.Children[0].Value, delNode.Children[1].Value...)), 64)
		hdr.ResponseDelay = time.Duration(sec * float64(time.Second))
	}
	return &hdr
}
