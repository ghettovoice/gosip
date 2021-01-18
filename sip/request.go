package sip

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/ghettovoice/gosip/log"
)

// Request RFC 3261 - 7.1.
type Request interface {
	Message
	Method() RequestMethod
	SetMethod(method RequestMethod)
	Recipient() Uri
	SetRecipient(recipient Uri)
	/* Common Helpers */
	IsInvite() bool
}

type request struct {
	message
	method    RequestMethod
	recipient Uri
}

func NewRequest(
	messID MessageID,
	method RequestMethod,
	recipient Uri,
	sipVersion string,
	hdrs []Header,
	body string,
	fields log.Fields,
) Request {
	req := new(request)
	if messID == "" {
		req.messID = NextMessageID()
	} else {
		req.messID = messID
	}
	req.startLine = req.StartLine
	req.sipVersion = sipVersion
	req.headers = newHeaders(hdrs)
	req.method = method
	req.recipient = recipient
	req.fields = fields.WithFields(log.Fields{
		"request_id": req.messID,
	})

	if strings.TrimSpace(body) != "" {
		req.SetBody(body, true)
	}

	return req
}

func (req *request) Short() string {
	if req == nil {
		return "<nil>"
	}

	fields := log.Fields{
		"method":    req.Method(),
		"recipient": req.Recipient(),
	}
	if cseq, ok := req.CSeq(); ok {
		fields["sequence"] = cseq.SeqNo
	}
	fields = req.Fields().WithFields(fields)

	return fmt.Sprintf("sip.Request<%s>", fields)
}

func (req *request) Method() RequestMethod {
	req.mu.RLock()
	defer req.mu.RUnlock()
	return req.method
}
func (req *request) SetMethod(method RequestMethod) {
	req.mu.Lock()
	req.method = method
	req.mu.Unlock()
}

func (req *request) Recipient() Uri {
	req.mu.RLock()
	defer req.mu.RUnlock()
	return req.recipient
}
func (req *request) SetRecipient(recipient Uri) {
	req.mu.Lock()
	req.recipient = recipient
	req.mu.Unlock()
}

// StartLine returns Request Line - RFC 2361 7.1.
func (req *request) StartLine() string {
	var buffer bytes.Buffer

	// Every SIP request starts with a Request Line - RFC 2361 7.1.
	buffer.WriteString(
		fmt.Sprintf(
			"%s %s %s",
			string(req.Method()),
			req.Recipient(),
			req.SipVersion(),
		),
	)

	return buffer.String()
}

func (req *request) Clone() Message {
	req.mu.RLock()
	hdrs := req.headers.CloneHeaders()
	req.mu.RUnlock()

	return NewRequest(
		"",
		req.Method(),
		req.Recipient().Clone(),
		req.SipVersion(),
		hdrs,
		req.Body(),
		req.Fields(),
	)
}

func (req *request) WithFields(fields log.Fields) Message {
	req.mu.Lock()
	req.fields = req.fields.WithFields(fields)
	req.mu.Unlock()

	return req
}

func (req *request) IsInvite() bool {
	return req.Method() == INVITE
}

func (req *request) IsAck() bool {
	return req.Method() == ACK
}

func (req *request) IsCancel() bool {
	return req.Method() == CANCEL
}

func (req *request) Source() string {
	req.mu.RLock()
	if req.src != "" {
		defer req.mu.RUnlock()
		return req.src
	}
	req.mu.RUnlock()

	viaHop, ok := req.ViaHop()
	if !ok {
		return ""
	}

	var (
		host string
		port Port
	)

	if received, ok := viaHop.Params.Get("received"); ok && received.String() != "" {
		host = received.String()
	} else {
		host = viaHop.Host
	}

	if rport, ok := viaHop.Params.Get("rport"); ok && rport != nil && rport.String() != "" {
		p, _ := strconv.Atoi(rport.String())
		port = Port(uint16(p))
	} else if viaHop.Port != nil {
		port = *viaHop.Port
	} else {
		port = DefaultPort(req.Transport())
	}

	return fmt.Sprintf("%v:%v", host, port)
}

func (req *request) Destination() string {
	req.mu.RLock()
	if req.dest != "" {
		defer req.mu.RUnlock()
		return req.dest
	}
	req.mu.RUnlock()

	var uri *SipUri
	if hdrs := req.GetHeaders("Route"); len(hdrs) > 0 {
		routeHeader := hdrs[0].(*RouteHeader)
		if len(routeHeader.Addresses) > 0 {
			uri = routeHeader.Addresses[0].(*SipUri)
		}
	}
	if uri == nil {
		if u, ok := req.Recipient().(*SipUri); ok {
			uri = u
		} else {
			return ""
		}
	}

	host := uri.FHost
	var port Port
	if uri.FPort == nil {
		port = DefaultPort(req.Transport())
	} else {
		port = *uri.FPort
	}

	return fmt.Sprintf("%v:%v", host, port)
}

// NewAckForInvite creates ACK request for 2xx INVITE
// https://tools.ietf.org/html/rfc3261#section-13.2.2.4
func NewAckRequest(ackID MessageID, inviteRequest Request, inviteResponse Response, body string, fields log.Fields) Request {
	recipient := inviteRequest.Recipient()
	if contact, ok := inviteResponse.Contact(); ok {
		recipient = contact.Address
	}
	ackRequest := NewRequest(
		ackID,
		ACK,
		recipient,
		inviteRequest.SipVersion(),
		[]Header{},
		body,
		inviteRequest.Fields().
			WithFields(fields).
			WithFields(log.Fields{
				"invite_request_id":  inviteRequest.MessageID(),
				"invite_response_id": inviteResponse.MessageID(),
			}),
	)

	CopyHeaders("Via", inviteRequest, ackRequest)
	if inviteResponse.IsSuccess() {
		// update branch, 2xx ACK is separate Tx
		viaHop, _ := ackRequest.ViaHop()
		viaHop.Params.Add("branch", String{Str: GenerateBranch()})
	}

	if len(inviteRequest.GetHeaders("Route")) > 0 {
		CopyHeaders("Route", inviteRequest, ackRequest)
	} else {
		for _, h := range inviteResponse.GetHeaders("Record-Route") {
			uris := make([]Uri, 0)
			for i := len(h.(*RecordRouteHeader).Addresses) - 1; i >= 0; i-- {
				uris = append(uris, h.(*RecordRouteHeader).Addresses[i].Clone())
			}
			ackRequest.AppendHeader(&RouteHeader{
				Addresses: uris,
			})
		}
	}

	maxForwardsHeader := MaxForwards(70)
	ackRequest.AppendHeader(&maxForwardsHeader)
	CopyHeaders("From", inviteRequest, ackRequest)
	CopyHeaders("To", inviteResponse, ackRequest)
	CopyHeaders("Call-ID", inviteRequest, ackRequest)
	CopyHeaders("CSeq", inviteRequest, ackRequest)
	cseq, _ := ackRequest.CSeq()
	cseq.MethodName = ACK

	return ackRequest
}

func NewCancelRequest(cancelID MessageID, requestForCancel Request, fields log.Fields) Request {
	cancelReq := NewRequest(
		cancelID,
		CANCEL,
		requestForCancel.Recipient(),
		requestForCancel.SipVersion(),
		[]Header{},
		"",
		requestForCancel.Fields().
			WithFields(fields).
			WithFields(log.Fields{
				"cancelling_request_id": requestForCancel.MessageID(),
			}),
	)

	viaHop, _ := requestForCancel.ViaHop()
	cancelReq.AppendHeader(ViaHeader{viaHop.Clone()})
	CopyHeaders("Route", requestForCancel, cancelReq)
	maxForwardsHeader := MaxForwards(70)
	cancelReq.AppendHeader(&maxForwardsHeader)
	CopyHeaders("From", requestForCancel, cancelReq)
	CopyHeaders("To", requestForCancel, cancelReq)
	CopyHeaders("Call-ID", requestForCancel, cancelReq)
	CopyHeaders("CSeq", requestForCancel, cancelReq)
	cseq, _ := cancelReq.CSeq()
	cseq.MethodName = CANCEL

	return cancelReq
}

func CopyRequest(req Request) Request {
	hdrs := make([]Header, 0)
	for _, header := range req.Headers() {
		hdrs = append(hdrs, header.Clone())
	}

	return NewRequest(
		req.MessageID(),
		req.Method(),
		req.Recipient().Clone(),
		req.SipVersion(),
		hdrs,
		req.Body(),
		req.Fields(),
	)
}
