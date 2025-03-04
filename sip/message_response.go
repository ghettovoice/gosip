package sip

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"

	"github.com/ghettovoice/gosip/internal/iterutils"
	"github.com/ghettovoice/gosip/internal/randutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/internal/shared"
)

type ResponseStatus = shared.ResponseStatus

const (
	ResponseStatusTrying               = shared.ResponseStatusTrying
	ResponseStatusRinging              = shared.ResponseStatusRinging
	ResponseStatusCallIsBeingForwarded = shared.ResponseStatusCallIsBeingForwarded
	ResponseStatusQueued               = shared.ResponseStatusQueued
	ResponseStatusSessionProgress      = shared.ResponseStatusSessionProgress

	ResponseStatusOK             = shared.ResponseStatusOK
	ResponseStatusAccepted       = shared.ResponseStatusAccepted
	ResponseStatusNoNotification = shared.ResponseStatusNoNotification

	ResponseStatusMultipleChoices    = shared.ResponseStatusMultipleChoices
	ResponseStatusMovedPermanently   = shared.ResponseStatusMovedPermanently
	ResponseStatusMovedTemporarily   = shared.ResponseStatusMovedTemporarily
	ResponseStatusUseProxy           = shared.ResponseStatusUseProxy
	ResponseStatusAlternativeService = shared.ResponseStatusAlternativeService

	ResponseStatusBadRequest                   = shared.ResponseStatusBadRequest
	ResponseStatusUnauthorized                 = shared.ResponseStatusUnauthorized
	ResponseStatusPaymentRequired              = shared.ResponseStatusPaymentRequired
	ResponseStatusForbidden                    = shared.ResponseStatusForbidden
	ResponseStatusNotFound                     = shared.ResponseStatusNotFound
	ResponseStatusMethodNotAllowed             = shared.ResponseStatusMethodNotAllowed
	ResponseStatusNotAcceptable                = shared.ResponseStatusNotAcceptable
	ResponseStatusProxyAuthenticationRequired  = shared.ResponseStatusProxyAuthenticationRequired
	ResponseStatusRequestTimeout               = shared.ResponseStatusRequestTimeout
	ResponseStatusConflict                     = shared.ResponseStatusConflict
	ResponseStatusGone                         = shared.ResponseStatusGone
	ResponseStatusLengthRequired               = shared.ResponseStatusLengthRequired
	ResponseStatusConditionalRequestFailed     = shared.ResponseStatusConditionalRequestFailed
	ResponseStatusRequestEntityTooLarge        = shared.ResponseStatusRequestEntityTooLarge
	ResponseStatusRequestURITooLong            = shared.ResponseStatusRequestURITooLong
	ResponseStatusUnsupportedMediaType         = shared.ResponseStatusUnsupportedMediaType
	ResponseStatusUnsupportedURIScheme         = shared.ResponseStatusUnsupportedURIScheme
	ResponseStatusUnknownResourcePriority      = shared.ResponseStatusUnknownResourcePriority
	ResponseStatusBadExtension                 = shared.ResponseStatusBadExtension
	ResponseStatusExtensionRequired            = shared.ResponseStatusExtensionRequired
	ResponseStatusSessionIntervalTooSmall      = shared.ResponseStatusSessionIntervalTooSmall
	ResponseStatusIntervalTooBrief             = shared.ResponseStatusIntervalTooBrief
	ResponseStatusUseIdentityHeader            = shared.ResponseStatusUseIdentityHeader
	ResponseStatusProvideReferrerIdentity      = shared.ResponseStatusProvideReferrerIdentity
	ResponseStatusFlowFailed                   = shared.ResponseStatusFlowFailed
	ResponseStatusAnonymityDisallowed          = shared.ResponseStatusAnonymityDisallowed
	ResponseStatusBadIdentityInfo              = shared.ResponseStatusBadIdentityInfo
	ResponseStatusUnsupportedCertificate       = shared.ResponseStatusUnsupportedCertificate
	ResponseStatusInvalidIdentityHeader        = shared.ResponseStatusInvalidIdentityHeader
	ResponseStatusFirstHopLacksOutboundSupport = shared.ResponseStatusFirstHopLacksOutboundSupport
	ResponseStatusMaxBreadthExceeded           = shared.ResponseStatusMaxBreadthExceeded
	ResponseStatusConsentNeeded                = shared.ResponseStatusConsentNeeded
	ResponseStatusTemporarilyUnavailable       = shared.ResponseStatusTemporarilyUnavailable
	ResponseStatusCallTransactionDoesNotExist  = shared.ResponseStatusCallTransactionDoesNotExist
	ResponseStatusLoopDetected                 = shared.ResponseStatusLoopDetected
	ResponseStatusTooManyHops                  = shared.ResponseStatusTooManyHops
	ResponseStatusAddressIncomplete            = shared.ResponseStatusAddressIncomplete
	ResponseStatusAmbiguous                    = shared.ResponseStatusAmbiguous
	ResponseStatusBusyHere                     = shared.ResponseStatusBusyHere
	ResponseStatusRequestTerminated            = shared.ResponseStatusRequestTerminated
	ResponseStatusNotAcceptableHere            = shared.ResponseStatusNotAcceptableHere
	ResponseStatusBadEvent                     = shared.ResponseStatusBadEvent
	ResponseStatusRequestPending               = shared.ResponseStatusRequestPending
	ResponseStatusUndecipherable               = shared.ResponseStatusUndecipherable
	ResponseStatusSecurityAgreementRequired    = shared.ResponseStatusSecurityAgreementRequired

	ResponseStatusServerInternalError = shared.ResponseStatusServerInternalError
	ResponseStatusNotImplemented      = shared.ResponseStatusNotImplemented
	ResponseStatusBadGateway          = shared.ResponseStatusBadGateway
	ResponseStatusServiceUnavailable  = shared.ResponseStatusServiceUnavailable
	ResponseStatusGatewayTimeout      = shared.ResponseStatusGatewayTimeout
	ResponseStatusVersionNotSupported = shared.ResponseStatusVersionNotSupported
	ResponseStatusMessageTooLarge     = shared.ResponseStatusMessageTooLarge
	ResponseStatusPreconditionFailure = shared.ResponseStatusPreconditionFailure

	ResponseStatusBusyEverywhere       = shared.ResponseStatusBusyEverywhere
	ResponseStatusDecline              = shared.ResponseStatusDecline
	ResponseStatusDoesNotExistAnywhere = shared.ResponseStatusDoesNotExistAnywhere
	ResponseStatusNotAcceptable606     = shared.ResponseStatusNotAcceptable606
	ResponseStatusDialogTerminated     = shared.ResponseStatusDialogTerminated
)

func ResponseStatusReason(status ResponseStatus) string { return shared.ResponseStatusReason(status) }

type Response struct {
	Status  ResponseStatus
	Reason  string
	Proto   ProtoInfo
	Headers Headers
	Body    []byte

	Metadata MessageMetadata
}

func (res *Response) RenderTo(w io.Writer) error {
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

func (res *Response) Render() string {
	if res == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = res.RenderTo(sb)
	return sb.String()
}

func (res *Response) String() string {
	if res == nil {
		return "<nil>"
	}
	return res.Render()
}

func (res *Response) LogValue() slog.Value {
	if res == nil {
		return slog.Value{}
	}
	_, viaHop := iterutils.IterFirst2(res.Headers.ViaHops())
	return slog.GroupValue(
		slog.String("type", fmt.Sprintf("%T", res)),
		slog.String("ptr", fmt.Sprintf("%p", res)),
		slog.Any("status", res.Status),
		slog.String("reason", res.Reason),
		slog.Group("headers",
			slog.Any("Via", utils.ValOrNil(viaHop)),
			slog.Any("From", res.Headers.From()),
			slog.Any("To", res.Headers.To()),
			slog.Any("Call-ID", res.Headers.CallID()),
			slog.Any("CSeq", res.Headers.CSeq()),
		),
		slog.Group("metadata",
			slog.Any(LocalAddrField, res.Metadata[LocalAddrField]),
			slog.Any(RemoteAddrField, res.Metadata[RemoteAddrField]),
			slog.Any(RequestTstampField, res.Metadata[RequestTstampField]),
			slog.Any(ResponseTstampField, res.Metadata[ResponseTstampField]),
		),
	)
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
		res.Status.IsValid() &&
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

	return res.Status.Equal(other.Status) &&
		stringutils.LCase(res.Reason) == stringutils.LCase(other.Reason) &&
		res.Proto.Equal(other.Proto) &&
		compareHeaders(res.Headers, other.Headers) &&
		slices.Equal(res.Body, other.Body)
}

// NewResponse generates a SIP response from a SIP request as described in RFC 3261 Section 8.2.6.
func NewResponse(req *Request, status ResponseStatus, reason string) *Response {
	if reason == "" {
		reason = ResponseStatusReason(status)
	}
	res := &Response{
		Status:   status,
		Reason:   reason,
		Proto:    req.Proto,
		Headers:  make(Headers, 6).CopyFrom(req.Headers, "Via", "From", "To", "Call-ID", "CSeq", "Timestamp"),
		Metadata: maps.Clone(req.Metadata),
	}
	if status != ResponseStatusTrying && res.Headers.To() != nil {
		if res.Headers.To().Params == nil || !res.Headers.To().Params.Has("tag") {
			if res.Headers.To().Params == nil {
				res.Headers.To().Params = make(header.Values)
			}
			res.Headers.To().Params.Set("tag", randutils.RandString(16))
		}
	}
	return res
}

// ResponseWriter is used to generate a SIP response on inbound request and send it to the remote peer
// using the procedure defined in RFC 3261 Section 18.2.2.
//
// Example of responding on inbound INVITE request:
//
//	w.Headers().Set(header.Contact{{URI: &uri.SIP{User: uri.User("bob"), Addr: uri.HostPort("192.0.2.4", 5060)}}})
//	w.SetTag("1234")
//	w.Write(ctx, sip.ResponseStatusRinging)
//	w.Write(ctx, sip.ResponseStatusOk, "OK", []byte("v=0\r\n...")/*, header.MIMEType{Type: "application", Subtype: "sdp"} */)
type ResponseWriter interface {
	// Headers returns a map for configuring additional response headers.
	Headers() Headers
	// SetTag sets a local tag to the To header for all responses generated with Write.
	SetTag(tag string)
	// Write generates a SIP response and sends to the remote peer.
	// Implementations should support at least following optional arguments:
	//  - reason as string
	//  - body as []byte
	//  - MIME type as [header.MIMEType]
	Write(ctx context.Context, status ResponseStatus, opts ...any) error
}
