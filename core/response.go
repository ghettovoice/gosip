package core

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ghettovoice/gosip/log"
)

// Response RFC 3261 - 7.2.
type Response interface {
	Message
	StatusCode() StatusCode
	SetStatusCode(code StatusCode)
	Reason() string
	SetReason(reason string)
	/* Common helpers */
	IsProvisional() bool
	IsSuccess() bool
	IsRedirection() bool
	IsClientError() bool
	IsServerError() bool
	IsGlobalError() bool
}

type response struct {
	message
	status StatusCode
	reason string
}

func NewResponse(
	sipVersion string,
	statusCode StatusCode,
	reason string,
	hdrs []Header,
	body string,
) Response {
	res := new(response)
	res.logger = log.NewSafeLocalLogger()
	res.startLine = res.StartLine
	res.SetSipVersion(sipVersion)
	res.headers = newHeaders(hdrs)
	res.SetStatusCode(statusCode)
	res.SetReason(reason)

	if strings.TrimSpace(body) != "" {
		res.SetBody(body, false)
	}

	return res
}

func (res *response) Short() string {
	return fmt.Sprintf("Response%s", res.message.Short())
}

func (res *response) StatusCode() StatusCode {
	return res.status
}
func (res *response) SetStatusCode(code StatusCode) {
	res.status = code
}

func (res *response) Reason() string {
	return res.reason
}
func (res *response) SetReason(reason string) {
	res.reason = reason
}

// StartLine returns Response Status Line - RFC 2361 7.2.
func (res *response) StartLine() string {
	var buffer bytes.Buffer

	// Every SIP response starts with a Status Line - RFC 2361 7.2.
	buffer.WriteString(
		fmt.Sprintf(
			"%s %d %s",
			res.SipVersion(),
			res.StatusCode(),
			res.Reason(),
		),
	)

	return buffer.String()
}

func (res *response) Clone() Message {
	clone := NewResponse(
		res.SipVersion(),
		res.StatusCode(),
		res.Reason(),
		res.headers.CloneHeaders(),
		res.Body(),
	)
	clone.SetLog(res.Log())
	return clone
}

func (res *response) IsProvisional() bool {
	return res.StatusCode() < 200
}

func (res *response) IsSuccess() bool {
	return res.StatusCode() >= 200 && res.StatusCode() < 300
}

func (res *response) IsRedirection() bool {
	return res.StatusCode() >= 300 && res.StatusCode() < 400
}

func (res *response) IsClientError() bool {
	return res.StatusCode() >= 400 && res.StatusCode() < 500
}

func (res *response) IsServerError() bool {
	return res.StatusCode() >= 500 && res.StatusCode() < 600
}

func (res *response) IsGlobalError() bool {
	return res.StatusCode() >= 600
}

// RFC 3261 - 8.2.6
func NewResponseFromRequest(
	req Request,
	statusCode StatusCode,
	reason string,
	body string,
) Response {
	res := NewResponse(
		req.SipVersion(),
		statusCode,
		reason,
		[]Header{},
		body,
	)
	res.SetLog(req.Log())

	CopyHeaders("Via", req, res)
	CopyHeaders("From", req, res)
	CopyHeaders("To", req, res)
	CopyHeaders("Call-ID", req, res)
	CopyHeaders("CSeq", req, res)

	if statusCode == 100 {
		CopyHeaders("Timestamp", req, res)
	}
	res.SetDestination(req.Source())

	return res
}

func (res *response) Source() string {
	return res.src
}

func (res *response) Destination() string {
	if res.dest != "" {
		return res.dest
	}

	viaHop, ok := res.ViaHop()
	if !ok {
		return ""
	}

	var host string
	var port Port
	if received, ok := viaHop.Params.Get("received"); ok && received.String() != "" {
		host = received.String()
	} else {
		host = viaHop.Host
	}

	if viaHop.Port == nil {
		port = DefaultPort(res.Transport())
	} else {
		port = *viaHop.Port
	}

	return fmt.Sprintf("%v:%v", host, port)
}
