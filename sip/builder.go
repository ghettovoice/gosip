package sip

import (
	"fmt"

	"github.com/ghettovoice/gosip/util"
)

type RequestBuilder struct {
	protocol        string
	protocolVersion string
	transport       string
	host            string
	method          RequestMethod
	cseq            *CSeq
	recipient       Uri
	body            string
	callID          CallID
	via             ViaHeader
	from            *FromHeader
	to              *ToHeader
	contact         *ContactHeader
	expires         *GenericHeader
	userAgent       *GenericHeader
	maxForwards     *GenericHeader
}

func NewRequestBuilder() *RequestBuilder {
	rb := &RequestBuilder{
		protocol:        "SIP",
		protocolVersion: "2.0",
		transport:       "UDP",
		host:            "localhost",
		cseq:            &CSeq{SeqNo: 1},
		body:            "",
		via:             make(ViaHeader, 0),
		callID:          CallID(util.RandString(32)),
		userAgent:       &GenericHeader{HeaderName: "User-Agent", Contents: "GoSIP"},
	}

	return rb
}

func (rb *RequestBuilder) SetTransport(transport string) *RequestBuilder {
	if transport != "" {
		rb.transport = transport
	}

	return rb
}

func (rb *RequestBuilder) SetHost(host string) *RequestBuilder {
	if host != "" {
		rb.host = host
	}

	return rb
}

func (rb *RequestBuilder) SetMethod(method RequestMethod) *RequestBuilder {
	rb.method = method
	rb.cseq.MethodName = method

	return rb
}

func (rb *RequestBuilder) SetSeqNo(seqNo uint) *RequestBuilder {
	rb.cseq.SeqNo = uint32(seqNo)

	return rb
}

func (rb *RequestBuilder) SetRecipient(uri Uri) *RequestBuilder {
	rb.recipient = uri.Clone()

	return rb
}

func (rb *RequestBuilder) SetBody(body string) *RequestBuilder {
	rb.body = body

	return rb
}

func (rb *RequestBuilder) SetCallID(callID CallID) *RequestBuilder {
	if callID != "" {
		rb.callID = callID
	}

	return rb
}

func (rb *RequestBuilder) AddVia(via *ViaHop) *RequestBuilder {
	if via.ProtocolName == "" {
		via.ProtocolName = rb.protocol
	}
	if via.ProtocolVersion == "" {
		via.ProtocolVersion = rb.protocolVersion
	}
	if via.Transport == "" {
		via.Transport = rb.transport
	}
	if via.Host == "" {
		via.Host = rb.host
	}
	if via.Params == nil {
		via.Params = NewParams()
	}

	rb.via = append(rb.via, via)

	return rb
}

func (rb *RequestBuilder) SetFrom(address *Address) *RequestBuilder {
	address = address.Clone()
	if address.Uri.Host == "" {
		address.Uri.Host = rb.host
	}

	rb.from = &FromHeader{
		DisplayName: address.DisplayName,
		Address:     address.Uri,
		Params:      address.Params,
	}

	return rb
}

func (rb *RequestBuilder) SetTo(address *Address) *RequestBuilder {
	address = address.Clone()
	if address.Uri.Host == "" {
		address.Uri.Host = rb.host
	}

	rb.to = &ToHeader{
		DisplayName: address.DisplayName,
		Address:     address.Uri,
		Params:      address.Params,
	}

	return rb
}

func (rb *RequestBuilder) SetContact(address *Address) *RequestBuilder {
	address = address.Clone()
	if address.Uri.Host == "" {
		address.Uri.Host = rb.host
	}

	rb.contact = &ContactHeader{
		DisplayName: address.DisplayName,
		Address:     address.Uri,
		Params:      address.Params,
	}

	return rb
}

func (rb *RequestBuilder) SetExpires(expires uint) *RequestBuilder {
	rb.expires = &GenericHeader{
		HeaderName: "Expires",
		Contents:   fmt.Sprintf("%d", expires),
	}

	return rb
}

func (rb *RequestBuilder) SetUserAgent(userAgent string) *RequestBuilder {
	rb.userAgent.Contents = userAgent

	return rb
}

func (rb *RequestBuilder) SetMaxForwards(maxForwards uint) *RequestBuilder {
	rb.maxForwards = &GenericHeader{
		HeaderName: "Max-Forwards",
		Contents:   fmt.Sprintf("%d", maxForwards),
	}

	return rb
}

func (rb *RequestBuilder) Build() (Request, error) {
	if rb.method == "" {
		return nil, fmt.Errorf("undefined method name")
	}
	if rb.recipient == nil {
		return nil, fmt.Errorf("empty recipient")
	}
	if rb.from == nil {
		return nil, fmt.Errorf("empty 'From' header")
	}
	if rb.to == nil {
		return nil, fmt.Errorf("empty 'From' header")
	}

	hdrs := []Header{
		rb.cseq,
		rb.from,
		rb.to,
		&rb.callID,
		rb.userAgent,
	}

	if len(rb.via) != 0 {
		via := make(ViaHeader, 0)
		for _, viaHop := range rb.via {
			via = append(via, viaHop)
		}
		hdrs = append([]Header{via}, hdrs...)
	}
	if rb.contact != nil {
		hdrs = append(hdrs, rb.contact)
	}
	if rb.maxForwards != nil {
		hdrs = append(hdrs, rb.maxForwards)
	}
	if rb.expires != nil {
		hdrs = append(hdrs, rb.expires)
	}

	sipVersion := rb.protocol + "/" + rb.protocolVersion
	// basic request
	req := NewRequest(rb.method, rb.recipient, sipVersion, hdrs, rb.body)

	return req, nil
}
