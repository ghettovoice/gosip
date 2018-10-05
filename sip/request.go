package sip

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/util"
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
	IsAck() bool
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
	req.logger = log.NewSafeLocalLogger()
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
	return fmt.Sprintf("Request%s", req.message.Short())
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
	clone := NewRequest(
		req.Method(),
		req.Recipient().Clone(),
		req.SipVersion(),
		req.headers.CloneHeaders(),
		req.Body(),
	)
	clone.SetLog(req.Log())
	return clone
}

func (req *request) IsInvite() bool {
	return req.Method() == INVITE
}

func (req *request) IsAck() bool {
	return req.Method() == ACK
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

	uri, ok := req.Recipient().(*SipUri)
	if !ok {
		return ""
	}

	host := uri.Host
	var port Port
	if uri.Port == nil {
		port = DefaultPort(req.Transport())
	} else {
		port = *uri.Port
	}

	return fmt.Sprintf("%v:%v", host, port)
}

type RequestBuilder struct {
	method        RequestMethod
	seqNo         uint
	recipientHost string
	recipientPort string
	body          string
	host          string
	port          string
	transport     string
	branch        string
	callID        string
	from          map[string]string
	to            map[string]string
	contact       map[string]string
	expires       string
	userAgent     string
	maxForwards   uint
	rport         bool
}

func NewRequestBuilder() *RequestBuilder {
	return &RequestBuilder{
		seqNo:       1,
		host:        "localhost",
		port:        "5060",
		transport:   "UDP",
		userAgent:   "GoSIP",
		callID:      util.RandString(32),
		branch:      GenerateBranch(),
		maxForwards: 70,
		rport:       false,
	}
}

func (rb *RequestBuilder) SetRPort(flag bool) *RequestBuilder {
	rb.rport = flag

	return rb
}

func (rb *RequestBuilder) SetMethod(method RequestMethod) *RequestBuilder {
	rb.method = method

	return rb
}

func (rb *RequestBuilder) SetSeqNo(seqNo uint) *RequestBuilder {
	rb.seqNo = seqNo

	return rb
}

func (rb *RequestBuilder) SetRecipientHost(host string) *RequestBuilder {
	if host != "" {
		rb.recipientHost = host
	}

	return rb
}

func (rb *RequestBuilder) SetRecipientPort(port string) *RequestBuilder {
	if port != "" {
		rb.recipientPort = port
	}

	return rb
}

func (rb *RequestBuilder) SetBody(body string) *RequestBuilder {
	rb.body = body

	return rb
}

func (rb *RequestBuilder) SetHost(host string) *RequestBuilder {
	if host != "" {
		rb.host = host
	}

	return rb
}

func (rb *RequestBuilder) SetPort(port string) *RequestBuilder {
	if port != "" {
		rb.port = port
	}

	return rb
}

func (rb *RequestBuilder) SetTransport(transport string) *RequestBuilder {
	if transport != "" {
		rb.transport = transport
	}

	return rb
}

func (rb *RequestBuilder) SetBranch(branch string) *RequestBuilder {
	if branch != "" {
		rb.branch = branch
	}

	return rb
}

func (rb *RequestBuilder) SetCallID(callID string) *RequestBuilder {
	if callID != "" {
		rb.callID = callID
	}

	return rb
}

func (rb *RequestBuilder) SetFrom(displayName, username, host, port string, params map[string]string) *RequestBuilder {
	rb.from = map[string]string{
		"display": displayName,
		"user":    username,
		"host":    host,
		"port":    port,
	}

	for key, val := range params {
		rb.from["param_"+key] = val
	}

	return rb
}

func (rb *RequestBuilder) SetTo(displayName, username, host, port string, params map[string]string) *RequestBuilder {
	rb.to = map[string]string{
		"display": displayName,
		"user":    username,
		"host":    host,
		"port":    port,
	}

	for key, val := range params {
		rb.to["param_"+key] = val
	}

	return rb
}

func (rb *RequestBuilder) SetContact(displayName, username, host, port string, params map[string]string) *RequestBuilder {
	rb.contact = map[string]string{
		"display": displayName,
		"user":    username,
		"host":    host,
		"port":    port,
	}

	for key, val := range params {
		rb.contact["param_"+key] = val
	}

	return rb
}

func (rb *RequestBuilder) SetExpires(expires string) *RequestBuilder {
	rb.expires = expires

	return rb
}

func (rb *RequestBuilder) SetUserAgent(userAgent string) *RequestBuilder {
	rb.userAgent = userAgent

	return rb
}

func (rb *RequestBuilder) Build() (Request, error) {
	var (
		from   *FromHeader
		to     *ToHeader
		callID CallID
		err    error
	)

	via, err := rb.buildVia()
	if err != nil {
		return nil, err
	}

	recipient, err := rb.buildRecipientUri()
	if err != nil {
		return nil, err
	}

	if rb.from == nil {
		return nil, fmt.Errorf("from header data required")
	} else {
		from, err = rb.buildFrom()
		if err != nil {
			return nil, err
		}
	}

	if rb.to == nil {
		return nil, fmt.Errorf("from header data required")
	} else {
		to, err = rb.buildTo()
		if err != nil {
			return nil, err
		}
	}

	if rb.callID == "" {
		callID = CallID(util.RandString(32))
	} else {
		callID = CallID(rb.callID)
	}

	cseq := &CSeq{
		SeqNo:      uint32(rb.seqNo),
		MethodName: rb.method,
	}

	headers := []Header{via, from, to, &callID, cseq}

	if rb.contact != nil {
		contact, err := rb.buildContact()
		if err != nil {
			return nil, err
		}

		headers = append(headers, contact)
	}

	if rb.userAgent != "" {
		headers = append(headers, &GenericHeader{HeaderName: "User-Agent", Contents: rb.userAgent})
	}

	if rb.expires != "" {
		headers = append(headers, &GenericHeader{
			HeaderName: "Expires",
			Contents:   rb.expires,
		})
	}

	if rb.maxForwards != 0 {
		headers = append(headers, &GenericHeader{
			HeaderName: "Max-Forwards",
			Contents:   strconv.Itoa(int(rb.maxForwards)),
		})
	}

	// basic REGISTER request
	req := NewRequest(rb.method, recipient, "SIP/2.0", headers, rb.body)

	return req, nil
}

func (rb *RequestBuilder) buildRecipientUri() (Uri, error) {
	var (
		port    Port
		portPtr *Port
	)

	if rb.recipientPort != "" {
		p, err := strconv.Atoi(rb.recipientPort)
		if err != nil {
			return nil, fmt.Errorf("invalid Recipient port: %s", err)
		}

		port = Port(p)
		portPtr = &port
	}

	recipient := &SipUri{
		Host: rb.recipientHost,
		Port: portPtr,
	}

	return recipient, nil
}

func (rb *RequestBuilder) buildVia() (ViaHeader, error) {
	var (
		port    Port
		portPtr *Port
		branch  string
	)

	if rb.transport == "" {
		return nil, fmt.Errorf("transport required")
	}

	if rb.port == "" {
		port = DefaultPort(rb.transport)
	} else {
		p, err := strconv.Atoi(rb.port)
		if err != nil {
			return nil, fmt.Errorf("invalid Via port: %s", err)
		}

		port = Port(p)

	}

	portPtr = &port

	if rb.branch == "" {
		branch = GenerateBranch()
	} else {
		branch = rb.branch
	}

	params := NewParams().
		Add("branch", String{branch})

	if rb.rport {
		params.Add("rport", nil)
	}

	via := ViaHeader{
		&ViaHop{
			ProtocolName:    "SIP",
			ProtocolVersion: "2.0",
			Transport:       rb.transport,
			Host:            rb.host,
			Port:            portPtr,
			Params:          params,
		},
	}

	return via, nil
}

func (rb *RequestBuilder) buildFrom() (*FromHeader, error) {
	var (
		port Port
	)

	if rb.from == nil {
		return nil, fmt.Errorf("from data required")
	}

	from := &FromHeader{
		Address: &SipUri{},
		Params:  NewParams(),
	}

	for key, val := range rb.from {
		switch key {
		case "display":
			if val != "" {
				from.DisplayName = String{val}
			}
		case "user":
			if val == "" {
				return nil, fmt.Errorf("username required")
			}

			from.Address.(*SipUri).User = String{val}
		case "host":
			if val == "" {
				return nil, fmt.Errorf("host required")
			}

			from.Address.(*SipUri).Host = val
		case "port":
			if val == "" {
				continue
			}

			p, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("invalid From port: %s", err)
			}

			port = Port(p)
			from.Address.(*SipUri).Port = &port
		default:
			key := strings.Replace(key, "param_", "", -1)
			from.Params.Add(key, String{val})
		}
	}

	return from, nil
}

func (rb *RequestBuilder) buildTo() (*ToHeader, error) {
	var (
		port Port
	)

	if rb.to == nil {
		return nil, fmt.Errorf("to data required")
	}

	to := &ToHeader{
		Address: &SipUri{},
		Params:  NewParams(),
	}

	for key, val := range rb.to {
		switch key {
		case "display":
			if val != "" {
				to.DisplayName = String{val}
			}
		case "user":
			if val == "" {
				return nil, fmt.Errorf("username required")
			}

			to.Address.(*SipUri).User = String{val}
		case "host":
			if val == "" {
				return nil, fmt.Errorf("host required")
			}

			to.Address.(*SipUri).Host = val
		case "port":
			if val == "" {
				continue
			}

			p, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("invalid To port: %s", err)
			}

			port = Port(p)
			to.Address.(*SipUri).Port = &port
		default:
			key := strings.Replace(key, "param_", "", -1)
			to.Params.Add(key, String{val})
		}
	}

	return to, nil
}

func (rb *RequestBuilder) buildContact() (*ContactHeader, error) {
	var (
		port Port
	)

	if rb.contact == nil {
		return nil, fmt.Errorf("contact data required")
	}

	contact := &ContactHeader{
		Address: &SipUri{},
		Params:  NewParams(),
	}

	for key, val := range rb.contact {
		switch key {
		case "display":
			if val != "" {
				contact.DisplayName = String{val}
			}
		case "user":
			if val == "" {
				return nil, fmt.Errorf("username required")
			}

			contact.Address.(*SipUri).User = String{val}
		case "host":
			if val == "" {
				return nil, fmt.Errorf("host required")
			}

			contact.Address.(*SipUri).Host = val
		case "port":
			if val == "" {
				continue
			}

			p, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("invalid Contact port: %s", err)
			}

			port = Port(p)
			contact.Address.(*SipUri).Port = &port
		default:
			key := strings.Replace(key, "param_", "", -1)
			contact.Params.Add(key, String{val})
		}
	}

	return contact, nil
}
