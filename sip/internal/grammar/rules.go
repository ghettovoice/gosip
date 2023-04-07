package grammar

import (
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/sip/internal/grammar/rfc3261"
)

var messageHeaders abnf.Operator

func MessageHeaders(s []byte, ns abnf.Nodes) abnf.Nodes {
	if messageHeaders == nil {
		messageHeaders = abnf.Repeat0Inf("message-headers", rfc3261.MessageHeader)
	}
	return messageHeaders(s, ns)
}

var messageStart abnf.Operator

func MessageStart(s []byte, ns abnf.Nodes) abnf.Nodes {
	if messageStart == nil {
		messageStart = abnf.AltFirst(
			"message-start",
			rfc3261.RequestLine,
			rfc3261.StatusLine,
		)
	}
	return messageStart(s, ns)
}
