package sip

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/netutil"
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
	Body    []byte         `json:"body,omitempty"`
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
		return errors.Wrap2(renderHdrs(w, res.Headers, opts))
	})
	cw.Fprint("\r\n")
	cw.Write(res.Body) //nolint:errcheck

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
		return sNilTag
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

		f.Write([]byte(res.String())) //nolint:errcheck

		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(res.Render(nil)))
			return
		}

		f.Write([]byte(strconv.Quote(res.String()))) //nolint:errcheck

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

	attrs := make([]slog.Attr, 0, 7)

	attrs = append(attrs, slog.Int("status", int(res.Status)), slog.String("reason", string(res.Reason)))
	if hop, ok := util.SeqFirst(res.Headers.Via()); ok {
		attrs = append(attrs, slog.Any("via", hop))
	}

	if from, ok := res.Headers.From(); ok {
		attrs = append(attrs, slog.Any("from", from))
	}

	if to, ok := res.Headers.To(); ok {
		attrs = append(attrs, slog.Any("to", to))
	}

	if callID, ok := res.Headers.CallID(); ok {
		attrs = append(attrs, slog.Any("call-id", callID))
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
		return errors.NewInvalidArgumentErrorWrap("nil response")
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
		errs = append(errs, errors.Errorf("invalid header %q: %w", cseq.CanonicName(), ErrMethodNotAllowed))
	}

	if ct, ok := res.Headers.ContentLength(); ok {
		if ct, bl := int(ct), len(res.Body); ct != bl {
			errs = append(errs, errors.Errorf("content length mismatch: got %d, want %d", ct, bl))
		}
	}

	if len(errs) > 0 {
		return errors.Wrap(newInvalidMsgErr(errors.Join(errs...)))
	}

	return nil
}

func (res *Response) UnmarshalJSON(data []byte) error {
	if res == nil {
		return errors.NewInvalidArgumentErrorWrap("nil response")
	}

	var resData struct {
		Status  ResponseStatus `json:"status"`
		Reason  ResponseReason `json:"reason"`
		Proto   ProtoInfo      `json:"proto"`
		Headers Headers        `json:"headers"`
		Body    []byte         `json:"body,omitempty"`
	}
	if err := json.Unmarshal(data, &resData); err != nil {
		*res = Response{}
		return errors.Wrap(err)
	}

	res.Status = resData.Status
	res.Reason = resData.Reason
	res.Proto = resData.Proto
	res.Headers = resData.Headers
	res.Body = resData.Body

	return nil
}

type InboundResponseEnvelope struct {
	*inboundMessageEnvelope[*Response]
}

func NewInboundResponseEnvelope(
	res *Response,
	tp TransportProto,
	laddr, raddr netip.AddrPort,
) (*InboundResponseEnvelope, error) {
	if res == nil {
		return nil, errors.NewInvalidArgumentErrorWrap("nil response")
	}

	if !tp.IsValid() {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid transport protocol %q", tp)
	}

	if !laddr.IsValid() {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid local address %q", laddr)
	}

	if !raddr.IsValid() {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid remote address %q", raddr)
	}

	r := &inboundMessageEnvelope[*Response]{
		msg:     res,
		msgTime: time.Now(),
		meta:    new(MessageMetadata),
	}
	r.tp.Store(tp)
	r.laddr.Store(netutil.UnmapAddrPort(laddr))
	r.raddr.Store(netutil.UnmapAddrPort(raddr))

	return &InboundResponseEnvelope{r}, nil
}

func (r *InboundResponseEnvelope) Message() *Response {
	if r == nil {
		return nil
	}
	return r.inboundMessageEnvelope.Message()
}

func (r *InboundResponseEnvelope) Headers() Headers {
	if r == nil {
		return nil
	}
	return r.inboundMessageEnvelope.Headers()
}

func (r *InboundResponseEnvelope) Body() []byte {
	if r == nil {
		return nil
	}
	return r.inboundMessageEnvelope.Body()
}

func (r *InboundResponseEnvelope) MessageTime() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.inboundMessageEnvelope.MessageTime()
}

func (r *InboundResponseEnvelope) Transport() TransportProto {
	if r == nil {
		return ""
	}
	return r.inboundMessageEnvelope.Transport()
}

func (r *InboundResponseEnvelope) LocalAddr() netip.AddrPort {
	if r == nil {
		return netip.AddrPort{}
	}
	return r.inboundMessageEnvelope.LocalAddr()
}

func (r *InboundResponseEnvelope) RemoteAddr() netip.AddrPort {
	if r == nil {
		return netip.AddrPort{}
	}
	return r.inboundMessageEnvelope.RemoteAddr()
}

func (r *InboundResponseEnvelope) Metadata() *MessageMetadata {
	if r == nil {
		return nil
	}
	return r.inboundMessageEnvelope.Metadata()
}

func (r *InboundResponseEnvelope) Status() ResponseStatus {
	if r == nil {
		return 0
	}
	return r.msg.Status
}

func (r *InboundResponseEnvelope) Reason() ResponseReason {
	if r == nil {
		return ""
	}
	return r.msg.Reason
}

func (r *InboundResponseEnvelope) RenderTo(w io.Writer, opts *RenderOptions) (int, error) {
	if r == nil {
		return 0, nil
	}
	return errors.Wrap2(r.inboundMessageEnvelope.RenderTo(w, opts))
}

func (r *InboundResponseEnvelope) Render(opts *RenderOptions) string {
	if r == nil {
		return ""
	}
	return r.inboundMessageEnvelope.Render(opts)
}

func (r *InboundResponseEnvelope) String() string {
	if r == nil {
		return sNilTag
	}
	return r.msg.String()
}

func (r *InboundResponseEnvelope) Format(f fmt.State, verb rune) {
	if r == nil {
		f.Write(bNilTag) //nolint:errcheck
		return
	}

	r.msg.Format(f, verb)
}

func (r *InboundResponseEnvelope) Clone() Message {
	if r == nil {
		return nil
	}

	return &InboundResponseEnvelope{
		r.inboundMessageEnvelope.Clone().(*inboundMessageEnvelope[*Response]), //nolint:forcetypeassert
	}
}

func (r *InboundResponseEnvelope) Equal(v any) bool {
	if r == nil {
		return v == nil
	}

	if other, ok := v.(*InboundResponseEnvelope); ok {
		return r.inboundMessageEnvelope.Equal(other.inboundMessageEnvelope)
	}

	return false
}

func (r *InboundResponseEnvelope) IsValid() bool {
	if r == nil {
		return false
	}
	return r.inboundMessageEnvelope.IsValid()
}

func (r *InboundResponseEnvelope) Validate() error {
	if r == nil {
		return errors.NewInvalidArgumentErrorWrap("nil envelope")
	}
	return errors.Wrap(r.inboundMessageEnvelope.Validate())
}

func (r *InboundResponseEnvelope) MarshalJSON() ([]byte, error) {
	if r == nil {
		return jsonNull, nil
	}
	return errors.Wrap2(r.inboundMessageEnvelope.MarshalJSON())
}

func (r *InboundResponseEnvelope) UnmarshalJSON(data []byte) error {
	if r == nil {
		return errors.NewInvalidArgumentErrorWrap("nil envelope")
	}

	if r.inboundMessageEnvelope == nil {
		r.inboundMessageEnvelope = new(inboundMessageEnvelope[*Response])
	}

	if err := r.inboundMessageEnvelope.UnmarshalJSON(data); err != nil {
		*r = InboundResponseEnvelope{}
		return errors.Wrap(err)
	}

	return nil
}

func (r *InboundResponseEnvelope) LogValue() slog.Value {
	if r == nil {
		return slog.Value{}
	}
	return r.inboundMessageEnvelope.LogValue()
}

type OutboundResponseEnvelope struct {
	*outboundMessageEnvelope[*Response]
}

func NewOutboundResponseEnvelope(res *Response) (*OutboundResponseEnvelope, error) {
	if res == nil {
		return nil, errors.NewInvalidArgumentErrorWrap("nil response")
	}

	me := &messageEnvelope[*Response]{
		msg:     res,
		msgTime: time.Now(),
		meta:    new(MessageMetadata),
	}

	return &OutboundResponseEnvelope{
		&outboundMessageEnvelope[*Response]{
			messageEnvelope: me,
		},
	}, nil
}

func (r *OutboundResponseEnvelope) Message() *Response {
	if r == nil {
		return nil
	}
	return r.outboundMessageEnvelope.Message()
}

func (r *OutboundResponseEnvelope) AccessMessage(update func(*Response)) {
	if r == nil {
		return
	}

	r.outboundMessageEnvelope.AccessMessage(update)
}

func (r *OutboundResponseEnvelope) Headers() Headers {
	if r == nil {
		return nil
	}
	return r.outboundMessageEnvelope.Headers()
}

func (r *OutboundResponseEnvelope) Body() []byte {
	if r == nil {
		return nil
	}
	return r.outboundMessageEnvelope.Body()
}

func (r *OutboundResponseEnvelope) MessageTime() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.outboundMessageEnvelope.MessageTime()
}

func (r *OutboundResponseEnvelope) Transport() TransportProto {
	if r == nil {
		return ""
	}
	return r.outboundMessageEnvelope.Transport()
}

func (r *OutboundResponseEnvelope) SetTransport(tp TransportProto) {
	if r == nil {
		return
	}

	r.outboundMessageEnvelope.SetTransport(tp)
}

func (r *OutboundResponseEnvelope) LocalAddr() netip.AddrPort {
	if r == nil {
		return netip.AddrPort{}
	}
	return r.outboundMessageEnvelope.LocalAddr()
}

func (r *OutboundResponseEnvelope) SetLocalAddr(addr netip.AddrPort) {
	if r == nil {
		return
	}

	r.outboundMessageEnvelope.SetLocalAddr(netutil.UnmapAddrPort(addr))
}

func (r *OutboundResponseEnvelope) RemoteAddr() netip.AddrPort {
	if r == nil {
		return netip.AddrPort{}
	}
	return r.outboundMessageEnvelope.RemoteAddr()
}

func (r *OutboundResponseEnvelope) SetRemoteAddr(addr netip.AddrPort) {
	if r == nil {
		return
	}

	r.outboundMessageEnvelope.SetRemoteAddr(netutil.UnmapAddrPort(addr))
}

func (r *OutboundResponseEnvelope) Metadata() *MessageMetadata {
	if r == nil {
		return nil
	}
	return r.outboundMessageEnvelope.Metadata()
}

func (r *OutboundResponseEnvelope) Status() ResponseStatus {
	if r == nil {
		return 0
	}

	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	return r.msg.Status
}

func (r *OutboundResponseEnvelope) Reason() ResponseReason {
	if r == nil {
		return ""
	}

	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	return r.msg.Reason
}

func (r *OutboundResponseEnvelope) RenderTo(w io.Writer, opts *RenderOptions) (int, error) {
	if r == nil {
		return 0, nil
	}
	return errors.Wrap2(r.outboundMessageEnvelope.RenderTo(w, opts))
}

func (r *OutboundResponseEnvelope) Render(opts *RenderOptions) string {
	if r == nil {
		return ""
	}
	return r.outboundMessageEnvelope.Render(opts)
}

func (r *OutboundResponseEnvelope) String() string {
	if r == nil {
		return sNilTag
	}

	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	return r.msg.String()
}

func (r *OutboundResponseEnvelope) Format(f fmt.State, verb rune) {
	if r == nil {
		f.Write(bNilTag) //nolint:errcheck
		return
	}

	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	r.msg.Format(f, verb)
}

func (r *OutboundResponseEnvelope) Clone() Message {
	if r == nil {
		return nil
	}

	return &OutboundResponseEnvelope{
		r.outboundMessageEnvelope.Clone().(*outboundMessageEnvelope[*Response]), //nolint:forcetypeassert
	}
}

func (r *OutboundResponseEnvelope) Equal(v any) bool {
	if r == nil {
		return v == nil
	}

	if other, ok := v.(*OutboundResponseEnvelope); ok {
		return r.outboundMessageEnvelope.Equal(other.outboundMessageEnvelope)
	}

	return false
}

func (r *OutboundResponseEnvelope) IsValid() bool {
	if r == nil {
		return false
	}
	return r.outboundMessageEnvelope.IsValid()
}

func (r *OutboundResponseEnvelope) Validate() error {
	if r == nil {
		return errors.NewInvalidArgumentErrorWrap("nil envelope")
	}
	return errors.Wrap(r.outboundMessageEnvelope.Validate())
}

func (r *OutboundResponseEnvelope) MarshalJSON() ([]byte, error) {
	if r == nil {
		return jsonNull, nil
	}
	return errors.Wrap2(r.outboundMessageEnvelope.MarshalJSON())
}

func (r *OutboundResponseEnvelope) UnmarshalJSON(data []byte) error {
	if r == nil {
		return errors.NewInvalidArgumentErrorWrap("nil envelope")
	}

	if r.outboundMessageEnvelope == nil {
		r.outboundMessageEnvelope = new(outboundMessageEnvelope[*Response])
	}

	r.msgMu.Lock()
	defer r.msgMu.Unlock()

	if err := r.unmarshalUnsafe(data); err != nil {
		*r = OutboundResponseEnvelope{}
		return errors.Wrap(err)
	}

	return nil
}

func (r *OutboundResponseEnvelope) LogValue() slog.Value {
	if r == nil {
		return slog.Value{}
	}
	return r.outboundMessageEnvelope.LogValue()
}

// ResponseReceiver is an interface for receiving responses.
type ResponseReceiver interface {
	// RecvResponse receives a valid inbound response from the transport or downstream receiver.
	RecvResponse(ctx context.Context, res *InboundResponseEnvelope) error
}

type ResponseReceiverFunc func(ctx context.Context, res *InboundResponseEnvelope) error

func (fn ResponseReceiverFunc) RecvResponse(ctx context.Context, res *InboundResponseEnvelope) error {
	return errors.Wrap(fn(ctx, res))
}

type ResponseSender interface {
	// SendResponse sends the response to a remote address resolved with steps
	// defined in RFC 3261 Section 18.2.2. and RFC 3263 Section 5.
	//
	// Context can be used to cancel the response sending process through the deadline.
	// If no deadline is specified on the context, the deadline is set to [SendResponseOptions.Timeout].
	//
	// Options are optional and can be nil.
	//
	// If no target is resolved, [ErrNoTarget] is returned.
	SendResponse(ctx context.Context, res *OutboundResponseEnvelope, opts *SendResponseOptions) error
}

type ResponseSenderFunc func(ctx context.Context, res *OutboundResponseEnvelope, opts *SendResponseOptions) error

func (fn ResponseSenderFunc) SendResponse(
	ctx context.Context,
	res *OutboundResponseEnvelope,
	opts *SendResponseOptions,
) error {
	return errors.Wrap(fn(ctx, res, opts))
}

// SendResponseOptions are options for sending a response.
type SendResponseOptions struct {
	// Timeout is the timeout for the response sending process.
	// If zero, the default timeout [MessageWriteTimeout] is used.
	Timeout time.Duration `json:"timeout,omitempty"`
	// RenderCompact is the flag that indicates whether the message should be rendered in compact form.
	// See [RenderOptions] for more details.
	RenderCompact bool `json:"render_compact,omitempty"`
}

func (o *SendResponseOptions) timeout() time.Duration {
	if o == nil || o.Timeout == 0 {
		return ConnWriteTimeout
	}
	return o.Timeout
}

func (o *SendResponseOptions) rendOpts() *RenderOptions {
	if o == nil {
		return nil
	}

	return &RenderOptions{
		Compact: o.RenderCompact,
	}
}

func cloneSendResOpts(opts *SendResponseOptions) *SendResponseOptions {
	if opts == nil {
		return nil
	}

	newOpts := *opts

	return &newOpts
}

// RespondOptions are options for respond helper functions.
type RespondOptions struct {
	*ResponseOptions     `json:"response_options,omitempty"`
	*SendResponseOptions `json:"send_response_options,omitempty"`
}

func (o *RespondOptions) resOpts() *ResponseOptions {
	if o == nil {
		return nil
	}
	return o.ResponseOptions
}

func (o *RespondOptions) sendOpts() *SendResponseOptions {
	if o == nil {
		return nil
	}
	return o.SendResponseOptions
}
