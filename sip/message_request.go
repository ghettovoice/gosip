package sip

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
)

// RequestMethod represents a SIP request method.
// See [types.RequestMethod].
type RequestMethod = types.RequestMethod

// Request method constants.
// See [types.RequestMethod].
const (
	RequestMethodAck       = types.RequestMethodAck
	RequestMethodBye       = types.RequestMethodBye
	RequestMethodCancel    = types.RequestMethodCancel
	RequestMethodInfo      = types.RequestMethodInfo
	RequestMethodInvite    = types.RequestMethodInvite
	RequestMethodMessage   = types.RequestMethodMessage
	RequestMethodNotify    = types.RequestMethodNotify
	RequestMethodOptions   = types.RequestMethodOptions
	RequestMethodPrack     = types.RequestMethodPrack
	RequestMethodPublish   = types.RequestMethodPublish
	RequestMethodRefer     = types.RequestMethodRefer
	RequestMethodRegister  = types.RequestMethodRegister
	RequestMethodSubscribe = types.RequestMethodSubscribe
	RequestMethodUpdate    = types.RequestMethodUpdate
)

// IsKnownRequestMethod returns whether the method is defined in RFC 3261 or one it's extensions.
func IsKnownRequestMethod(method RequestMethod) bool {
	return types.IsKnownRequestMethod(method)
}

// Request represents a SIP request message.
type Request struct {
	Method  RequestMethod `json:"method"`
	URI     URI           `json:"uri"`
	Proto   ProtoInfo     `json:"proto"`
	Headers Headers       `json:"headers"`
	Body    []byte        `json:"body"`
}

// RenderTo renders the SIP request to the given writer.
func (req *Request) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
	if req == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Call(func(w io.Writer) (int, error) {
		return errtrace.Wrap2(req.renderStartLine(w, opts))
	})
	cw.Fprint("\r\n")
	cw.Call(func(w io.Writer) (int, error) {
		return errtrace.Wrap2(renderHdrs(w, req.Headers, opts))
	})
	cw.Fprint("\r\n")
	cw.Write(req.Body)
	return errtrace.Wrap2(cw.Result())
}

func (req *Request) renderStartLine(w io.Writer, opts *RenderOptions) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(req.Method, " ")
	if req.URI != nil {
		cw.Call(func(w io.Writer) (int, error) {
			return errtrace.Wrap2(req.URI.RenderTo(w, opts))
		})
	}
	cw.Fprint(" ", req.Proto)
	return errtrace.Wrap2(cw.Result())
}

// Render renders the SIP request to a string.
func (req *Request) Render(opts *RenderOptions) string {
	if req == nil {
		return ""
	}
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	req.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// String returns a short string representation of the request.
func (req *Request) String() string {
	if req == nil {
		return sNilTag
	}
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	// TODO make a better short representation of the request
	req.renderStartLine(sb, nil) //nolint:errcheck
	return sb.String()
}

// Format implements [fmt.Formatter] for custom formatting.
func (req *Request) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			req.RenderTo(f, nil) //nolint:errcheck
			return
		}
		f.Write([]byte(req.String()))
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(req.Render(nil)))
			return
		}
		f.Write([]byte(strconv.Quote(req.String())))
		return
	default:
		type hideMethods Request
		type Request hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*Request)(req))
		return
	}
}

// LogValue implements [slog.LogValuer] for structured logging.
func (req *Request) LogValue() slog.Value {
	if req == nil {
		return zeroSlogValue
	}

	attrs := make([]slog.Attr, 0, 7)
	attrs = append(attrs, slog.String("method", string(req.Method)), slog.Any("uri", req.URI))
	if hop, ok := util.SeqFirst(req.Headers.Via()); ok {
		attrs = append(attrs, slog.Any("Via", hop))
	}
	if from, ok := req.Headers.From(); ok {
		attrs = append(attrs, slog.Any("From", from))
	}
	if to, ok := req.Headers.To(); ok {
		attrs = append(attrs, slog.Any("To", to))
	}
	if callID, ok := req.Headers.CallID(); ok {
		attrs = append(attrs, slog.Any("Call-ID", callID))
	}
	if cseq, ok := req.Headers.CSeq(); ok {
		attrs = append(attrs, slog.Any("CSeq", cseq))
	}

	return slog.GroupValue(attrs...)
}

// Clone returns a deep copy of the request.
func (req *Request) Clone() Message {
	if req == nil {
		return nil
	}

	req2 := *req
	req2.URI = types.Clone[URI](req.URI)
	req2.Headers = req.Headers.Clone()
	req2.Body = slices.Clone(req.Body)
	return &req2
}

// Equal returns whether the request is equal to another value.
func (req *Request) Equal(val any) bool {
	var other *Request
	switch v := val.(type) {
	case Request:
		other = &v
	case *Request:
		other = v
	default:
		return false
	}

	if req == other {
		return true
	} else if req == nil || other == nil {
		return false
	}

	return req.Method.Equal(other.Method) &&
		req.Proto.Equal(other.Proto) &&
		types.IsEqual(req.URI, other.URI) &&
		compareHdrs(req.Headers, other.Headers) &&
		slices.Equal(req.Body, other.Body)
}

// IsValid returns whether the request is valid.
func (req *Request) IsValid() bool {
	return req.Validate() == nil
}

var reqMandatoryHdrs = map[HeaderName]bool{
	"Via":     true,
	"From":    true,
	"To":      true,
	"Call-ID": true,
	"CSeq":    true,
}

// Validate validates the request and returns an error if invalid.
func (req *Request) Validate() error {
	if req == nil {
		return errtrace.Wrap(NewInvalidArgumentError("invalid request"))
	}

	errs := make([]error, 0, 10)

	if !req.Method.IsValid() {
		errs = append(errs, errorutil.Errorf("invalid method %q", req.Method))
	}
	if !types.IsValid(req.URI) {
		errs = append(errs, errorutil.Errorf("invalid URI %q", req.URI))
	}
	if !req.Proto.IsValid() {
		errs = append(errs, errorutil.Errorf("invalid protocol %q", req.Proto))
	}
	if err := validateHdrs(req.Headers); err != nil {
		errs = append(errs, err)
	}
	for n := range reqMandatoryHdrs {
		if !req.Headers.Has(n) {
			errs = append(errs, newMissHdrErr(n))
		}
	}
	if ct, ok := req.Headers.ContentLength(); ok {
		if ct, bl := int(ct), len(req.Body); ct != bl {
			errs = append(errs, errorutil.Errorf("content length mismatch: got %d, want %d", ct, bl))
		}
	}

	if len(errs) > 0 {
		return errtrace.Wrap(NewInvalidMessageError(errorutil.Join(errs...)))
	}
	return nil
}

func (req *Request) UnmarshalJSON(data []byte) error {
	var reqData struct {
		Method  RequestMethod `json:"method"`
		URI     string        `json:"uri"`
		Proto   ProtoInfo     `json:"proto"`
		Headers Headers       `json:"headers"`
		Body    []byte        `json:"body"`
	}
	if err := json.Unmarshal(data, &reqData); err != nil {
		return errtrace.Wrap(err)
	}

	req.Method = reqData.Method
	req.Proto = reqData.Proto
	req.Headers = reqData.Headers
	req.Body = reqData.Body

	if reqData.URI != "" {
		u, err := ParseURI(reqData.URI)
		if err != nil {
			return errtrace.Wrap(fmt.Errorf("parse URI: %w", err))
		}
		req.URI = u
	} else {
		req.URI = nil
	}
	return nil
}

type ResponseOptions struct {
	Reason   ResponseReason `json:"reason,omitempty"`
	Headers  Headers        `json:"headers,omitempty"`
	Body     []byte         `json:"body,omitempty"`
	LocalTag string         `json:"loc_tag,omitempty"`
}

func (o *ResponseOptions) reason() ResponseReason {
	if o == nil {
		return ""
	}
	return o.Reason
}

func (o *ResponseOptions) headers() Headers {
	if o == nil {
		return nil
	}
	return o.Headers
}

func (o *ResponseOptions) body() []byte {
	if o == nil {
		return nil
	}
	return o.Body
}

func (o *ResponseOptions) locTag() string {
	if o == nil {
		return GenerateTag(0)
	}
	return o.LocalTag
}

var (
	reqCopyHdrsMap = map[HeaderName]bool{
		"Via":       true,
		"From":      true,
		"To":        true,
		"Call-ID":   true,
		"CSeq":      true,
		"Timestamp": true,
	}
	reqCopyHdrsSlice = slices.Collect(maps.Keys(reqCopyHdrsMap))
)

func (req *Request) NewResponse(sts ResponseStatus, opts *ResponseOptions) (*Response, error) {
	if req == nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid request"))
	}
	if req.Method.Equal(RequestMethodAck) {
		return nil, errtrace.Wrap(NewInvalidArgumentError(ErrMethodNotAllowed))
	}

	res := &Response{
		Status:  sts,
		Reason:  opts.reason(),
		Proto:   req.Proto,
		Headers: make(Headers, 6).CopyFrom(req.Headers, reqCopyHdrsSlice[0], reqCopyHdrsSlice[1:]...),
		Body:    opts.body(),
	}
	// local tag for all responses except Trying
	if sts != ResponseStatusTrying {
		if to, ok := res.Headers.To(); ok && to != nil && (to.Params == nil || !to.Params.Has("tag")) {
			if to.Params == nil {
				to.Params = make(Values)
			}
			to.Params.Set("tag", opts.locTag())
		}
	}
	for n, hs := range opts.headers() {
		if reqCopyHdrsMap[n] || len(hs) == 0 {
			continue
		}
		res.Headers.Append(hs[0], hs[1:]...)
	}
	return res, nil
}

// RequestOptions are used to create a new request.
// All fields are optional, default values are used if zero.
type RequestOptions struct {
	// Transport is the transport protocol to use. Default is "UDP".
	Transport TransportProto
	// Branch is the branch parameter to use. Default is generated using [GenerateBranch].
	Branch string
	// LocalTag is the tag used in From header. Default is generated using [GenerateTag].
	LocalTag string
	// RemoteTag is the tag used in To header. Default is empty.
	RemoteTag string
	// CallID is the Call-ID to use. Default is generated using [GenerateCallID].
	CallID string
	// SeqNum is the sequence number to be used in CSeq header. Default is 1.
	SeqNum uint
	// MaxForwards used in Max-Forwards header. Default is 70.
	MaxForwards uint
	// Headers are additional headers to be added to the request.
	Headers Headers
	// Body is the body of the request.
	// It is responsibility of the caller to set the Content-Type header if the body is not empty.
	Body []byte
	// AddRPort adds the "rport" parameter in the Via header. Default is false.
	// RFC 3581 Section 3.
	AddRPort bool
}

func (o *RequestOptions) transp() TransportProto {
	if o == nil || o.Transport == "" {
		return "UDP"
	}
	return o.Transport
}

func (o *RequestOptions) branch() string {
	if o == nil || o.Branch == "" || !strings.HasPrefix(o.Branch, MagicCookie) {
		return GenerateBranch(0)
	}
	return o.Branch
}

func (o *RequestOptions) locTag() string {
	if o == nil || o.LocalTag == "" {
		return GenerateTag(0)
	}
	return o.LocalTag
}

func (o *RequestOptions) rmtTag() string {
	if o == nil || o.RemoteTag == "" {
		return ""
	}
	return o.RemoteTag
}

func (o *RequestOptions) callID() string {
	if o == nil || o.CallID == "" {
		return GenerateCallID(0, "")
	}
	return o.CallID
}

func (o *RequestOptions) seqNum() uint {
	if o == nil || o.SeqNum == 0 {
		return 1
	}
	return o.SeqNum
}

func (o *RequestOptions) maxFwd() uint {
	if o == nil || o.MaxForwards == 0 {
		return 70
	}
	return o.MaxForwards
}

func (o *RequestOptions) headers() Headers {
	if o == nil {
		return nil
	}
	return o.Headers
}

func (o *RequestOptions) body() []byte {
	if o == nil {
		return nil
	}
	return o.Body
}

func (o *RequestOptions) addRPort() bool {
	return o != nil && o.AddRPort
}

// NewRequest is a helper function to create a minimally valid SIP request.
// The returned request has all mandatory headers and parameters set following the rules of RFC 3261 Section 8.1.1.
func NewRequest(mtd RequestMethod, ruri, from, to URI, opts *RequestOptions) (*Request, error) {
	if !mtd.IsValid() {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid request method"))
	}
	if !ruri.IsValid() {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid request URI"))
	}
	if !from.IsValid() {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid from URI"))
	}
	if !to.IsValid() {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid to URI"))
	}

	via := header.ViaHop{
		Proto:     protoVer20,
		Transport: opts.transp(),
		Addr:      Host(util.RandString(8) + ".invalid"), // will be replaced by the transport
		Params:    make(Values).Set("branch", opts.branch()),
	}
	if opts.addRPort() {
		via.Params.Set("rport", "")
	}

	toHdr := &header.To{URI: to}
	if opts.rmtTag() != "" {
		toHdr.Params = make(Values).Set("tag", opts.rmtTag())
	}

	req := &Request{
		Method: mtd,
		URI:    ruri,
		Proto:  protoVer20,
		Headers: make(Headers).
			Set(header.Via{via}).
			Set(&header.From{
				URI:    from,
				Params: make(Values).Set("tag", opts.locTag()),
			}).
			Set(toHdr).
			Set(header.CallID(opts.callID())).
			Set(&header.CSeq{Method: mtd, SeqNum: opts.seqNum()}).
			Set(header.MaxForwards(opts.maxFwd())),
		Body: opts.body(),
	}
	for n, hs := range opts.headers() {
		if reqMandatoryHdrs[n] || len(hs) == 0 {
			continue
		}
		req.Headers.Append(hs[0], hs[1:]...)
	}
	return req, nil
}

type InboundRequestEnvelope struct {
	*inboundMessageEnvelope[*Request]
}

func NewInboundRequestEnvelope(
	req *Request,
	tp TransportProto,
	laddr, raddr netip.AddrPort,
) (*InboundRequestEnvelope, error) {
	if req == nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid request"))
	}
	if tp == "" {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid transport protocol"))
	}
	if !laddr.IsValid() {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid local address"))
	}
	if !raddr.IsValid() {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid remote address"))
	}

	me := &inboundMessageEnvelope[*Request]{
		msgTime: time.Now(),
		data:    new(MessageMetadata),
	}
	me.msg.Store(req)
	me.tp.Store(tp)
	me.locAddr.Store(laddr)
	me.rmtAddr.Store(raddr)
	return &InboundRequestEnvelope{me}, nil
}

func (r *InboundRequestEnvelope) Message() *Request {
	if r == nil {
		return nil
	}
	return r.inboundMessageEnvelope.Message()
}

func (r *InboundRequestEnvelope) Headers() Headers {
	if r == nil {
		return nil
	}
	return r.inboundMessageEnvelope.Headers()
}

func (r *InboundRequestEnvelope) Body() []byte {
	if r == nil {
		return nil
	}
	return r.inboundMessageEnvelope.Body()
}

func (r *InboundRequestEnvelope) Transport() TransportProto {
	if r == nil {
		return ""
	}
	return r.inboundMessageEnvelope.Transport()
}

func (r *InboundRequestEnvelope) LocalAddr() netip.AddrPort {
	if r == nil {
		return zeroAddrPort
	}
	return r.inboundMessageEnvelope.LocalAddr()
}

func (r *InboundRequestEnvelope) RemoteAddr() netip.AddrPort {
	if r == nil {
		return zeroAddrPort
	}
	return r.inboundMessageEnvelope.RemoteAddr()
}

func (r *InboundRequestEnvelope) MessageTime() time.Time {
	if r == nil {
		return zeroTime
	}
	return r.inboundMessageEnvelope.MessageTime()
}

func (r *InboundRequestEnvelope) Metadata() *MessageMetadata {
	if r == nil {
		return nil
	}
	return r.inboundMessageEnvelope.Metadata()
}

func (r *InboundRequestEnvelope) Method() RequestMethod {
	if r == nil {
		return ""
	}
	return r.message().Method
}

func (r *InboundRequestEnvelope) URI() URI {
	if r == nil {
		return nil
	}
	return r.message().URI.Clone()
}

var reqTimeDataKey = "sip.request_time"

func (r *InboundRequestEnvelope) NewResponse(
	sts ResponseStatus,
	opts *ResponseOptions,
) (*OutboundResponseEnvelope, error) {
	if r == nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid request"))
	}

	msg, err := r.message().NewResponse(sts, opts)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}

	res, err := NewOutboundResponseEnvelope(msg)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	res.tp.Store(r.transport())
	res.locAddr.Store(r.localAddr())
	res.rmtAddr.Store(r.remoteAddr())
	res.data.Set(reqTimeDataKey, r.msgTime)
	return res, nil
}

func (r *InboundRequestEnvelope) RenderTo(w io.Writer, opts *RenderOptions) (int, error) {
	if r == nil {
		return 0, nil
	}
	return errtrace.Wrap2(r.inboundMessageEnvelope.RenderTo(w, opts))
}

func (r *InboundRequestEnvelope) Render(opts *RenderOptions) string {
	if r == nil {
		return ""
	}
	return r.inboundMessageEnvelope.Render(opts)
}

func (r *InboundRequestEnvelope) String() string {
	if r == nil {
		return sNilTag
	}
	return r.message().String()
}

func (r *InboundRequestEnvelope) Format(f fmt.State, verb rune) {
	if r == nil {
		f.Write(bNilTag)
		return
	}
	r.message().Format(f, verb)
}

func (r *InboundRequestEnvelope) Clone() Message {
	if r == nil {
		return nil
	}
	return &InboundRequestEnvelope{
		r.inboundMessageEnvelope.Clone().(*inboundMessageEnvelope[*Request]), //nolint:forcetypeassert
	}
}

func (r *InboundRequestEnvelope) Equal(v any) bool {
	if r == nil {
		return v == nil
	}
	if other, ok := v.(*InboundRequestEnvelope); ok {
		return r.inboundMessageEnvelope.Equal(other.inboundMessageEnvelope)
	}
	return false
}

func (r *InboundRequestEnvelope) IsValid() bool {
	if r == nil {
		return false
	}
	return r.inboundMessageEnvelope.IsValid()
}

func (r *InboundRequestEnvelope) Validate() error {
	if r == nil {
		return errtrace.Wrap(NewInvalidArgumentError("invalid request"))
	}
	return errtrace.Wrap(r.inboundMessageEnvelope.Validate())
}

func (r *InboundRequestEnvelope) MarshalJSON() ([]byte, error) {
	if r == nil {
		return jsonNull, nil
	}
	return errtrace.Wrap2(r.inboundMessageEnvelope.MarshalJSON())
}

func (r *InboundRequestEnvelope) UnmarshalJSON(data []byte) error {
	if r.inboundMessageEnvelope == nil {
		r.inboundMessageEnvelope = new(inboundMessageEnvelope[*Request])
	}
	return errtrace.Wrap(r.inboundMessageEnvelope.UnmarshalJSON(data))
}

func (r *InboundRequestEnvelope) LogValue() slog.Value {
	if r == nil {
		return zeroSlogValue
	}
	return r.inboundMessageEnvelope.LogValue()
}

type OutboundRequestEnvelope struct {
	*outboundMessageEnvelope[*Request]
}

func NewOutboundRequestEnvelope(req *Request) (*OutboundRequestEnvelope, error) {
	if req == nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid request"))
	}

	me := &messageEnvelope[*Request]{
		msgTime: time.Now(),
		data:    new(MessageMetadata),
	}
	me.msg.Store(req)
	return &OutboundRequestEnvelope{
		&outboundMessageEnvelope[*Request]{
			messageEnvelope: me,
		},
	}, nil
}

func (r *OutboundRequestEnvelope) Message() *Request {
	if r == nil {
		return nil
	}
	return r.outboundMessageEnvelope.Message()
}

func (r *OutboundRequestEnvelope) AccessMessage(update func(*Request)) {
	if r == nil {
		return
	}
	r.outboundMessageEnvelope.AccessMessage(update)
}

func (r *OutboundRequestEnvelope) Headers() Headers {
	if r == nil {
		return nil
	}
	return r.outboundMessageEnvelope.Headers()
}

func (r *OutboundRequestEnvelope) Body() []byte {
	if r == nil {
		return nil
	}
	return r.outboundMessageEnvelope.Body()
}

func (r *OutboundRequestEnvelope) Transport() TransportProto {
	if r == nil {
		return ""
	}
	return r.outboundMessageEnvelope.Transport()
}

func (r *OutboundRequestEnvelope) SetTransport(tp TransportProto) {
	if r == nil {
		return
	}
	r.outboundMessageEnvelope.SetTransport(tp)
}

func (r *OutboundRequestEnvelope) LocalAddr() netip.AddrPort {
	if r == nil {
		return zeroAddrPort
	}
	return r.outboundMessageEnvelope.LocalAddr()
}

func (r *OutboundRequestEnvelope) SetLocalAddr(addr netip.AddrPort) {
	if r == nil {
		return
	}
	r.outboundMessageEnvelope.SetLocalAddr(addr)
}

func (r *OutboundRequestEnvelope) RemoteAddr() netip.AddrPort {
	if r == nil {
		return zeroAddrPort
	}
	return r.outboundMessageEnvelope.RemoteAddr()
}

func (r *OutboundRequestEnvelope) SetRemoteAddr(addr netip.AddrPort) {
	if r == nil {
		return
	}
	r.outboundMessageEnvelope.SetRemoteAddr(addr)
}

func (r *OutboundRequestEnvelope) MessageTime() time.Time {
	if r == nil {
		return zeroTime
	}
	return r.outboundMessageEnvelope.MessageTime()
}

func (r *OutboundRequestEnvelope) Metadata() *MessageMetadata {
	if r == nil {
		return nil
	}
	return r.outboundMessageEnvelope.Metadata()
}

func (r *OutboundRequestEnvelope) Method() RequestMethod {
	if r == nil {
		return ""
	}

	r.msgMu.RLock()
	defer r.msgMu.RUnlock()
	return r.message().Method
}

func (r *OutboundRequestEnvelope) URI() URI {
	if r == nil {
		return nil
	}

	r.msgMu.RLock()
	defer r.msgMu.RUnlock()
	return r.message().URI.Clone()
}

func (r *OutboundRequestEnvelope) RenderTo(w io.Writer, opts *RenderOptions) (int, error) {
	if r == nil {
		return 0, nil
	}
	return errtrace.Wrap2(r.outboundMessageEnvelope.RenderTo(w, opts))
}

func (r *OutboundRequestEnvelope) Render(opts *RenderOptions) string {
	if r == nil {
		return ""
	}
	return r.outboundMessageEnvelope.Render(opts)
}

func (r *OutboundRequestEnvelope) String() string {
	if r == nil {
		return sNilTag
	}

	r.msgMu.RLock()
	defer r.msgMu.RUnlock()
	return r.message().String()
}

func (r *OutboundRequestEnvelope) Format(f fmt.State, verb rune) {
	if r == nil {
		f.Write(bNilTag)
		return
	}

	r.msgMu.RLock()
	defer r.msgMu.RUnlock()
	r.message().Format(f, verb)
}

func (r *OutboundRequestEnvelope) Clone() Message {
	if r == nil {
		return nil
	}
	return &OutboundRequestEnvelope{
		r.outboundMessageEnvelope.Clone().(*outboundMessageEnvelope[*Request]), //nolint:forcetypeassert
	}
}

func (r *OutboundRequestEnvelope) Equal(v any) bool {
	if r == nil {
		return v == nil
	}
	if other, ok := v.(*OutboundRequestEnvelope); ok {
		return r.outboundMessageEnvelope.Equal(other.outboundMessageEnvelope)
	}
	return false
}

func (r *OutboundRequestEnvelope) IsValid() bool {
	if r == nil {
		return false
	}
	return r.outboundMessageEnvelope.IsValid()
}

func (r *OutboundRequestEnvelope) Validate() error {
	if r == nil {
		return errtrace.Wrap(NewInvalidArgumentError("invalid request"))
	}
	return errtrace.Wrap(r.outboundMessageEnvelope.Validate())
}

func (r *OutboundRequestEnvelope) MarshalJSON() ([]byte, error) {
	if r == nil {
		return jsonNull, nil
	}
	return errtrace.Wrap2(r.outboundMessageEnvelope.MarshalJSON())
}

func (r *OutboundRequestEnvelope) UnmarshalJSON(data []byte) error {
	if r.outboundMessageEnvelope == nil {
		r.outboundMessageEnvelope = new(outboundMessageEnvelope[*Request])
	}
	return errtrace.Wrap(r.outboundMessageEnvelope.UnmarshalJSON(data))
}

func (r *OutboundRequestEnvelope) LogValue() slog.Value {
	if r == nil {
		return zeroSlogValue
	}
	return r.outboundMessageEnvelope.LogValue()
}

// RequestReceiver is an interface for receiving requests.
type RequestReceiver interface {
	// RecvRequest receives a valid inbound request from the transport or downstream receiver.
	RecvRequest(ctx context.Context, req *InboundRequestEnvelope) error
}

type RequestReceiverFunc func(ctx context.Context, req *InboundRequestEnvelope) error

func (fn RequestReceiverFunc) RecvRequest(ctx context.Context, req *InboundRequestEnvelope) error {
	return fn(ctx, req) //errtrace:skip
}

// RequestSender is an interface for sending requests.
type RequestSender interface {
	// SendRequest sends the request to the remote address specified in the envelope.
	//
	// Context can be used to cancel the request sending process through the deadline.
	// If no deadline is specified on the context, the deadline is set to [SendRequestOptions.Timeout].
	//
	// Options are optional, if nil is passed, default options are used (see [SendRequestOptions]).
	SendRequest(ctx context.Context, req *OutboundRequestEnvelope, opts *SendRequestOptions) error
}

type RequestSenderFunc func(ctx context.Context, req *OutboundRequestEnvelope, opts *SendRequestOptions) error

func (fn RequestSenderFunc) SendRequest(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	opts *SendRequestOptions,
) error {
	return fn(ctx, req, opts) //errtrace:skip
}

// SendRequestOptions are options for sending a request.
type SendRequestOptions struct {
	// Timeout is the timeout for the request sending process.
	// If zero, the default timeout 1m is used.
	Timeout time.Duration `json:"timeout,omitempty"`
	// RenderCompact is the flag that indicates whether the message should be rendered in compact form.
	// See [RenderOptions] for more details.
	RenderCompact bool `json:"render_compact,omitempty"`
	// TODO: options for multicast
}

func (o *SendRequestOptions) timeout() time.Duration {
	if o == nil || o.Timeout == 0 {
		return msgSendTimeout
	}
	return o.Timeout
}

func (o *SendRequestOptions) rendOpts() *RenderOptions {
	if o == nil {
		return nil
	}
	return &RenderOptions{
		Compact: o.RenderCompact,
	}
}

func cloneSendReqOpts(opts *SendRequestOptions) *SendRequestOptions {
	if opts == nil {
		return nil
	}
	newOpts := *opts
	return &newOpts
}
