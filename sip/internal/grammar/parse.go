package grammar

import (
	"bytes"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/constraints"
	"github.com/ghettovoice/gosip/sip/internal/grammar/rfc3261"
	"github.com/ghettovoice/gosip/sip/internal/grammar/rfc3966"
)

var (
	ErrEmptyInput     = Error("empty input")
	ErrMalformedInput = Error("malformed input")
)

func ParseSIPURI[T constraints.Byteseq](s T) (n *abnf.Node, err error) {
	if len(s) == 0 {
		return n, ErrEmptyInput
	}

	ns := getNodes()
	defer putNodes(ns)

	n = rfc3261.SIPURI([]byte(s), ns).Best()
	if n.Len() < len(s) {
		err = ErrMalformedInput
	}
	return n, err
}

func ParseSIPSURI[T constraints.Byteseq](s T) (n *abnf.Node, err error) {
	if len(s) == 0 {
		return n, ErrEmptyInput
	}

	ns := getNodes()
	defer putNodes(ns)

	n = rfc3261.SIPSURI([]byte(s), ns).Best()
	if n.Len() < len(s) {
		err = ErrMalformedInput
	}
	return n, err
}

func ParseTelURI[T constraints.Byteseq](s T) (n *abnf.Node, err error) {
	if len(s) == 0 {
		return n, ErrEmptyInput
	}

	ns := getNodes()
	defer putNodes(ns)

	n = rfc3966.TelephoneUri([]byte(s), ns).Best()
	if n.Len() < len(s) {
		err = ErrMalformedInput
	}
	return n, err
}

func ParseRequest[T constraints.Byteseq](s T) (n *abnf.Node, err error) {
	if len(s) == 0 {
		return n, ErrEmptyInput
	}

	ns := getNodes()
	defer putNodes(ns)

	n = rfc3261.Request([]byte(s), ns).Best()
	if n.Len() < len(s) {
		err = ErrMalformedInput
	}
	return n, err
}

func ParseResponse[T constraints.Byteseq](s T) (n *abnf.Node, err error) {
	if len(s) == 0 {
		return n, ErrEmptyInput
	}

	ns := getNodes()
	defer putNodes(ns)

	n = rfc3261.Response([]byte(s), ns).Best()
	if n.Len() < len(s) {
		err = ErrMalformedInput
	}
	return n, err
}

func ParseMessage[T constraints.Byteseq](s T) (n *abnf.Node, err error) {
	if len(s) == 0 {
		return n, ErrEmptyInput
	}

	ns := getNodes()
	defer putNodes(ns)

	n = rfc3261.SIPMessage([]byte(s), ns).Best()
	if n.Len() < len(s) {
		err = ErrMalformedInput
	}
	return n, err
}

func ParseMessageStart[T constraints.Byteseq](s T) (n *abnf.Node, err error) {
	if len(s) == 0 {
		return n, ErrEmptyInput
	}

	ns := getNodes()
	defer putNodes(ns)

	n = MessageStart(append(bytes.Trim([]byte(s), "\r\n"), '\r', '\n'), ns).Best()
	if n.Len() < len(s) {
		err = ErrMalformedInput
	}
	return n, err
}

func ParseMessageHeader[T constraints.Byteseq](s T) (n *abnf.Node, err error) {
	if len(s) == 0 {
		return n, ErrEmptyInput
	}

	ns := getNodes()
	defer putNodes(ns)

	n = rfc3261.MessageHeader(append(bytes.Trim([]byte(s), "\r\n"), '\r', '\n'), ns).Best()
	if n.Len() < len(s) {
		err = ErrMalformedInput
	}
	return n, err
}
