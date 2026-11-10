package sip

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/internal/util"
)

// ResponseStatus represents a SIP response status.
// See [shared.ResponseStatus].
type ResponseStatus = shared.ResponseStatus

// Response status constants.
// See [shared.ResponseStatus].
const (
	ResponseStatusTrying                = shared.ResponseStatusTrying
	ResponseStatusRinging               = shared.ResponseStatusRinging
	ResponseStatusCallIsBeingForwarded  = shared.ResponseStatusCallIsBeingForwarded
	ResponseStatusQueued                = shared.ResponseStatusQueued
	ResponseStatusSessionProgress       = shared.ResponseStatusSessionProgress
	ResponseStatusEarlyDialogTerminated = shared.ResponseStatusEarlyDialogTerminated

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
	ResponseStatusBadLocationInformation       = shared.ResponseStatusBadLocationInformation
	ResponseStatusBadAlertMessage              = shared.ResponseStatusBadAlertMessage
	ResponseStatusUseIdentityHeader            = shared.ResponseStatusUseIdentityHeader
	ResponseStatusProvideReferrerIdentity      = shared.ResponseStatusProvideReferrerIdentity
	ResponseStatusFlowFailed                   = shared.ResponseStatusFlowFailed
	ResponseStatusAnonymityDisallowed          = shared.ResponseStatusAnonymityDisallowed
	ResponseStatusBadIdentityInfo              = shared.ResponseStatusBadIdentityInfo
	ResponseStatusUnsupportedCredential        = shared.ResponseStatusUnsupportedCredential
	ResponseStatusInvalidIdentityHeader        = shared.ResponseStatusInvalidIdentityHeader
	ResponseStatusFirstHopLacksOutboundSupport = shared.ResponseStatusFirstHopLacksOutboundSupport
	ResponseStatusMaxBreadthExceeded           = shared.ResponseStatusMaxBreadthExceeded
	ResponseStatusBadInfoPackage               = shared.ResponseStatusBadInfoPackage
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

	ResponseStatusServerInternalError                 = shared.ResponseStatusServerInternalError
	ResponseStatusNotImplemented                      = shared.ResponseStatusNotImplemented
	ResponseStatusBadGateway                          = shared.ResponseStatusBadGateway
	ResponseStatusServiceUnavailable                  = shared.ResponseStatusServiceUnavailable
	ResponseStatusGatewayTimeout                      = shared.ResponseStatusGatewayTimeout
	ResponseStatusVersionNotSupported                 = shared.ResponseStatusVersionNotSupported
	ResponseStatusMessageTooLarge                     = shared.ResponseStatusMessageTooLarge
	ResponseStatusPushNotificationServiceNotSupported = shared.ResponseStatusPushNotificationServiceNotSupported
	ResponseStatusPreconditionFailure                 = shared.ResponseStatusPreconditionFailure

	ResponseStatusBusyEverywhere       = shared.ResponseStatusBusyEverywhere
	ResponseStatusDecline              = shared.ResponseStatusDecline
	ResponseStatusDoesNotExistAnywhere = shared.ResponseStatusDoesNotExistAnywhere
	ResponseStatusNotAcceptable606     = shared.ResponseStatusNotAcceptable606
	ResponseStatusUnwanted             = shared.ResponseStatusUnwanted
	ResponseStatusRejected             = shared.ResponseStatusRejected
)

// ResponseReason represents a SIP response reason.
// See [shared.ResponseReason].
type ResponseReason = shared.ResponseReason

// Response represents a SIP response message.
type Response struct {
	Status  ResponseStatus `json:"status"`
	Reason  ResponseReason `json:"reason"`
	Proto   ProtoInfo      `json:"proto"`
	Headers Headers        `json:"headers,omitempty"`
	Body    []byte         `json:"body,omitempty"`
}

var _ Message = (*Response)(nil)

// RenderTo renders the SIP response to the given writer.
func (res *Response) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if res == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Call(res.renderStartLine)
	cw.Fprint("\r\n")
	cw.Call(func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrs(w, res.Headers, opts...))
	})
	cw.Fprint("\r\n")
	cw.Write(res.Body)
	return errors.Wrap2(cw.Result())
}

func (res *Response) renderStartLine(w io.Writer) (num int, err error) {
	rsn := res.Reason
	if rsn == "" {
		rsn = res.Status.Reason()
	}
	return errors.Wrap2(fmt.Fprint(w, res.Proto, " ", uint(res.Status), " ", rsn))
}

// Render renders the SIP response to a string.
func (res *Response) Render(opts ...RenderOptions) string {
	if res == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	_, _ = res.RenderTo(sb, opts...)
	return sb.String()
}

// String returns a short string representation of the response.
func (res *Response) String() string {
	if res == nil {
		return sNilTag
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	// TODO make a better short representation of the response
	_, _ = res.renderStartLine(sb)
	return sb.String()
}

// Format implements [fmt.Formatter] for custom formatting.
func (res *Response) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			_, _ = res.RenderTo(f)
			return
		}
		f.Write([]byte(res.String()))
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(res.Render()))
			return
		}
		f.Write([]byte(strconv.Quote(res.String())))
		return
	default:
		type (
			hideMethods Response
			Response    hideMethods
		)
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*Response)(res))
		return
	}
}

// LogValue implements [slog.LogValuer] for structured logging.
func (res *Response) LogValue() slog.Value {
	if res == nil {
		return slog.Value{}
	}

	attrs := append(
		make([]slog.Attr, 0, 7),
		slog.Any("status", res.Status),
		slog.Any("reason", res.Reason),
	)
	if hop, ok := util.SeqFirst(res.Headers.Vias()); ok {
		attrs = append(attrs, slog.Any("via", hop))
	}
	if from, ok := res.Headers.From(); ok {
		attrs = append(attrs, slog.Any("from", from))
	}
	if to, ok := res.Headers.To(); ok {
		attrs = append(attrs, slog.Any("to", to))
	}
	if callID, ok := res.Headers.CallID(); ok {
		attrs = append(attrs, slog.Any("call_id", callID))
	}
	if cseq, ok := res.Headers.CSeq(); ok {
		attrs = append(attrs, slog.Any("cseq", cseq))
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

var resMandatoryHdrs = map[HeaderName]struct{}{
	"Via":     {},
	"From":    {},
	"To":      {},
	"Call-ID": {},
	"CSeq":    {},
}

// Validate validates the response and returns an error if invalid.
func (res *Response) Validate() error {
	if res == nil {
		return errors.ErrorWrap("nil response")
	}

	errs := make([]error, 0, 11)
	if !res.Status.IsValid() {
		errs = append(errs, errors.Errorf("invalid status %v", res.Status))
	}
	if !res.Reason.IsValid() {
		errs = append(errs, errors.Errorf("invalid reason %q", res.Reason))
	}
	if !res.Proto.IsValid() {
		errs = append(errs, errors.Errorf("invalid protocol %q", res.Proto))
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
		errs = append(
			errs,
			errors.Errorf("invalid header %q: %w", cseq.CanonicName(), NewRequestMethodNotAllowedError()),
		)
	}
	if ct, ok := res.Headers.ContentLength(); ok {
		if ct, bl := int(ct), len(res.Body); ct != bl {
			errs = append(errs, errors.Errorf("content length mismatch: got %d, want %d", ct, bl))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Wrap(NewInvalidMessageError(errors.Join(errs...)))
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
		return errors.Wrap(err)
	}

	res.Status = resData.Status
	res.Reason = resData.Reason
	res.Proto = resData.Proto
	res.Headers = resData.Headers
	res.Body = resData.Body
	return nil
}

type ResponseOptions struct {
	Reason   ResponseReason `json:"reason,omitempty"`
	Headers  Headers        `json:"headers,omitempty"`
	Body     []byte         `json:"body,omitempty"`
	LocalTag string         `json:"local_tag,omitempty"`
}

var (
	reqCopyHdrsMap = map[HeaderName]struct{}{
		"Via":       {},
		"From":      {},
		"To":        {},
		"Call-ID":   {},
		"CSeq":      {},
		"Timestamp": {},
	}
	reqCopyHdrsSlice = slices.Collect(maps.Keys(reqCopyHdrsMap))
)

func (req *Request) NewResponse(sts ResponseStatus, opts ...ResponseOptions) (*Response, error) {
	if req.Method.Equal(RequestMethodAck) {
		return nil, errors.Wrap(NewRequestMethodNotAllowedError())
	}

	o := util.LastSliceElem(opts, ResponseOptions{})

	res := &Response{
		Status:  sts,
		Reason:  o.Reason,
		Proto:   req.Proto,
		Headers: make(Headers, 6).CopyFrom(req.Headers, reqCopyHdrsSlice[0], reqCopyHdrsSlice[1:]...),
		Body:    o.Body,
	}
	EnsureResponseToTag(res, o.LocalTag)
	for n, hs := range o.Headers {
		if _, ok := reqCopyHdrsMap[n]; ok || len(hs) == 0 {
			continue
		}

		res.Headers.Append(hs[0], hs[1:]...)
	}
	return res, nil
}

// EnsureResponseToTag fills the response's To header with tag (or a
// generated tag when tag is empty) when the tag is absent. Responses
// without a To header, with an existing tag, or with status 100 Trying are
// left unchanged.
//
// It is a raw endpoint helper: it is not forwarding-aware. Use
// [ResponseEnvelope.EnsureToTag] on envelopes so forwarded responses keep
// their downstream To identity, including an absent tag.
func EnsureResponseToTag(res *Response, tag string) {
	if res.Status.Equal(ResponseStatusTrying) {
		return
	}

	to, ok := res.Headers.To()
	if !ok || to == nil {
		return
	}
	if t, ok := to.Tag(); ok && t != "" {
		return
	}

	if tag == "" {
		tag = GenerateTag(0)
	}
	if to.Params == nil {
		to.Params = make(Values)
	}
	to.Params.Set("tag", tag)
}

type ResponseEnvelope struct {
	*MessageEnvelope[*Response]
}

func NewResponseEnvelope(res *Response) *ResponseEnvelope {
	return &ResponseEnvelope{NewMessageEnvelope(res)}
}

func (r *ResponseEnvelope) WithMessage(fn func(*Response)) *ResponseEnvelope {
	r.MessageEnvelope.WithMessage(fn)
	return r
}

func (r *ResponseEnvelope) SetTransport(tp TransportMetadata) *ResponseEnvelope {
	r.MessageEnvelope.SetTransport(tp)
	return r
}

func (r *ResponseEnvelope) SetLocalAddr(addr netip.AddrPort) *ResponseEnvelope {
	r.MessageEnvelope.SetLocalAddr(addr)
	return r
}

func (r *ResponseEnvelope) SetRemoteAddr(addr netip.AddrPort) *ResponseEnvelope {
	r.MessageEnvelope.SetRemoteAddr(addr)
	return r
}

func (r *ResponseEnvelope) UpdateFrom(other *ResponseEnvelope) *ResponseEnvelope {
	r.MessageEnvelope.UpdateFrom(other.MessageEnvelope)
	return r
}

// RequestTime returns the request timestamp carried in the envelope metadata
// by [RequestEnvelope.NewResponse]. The boolean result reports whether a
// well-typed value was stored; a stored zero time still reports true so the
// Timestamp-delay policy can decide how to treat it.
func (r *ResponseEnvelope) RequestTime() (time.Time, bool) {
	if r == nil || r.MessageEnvelope == nil {
		return time.Time{}, false
	}

	val, ok := r.Metadata().Get(reqTimeMetaKey)
	if !ok {
		return time.Time{}, false
	}
	ts, ok := val.(time.Time)
	if !ok {
		return time.Time{}, false
	}
	return ts, true
}

func (r *ResponseEnvelope) Status() ResponseStatus {
	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	return r.msg.Status
}

func (r *ResponseEnvelope) Reason() ResponseReason {
	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	return r.msg.Reason
}

func (r *ResponseEnvelope) String() string {
	if r == nil {
		return sNilTag
	}

	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	return r.msg.String()
}

func (r *ResponseEnvelope) Format(f fmt.State, verb rune) {
	if r == nil {
		f.Write(bNilTag)
		return
	}

	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	r.msg.Format(f, verb)
}

func (r *ResponseEnvelope) Clone() Message {
	if r == nil {
		return nil
	}

	cloned := r.MessageEnvelope.Clone()
	if cloned == nil {
		return nil
	}
	return &ResponseEnvelope{
		cloned.(*MessageEnvelope[*Response]), //nolint:forcetypeassert // the concrete type is fixed by construction
	}
}

func cloneResEnvelope(res *ResponseEnvelope) *ResponseEnvelope {
	if res == nil {
		return nil
	}
	return res.Clone().(*ResponseEnvelope) //nolint:forcetypeassert // the concrete type is fixed by construction
}

// resPreservesToTag reports whether the envelope's response carries the
// forwarded-copy To-tag preservation marker.
func resPreservesToTag(res *ResponseEnvelope) bool {
	return res != nil && (res.Forwarded() || res.PreservesToTag())
}

// setResPreserveToTag merges a legacy server checkpoint marker into the
// envelope metadata of the transaction's last response. A true marker
// re-establishes To-tag preservation; a false or absent marker never clears
// metadata the checkpoint already carries, so a redundant legacy field can
// not silently remove forwarding protection.
func setResPreserveToTag(res *ResponseEnvelope, preserve bool) {
	if res != nil && preserve {
		res.SetPreserveToTag(true)
	}
}

func (r *ResponseEnvelope) Equal(val any) bool {
	var other *ResponseEnvelope
	switch v := val.(type) {
	case ResponseEnvelope:
		other = &v
	case *ResponseEnvelope:
		other = v
	default:
		return false
	}

	if r == other {
		return true
	} else if r == nil || other == nil {
		return false
	}

	return r.MessageEnvelope.Equal(other.MessageEnvelope)
}

func (r *ResponseEnvelope) UnmarshalJSON(data []byte) error {
	if r.MessageEnvelope == nil {
		r.MessageEnvelope = new(MessageEnvelope[*Response])
	}
	return errors.Wrap(r.MessageEnvelope.UnmarshalJSON(data))
}
