package uri

import (
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/constraints"
	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

// Any implements any URI (usually not SIP or tel).
type Any url.URL

func (u *Any) Clone() URI {
	if u == nil {
		return nil
	}
	u2 := *u
	if u.User != nil {
		if pwd, ok := u.User.Password(); ok {
			u2.User = url.UserPassword(u.User.Username(), pwd)
		} else {
			u2.User = url.User(u.User.Username())
		}
	}
	return &u2
}

func (u *Any) RenderTo(w io.Writer) error {
	if u == nil {
		return nil
	}
	_, err := fmt.Fprint(w, (*url.URL)(u).String())
	return err
}

func (u *Any) Render() string {
	if u == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = u.RenderTo(sb)
	return sb.String()
}

func (u *Any) String() string {
	if u == nil {
		return nilTag
	}
	return u.Render()
}

func (u *Any) Equal(val any) bool {
	var other *Any
	switch v := val.(type) {
	case Any:
		other = &v
	case *Any:
		other = v
	default:
		return false
	}

	if u == other {
		return true
	} else if u == nil || other == nil {
		return false
	}
	// FIXME compare by url.URL parts
	return stringutils.LCase(u.Render()) == stringutils.LCase(other.Render())
}

func (u *Any) IsValid() bool {
	return u != nil &&
		(stringutils.TrimSP(u.Opaque) != "" ||
			stringutils.TrimSP(u.Host) != "" ||
			stringutils.TrimSP(u.Path) != "" ||
			stringutils.TrimSP(u.RawPath) != "")
}

func ParseAny[T constraints.Byteseq](src T) (*Any, error) {
	if len(src) == 0 {
		return nil, grammar.ErrEmptyInput
	}
	u, err := url.Parse(string(src))
	if err != nil {
		return nil, errors.Join(grammar.ErrMalformedInput, err)
	}
	return (*Any)(u), nil
}

func buildFromAnyNode(node *abnf.Node) *Any {
	u, _ := url.Parse(node.String())
	return (*Any)(u)
}
