package header

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type Timestamp struct {
	RequestTime   time.Time
	ResponseDelay time.Duration
}

func (*Timestamp) CanonicName() Name { return "Timestamp" }

func (*Timestamp) CompactName() Name { return "Timestamp" }

func (hdr *Timestamp) RenderTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)
	return errors.Wrap2(cw.Result())
}

func (hdr *Timestamp) renderValueTo(w io.Writer) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	if !hdr.RequestTime.IsZero() {
		cw.Fprint(formatTimestampSecs(hdr.RequestTime.UnixNano()))
	} else {
		cw.Fprint("0")
	}

	if hdr.ResponseDelay > 0 {
		cw.Fprint(" ")
		cw.Fprint(formatTimestampSecs(int64(hdr.ResponseDelay)))
	}

	return errors.Wrap2(cw.Result())
}

// formatTimestampSecs formats nanoseconds as a decimal seconds string with up to 9
// fractional digits and no trailing zeros (e.g. 543_000_000 ns → "0.543").
func formatTimestampSecs(ns int64) string {
	sec := ns / int64(time.Second)
	frac := ns % int64(time.Second)

	if frac == 0 {
		return strconv.FormatInt(sec, 10)
	}

	// Ensure fractional part is positive for negative timestamps.
	if frac < 0 {
		sec--
		frac += int64(time.Second)
	}

	// Format 9-digit fractional part, then trim trailing zeros.
	buf := fmt.Sprintf("%d.%09d", sec, frac)
	i := len(buf)
	for i > 0 && buf[i-1] == '0' {
		i--
	}
	return buf[:i]
}

func (hdr *Timestamp) Render(opts ...RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	_, _ = hdr.RenderTo(sb, opts...)
	return sb.String()
}

func (hdr *Timestamp) RenderValue() string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	_, _ = hdr.renderValueTo(sb)
	return sb.String()
}

func (hdr *Timestamp) String() string { return hdr.RenderValue() }

func (hdr *Timestamp) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			_, _ = hdr.RenderTo(f)
			return
		}
		fmt.Fprint(f, hdr.String())
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(hdr.Render()))
			return
		}
		fmt.Fprint(f, strconv.Quote(hdr.String()))
		return
	default:
		type (
			hideMethods Timestamp
			Timestamp   hideMethods
		)
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
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Timestamp) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		return errors.Wrap(err)
	}

	if gh == nil {
		*hdr = Timestamp{}
		return nil
	}

	h, ok := gh.(*Timestamp)
	if !ok {
		ah, ok := gh.(*Any)
		if ok && ah.CanonicName().Equal(hdr.CanonicName()) && len(ah.Value) == 0 {
			return nil
		}
		return errors.Wrap(newUnexpectHdrTypeErr(gh))
	}

	*hdr = *h
	return nil
}

func buildFromTimestampNode(node *abnf.Node) *Timestamp {
	var hdr Timestamp

	// node.Children: [0]="Timestamp" [1]=HCOLON [2]=1*(DIGIT) [3]=[ "." *(DIGIT) ] [4]=[ LWS delay ]
	intNs, fracNs, err := parseSecsNodes(node.Children[2].Value, node.Children[3].Value)
	if err != nil {
		panic(errors.ErrorfWrap("invalid request time: %w", err))
	}
	hdr.RequestTime = time.Unix(intNs, fracNs).UTC()

	if delNode, ok := node.GetNode("delay"); ok && !delNode.IsEmpty() {
		// delNode.Children: [0]=*(DIGIT) [1]=[ "." *(DIGIT) ]
		intNs, fracNs, err = parseSecsNodes(delNode.Children[0].Value, delNode.Children[1].Value)
		if err != nil {
			panic(errors.ErrorfWrap("invalid response delay: %w", err))
		}

		hdr.ResponseDelay = time.Duration(intNs*int64(time.Second) + fracNs)
	}

	return &hdr
}

// parseSecsNodes parses integer and optional fractional parts of a seconds value
// from raw ABNF node bytes, returning (wholeSecs, fracNanos, error).
// intBytes contains the digits of the integer part; fracBytes contains the optional
// "." *(DIGIT) node value (may be empty if the optional node was absent).
func parseSecsNodes(intBytes, fracBytes []byte) (wholeSecs, fracNanos int64, err error) {
	if len(intBytes) > 0 {
		wholeSecs, err = strconv.ParseInt(string(intBytes), 10, 64)
		if err != nil {
			return 0, 0, err
		}
	}

	// fracBytes is the value of the Optional node "[ '.' *(DIGIT) ]".
	// It is either empty (optional absent) or starts with '.'.
	if len(fracBytes) <= 1 {
		return wholeSecs, 0, nil
	}

	// Drop the leading '.'.
	digits := fracBytes[1:]

	// Pad or truncate to exactly 9 digits for nanoseconds.
	const nsDigits = 9
	var buf [nsDigits]byte
	if len(digits) >= nsDigits {
		copy(buf[:], digits[:nsDigits])
	} else {
		copy(buf[:], digits)
		// Zero-pad on the right.
		for i := len(digits); i < nsDigits; i++ {
			buf[i] = '0'
		}
	}

	fracNanos, err = strconv.ParseInt(string(buf[:]), 10, 64)
	if err != nil {
		return 0, 0, err
	}

	return wholeSecs, fracNanos, nil
}
