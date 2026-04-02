package uri

import (
	"fmt"
	"io"
	"net/url"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

// Any implements any URI (usually not SIP or tel).
type Any struct {
	url.URL
}

// Clone returns a deep copy of the Any URI.
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

// Scheme returns the URI scheme.
func (u *Any) Scheme() string {
	if u == nil {
		return ""
	}
	return u.URL.Scheme
}

// RenderTo writes the URI to the provided writer.
func (u *Any) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if u == nil {
		return 0, nil
	}
	return errors.Wrap2(fmt.Fprint(w, u.URL.String()))
}

// Render returns the string representation of the URI.
func (u *Any) Render(opts *RenderOptions) string {
	if u == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	u.RenderTo(sb, opts) //nolint:errcheck

	return sb.String()
}

// String returns the string representation of the URI.
func (u *Any) String() string {
	if u == nil {
		return ""
	}
	return u.Render(nil)
}

// Format implements fmt.Formatter for custom formatting of the Any URI.
func (u *Any) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			u.RenderTo(f, nil) //nolint:errcheck
			return
		}

		fmt.Fprint(f, u.String())

		return
	case 'q':
		fmt.Fprint(f, strconv.Quote(u.String()))
		return
	default:
		type (
			hideMethods Any
			Any         hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), (*Any)(u))

		return
	}
}

// Equal compares this URI with another for equality.
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
	return util.EqFold(u.Render(nil), other.Render(nil))
}

// IsValid checks whether the Any URI is syntactically valid.
func (u *Any) IsValid() bool {
	return u != nil &&
		(util.TrimSP(u.Opaque) != "" ||
			util.TrimSP(u.Host) != "" ||
			util.TrimSP(u.Path) != "" ||
			util.TrimSP(u.RawPath) != "")
}

// MarshalText implements [encoding.TextMarshaler].
func (u *Any) MarshalText() ([]byte, error) {
	return errors.Wrap2(u.AppendText(nil))
}

func (u *Any) AppendText(b []byte) ([]byte, error) {
	return append(b, u.String()...), nil
}

// UnmarshalText implements [encoding.TextUnmarshaler].
func (u *Any) UnmarshalText(text []byte) error {
	if u == nil {
		return errors.NewInvalidArgumentErrorWrap("nil URI")
	}

	if len(text) == 0 {
		*u = Any{}
		return nil
	}

	u1, err := ParseAny(string(text))
	if err != nil {
		*u = Any{}
		return errors.Wrap(err)
	}

	*u = *u1

	return nil
}

// ParseAny parses an arbitrary URI from the given input src (string or []byte).
func ParseAny[T ~string | ~[]byte](src T) (u *Any, err error) {
	if len(src) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(grammar.ErrEmptyInput)
	}

	defer func() {
		if rv := recover(); rv != nil {
			u = nil

			if e, ok := rv.(error); ok {
				err = errors.Wrap(e)
			} else {
				err = errors.ErrorfWrap("%v", rv)
			}
		}
	}()

	nu, err := url.Parse(string(src))
	if err != nil {
		return nil, grammar.NewMalformedInputErrorWrap(err)
	}

	return &Any{URL: *nu}, nil
}

func buildFromAnyNode(node *abnf.Node) *Any {
	u := util.Must2(url.Parse(node.String()))
	return &Any{URL: *u}
}
