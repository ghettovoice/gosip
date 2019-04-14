package sip

import (
	"fmt"
	"strings"

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
	supported       *SupportedHeader
	require         *RequireHeader
	allow           *GenericHeader
	contentType     *GenericHeader
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
	if transport == "" {
		rb.transport = "UDP"
	} else {
		rb.transport = transport
	}

	return rb
}

func (rb *RequestBuilder) SetHost(host string) *RequestBuilder {
	if host == "" {
		rb.host = "localhost"
	} else {
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
	if callID == "" {
		rb.callID = CallID(util.RandString(32))
	} else {
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

func (rb *RequestBuilder) SetExpires(expires int) *RequestBuilder {
	if expires < 0 {
		rb.expires = nil
	} else {
		rb.expires = &GenericHeader{
			HeaderName: "Expires",
			Contents:   fmt.Sprintf("%d", expires),
		}
	}

	return rb
}

func (rb *RequestBuilder) SetUserAgent(userAgent string) *RequestBuilder {
	if userAgent != "" {
		rb.userAgent.Contents = userAgent
	}

	return rb
}

func (rb *RequestBuilder) SetMaxForwards(maxForwards int) *RequestBuilder {
	if maxForwards < 0 {
		rb.maxForwards = nil
	} else {
		rb.maxForwards = &GenericHeader{
			HeaderName: "Max-Forwards",
			Contents:   fmt.Sprintf("%d", maxForwards),
		}
	}

	return rb
}

func (rb *RequestBuilder) SetAllow(methods []RequestMethod) *RequestBuilder {
	if len(methods) == 0 {
		rb.allow = nil
	} else {
		parts := make([]string, 0)
		for _, method := range methods {
			parts = append(parts, string(method))
		}

		rb.allow = &GenericHeader{
			HeaderName: "Allow",
			Contents:   strings.Join(parts, ", "),
		}
	}

	return rb
}

func (rb *RequestBuilder) SetSupported(options []string) *RequestBuilder {
	if len(options) == 0 {
		rb.supported = nil
	} else {
		rb.supported = &SupportedHeader{
			Options: options,
		}
	}

	return rb
}

func (rb *RequestBuilder) SetRequire(options []string) *RequestBuilder {
	if len(options) == 0 {
		rb.require = nil
	} else {
		rb.require = &RequireHeader{
			Options: options,
		}
	}

	return rb
}

func (rb *RequestBuilder) SetContentType(value string) *RequestBuilder {
	if value == "" {
		rb.contentType = nil
	} else {
		rb.contentType = &GenericHeader{
			HeaderName: "Content-Type",
			Contents:   value,
		}
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
	if rb.supported != nil {
		hdrs = append(hdrs, rb.supported)
	}
	if rb.allow != nil {
		hdrs = append(hdrs, rb.allow)
	}
	if rb.contentType != nil {
		hdrs = append(hdrs, rb.contentType)
	}

	sipVersion := rb.protocol + "/" + rb.protocolVersion
	// basic request
	req := NewRequest(rb.method, rb.recipient, sipVersion, hdrs, rb.body)

	return req, nil
}
