package sip

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
)

const (
	ResponseStatusTrying               uint = 100
	ResponseStatusRinging              uint = 180
	ResponseStatusCallIsBeingForwarded uint = 181
	ResponseStatusQueued               uint = 182
	ResponseStatusSessionProgress      uint = 183

	ResponseStatusOK             uint = 200
	ResponseStatusAccepted       uint = 202 // [RFC3265]
	ResponseStatusNoNotification uint = 204 // [RFC5839]

	ResponseStatusMultipleChoices    uint = 300
	ResponseStatusMovedPermanently   uint = 301
	ResponseStatusMovedTemporarily   uint = 302
	ResponseStatusUseProxy           uint = 305
	ResponseStatusAlternativeService uint = 380

	ResponseStatusBadRequest                   uint = 400
	ResponseStatusUnauthorized                 uint = 401
	ResponseStatusPaymentRequired              uint = 402
	ResponseStatusForbidden                    uint = 403
	ResponseStatusNotFound                     uint = 404
	ResponseStatusMethodNotAllowed             uint = 405
	ResponseStatusNotAcceptable                uint = 406
	ResponseStatusProxyAuthenticationRequired  uint = 407
	ResponseStatusRequestTimeout               uint = 408
	ResponseStatusConflict                     uint = 409
	ResponseStatusGone                         uint = 410
	ResponseStatusLengthRequired               uint = 411
	ResponseStatusConditionalRequestFailed     uint = 412 // [RFC3903]
	ResponseStatusRequestEntityTooLarge        uint = 413
	ResponseStatusRequestURITooLong            uint = 414
	ResponseStatusUnsupportedMediaType         uint = 415
	ResponseStatusUnsupportedURIScheme         uint = 416
	ResponseStatusUnknownResourcePriority      uint = 417
	ResponseStatusBadExtension                 uint = 420
	ResponseStatusExtensionRequired            uint = 421
	ResponseStatusSessionIntervalTooSmall      uint = 422 // [RFC4028]
	ResponseStatusIntervalTooBrief             uint = 423
	ResponseStatusUseIdentityHeader            uint = 428 // [RFC4474]
	ResponseStatusProvideReferrerIdentity      uint = 429 // [RFC3892]
	ResponseStatusFlowFailed                   uint = 430 // [RFC5626]
	ResponseStatusAnonymityDisallowed          uint = 433 // [RFC5079]
	ResponseStatusBadIdentityInfo              uint = 436 // [RFC4474]
	ResponseStatusUnsupportedCertificate       uint = 437 // [RFC4474]
	ResponseStatusInvalidIdentityHeader        uint = 438 // [RFC4474]
	ResponseStatusFirstHopLacksOutboundSupport uint = 439 // [RFC5626]
	ResponseStatusMaxBreadthExceeded           uint = 440 // [RFC5393]
	ResponseStatusConsentNeeded                uint = 470 // [RFC5360]
	ResponseStatusTemporarilyUnavailable       uint = 480
	ResponseStatusCallTransactionDoesNotExist  uint = 481
	ResponseStatusLoopDetected                 uint = 482
	ResponseStatusTooManyHops                  uint = 483
	ResponseStatusAddressIncomplete            uint = 484
	ResponseStatusAmbiguous                    uint = 485
	ResponseStatusBusyHere                     uint = 486
	ResponseStatusRequestTerminated            uint = 487
	ResponseStatusNotAcceptableHere            uint = 488
	ResponseStatusBadEvent                     uint = 489 // [RFC3265]
	ResponseStatusRequestPending               uint = 491
	ResponseStatusUndecipherable               uint = 493
	ResponseStatusSecurityAgreementRequired    uint = 494 // [RFC3329]

	ResponseStatusServerInternalError uint = 500
	ResponseStatusNotImplemented      uint = 501
	ResponseStatusBadGateway          uint = 502
	ResponseStatusServiceUnavailable  uint = 503
	ResponseStatusGatewayTimeout      uint = 504
	ResponseStatusVersionNotSupported uint = 505
	ResponseStatusMessageTooLarge     uint = 513
	ResponseStatusPreconditionFailure uint = 580 // [RFC3312]

	ResponseStatusBusyEverywhere       uint = 600
	ResponseStatusDecline              uint = 603
	ResponseStatusDoesNotExistAnywhere uint = 604
	ResponseStatusNotAcceptable606     uint = 606
	ResponseStatusDialogTerminated     uint = 687
)

var responseReasons = map[uint]string{
	ResponseStatusTrying:               "Trying",
	ResponseStatusRinging:              "Ringing",
	ResponseStatusCallIsBeingForwarded: "Call Is Being Forwarded",
	ResponseStatusQueued:               "Queued",
	ResponseStatusSessionProgress:      "Session Progress",

	ResponseStatusOK:             "OK",
	ResponseStatusAccepted:       "Accepted",
	ResponseStatusNoNotification: "No Notification",

	ResponseStatusMultipleChoices:    "Multiple Choices",
	ResponseStatusMovedPermanently:   "Moved Permanently",
	ResponseStatusMovedTemporarily:   "Moved Temporarily",
	ResponseStatusUseProxy:           "Use Proxy",
	ResponseStatusAlternativeService: "Alternative Service",

	ResponseStatusBadRequest:                   "Bad Request",
	ResponseStatusUnauthorized:                 "Unauthorized",
	ResponseStatusPaymentRequired:              "Payment Required",
	ResponseStatusForbidden:                    "Forbidden",
	ResponseStatusNotFound:                     "Not Found",
	ResponseStatusMethodNotAllowed:             "Method Not Allowed",
	ResponseStatusNotAcceptable:                "Not Acceptable",
	ResponseStatusProxyAuthenticationRequired:  "Proxy Authentication Required",
	ResponseStatusRequestTimeout:               "Request Timeout",
	ResponseStatusConflict:                     "Conflict",
	ResponseStatusGone:                         "Gone",
	ResponseStatusLengthRequired:               "Length Required",
	ResponseStatusConditionalRequestFailed:     "Conditional Request Failed",
	ResponseStatusRequestEntityTooLarge:        "Request Entity Too Large",
	ResponseStatusRequestURITooLong:            "Request-URI Too Long",
	ResponseStatusUnsupportedMediaType:         "Unsupported Media Type",
	ResponseStatusUnsupportedURIScheme:         "Unsupported URI Scheme",
	ResponseStatusUnknownResourcePriority:      "Unknown Resource-Priority",
	ResponseStatusBadExtension:                 "Bad Extension",
	ResponseStatusExtensionRequired:            "Extension Required",
	ResponseStatusSessionIntervalTooSmall:      "Session Interval Too Small",
	ResponseStatusIntervalTooBrief:             "Interval Too Brief",
	ResponseStatusUseIdentityHeader:            "Use Identity Header",
	ResponseStatusProvideReferrerIdentity:      "Provide Referrer Identity",
	ResponseStatusFlowFailed:                   "Flow Failed",
	ResponseStatusAnonymityDisallowed:          "Anonymity Disallowed",
	ResponseStatusBadIdentityInfo:              "Bad Identity-Info",
	ResponseStatusUnsupportedCertificate:       "Unsupported Certificate",
	ResponseStatusInvalidIdentityHeader:        "Invalid Identity Header",
	ResponseStatusFirstHopLacksOutboundSupport: "First Hop Lacks Outbound Support",
	ResponseStatusMaxBreadthExceeded:           "Max-Breadth Exceeded",
	ResponseStatusConsentNeeded:                "Consent Needed",
	ResponseStatusTemporarilyUnavailable:       "Temporarily Unavailable",
	ResponseStatusCallTransactionDoesNotExist:  "Call/Transaction Does Not Exist",
	ResponseStatusLoopDetected:                 "Loop Detected",
	ResponseStatusTooManyHops:                  "Too Many Hops",
	ResponseStatusAddressIncomplete:            "Address StatusIncomplete",
	ResponseStatusAmbiguous:                    "Ambiguous",
	ResponseStatusBusyHere:                     "Busy Here",
	ResponseStatusRequestTerminated:            "Request Terminated",
	ResponseStatusNotAcceptableHere:            "Not Acceptable Here",
	ResponseStatusBadEvent:                     "Bad Event",
	ResponseStatusRequestPending:               "Request Pending",
	ResponseStatusUndecipherable:               "Undecipherable",
	ResponseStatusSecurityAgreementRequired:    "Security Agreement Required",

	ResponseStatusServerInternalError:  "Server Internal Error",
	ResponseStatusNotImplemented:       "Not Implemented",
	ResponseStatusBadGateway:           "Bad Gateway",
	ResponseStatusServiceUnavailable:   "Service Unavailable",
	ResponseStatusGatewayTimeout:       "Gateway Time-out",
	ResponseStatusVersionNotSupported:  "Version Not Supported",
	ResponseStatusMessageTooLarge:      "Message Too Large",
	ResponseStatusPreconditionFailure:  "Precondition Failure",
	ResponseStatusBusyEverywhere:       "Busy Everywhere",
	ResponseStatusDecline:              "Decline",
	ResponseStatusDoesNotExistAnywhere: "Does Not Exist Anywhere",
	ResponseStatusNotAcceptable606:     "Not Acceptable",
	ResponseStatusDialogTerminated:     "Dialog Terminated",
}

func ResponseStatusReason(status uint) string {
	return responseReasons[status]
}

type Response struct {
	Status  uint
	Reason  string
	Proto   Proto
	Headers Headers
	Body    []byte

	Metadata Metadata
}

func (res *Response) MessageHeaders() Headers { return res.Headers }

func (res *Response) SetMessageHeaders(h Headers) Message {
	res.Headers = h
	return res
}

func (res *Response) MessageBody() []byte { return res.Body }

func (res *Response) SetMessageBody(b []byte) Message {
	res.Body = b
	return res
}

func (res *Response) MessageMetadata() Metadata { return res.Metadata }

func (res *Response) SetMessageMetadata(data Metadata) Message {
	res.Metadata = data
	return res
}

func (res *Response) RenderMessageTo(w io.Writer) error {
	if res == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, res.Proto, " ", res.Status, " ", res.Reason, "\r\n"); err != nil {
		return err
	}
	if err := renderHeaders(w, res.Headers); err != nil {
		return err
	}
	if _, err := fmt.Fprint(w, "\r\n"); err != nil {
		return err
	}
	if _, err := w.Write(res.Body); err != nil {
		return err
	}
	return nil
}

func (res *Response) RenderMessage() string {
	if res == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	res.RenderMessageTo(sb)
	return sb.String()
}

func (res *Response) Clone() Message {
	if res == nil {
		return nil
	}
	res2 := *res
	res2.Headers = res.Headers.Clone()
	res2.Body = slices.Clone(res.Body)
	res2.Metadata = maps.Clone(res.Metadata)
	return &res2
}

func (res *Response) IsValid() bool {
	return res != nil &&
		res.Status >= 100 &&
		res.Proto.IsValid() &&
		validateHeaders(res.Headers) &&
		res.Headers.Has("Via") &&
		res.Headers.Has("From") &&
		res.Headers.Has("To") &&
		res.Headers.Has("Call-ID") &&
		res.Headers.Has("CSeq")
}

func (res *Response) Equal(val any) bool {
	var other *Response
	switch v := val.(type) {
	case Response:
		other = &v
	case *Response:
		other = v
	default:
		return false
	}

	if res == other {
		return true
	} else if res == nil || other == nil {
		return false
	}

	return res.Status == other.Status &&
		utils.LCase(res.Reason) == utils.LCase(other.Reason) &&
		res.Proto.Equal(other.Proto) &&
		compareHeaders(res.Headers, other.Headers) &&
		slices.Equal(res.Body, other.Body)
}

// BuildResponse generates a SIP response from a SIP request as described in RFC 3261 Section 8.2.6.
func BuildResponse(req *Request, status uint, reason string) (*Response, error) {
	if !req.IsValid() {
		return nil, errors.New("request is invalid")
	}

	if reason == "" {
		reason = ResponseStatusReason(status)
	}
	res := &Response{
		Status:   status,
		Reason:   reason,
		Proto:    req.Proto,
		Headers:  make(Headers, 6).CopyFrom(req.Headers, "Via", "From", "To", "Call-ID", "CSeq"),
		Body:     slices.Clone(req.Body),
		Metadata: maps.Clone(req.Metadata),
	}
	if status == ResponseStatusTrying {
		res.Headers.CopyFrom(req.Headers, "Timestamp")
	} else {
		if res.Headers.To().Params == nil || !res.Headers.To().Params.Has("tag") {
			res.Headers.To().Params.Set("tag", utils.RandString(16))
		}
	}
	return res, nil
}
