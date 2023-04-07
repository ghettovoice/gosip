package sip

import (
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

const (
	RequestMethodAck       = "ACK"
	RequestMethodBye       = "BYE"
	RequestMethodCancel    = "CANCEL"
	RequestMethodInfo      = "INFO"
	RequestMethodInvite    = "INVITE"
	RequestMethodMessage   = "MESSAGE"
	RequestMethodNotify    = "NOTIFY"
	RequestMethodOptions   = "OPTIONS"
	RequestMethodPrack     = "PRACK"
	RequestMethodPublish   = "PUBLISH"
	RequestMethodRefer     = "REFER"
	RequestMethodRegister  = "REGISTER"
	RequestMethodSubscribe = "SUBSCRIBE"
	RequestMethodUpdate    = "UPDATE"
)

type Request struct {
	Method   string
	URI      URI
	Proto    Proto
	Headers  Headers
	Body     []byte
	Metadata Metadata
}

func (req *Request) MessageHeaders() Headers { return req.Headers }

func (req *Request) SetMessageHeaders(headers Headers) Message {
	req.Headers = headers
	return req
}

func (req *Request) MessageBody() []byte { return req.Body }

func (req *Request) SetMessageBody(body []byte) Message {
	req.Body = body
	return req
}

func (req *Request) MessageMetadata() Metadata { return req.Metadata }

func (req *Request) SetMessageMetadata(data Metadata) Message {
	req.Metadata = data
	return req
}

func (req *Request) RenderMessageTo(w io.Writer) error {
	if req == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, req.Method, " "); err != nil {
		return err
	}
	if req.URI != nil {
		if err := utils.RenderTo(w, req.URI); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(w, " ", req.Proto, "\r\n"); err != nil {
		return err
	}
	if err := renderHeaders(w, req.Headers); err != nil {
		return err
	}
	if _, err := fmt.Fprint(w, "\r\n"); err != nil {
		return err
	}
	if _, err := w.Write(req.Body); err != nil {
		return err
	}
	return nil
}

func (req *Request) RenderMessage() string {
	if req == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	req.RenderMessageTo(sb)
	return sb.String()
}

func (req *Request) Clone() Message {
	if req == nil {
		return nil
	}
	req2 := *req
	req2.URI = utils.Clone[URI](req.URI)
	req2.Headers = req.Headers.Clone()
	req2.Body = slices.Clone(req.Body)
	req2.Metadata = maps.Clone(req.Metadata)
	return &req2
}

func (req *Request) Equal(val any) bool {
	var other *Request
	switch v := val.(type) {
	case Request:
		other = &v
	case *Request:
		other = v
	default:
		return false
	}

	if req == other {
		return true
	} else if req == nil || other == nil {
		return false
	}

	return utils.UCase(req.Method) == utils.UCase(other.Method) &&
		req.Proto.Equal(other.Proto) &&
		utils.IsEqual(req.URI, other.URI) &&
		compareHeaders(req.Headers, other.Headers) &&
		slices.Equal(req.Body, other.Body)
}

func (req *Request) IsValid() bool {
	return req != nil &&
		grammar.IsToken(req.Method) &&
		utils.IsValid(req.URI) &&
		req.Proto.IsValid() &&
		validateHeaders(req.Headers) &&
		req.Headers.Has("From") &&
		req.Headers.Has("To") &&
		req.Headers.Has("Call-ID") &&
		req.Headers.Has("CSeq") &&
		req.Headers.Has("Max-Forwards") &&
		req.Headers.Has("Via")
}

type InboundRequest interface {
	Message
	Respond(res *Response) error
}
