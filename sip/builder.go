package sip

import (
	"fmt"

	"github.com/ghettovoice/gosip/util"
)

type RequestBuilder struct {
	method      string
	seqNo       uint
	recipient   *uriValues
	body        string
	callID      string
	defaultVia  *viaValues
	via         []*viaValues
	from        []*uriValues
	to          []*uriValues
	contact     []*uriValues
	expires     uint
	userAgent   string
	maxForwards uint
}

type viaValues struct {
	transport string
	host      string
	port      uint
	params    map[string]interface{}
}

type uriValues struct {
	displayName string
	username    string
	host        string
	port        uint
	params      map[string]interface{}
}

func NewRequestBuilder() *RequestBuilder {
	defViaParams := make(map[string]interface{})
	defViaParams["branch"] = GenerateBranch()

	return &RequestBuilder{
		seqNo: 1,
		defaultVia: &viaValues{
			host:      "localhost",
			port:      5060,
			transport: "UDP",
			params:    defViaParams,
		},
		userAgent:   "GoSIP",
		callID:      util.RandString(32),
		maxForwards: 70,
	}
}

func (rb *RequestBuilder) SetMethod(method string) *RequestBuilder {
	rb.method = method

	return rb
}

func (rb *RequestBuilder) SetSeqNo(seqNo uint) *RequestBuilder {
	rb.seqNo = seqNo

	return rb
}

func (rb *RequestBuilder) SetRecipient(username, host string, port uint, params map[string]interface{}) *RequestBuilder {
	rb.recipient = &uriValues{
		username: username,
		host:     host,
		port:     port,
		params:   params,
	}

	return rb
}

func (rb *RequestBuilder) SetBody(body string) *RequestBuilder {
	rb.body = body

	return rb
}

func (rb *RequestBuilder) SetCallID(callID string) *RequestBuilder {
	if callID != "" {
		rb.callID = callID
	}

	return rb
}

func (rb *RequestBuilder) AddVia(transport, host string, port uint, params map[string]interface{}) *RequestBuilder {
	rb.via = append(rb.via, &viaValues{
		transport: transport,
		host:      host,
		port:      port,
		params:    params,
	})

	return rb
}

func (rb *RequestBuilder) AddFrom(username, displayName, host string, port uint, params map[string]interface{}) *RequestBuilder {
	rb.from = append(rb.from, &uriValues{
		username:    username,
		displayName: displayName,
		host:        host,
		port:        port,
		params:      params,
	})

	return rb
}

func (rb *RequestBuilder) AddTo(username, displayName, host string, port uint, params map[string]interface{}) *RequestBuilder {
	rb.to = append(rb.to, &uriValues{
		username:    username,
		displayName: displayName,
		host:        host,
		port:        port,
		params:      params,
	})

	return rb
}

func (rb *RequestBuilder) AddContact(username, displayName, host string, port uint, params map[string]interface{}) *RequestBuilder {
	rb.contact = append(rb.contact, &uriValues{
		username:    username,
		displayName: displayName,
		host:        host,
		port:        port,
		params:      params,
	})

	return rb
}

func (rb *RequestBuilder) SetExpires(expires uint) *RequestBuilder {
	rb.expires = expires

	return rb
}

func (rb *RequestBuilder) SetUserAgent(userAgent string) *RequestBuilder {
	rb.userAgent = userAgent

	return rb
}

func (rb *RequestBuilder) SetMaxForwards(maxForwards uint) *RequestBuilder {
	rb.maxForwards = maxForwards

	return rb
}

func (rb *RequestBuilder) Build() (Request, error) {
	if rb.method == "" {
		return nil, fmt.Errorf("undefined method name")
	}

	reqMethod := RequestMethod(rb.method)

	recipient, err := rb.buildRecipientUri()
	if err != nil {
		return nil, err
	}

	hdrs := make([]Header, 0)

	if via, err := rb.buildVia(); err == nil && via != nil {
		hdrs = append(hdrs, via)
	} else if err != nil {
		return nil, err
	}

	if callID, err := rb.buildCallID(); err == nil {
		hdrs = append(hdrs, callID)
	} else {
		return nil, err
	}

	if cseq, err := rb.buildCSeq(); err == nil {
		hdrs = append(hdrs, cseq)
	} else {
		return nil, err
	}

	if userAgent, err := rb.buildUserAgent(); err == nil {
		hdrs = append(hdrs, userAgent)
	} else {
		return nil, err
	}

	if maxFwds, err := rb.buildMaxForwards(); err == nil {
		hdrs = append(hdrs, maxFwds)
	}

	if expires, err := rb.buildExpires(); err == nil {
		hdrs = append(hdrs, expires)
	}

	if froms, err := rb.buildFrom(); err == nil {
		for _, from := range froms {
			hdrs = append(hdrs, from)
		}
	} else {
		return nil, err
	}

	if tos, err := rb.buildTo(); err == nil {
		for _, to := range tos {
			hdrs = append(hdrs, to)
		}
	} else {
		return nil, err
	}

	if contacts, err := rb.buildContact(); err == nil {
		for _, cnt := range contacts {
			hdrs = append(hdrs, cnt)
		}
	} else {
		return nil, err
	}

	// basic REGISTER request
	req := NewRequest(reqMethod, recipient, "SIP/2.0", hdrs, rb.body)

	return req, nil
}

func (rb *RequestBuilder) buildCallID() (*CallID, error) {
	var callID CallID

	if rb.callID == "" {
		callID = CallID(util.RandString(32))
	} else {
		callID = CallID(rb.callID)
	}

	return &callID, nil
}

func (rb *RequestBuilder) buildCSeq() (*CSeq, error) {
	if rb.method == "" {
		return nil, fmt.Errorf("unknown request method")
	}

	cseq := &CSeq{
		SeqNo:      uint32(rb.seqNo),
		MethodName: RequestMethod(rb.method),
	}

	return cseq, nil
}

func (rb *RequestBuilder) buildUserAgent() (*GenericHeader, error) {
	userAgent := "GoSIP"
	if rb.userAgent != "" {
		userAgent = rb.userAgent
	}

	ua := &GenericHeader{
		HeaderName: "User-Agent",
		Contents:   userAgent,
	}

	return ua, nil
}

func (rb *RequestBuilder) buildExpires() (*GenericHeader, error) {
	if rb.expires == 0 {
		return nil, fmt.Errorf("empty expires value")
	}

	exp := &GenericHeader{
		HeaderName: "Expires",
		Contents:   fmt.Sprintf("%d", rb.expires),
	}

	return exp, nil
}

func (rb *RequestBuilder) buildMaxForwards() (*MaxForwards, error) {
	mf := MaxForwards(70)

	if rb.maxForwards != 0 {
		mf = MaxForwards(rb.maxForwards)
	}

	return &mf, nil
}

func (rb *RequestBuilder) buildRecipientUri() (Uri, error) {
	if rb.recipient.host == "" {
		return nil, fmt.Errorf("empty recipient host")
	}

	port := Port(5060)
	if rb.recipient.port != 0 {
		port = Port(rb.recipient.port)
	}

	var username MaybeString
	if rb.recipient.username != "" {
		username = String{rb.recipient.username}
	}

	params := makeParamsFromMap(rb.recipient.params)

	recipient := &SipUri{
		User:      username,
		Host:      rb.recipient.host,
		Port:      &port,
		UriParams: params,
	}

	return recipient, nil
}

func (rb *RequestBuilder) buildVia() (ViaHeader, error) {
	if len(rb.via) == 0 {
		return nil, nil
	}

	via := make(ViaHeader, 0)

	for _, viaVals := range rb.via {
		transport := "UDP"
		if viaVals.transport != "" {
			transport = viaVals.transport
		}

		host := "localhost"
		if viaVals.host != "" {
			host = viaVals.host
		}

		port := Port(5060)
		if viaVals.port != 0 {
			port = Port(viaVals.port)
		}

		params := makeParamsFromMap(viaVals.params)
		if !params.Has("branch") {
			params.Add("branch", String{GenerateBranch()})
		}

		via = append(via, &ViaHop{
			ProtocolName:    "SIP",
			ProtocolVersion: "2.0",
			Transport:       transport,
			Host:            host,
			Port:            &port,
			Params:          params,
		})
	}

	return via, nil
}

func (rb *RequestBuilder) buildFrom() ([]*FromHeader, error) {
	froms := make([]*FromHeader, 0)

	if len(rb.from) == 0 {
		return nil, fmt.Errorf("empty from list")
	}

	for _, fromVals := range rb.from {
		if fromVals.username == "" {
			continue
		}

		username := String{fromVals.username}

		var displayName MaybeString
		if fromVals.displayName != "" {
			displayName = String{fromVals.displayName}
		}

		host := "localhost"
		if fromVals.host != "" {
			host = fromVals.host
		}

		port := Port(5060)
		if fromVals.port != 0 {
			port = Port(fromVals.port)
		}

		params := makeParamsFromMap(fromVals.params)

		froms = append(froms, &FromHeader{
			Address: &SipUri{
				User: username,
				Host: host,
				Port: &port,
			},
			DisplayName: displayName,
			Params:      params,
		})
	}

	return froms, nil
}

func (rb *RequestBuilder) buildTo() ([]*ToHeader, error) {
	tos := make([]*ToHeader, 0)

	if len(rb.to) == 0 {
		return nil, fmt.Errorf("empty to list")
	}

	for _, toVals := range rb.to {
		if toVals.username == "" {
			continue
		}

		username := String{toVals.username}

		var displayName MaybeString
		if toVals.displayName != "" {
			displayName = String{toVals.displayName}
		}

		host := "localhost"
		if toVals.host != "" {
			host = toVals.host
		}

		port := Port(5060)
		if toVals.port != 0 {
			port = Port(toVals.port)
		}

		params := makeParamsFromMap(toVals.params)

		tos = append(tos, &ToHeader{
			Address: &SipUri{
				User: username,
				Host: host,
				Port: &port,
			},
			DisplayName: displayName,
			Params:      params,
		})
	}

	return tos, nil
}

func (rb *RequestBuilder) buildContact() ([]*ContactHeader, error) {
	contacts := make([]*ContactHeader, 0)

	if len(rb.contact) == 0 {
		return contacts, nil
	}

	for _, contactVals := range rb.contact {
		if contactVals.username == "" {
			continue
		}

		username := String{contactVals.username}

		var displayName MaybeString
		if contactVals.displayName != "" {
			displayName = String{contactVals.displayName}
		}

		host := "localhost"
		if contactVals.host != "" {
			host = contactVals.host
		}

		port := Port(5060)
		if contactVals.port != 0 {
			port = Port(contactVals.port)
		}

		params := makeParamsFromMap(contactVals.params)

		contacts = append(contacts, &ContactHeader{
			Address: &SipUri{
				User: username,
				Host: host,
				Port: &port,
			},
			DisplayName: displayName,
			Params:      params,
		})
	}

	return contacts, nil
}

func makeParamsFromMap(rawParams map[string]interface{}) Params {
	params := NewParams()

	if rawParams == nil {
		return params
	}

	for param, paramVal := range rawParams {
		switch val := paramVal.(type) {
		case string:
			params.Add(param, String{val})
		case bool:
			if val {
				params.Add(param, nil)
			}
		}
	}

	return params
}
