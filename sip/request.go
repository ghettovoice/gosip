package sip

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
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
	method RequestMethod,
	recipient Uri,
	sipVersion string,
	hdrs []Header,
	body string,
) Request {
	req := new(request)
	req.startLine = req.StartLine
	req.SetSipVersion(sipVersion)
	req.headers = newHeaders(hdrs)
	req.SetMethod(method)
	req.SetRecipient(recipient)

	if strings.TrimSpace(body) != "" {
		req.SetBody(body, true)
	}

	return req
}

func (req *request) Short() string {
	if req == nil {
		return "<nil>"
	}

	return fmt.Sprintf("sip.Request<%s>", req.logFields())
}

func (req *request) Method() RequestMethod {
	return req.method
}
func (req *request) SetMethod(method RequestMethod) {
	req.method = method
}

func (req *request) Recipient() Uri {
	return req.recipient
}
func (req *request) SetRecipient(recipient Uri) {
	req.recipient = recipient
}

// StartLine returns Request Line - RFC 2361 7.1.
func (req *request) StartLine() string {
	var buffer bytes.Buffer

	// Every SIP request starts with a Request Line - RFC 2361 7.1.
	buffer.WriteString(
		fmt.Sprintf(
			"%s %s %s",
			string(req.method),
			req.Recipient(),
			req.SipVersion(),
		),
	)

	return buffer.String()
}

func (req *request) Clone() Message {
	return NewRequest(
		req.Method(),
		req.Recipient().Clone(),
		req.SipVersion(),
		req.headers.CloneHeaders(),
		req.Body(),
	)
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
	if req.src != "" {
		return req.src
	}

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
	if req.dest != "" {
		return req.dest
	}

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
func NewAckRequest(inviteRequest Request, inviteResponse Response) Request {
	contact, _ := inviteResponse.Contact()
	ackRequest := NewRequest(ACK, contact.Address, inviteResponse.SipVersion(), []Header{}, "")

	CopyHeaders("Via", inviteRequest, ackRequest)
	viaHop, _ := ackRequest.ViaHop()
	// update branch, 2xx ACK is separate Tx
	viaHop.Params.Add("branch", String{Str: GenerateBranch()})

	if len(inviteRequest.GetHeaders("Route")) > 0 {
		CopyHeaders("Route", inviteRequest, ackRequest)
	} else {
		for _, h := range inviteResponse.GetHeaders("Record-Route") {
			uris := make([]Uri, 0)
			for _, u := range h.(*RecordRouteHeader).Addresses {
				uris = append(uris, u.Clone())
			}
			ackRequest.AppendHeader(&RouteHeader{
				Addresses: uris,
			})
		}
	}

	CopyHeaders("From", inviteRequest, ackRequest)
	CopyHeaders("To", inviteResponse, ackRequest)
	CopyHeaders("Call-ID", inviteRequest, ackRequest)
	CopyHeaders("CSeq", inviteRequest, ackRequest)
	cseq, _ := ackRequest.CSeq()
	cseq.MethodName = ACK

	return ackRequest
}

func NewCancelRequest(requestForCancel Request) Request {
	cancelReq := NewRequest(CANCEL, requestForCancel.Recipient(), requestForCancel.SipVersion(), []Header{}, "")

	viaHop, _ := requestForCancel.ViaHop()
	cancelReq.AppendHeader(ViaHeader{viaHop.Clone()})
	CopyHeaders("Route", requestForCancel, cancelReq)
	CopyHeaders("From", requestForCancel, cancelReq)
	CopyHeaders("To", requestForCancel, cancelReq)
	CopyHeaders("Call-ID", requestForCancel, cancelReq)
	CopyHeaders("CSeq", requestForCancel, cancelReq)
	cseq, _ := cancelReq.CSeq()
	cseq.MethodName = CANCEL

	return cancelReq
}
