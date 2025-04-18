package sip

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"

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

type ResponseReason = shared.ResponseReason

type Response struct {
	Status  ResponseStatus
	Reason  ResponseReason
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

	return slog.GroupValue(
		slog.String("type", fmt.Sprintf("%T", res)),
		slog.String("ptr", fmt.Sprintf("%p", res)),
		slog.Any("status", res.Status),
		slog.Any("reason", res.Reason),
		slog.Group("headers",
			slog.Any("Via", utils.ValOrNil(FirstHeaderElem[header.Via](res.Headers, "Via"))),
			slog.Any("From", FirstHeader[*header.From](res.Headers, "From")),
			slog.Any("To", FirstHeader[*header.To](res.Headers, "To")),
			slog.Any("Call-ID", FirstHeader[header.CallID](res.Headers, "Call-ID")),
			slog.Any("CSeq", FirstHeader[*header.CSeq](res.Headers, "CSeq")),
		),
		slog.Group("metadata",
			slog.Any(TransportField, res.Metadata[TransportField]),
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
		res.Reason.IsValid() &&
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

	return res.Status.Equal(other.Status) && res.Reason.Equal(other.Reason) &&
		res.Proto.Equal(other.Proto) &&
		compareHeaders(res.Headers, other.Headers) &&
		slices.Equal(res.Body, other.Body)
}

// NewResponse generates a SIP response from a SIP request as described in RFC 3261 Section 8.2.6.
//
// Optional arguments:
//   - reason as [ResponseReason];
//   - additional headers as [Headers], [][Header] or [Header];
//   - local tag as string;
//   - body as []byte.
func NewResponse(req *Request, status ResponseStatus, opts ...any) *Response {
	var (
		reason    ResponseReason
		otherHdrs Headers
		locTag    string
		body      []byte
	)
	for _, opt := range opts {
		switch v := opt.(type) {
		case ResponseReason:
			reason = v
		case Headers:
			if otherHdrs == nil {
				otherHdrs = make(Headers, len(v))
			}
			maps.Copy(v, otherHdrs)
		case header.Header:
			if otherHdrs == nil {
				otherHdrs = make(Headers)
			}
			otherHdrs.Append(v)
		case []header.Header:
			if otherHdrs == nil {
				otherHdrs = make(Headers)
			}
			for _, h := range v {
				otherHdrs.Append(h)
			}
		case []byte:
			body = v
		case string:
			locTag = v
		}
	}
	if reason == "" {
		reason = status.Reason()
	}
	if locTag == "" {
		locTag = randutils.RandString(16)
	}

	stdHdrNames := []HeaderName{"Via", "From", "To", "Call-ID", "CSeq", "Timestamp"}
	res := &Response{
		Status:   status,
		Reason:   reason,
		Proto:    req.Proto,
		Headers:  make(Headers, 6).CopyFrom(req.Headers, stdHdrNames[0], stdHdrNames[1:]...),
		Metadata: maps.Clone(req.Metadata),
		Body:     body,
	}
	// local tag for all responses except Trying
	if to := FirstHeader[*header.To](res.Headers, "To"); status != ResponseStatusTrying && to != nil {
		if to.Params == nil || !to.Params.Has("tag") {
			if to.Params == nil {
				to.Params = make(header.Values)
			}
			to.Params.Set("tag", locTag)
		}
	}
	// append additional headers
	otherHdrs.Del(stdHdrNames[0], stdHdrNames[1:]...)
	for _, hs := range otherHdrs {
		for _, h := range hs {
			res.Headers.Append(h)
		}
	}
	return res
}

type ResponseHandler interface {
	HandleResponse(ctx context.Context, res *Response) error
}

type ResponseHandlerFunc func(ctx context.Context, res *Response) error

func (f ResponseHandlerFunc) HandleResponse(ctx context.Context, res *Response) error {
	return f(ctx, res)
}
