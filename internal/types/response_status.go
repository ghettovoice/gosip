package types

import (
	"fmt"

	"github.com/ghettovoice/gosip/internal/util"
)

const (
	ResponseStatusTrying               ResponseStatus = 100
	ResponseStatusRinging              ResponseStatus = 180
	ResponseStatusCallIsBeingForwarded ResponseStatus = 181
	ResponseStatusQueued               ResponseStatus = 182
	ResponseStatusSessionProgress      ResponseStatus = 183

	ResponseStatusOK             ResponseStatus = 200
	ResponseStatusAccepted       ResponseStatus = 202 // [RFC3265]
	ResponseStatusNoNotification ResponseStatus = 204 // [RFC5839]

	ResponseStatusMultipleChoices    ResponseStatus = 300
	ResponseStatusMovedPermanently   ResponseStatus = 301
	ResponseStatusMovedTemporarily   ResponseStatus = 302
	ResponseStatusUseProxy           ResponseStatus = 305
	ResponseStatusAlternativeService ResponseStatus = 380

	ResponseStatusBadRequest                   ResponseStatus = 400
	ResponseStatusUnauthorized                 ResponseStatus = 401
	ResponseStatusPaymentRequired              ResponseStatus = 402
	ResponseStatusForbidden                    ResponseStatus = 403
	ResponseStatusNotFound                     ResponseStatus = 404
	ResponseStatusMethodNotAllowed             ResponseStatus = 405
	ResponseStatusNotAcceptable                ResponseStatus = 406
	ResponseStatusProxyAuthenticationRequired  ResponseStatus = 407
	ResponseStatusRequestTimeout               ResponseStatus = 408
	ResponseStatusConflict                     ResponseStatus = 409
	ResponseStatusGone                         ResponseStatus = 410
	ResponseStatusLengthRequired               ResponseStatus = 411
	ResponseStatusConditionalRequestFailed     ResponseStatus = 412 // [RFC3903]
	ResponseStatusRequestEntityTooLarge        ResponseStatus = 413
	ResponseStatusRequestURITooLong            ResponseStatus = 414
	ResponseStatusUnsupportedMediaType         ResponseStatus = 415
	ResponseStatusUnsupportedURIScheme         ResponseStatus = 416
	ResponseStatusUnknownResourcePriority      ResponseStatus = 417
	ResponseStatusBadExtension                 ResponseStatus = 420
	ResponseStatusExtensionRequired            ResponseStatus = 421
	ResponseStatusSessionIntervalTooSmall      ResponseStatus = 422 // [RFC4028]
	ResponseStatusIntervalTooBrief             ResponseStatus = 423
	ResponseStatusUseIdentityHeader            ResponseStatus = 428 // [RFC4474]
	ResponseStatusProvideReferrerIdentity      ResponseStatus = 429 // [RFC3892]
	ResponseStatusFlowFailed                   ResponseStatus = 430 // [RFC5626]
	ResponseStatusAnonymityDisallowed          ResponseStatus = 433 // [RFC5079]
	ResponseStatusBadIdentityInfo              ResponseStatus = 436 // [RFC4474]
	ResponseStatusUnsupportedCertificate       ResponseStatus = 437 // [RFC4474]
	ResponseStatusInvalidIdentityHeader        ResponseStatus = 438 // [RFC4474]
	ResponseStatusFirstHopLacksOutboundSupport ResponseStatus = 439 // [RFC5626]
	ResponseStatusMaxBreadthExceeded           ResponseStatus = 440 // [RFC5393]
	ResponseStatusConsentNeeded                ResponseStatus = 470 // [RFC5360]
	ResponseStatusTemporarilyUnavailable       ResponseStatus = 480
	ResponseStatusCallTransactionDoesNotExist  ResponseStatus = 481
	ResponseStatusLoopDetected                 ResponseStatus = 482
	ResponseStatusTooManyHops                  ResponseStatus = 483
	ResponseStatusAddressIncomplete            ResponseStatus = 484
	ResponseStatusAmbiguous                    ResponseStatus = 485
	ResponseStatusBusyHere                     ResponseStatus = 486
	ResponseStatusRequestTerminated            ResponseStatus = 487
	ResponseStatusNotAcceptableHere            ResponseStatus = 488
	ResponseStatusBadEvent                     ResponseStatus = 489 // [RFC3265]
	ResponseStatusRequestPending               ResponseStatus = 491
	ResponseStatusUndecipherable               ResponseStatus = 493
	ResponseStatusSecurityAgreementRequired    ResponseStatus = 494 // [RFC3329]

	ResponseStatusServerInternalError ResponseStatus = 500
	ResponseStatusNotImplemented      ResponseStatus = 501
	ResponseStatusBadGateway          ResponseStatus = 502
	ResponseStatusServiceUnavailable  ResponseStatus = 503
	ResponseStatusGatewayTimeout      ResponseStatus = 504
	ResponseStatusVersionNotSupported ResponseStatus = 505
	ResponseStatusMessageTooLarge     ResponseStatus = 513
	ResponseStatusPreconditionFailure ResponseStatus = 580 // [RFC3312]

	ResponseStatusBusyEverywhere       ResponseStatus = 600
	ResponseStatusDecline              ResponseStatus = 603
	ResponseStatusDoesNotExistAnywhere ResponseStatus = 604
	ResponseStatusNotAcceptable606     ResponseStatus = 606
	ResponseStatusDialogTerminated     ResponseStatus = 687
)

type ResponseStatus uint

func (s ResponseStatus) IsValid() bool { return s >= 100 }

func (s ResponseStatus) Equal(val any) bool {
	var other ResponseStatus
	switch v := val.(type) {
	case ResponseStatus:
		other = v
	case *ResponseStatus:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return s == other
}

func (s ResponseStatus) IsProvisional() bool { return s >= 100 && s < 200 }

func (s ResponseStatus) IsSuccessful() bool { return s >= 200 && s < 300 }

func (s ResponseStatus) IsRedirection() bool { return s >= 300 && s < 400 }

func (s ResponseStatus) IsRequestFailure() bool { return s >= 400 && s < 500 }

func (s ResponseStatus) IsServerFailure() bool { return s >= 500 && s < 600 }

func (s ResponseStatus) IsGlobalFailure() bool { return s >= 600 && s < 700 }

func (s ResponseStatus) IsFinal() bool { return s >= 200 && s < 700 }

func (s ResponseStatus) Reason() ResponseReason { return responseReasons[s] }

func (s ResponseStatus) String() string { return fmt.Sprintf("%d %s", s, s.Reason()) }

type ResponseReason string

func (ResponseReason) IsValid() bool { return true }

func (r ResponseReason) Equal(val any) bool {
	var other ResponseReason
	switch v := val.(type) {
	case ResponseReason:
		other = v
	case *ResponseReason:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return util.EqFold(r, other)
}

var responseReasons = map[ResponseStatus]ResponseReason{
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
