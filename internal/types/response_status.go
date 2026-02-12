package types

import (
	"fmt"

	"github.com/ghettovoice/gosip/internal/util"
)

const (
	ResponseStatusTrying                ResponseStatus = 100
	ResponseStatusRinging               ResponseStatus = 180
	ResponseStatusCallIsBeingForwarded  ResponseStatus = 181
	ResponseStatusQueued                ResponseStatus = 182
	ResponseStatusSessionProgress       ResponseStatus = 183
	ResponseStatusEarlyDialogTerminated ResponseStatus = 199 // RFC 6228

	ResponseStatusOK             ResponseStatus = 200
	ResponseStatusAccepted       ResponseStatus = 202 // RFC 6665
	ResponseStatusNoNotification ResponseStatus = 204 // RFC 5839

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
	ResponseStatusGone                         ResponseStatus = 410
	ResponseStatusLengthRequired               ResponseStatus = 411
	ResponseStatusConditionalRequestFailed     ResponseStatus = 412 // RFC 3903
	ResponseStatusRequestEntityTooLarge        ResponseStatus = 413
	ResponseStatusRequestURITooLong            ResponseStatus = 414
	ResponseStatusUnsupportedMediaType         ResponseStatus = 415
	ResponseStatusUnsupportedURIScheme         ResponseStatus = 416
	ResponseStatusUnknownResourcePriority      ResponseStatus = 417 // RFC 4412
	ResponseStatusBadExtension                 ResponseStatus = 420
	ResponseStatusExtensionRequired            ResponseStatus = 421
	ResponseStatusSessionIntervalTooSmall      ResponseStatus = 422 // RFC 4028
	ResponseStatusIntervalTooBrief             ResponseStatus = 423
	ResponseStatusBadLocationInformation       ResponseStatus = 424 // RFC 6442
	ResponseStatusBadAlertMessage              ResponseStatus = 425 // RFC 8876
	ResponseStatusUseIdentityHeader            ResponseStatus = 428 // RFC 8224
	ResponseStatusProvideReferrerIdentity      ResponseStatus = 429 // RFC 3892
	ResponseStatusFlowFailed                   ResponseStatus = 430 // RFC 5626
	ResponseStatusAnonymityDisallowed          ResponseStatus = 433 // RFC 5079
	ResponseStatusBadIdentityInfo              ResponseStatus = 436 // RFC 8224
	ResponseStatusUnsupportedCredential        ResponseStatus = 437 // RFC 8224
	ResponseStatusInvalidIdentityHeader        ResponseStatus = 438 // RFC 8224
	ResponseStatusFirstHopLacksOutboundSupport ResponseStatus = 439 // RFC 5626
	ResponseStatusMaxBreadthExceeded           ResponseStatus = 440 // RFC 5393
	ResponseStatusBadInfoPackage               ResponseStatus = 469 // RFC 6086
	ResponseStatusConsentNeeded                ResponseStatus = 470 // RFC 5360
	ResponseStatusTemporarilyUnavailable       ResponseStatus = 480
	ResponseStatusCallTransactionDoesNotExist  ResponseStatus = 481
	ResponseStatusLoopDetected                 ResponseStatus = 482
	ResponseStatusTooManyHops                  ResponseStatus = 483
	ResponseStatusAddressIncomplete            ResponseStatus = 484
	ResponseStatusAmbiguous                    ResponseStatus = 485
	ResponseStatusBusyHere                     ResponseStatus = 486
	ResponseStatusRequestTerminated            ResponseStatus = 487
	ResponseStatusNotAcceptableHere            ResponseStatus = 488
	ResponseStatusBadEvent                     ResponseStatus = 489 // RFC 6665
	ResponseStatusRequestPending               ResponseStatus = 491
	ResponseStatusUndecipherable               ResponseStatus = 493
	ResponseStatusSecurityAgreementRequired    ResponseStatus = 494 // RFC 3329

	ResponseStatusServerInternalError                 ResponseStatus = 500
	ResponseStatusNotImplemented                      ResponseStatus = 501
	ResponseStatusBadGateway                          ResponseStatus = 502
	ResponseStatusServiceUnavailable                  ResponseStatus = 503
	ResponseStatusGatewayTimeout                      ResponseStatus = 504
	ResponseStatusVersionNotSupported                 ResponseStatus = 505
	ResponseStatusMessageTooLarge                     ResponseStatus = 513
	ResponseStatusPushNotificationServiceNotSupported ResponseStatus = 555 // RFC 8599
	ResponseStatusPreconditionFailure                 ResponseStatus = 580 // RFC 3312

	ResponseStatusBusyEverywhere       ResponseStatus = 600
	ResponseStatusDecline              ResponseStatus = 603
	ResponseStatusDoesNotExistAnywhere ResponseStatus = 604
	ResponseStatusNotAcceptable606     ResponseStatus = 606
	ResponseStatusUnwanted             ResponseStatus = 607 // RFC 8197
	ResponseStatusRejected             ResponseStatus = 608 // RFC 8688
)

type ResponseStatus uint

func (s ResponseStatus) IsValid() bool { return s >= 100 && s < 700 }

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

func (s ResponseStatus) IsProvisional() bool { return s/100 == 1 }

func (s ResponseStatus) IsSuccessful() bool { return s/100 == 2 }

func (s ResponseStatus) IsRedirection() bool { return s/100 == 3 }

func (s ResponseStatus) IsRequestFailure() bool { return s/100 == 4 }

func (s ResponseStatus) IsServerFailure() bool { return s/100 == 5 }

func (s ResponseStatus) IsGlobalFailure() bool { return s/100 == 6 }

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
	ResponseStatusTrying:                "Trying",
	ResponseStatusRinging:               "Ringing",
	ResponseStatusCallIsBeingForwarded:  "Call Is Being Forwarded",
	ResponseStatusQueued:                "Queued",
	ResponseStatusSessionProgress:       "Session Progress",
	ResponseStatusEarlyDialogTerminated: "Early Dialog Terminated",

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
	ResponseStatusBadLocationInformation:       "Bad Location Information",
	ResponseStatusBadAlertMessage:              "Bad Alert Message",
	ResponseStatusUseIdentityHeader:            "Use Identity Header",
	ResponseStatusProvideReferrerIdentity:      "Provide Referrer Identity",
	ResponseStatusFlowFailed:                   "Flow Failed",
	ResponseStatusAnonymityDisallowed:          "Anonymity Disallowed",
	ResponseStatusBadIdentityInfo:              "Bad Identity Info",
	ResponseStatusUnsupportedCredential:        "Unsupported Credential",
	ResponseStatusInvalidIdentityHeader:        "Invalid Identity Header",
	ResponseStatusFirstHopLacksOutboundSupport: "First Hop Lacks Outbound Support",
	ResponseStatusMaxBreadthExceeded:           "Max-Breadth Exceeded",
	ResponseStatusBadInfoPackage:               "Bad Info Package",
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

	ResponseStatusServerInternalError:                 "Server Internal Error",
	ResponseStatusNotImplemented:                      "Not Implemented",
	ResponseStatusBadGateway:                          "Bad Gateway",
	ResponseStatusServiceUnavailable:                  "Service Unavailable",
	ResponseStatusGatewayTimeout:                      "Gateway Time-out",
	ResponseStatusVersionNotSupported:                 "Version Not Supported",
	ResponseStatusMessageTooLarge:                     "Message Too Large",
	ResponseStatusPushNotificationServiceNotSupported: "Push Notification Service Not Supported",
	ResponseStatusPreconditionFailure:                 "Precondition Failure",
	ResponseStatusBusyEverywhere:                      "Busy Everywhere",
	ResponseStatusDecline:                             "Decline",
	ResponseStatusDoesNotExistAnywhere:                "Does Not Exist Anywhere",
	ResponseStatusNotAcceptable606:                    "Not Acceptable",
	ResponseStatusUnwanted:                            "Unwanted",
	ResponseStatusRejected:                            "Rejected",
}
