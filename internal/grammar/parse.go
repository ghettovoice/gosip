package grammar

import (
	"bytes"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar/rfc3261"
	"github.com/ghettovoice/gosip/internal/grammar/rfc3966"
)

const (
	ErrEmptyInput     Error = "empty input"
	ErrMalformedInput Error = "malformed input"
)

// NewMalformedInputError creates a new error prefixed with [ErrMalformedInput]
// and wrapped with trace data.
func NewMalformedInputError(args ...any) error {
	return errors.Prefix(ErrMalformedInput, args...)
}

// NewMalformedInputErrorWrap creates a new error prefixed with [ErrMalformedInput]
// and wrapped with trace data.
// It is similar to calling [NewMalformedInputError] and then [errors.Wrap].
func NewMalformedInputErrorWrap(args ...any) error {
	return errors.GetCaller().Prefix(ErrMalformedInput, args...)
}

func ParseSIPURI[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().SIPURI([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseSIPSURI[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().SIPSURI([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseTelURI[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3966.Rules().TelephoneUri([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseRequest[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().Request([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseResponse[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().Response([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseMessage[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().SIPMessage([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseMessageStart[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := MessageStart(append(bytes.Trim([]byte(s), "\r\n"), '\r', '\n'), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseMessageHeader[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().MessageHeader(append(bytes.Trim([]byte(s), "\r\n"), '\r', '\n'), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseHostport[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().Hostport([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseEncoding[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().Encoding([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseLanguage[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().Language([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseMediaRange[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().MediaRange([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseAcceptRange[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().AcceptRange([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseInfo[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().Info([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseContactParam[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().ContactParam([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseViaParm[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().ViaParm([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}

func ParseWarningValue[T ~string | ~[]byte](s T) (*abnf.Node, error) {
	if len(s) == 0 {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrEmptyInput)
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().WarningValue([]byte(s), ns); err != nil {
		return nil, NewMalformedInputErrorWrap(err)
	}

	n := ns.Best()
	if nl, il := n.Len(), len(s); nl < il {
		return nil, NewMalformedInputErrorWrap("node length %d < input length %d", nl, il)
	}

	return n, nil
}
