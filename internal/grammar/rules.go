package grammar

import (
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/grammar/rfc3261"
)

var messageHeaders = abnf.Repeat0Inf("message-headers", rfc3261.Operators().MessageHeader)

func MessageHeaders(s []byte, ns *abnf.Nodes) error {
	return messageHeaders(s, 0, ns) //errtrace:skip
}

var messageStart = abnf.AltFirst(
	"message-start",
	rfc3261.Operators().RequestLine,
	rfc3261.Operators().StatusLine,
)

func MessageStart(s []byte, ns *abnf.Nodes) error {
	return messageStart(s, 0, ns) //errtrace:skip
}
