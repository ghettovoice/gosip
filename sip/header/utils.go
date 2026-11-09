package header

import (
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/internal/util"
)

func parseDeltaSecs(value string) (time.Duration, error) {
	seconds, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, errors.ErrorfWrap("invalid expiry value: %w", err)
	}
	return time.Duration(seconds) * time.Second, nil
}

func validDeltaSecs(duration time.Duration) bool {
	return duration >= 0 && duration <= time.Duration(math.MaxUint32)*time.Second
}

func compareHdrParams(params1, params2 Values, specParams map[string]bool, caseSensParams ...string) bool {
	switch {
	case len(params1) == 0 && len(params2) == 0:
		return true
	case len(params1) == 0:
		return !hasSpecHdrParam(params2, specParams)
	case len(params2) == 0:
		return !hasSpecHdrParam(params1, specParams)
	}

	checked := make(map[string]bool)
	// Any non-special parameters appearing in only one list are ignored.
	// First, traverse over self-parameters, compare values appearing in both lists,
	// check on speciality and save checked param names.
	for k := range params1 {
		if params2.Has(k) {
			// Any parameter appearing in both URIs must match.
			v1, _ := params1.Last(k)
			v2, _ := params2.Last(k)
			if !slices.Contains(caseSensParams, util.ToLower(k)) {
				if !grammar.IsQuoted(v1) {
					v1 = util.ToLower(v1)
				}
				if !grammar.IsQuoted(v2) {
					v2 = util.ToLower(v2)
				}
			}
			if v1 != v2 {
				return false
			}
		} else if specParams[util.ToLower(k)] {
			// Any special SIP URI parameter appearing in one URI must appear in the other.
			return false
		}

		checked[util.ToLower(k)] = true
	}
	// Then need only check that there are no non-checked special parameters in the other list.
	for k := range specParams {
		if checked[k] {
			continue
		}
		if params2.Has(k) {
			return false
		}
	}
	return true
}

func hasSpecHdrParam(params Values, specParams map[string]bool) bool {
	for k := range specParams {
		if params.Has(k) {
			return true
		}
	}
	return false
}

func validateHdrParams(params Values) bool {
	for k := range params {
		if !grammar.IsToken(k) {
			return false
		}

		v, _ := params.Last(k)
		if v != "" &&
			(!grammar.IsToken(v) &&
				!grammar.IsHost(v) &&
				!grammar.IsQuoted(v)) {
			return false
		}
	}
	return true
}

func cloneHdrVals[H ~[]E, E interface{ Clone() E }](hdr H) H {
	var clone H
	if hdr == nil {
		return clone
	}

	clone = make(H, len(hdr))
	for i := range hdr {
		clone[i] = hdr[i].Clone()
	}
	return clone
}

// unmarshalHdrJSON accepts parsed value and pointer forms, and assigns only after successful decoding.
func unmarshalHdrJSON[T any](data []byte, dst *T, name Name, emptyValue func(string) bool) error {
	parsed, err := FromJSON(data)
	if err != nil {
		return errors.Wrap(err)
	}

	var result T
	switch hdr := any(parsed).(type) {
	case nil:
	case *T:
		if hdr == nil {
			return errors.Wrap(newUnexpectHdrTypeErr(parsed))
		}
		result = *hdr
	case T:
		result = hdr
	default:
		anyHdr, ok := parsed.(*Any)
		if !ok ||
			anyHdr == nil ||
			emptyValue == nil ||
			!anyHdr.CanonicName().Equal(name) ||
			!emptyValue(anyHdr.Value) {
			return errors.Wrap(newUnexpectHdrTypeErr(parsed))
		}
	}

	*dst = result
	return nil
}

func isEmptyHdrVal(value string) bool {
	return value == ""
}

func isEmptyNameAddrVal(value string) bool {
	return value == "" || value == "<>"
}

type hdrValRenderer interface {
	renderValTo(w io.Writer, opts ...RenderOptions) (int, error)
}

type hdrRenderer interface {
	hdrValRenderer
	CanonicName() Name
	CompactName() Name
}

type valRenderer interface {
	renderTo(w io.Writer) (int, error)
}

// render writes r to a pooled string builder and returns the result.
func render(r shared.Renderer, opts ...RenderOptions) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	_, _ = renderTo(sb, r, opts...)
	return sb.String()
}

func renderTo(w io.Writer, r shared.Renderer, opts ...RenderOptions) (int, error) {
	if r == nil {
		return 0, nil
	}
	return errors.Wrap2(r.RenderTo(w, opts...))
}

func renderHdrVal(hdr hdrValRenderer) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	_, _ = hdr.renderValTo(sb)
	return sb.String()
}

func renderVal(value valRenderer) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	_, _ = value.renderTo(sb)
	return sb.String()
}

func renderHdrName(hdr hdrRenderer, opts ...RenderOptions) Name {
	if util.LastSliceElem(opts, RenderOptions{}).Compact {
		return hdr.CompactName()
	}
	return hdr.CanonicName()
}

func renderHdrTo(w io.Writer, hdr hdrRenderer, opts ...RenderOptions) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(renderHdrName(hdr, opts...), ": ")
	cw.Call(func(w io.Writer) (int, error) {
		return hdr.renderValTo(w, opts...)
	})
	return errors.Wrap2(cw.Result())
}

func renderHdrValsTo[H ~[]E, E valRenderer](w io.Writer, values H) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	for i := range values {
		if i > 0 {
			cw.Fprint(", ")
		}
		cw.Call(values[i].renderTo)
	}
	return errors.Wrap2(cw.Result())
}

func renderHdrTokensTo[H ~[]E, E ~string](w io.Writer, values H) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	for i := range values {
		if i > 0 {
			cw.Fprint(", ")
		}
		cw.WriteString(string(values[i]))
	}
	return errors.Wrap2(cw.Result())
}

func renderHdrParamsTo(w io.Writer, params Values, addQParam bool) (num int, err error) {
	if len(params) == 0 {
		return 0, nil
	}

	// Sort parameters in alphabet order, but with "q" parameter always the first place.
	// If missing the "q" param, then dump it with the default value.
	// RFC 2616 Section 14.1.
	var kvs [][]string
	if addQParam && !params.Has("q") {
		kvs = append(kvs, []string{"q", "1"})
	}

	for k := range params {
		v, _ := params.Last(k)
		kvs = append(kvs, []string{util.ToLower(k), v})
	}
	slices.SortFunc(kvs, func(a, b []string) int {
		if a[0] == "q" && b[0] != "q" {
			return -1
		} else if a[0] != "q" && b[0] == "q" {
			return 1
		}
		return util.CompareKeys(a, b)
	})

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	for _, kv := range kvs {
		cw.Fprint(";", kv[0])
		if kv[1] != "" {
			cw.Fprint("=", kv[1])
		}
	}
	return errors.Wrap2(cw.Result())
}

func renderDeltaSecsTo(w io.Writer, dur time.Duration) (int, error) {
	return errors.Wrap2(fmt.Fprint(w, int64(dur/time.Second)))
}

func renderDeltaSecs(dur time.Duration) string {
	return strconv.FormatInt(int64(dur/time.Second), 10)
}

// formatTstampSecs formats nanoseconds as a decimal seconds string with up to 9
// fractional digits and no trailing zeros (e.g. 543_000_000 ns → "0.543").
func formatTstampSecs(ns int64) string {
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

func formatHdr(f fmt.State, verb rune, hdr Header) bool {
	switch verb {
	case 's':
		if f.Flag('+') {
			_, _ = hdr.RenderTo(f)
		} else {
			fmt.Fprint(f, hdr.RenderValue())
		}
		return true
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(render(hdr)))
		} else {
			fmt.Fprint(f, strconv.Quote(hdr.RenderValue()))
		}
		return true
	default:
		return false
	}
}

func formatRndr(f fmt.State, verb rune, r shared.Renderer) bool {
	switch verb {
	case 's':
		if f.Flag('+') {
			_, _ = renderTo(f, r)
		} else {
			fmt.Fprint(f, render(r))
		}
		return true
	case 'q':
		fmt.Fprint(f, strconv.Quote(render(r)))
		return true
	default:
		return false
	}
}

func formatVal(f fmt.State, verb rune, value valRenderer) bool {
	switch verb {
	case 's':
		fmt.Fprint(f, renderVal(value))
		return true
	case 'q':
		fmt.Fprint(f, strconv.Quote(renderVal(value)))
		return true
	default:
		if !f.Flag('+') && !f.Flag('#') {
			fmt.Fprint(f, renderVal(value))
			return true
		}
		return false
	}
}
