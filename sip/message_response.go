package sip

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
)

// ResponseStatus represents a SIP response status.
// See [types.ResponseStatus].
type ResponseStatus = types.ResponseStatus

// Response status constants.
// See [types.ResponseStatus].
const (
	ResponseStatusTrying                = types.ResponseStatusTrying
	ResponseStatusRinging               = types.ResponseStatusRinging
	ResponseStatusCallIsBeingForwarded  = types.ResponseStatusCallIsBeingForwarded
	ResponseStatusQueued                = types.ResponseStatusQueued
	ResponseStatusSessionProgress       = types.ResponseStatusSessionProgress
	ResponseStatusEarlyDialogTerminated = types.ResponseStatusEarlyDialogTerminated

	ResponseStatusOK             = types.ResponseStatusOK
	ResponseStatusAccepted       = types.ResponseStatusAccepted
	ResponseStatusNoNotification = types.ResponseStatusNoNotification

	ResponseStatusMultipleChoices    = types.ResponseStatusMultipleChoices
	ResponseStatusMovedPermanently   = types.ResponseStatusMovedPermanently
	ResponseStatusMovedTemporarily   = types.ResponseStatusMovedTemporarily
	ResponseStatusUseProxy           = types.ResponseStatusUseProxy
	ResponseStatusAlternativeService = types.ResponseStatusAlternativeService

	ResponseStatusBadRequest                   = types.ResponseStatusBadRequest
	ResponseStatusUnauthorized                 = types.ResponseStatusUnauthorized
	ResponseStatusPaymentRequired              = types.ResponseStatusPaymentRequired
	ResponseStatusForbidden                    = types.ResponseStatusForbidden
	ResponseStatusNotFound                     = types.ResponseStatusNotFound
	ResponseStatusMethodNotAllowed             = types.ResponseStatusMethodNotAllowed
	ResponseStatusNotAcceptable                = types.ResponseStatusNotAcceptable
	ResponseStatusProxyAuthenticationRequired  = types.ResponseStatusProxyAuthenticationRequired
	ResponseStatusRequestTimeout               = types.ResponseStatusRequestTimeout
	ResponseStatusGone                         = types.ResponseStatusGone
	ResponseStatusLengthRequired               = types.ResponseStatusLengthRequired
	ResponseStatusConditionalRequestFailed     = types.ResponseStatusConditionalRequestFailed
	ResponseStatusRequestEntityTooLarge        = types.ResponseStatusRequestEntityTooLarge
	ResponseStatusRequestURITooLong            = types.ResponseStatusRequestURITooLong
	ResponseStatusUnsupportedMediaType         = types.ResponseStatusUnsupportedMediaType
	ResponseStatusUnsupportedURIScheme         = types.ResponseStatusUnsupportedURIScheme
	ResponseStatusUnknownResourcePriority      = types.ResponseStatusUnknownResourcePriority
	ResponseStatusBadExtension                 = types.ResponseStatusBadExtension
	ResponseStatusExtensionRequired            = types.ResponseStatusExtensionRequired
	ResponseStatusSessionIntervalTooSmall      = types.ResponseStatusSessionIntervalTooSmall
	ResponseStatusIntervalTooBrief             = types.ResponseStatusIntervalTooBrief
	ResponseStatusBadLocationInformation       = types.ResponseStatusBadLocationInformation
	ResponseStatusBadAlertMessage              = types.ResponseStatusBadAlertMessage
	ResponseStatusUseIdentityHeader            = types.ResponseStatusUseIdentityHeader
	ResponseStatusProvideReferrerIdentity      = types.ResponseStatusProvideReferrerIdentity
	ResponseStatusFlowFailed                   = types.ResponseStatusFlowFailed
	ResponseStatusAnonymityDisallowed          = types.ResponseStatusAnonymityDisallowed
	ResponseStatusBadIdentityInfo              = types.ResponseStatusBadIdentityInfo
	ResponseStatusUnsupportedCredential        = types.ResponseStatusUnsupportedCredential
	ResponseStatusInvalidIdentityHeader        = types.ResponseStatusInvalidIdentityHeader
	ResponseStatusFirstHopLacksOutboundSupport = types.ResponseStatusFirstHopLacksOutboundSupport
	ResponseStatusMaxBreadthExceeded           = types.ResponseStatusMaxBreadthExceeded
	ResponseStatusBadInfoPackage               = types.ResponseStatusBadInfoPackage
	ResponseStatusConsentNeeded                = types.ResponseStatusConsentNeeded
	ResponseStatusTemporarilyUnavailable       = types.ResponseStatusTemporarilyUnavailable
	ResponseStatusCallTransactionDoesNotExist  = types.ResponseStatusCallTransactionDoesNotExist
	ResponseStatusLoopDetected                 = types.ResponseStatusLoopDetected
	ResponseStatusTooManyHops                  = types.ResponseStatusTooManyHops
	ResponseStatusAddressIncomplete            = types.ResponseStatusAddressIncomplete
	ResponseStatusAmbiguous                    = types.ResponseStatusAmbiguous
	ResponseStatusBusyHere                     = types.ResponseStatusBusyHere
	ResponseStatusRequestTerminated            = types.ResponseStatusRequestTerminated
	ResponseStatusNotAcceptableHere            = types.ResponseStatusNotAcceptableHere
	ResponseStatusBadEvent                     = types.ResponseStatusBadEvent
	ResponseStatusRequestPending               = types.ResponseStatusRequestPending
	ResponseStatusUndecipherable               = types.ResponseStatusUndecipherable
	ResponseStatusSecurityAgreementRequired    = types.ResponseStatusSecurityAgreementRequired

	ResponseStatusServerInternalError                 = types.ResponseStatusServerInternalError
	ResponseStatusNotImplemented                      = types.ResponseStatusNotImplemented
	ResponseStatusBadGateway                          = types.ResponseStatusBadGateway
	ResponseStatusServiceUnavailable                  = types.ResponseStatusServiceUnavailable
	ResponseStatusGatewayTimeout                      = types.ResponseStatusGatewayTimeout
	ResponseStatusVersionNotSupported                 = types.ResponseStatusVersionNotSupported
	ResponseStatusMessageTooLarge                     = types.ResponseStatusMessageTooLarge
	ResponseStatusPushNotificationServiceNotSupported = types.ResponseStatusPushNotificationServiceNotSupported
	ResponseStatusPreconditionFailure                 = types.ResponseStatusPreconditionFailure

	ResponseStatusBusyEverywhere       = types.ResponseStatusBusyEverywhere
	ResponseStatusDecline              = types.ResponseStatusDecline
	ResponseStatusDoesNotExistAnywhere = types.ResponseStatusDoesNotExistAnywhere
	ResponseStatusNotAcceptable606     = types.ResponseStatusNotAcceptable606
	ResponseStatusUnwanted             = types.ResponseStatusUnwanted
	ResponseStatusRejected             = types.ResponseStatusRejected
)

// ResponseReason represents a SIP response reason.
// See [types.ResponseReason].
type ResponseReason = types.ResponseReason

// Response represents a SIP response message.
type Response struct {
	Status  ResponseStatus `json:"status"`
	Reason  ResponseReason `json:"reason"`
	Proto   ProtoInfo      `json:"proto"`
	Headers Headers        `json:"headers"`
	Body    []byte         `json:"body"`
}

// RenderTo renders the SIP response to the given writer.
func (res *Response) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
	if res == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Call(res.renderStartLine)
	cw.Fprint("\r\n")
	cw.Call(func(w io.Writer) (int, error) {
		return errtrace.Wrap2(renderHdrs(w, res.Headers, opts))
	})
	cw.Fprint("\r\n")
	cw.Write(res.Body)
	return errtrace.Wrap2(cw.Result())
}

func (res *Response) renderStartLine(w io.Writer) (num int, err error) {
	rsn := res.Reason
	if rsn == "" {
		rsn = res.Status.Reason()
	}
	return errtrace.Wrap2(fmt.Fprint(w, res.Proto, " ", uint(res.Status), " ", rsn))
}

// Render renders the SIP response to a string.
func (res *Response) Render(opts *RenderOptions) string {
	if res == nil {
		return ""
	}
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	res.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// String returns a short string representation of the response.
func (res *Response) String() string {
	if res == nil {
		return "<nil>"
	}
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	// TODO make a better short representation of the response
	res.renderStartLine(sb) //nolint:errcheck
	return sb.String()
}

// Format implements [fmt.Formatter] for custom formatting.
func (res *Response) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			res.RenderTo(f, nil) //nolint:errcheck
			return
		}
		f.Write([]byte(res.String()))
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(res.Render(nil)))
			return
		}
		f.Write([]byte(strconv.Quote(res.String())))
		return
	default:
		type hideMethods Response
		type Response hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*Response)(res))
		return
	}
}

// LogValue implements [slog.LogValuer] for structured logging.
func (res *Response) LogValue() slog.Value {
	if res == nil {
		return slog.Value{}
	}

	attrs := make([]slog.Attr, 0, 7)
	attrs = append(attrs, slog.Int("status", int(res.Status)), slog.String("reason", string(res.Reason)))
	if hop, ok := util.IterFirst(res.Headers.Via()); ok {
		attrs = append(attrs, slog.Any("Via", hop))
	}
	if from, ok := res.Headers.From(); ok {
		attrs = append(attrs, slog.Any("From", from))
	}
	if to, ok := res.Headers.To(); ok {
		attrs = append(attrs, slog.Any("To", to))
	}
	if callID, ok := res.Headers.CallID(); ok {
		attrs = append(attrs, slog.Any("Call-ID", callID))
	}
	if cseq, ok := res.Headers.CSeq(); ok {
		attrs = append(attrs, slog.Any("CSeq", cseq))
	}

	return slog.GroupValue(attrs...)
}

// Clone returns a deep copy of the response.
func (res *Response) Clone() Message {
	if res == nil {
		return nil
	}

	res2 := *res
	res2.Headers = res.Headers.Clone()
	res2.Body = slices.Clone(res.Body)
	return &res2
}

// Equal returns whether the response is equal to another value.
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
		res.Reason.Equal(other.Reason) &&
		res.Proto.Equal(other.Proto) &&
		compareHdrs(res.Headers, other.Headers) &&
		slices.Equal(res.Body, other.Body)
}

// IsValid returns whether the response is valid.
func (res *Response) IsValid() bool {
	return res.Validate() == nil
}

var resMandatoryHdrs = map[HeaderName]bool{
	"Via":     true,
	"From":    true,
	"To":      true,
	"Call-ID": true,
	"CSeq":    true,
}

// Validate validates the response and returns an error if invalid.
func (res *Response) Validate() error {
	if res == nil {
		return errtrace.Wrap(NewInvalidMessageError("invalid response"))
	}

	errs := make([]error, 0, 9)

	if !res.Status.IsValid() {
		errs = append(errs, errorutil.Errorf("invalid status %v", res.Status))
	}
	if !res.Reason.IsValid() {
		errs = append(errs, errorutil.Errorf("invalid reason %q", res.Reason))
	}
	if !res.Proto.IsValid() {
		errs = append(errs, errorutil.Errorf("invalid protocol %q", res.Proto))
	}
	if err := validateHdrs(res.Headers); err != nil {
		errs = append(errs, err)
	}
	for n := range resMandatoryHdrs {
		if !res.Headers.Has(n) {
			errs = append(errs, newMissHdrErr(n))
		}
	}
	if cseq, ok := res.Headers.CSeq(); ok && cseq.Method.Equal(RequestMethodAck) {
		errs = append(errs, fmt.Errorf("invalid header %q: %w", cseq.CanonicName(), ErrMethodNotAllowed))
	}
	if ct, ok := res.Headers.ContentLength(); ok {
		if ct, bl := int(ct), len(res.Body); ct != bl {
			errs = append(errs, errorutil.Errorf("content length mismatch: got %d, want %d", ct, bl))
		}
	}

	if len(errs) > 0 {
		return errtrace.Wrap(NewInvalidMessageError(errorutil.Join(errs...)))
	}
	return nil
}

func (res *Response) UnmarshalJSON(data []byte) error {
	var resData struct {
		Status  ResponseStatus `json:"status"`
		Reason  ResponseReason `json:"reason"`
		Proto   ProtoInfo      `json:"proto"`
		Headers Headers        `json:"headers"`
		Body    []byte         `json:"body"`
	}
	if err := json.Unmarshal(data, &resData); err != nil {
		return errtrace.Wrap(err)
	}

	res.Status = resData.Status
	res.Reason = resData.Reason
	res.Proto = resData.Proto
	res.Headers = resData.Headers
	res.Body = resData.Body
	return nil
}

type InboundResponse struct {
	inboundMessage[*Response]
}

func NewInboundResponse(res *Response, laddr, raddr netip.AddrPort) *InboundResponse {
	return &InboundResponse{
		inboundMessage[*Response]{
			msg:     res,
			msgTime: time.Now(),
			locAddr: laddr,
			rmtAddr: raddr,
			data:    new(MessageMetadata),
		},
	}
}

func (r *InboundResponse) Status() ResponseStatus {
	if r == nil {
		return 0
	}
	return r.msg.Status
}

func (r *InboundResponse) Reason() ResponseReason {
	if r == nil {
		return ""
	}
	return r.msg.Reason
}

func (r *InboundResponse) RenderTo(w io.Writer, opts *RenderOptions) (int, error) {
	if r == nil {
		return 0, nil
	}
	return errtrace.Wrap2(r.msg.RenderTo(w, opts))
}

func (r *InboundResponse) Render(opts *RenderOptions) string {
	if r == nil {
		return ""
	}
	return r.msg.Render(opts)
}

func (r *InboundResponse) String() string {
	if r == nil {
		return "<nil>"
	}
	return r.msg.String()
}

func (r *InboundResponse) Format(f fmt.State, verb rune) {
	if r == nil {
		f.Write([]byte("<nil>"))
		return
	}
	r.msg.Format(f, verb)
}

func (r *InboundResponse) Clone() Message {
	if r == nil {
		return nil
	}
	return &InboundResponse{
		inboundMessage[*Response]{
			msg:     r.msg.Clone().(*Response), //nolint:forcetypeassert
			msgTime: time.Now(),
			locAddr: r.locAddr,
			rmtAddr: r.rmtAddr,
			data:    r.data.Clone(),
		},
	}
}

func (r *InboundResponse) Equal(v any) bool {
	if r == nil {
		return v == nil
	}
	if other, ok := v.(*InboundResponse); ok {
		return r.msg.Equal(other.msg)
	}
	return false
}

func (r *InboundResponse) IsValid() bool {
	if r == nil {
		return false
	}
	return r.msg.IsValid()
}

func (r *InboundResponse) Validate() error {
	if r == nil {
		return errtrace.Wrap(NewInvalidArgumentError("invalid response"))
	}
	return errtrace.Wrap(r.msg.Validate())
}

type OutboundResponse struct {
	outboundMessage[*Response]
}

func NewOutboundResponse(res *Response) *OutboundResponse {
	return &OutboundResponse{
		outboundMessage[*Response]{
			message: message[*Response]{
				msg:     res,
				msgTime: time.Now(),
				data:    new(MessageMetadata),
			},
		},
	}
}

func (r *OutboundResponse) Status() ResponseStatus {
	if r == nil {
		return 0
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.msg.Status
}

func (r *OutboundResponse) Reason() ResponseReason {
	if r == nil {
		return ""
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.msg.Reason
}

func (r *OutboundResponse) RenderTo(w io.Writer, opts *RenderOptions) (int, error) {
	if r == nil {
		return 0, nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return errtrace.Wrap2(r.msg.RenderTo(w, opts))
}

func (r *OutboundResponse) Render(opts *RenderOptions) string {
	if r == nil {
		return ""
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.msg.Render(opts)
}

func (r *OutboundResponse) String() string {
	if r == nil {
		return "<nil>"
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.msg.String()
}

func (r *OutboundResponse) Format(f fmt.State, verb rune) {
	if r == nil {
		f.Write([]byte("<nil>"))
		return
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	r.msg.Format(f, verb)
}

func (r *OutboundResponse) Clone() Message {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return &OutboundResponse{
		outboundMessage[*Response]{
			message: message[*Response]{
				msg:     r.msg.Clone().(*Response), //nolint:forcetypeassert
				msgTime: time.Now(),
				locAddr: r.locAddr,
				rmtAddr: r.rmtAddr,
				data:    r.data.Clone(),
			},
		},
	}
}

func (r *OutboundResponse) Equal(v any) bool {
	if r == nil {
		return v == nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	if other, ok := v.(*OutboundResponse); ok {
		return r.msg.Equal(other.msg)
	}
	return false
}

func (r *OutboundResponse) IsValid() bool {
	if r == nil {
		return false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.msg.IsValid()
}

func (r *OutboundResponse) Validate() error {
	if r == nil {
		return errtrace.Wrap(NewInvalidArgumentError("invalid response"))
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return errtrace.Wrap(r.msg.Validate())
}
