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

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/netutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip/header"
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
	Body    []byte        `json:"body,omitempty"`
}

// RenderTo renders the SIP request to the given writer.
func (req *Request) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
	if req == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Call(func(w io.Writer) (int, error) {
		return errors.Wrap2(req.renderStartLine(w, opts))
	})
	cw.Fprint("\r\n")
	cw.Call(func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrs(w, req.Headers, opts))
	})
	cw.Fprint("\r\n")
	cw.Write(req.Body) //nolint:errcheck

	return errors.Wrap2(cw.Result())
}

func (req *Request) renderStartLine(w io.Writer, opts *RenderOptions) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(req.Method, " ")

	if req.URI != nil {
		cw.Call(func(w io.Writer) (int, error) {
			return errors.Wrap2(req.URI.RenderTo(w, opts))
		})
	}

	cw.Fprint(" ", req.Proto)

	return errors.Wrap2(cw.Result())
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

		f.Write([]byte(req.String())) //nolint:errcheck

		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(req.Render(nil)))
			return
		}

		f.Write([]byte(strconv.Quote(req.String()))) //nolint:errcheck

		return
	default:
		type (
			hideMethods Request
			Request     hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), (*Request)(req))

		return
	}
}

// LogValue implements [slog.LogValuer] for structured logging.
func (req *Request) LogValue() slog.Value {
	if req == nil {
		return slog.Value{}
	}

	attrs := make([]slog.Attr, 0, 7)

	attrs = append(attrs, slog.String("method", string(req.Method)), slog.Any("uri", req.URI))
	if hop, ok := util.SeqFirst(req.Headers.Via()); ok {
		attrs = append(attrs, slog.Any("via", hop))
	}

	if from, ok := req.Headers.From(); ok {
		attrs = append(attrs, slog.Any("from", from))
	}

	if to, ok := req.Headers.To(); ok {
		attrs = append(attrs, slog.Any("to", to))
	}

	if callID, ok := req.Headers.CallID(); ok {
		attrs = append(attrs, slog.Any("call_id", callID))
	}

	if cseq, ok := req.Headers.CSeq(); ok {
		attrs = append(attrs, slog.Any("cseq", cseq))
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

var reqMandatoryHdrs = map[HeaderName]struct{}{
	"Via":     {},
	"From":    {},
	"To":      {},
	"Call-ID": {},
	"CSeq":    {},
}

// Validate validates the request and returns an error if invalid.
func (req *Request) Validate() error {
	if req == nil {
		return errors.NewInvalidArgumentErrorWrap("nil request")
	}

	errs := make([]error, 0, 10)
	if !req.Method.IsValid() {
		errs = append(errs, errors.Errorf("invalid method %q", req.Method))
	}

	if !types.IsValid(req.URI) {
		errs = append(errs, errors.Errorf("invalid URI %q", req.URI))
	}

	if !req.Proto.IsValid() {
		errs = append(errs, errors.Errorf("invalid protocol %q", req.Proto))
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
			errs = append(errs, errors.Errorf("content length mismatch: got %d, want %d", ct, bl))
		}
	}

	if len(errs) > 0 {
		return errors.Wrap(newInvalidMsgErr(errors.Join(errs...)))
	}

	return nil
}

func (req *Request) UnmarshalJSON(data []byte) error {
	if req == nil {
		return errors.NewInvalidArgumentErrorWrap("nil request")
	}

	var reqData struct {
		Method  RequestMethod `json:"method"`
		URI     string        `json:"uri"`
		Proto   ProtoInfo     `json:"proto"`
		Headers Headers       `json:"headers"`
		Body    []byte        `json:"body,omitempty"`
	}
	if err := json.Unmarshal(data, &reqData); err != nil {
		*req = Request{}
		return errors.Wrap(err)
	}

	req.Method = reqData.Method
	req.Proto = reqData.Proto
	req.Headers = reqData.Headers
	req.Body = reqData.Body

	if reqData.URI != "" {
		if u, err := ParseURI(reqData.URI); err == nil {
			req.URI = u
		}
	} else {
		req.URI = nil
	}

	return nil
}

type ResponseOptions struct {
	Reason   ResponseReason `json:"reason,omitempty"`
	Headers  Headers        `json:"headers,omitempty"`
	Body     []byte         `json:"body,omitempty"`
	LocalTag string         `json:"local_tag,omitempty"`
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

func (req *Request) NewResponse(sts ResponseStatus, opts *ResponseOptions) (*Response, error) {
	if req == nil {
		return nil, errors.NewInvalidArgumentErrorWrap("nil request")
	}

	if req.Method.Equal(RequestMethodAck) {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrMethodNotAllowed)
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
		if _, ok := reqCopyHdrsMap[n]; ok || len(hs) == 0 {
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
	Transport TransportProto `json:"transport,omitempty"`
	// Branch is the branch parameter to use. Default is generated using [GenerateBranch].
	Branch string `json:"branch,omitempty"`
	// LocalTag is the tag used in From header. Default is generated using [GenerateTag].
	LocalTag string `json:"local_tag,omitempty"`
	// RemoteTag is the tag used in To header. Default is empty.
	RemoteTag string `json:"remote_tag,omitempty"`
	// CallID is the Call-ID to use. Default is generated using [GenerateCallID].
	CallID string `json:"call_id,omitempty"`
	// SeqNum is the sequence number to be used in CSeq header. Default is 1.
	SeqNum uint `json:"seq_num,omitempty"`
	// MaxForwards used in Max-Forwards header. Default is 70.
	MaxForwards uint `json:"max_forwards,omitempty"`
	// Headers are additional headers to be added to the request.
	Headers Headers `json:"headers,omitempty"`
	// Body is the body of the request.
	// It is responsibility of the caller to set the Content-Type header if the body is not empty.
	Body []byte `json:"body,omitempty"`
	// RPort adds the "rport" parameter in the Via header. Default is false.
	// RFC 3581 Section 3.
	RPort bool `json:"rport,omitempty"`
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

func (o *RequestOptions) rport() bool {
	return o != nil && o.RPort
}

// NewRequest is a helper function to create a minimally valid SIP request.
// The returned request has all mandatory headers and parameters set following the rules of RFC 3261 Section 8.1.1.
func NewRequest(mtd RequestMethod, ruri, furi, turi URI, opts *RequestOptions) (*Request, error) {
	if !mtd.IsValid() {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid request method %q", mtd)
	}

	if !ruri.IsValid() {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid request URI %q", ruri)
	}

	if !furi.IsValid() {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid from URI %q", furi)
	}

	if !turi.IsValid() {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid to URI %q", turi)
	}

	via := header.ViaHop{
		Proto:     protoVer20,
		Transport: opts.transp(),
		Addr:      AddrFromHost(util.RandString(8) + ".invalid"), // will be replaced by the transport
		Params:    make(Values).Set("branch", opts.branch()),
	}
	if opts.rport() {
		via.Params.Set("rport", "")
	}

	toHdr := &header.To{URI: turi}
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
				URI:    furi,
				Params: make(Values).Set("tag", opts.locTag()),
			}).
			Set(toHdr).
			Set(header.CallID(opts.callID())).
			Set(&header.CSeq{Method: mtd, SeqNum: opts.seqNum()}).
			Set(header.MaxForwards(opts.maxFwd())),
		Body: opts.body(),
	}
	for n, hs := range opts.headers() {
		if _, ok := reqMandatoryHdrs[n]; ok || len(hs) == 0 {
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
		return nil, errors.NewInvalidArgumentErrorWrap("nil request")
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

	r := &inboundMessageEnvelope[*Request]{
		msg:     req,
		msgTime: time.Now(),
		meta:    new(MessageMetadata),
	}
	r.tp.Store(tp)
	r.laddr.Store(netutil.UnmapAddrPort(laddr))
	r.raddr.Store(netutil.UnmapAddrPort(raddr))

	return &InboundRequestEnvelope{r}, nil
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

func (r *InboundRequestEnvelope) MessageTime() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.inboundMessageEnvelope.MessageTime()
}

func (r *InboundRequestEnvelope) Transport() TransportProto {
	if r == nil {
		return ""
	}
	return r.inboundMessageEnvelope.Transport()
}

func (r *InboundRequestEnvelope) LocalAddr() netip.AddrPort {
	if r == nil {
		return netip.AddrPort{}
	}
	return r.inboundMessageEnvelope.LocalAddr()
}

func (r *InboundRequestEnvelope) RemoteAddr() netip.AddrPort {
	if r == nil {
		return netip.AddrPort{}
	}
	return r.inboundMessageEnvelope.RemoteAddr()
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
	return r.msg.Method
}

func (r *InboundRequestEnvelope) URI() URI {
	if r == nil {
		return nil
	}
	return r.msg.URI.Clone()
}

var reqTimeDataKey = "sip.request_time"

func (r *InboundRequestEnvelope) NewResponse(
	sts ResponseStatus,
	opts *ResponseOptions,
) (*OutboundResponseEnvelope, error) {
	if r == nil {
		return nil, errors.NewInvalidArgumentErrorWrap("nil envelope")
	}

	msg, err := r.msg.NewResponse(sts, opts)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	res, err := NewOutboundResponseEnvelope(msg)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	res.tp.Store(r.transport())
	res.laddr.Store(r.localAddr())
	res.raddr.Store(r.remoteAddr())
	res.meta.Set(reqTimeDataKey, r.msgTime)

	return res, nil
}

func (r *InboundRequestEnvelope) RenderTo(w io.Writer, opts *RenderOptions) (int, error) {
	if r == nil {
		return 0, nil
	}
	return errors.Wrap2(r.inboundMessageEnvelope.RenderTo(w, opts))
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
	return r.msg.String()
}

func (r *InboundRequestEnvelope) Format(f fmt.State, verb rune) {
	if r == nil {
		f.Write(bNilTag) //nolint:errcheck
		return
	}

	r.msg.Format(f, verb)
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
		return errors.NewInvalidArgumentErrorWrap("nil envelope")
	}
	return errors.Wrap(r.inboundMessageEnvelope.Validate())
}

func (r *InboundRequestEnvelope) MarshalJSON() ([]byte, error) {
	if r == nil {
		return jsonNull, nil
	}
	return errors.Wrap2(r.inboundMessageEnvelope.MarshalJSON())
}

func (r *InboundRequestEnvelope) UnmarshalJSON(data []byte) error {
	if r == nil {
		return errors.NewInvalidArgumentErrorWrap("nil envelope")
	}

	if r.inboundMessageEnvelope == nil {
		r.inboundMessageEnvelope = new(inboundMessageEnvelope[*Request])
	}

	if err := r.inboundMessageEnvelope.UnmarshalJSON(data); err != nil {
		*r = InboundRequestEnvelope{}
		return errors.Wrap(err)
	}

	return nil
}

func (r *InboundRequestEnvelope) LogValue() slog.Value {
	if r == nil {
		return slog.Value{}
	}
	return r.inboundMessageEnvelope.LogValue()
}

type OutboundRequestEnvelope struct {
	*outboundMessageEnvelope[*Request]
}

func NewOutboundRequestEnvelope(req *Request) (*OutboundRequestEnvelope, error) {
	if req == nil {
		return nil, errors.NewInvalidArgumentErrorWrap("nil request")
	}

	me := &messageEnvelope[*Request]{
		msg:     req,
		msgTime: time.Now(),
		meta:    new(MessageMetadata),
	}

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

func (r *OutboundRequestEnvelope) MessageTime() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.outboundMessageEnvelope.MessageTime()
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
		return netip.AddrPort{}
	}
	return r.outboundMessageEnvelope.LocalAddr()
}

func (r *OutboundRequestEnvelope) SetLocalAddr(addr netip.AddrPort) {
	if r == nil {
		return
	}

	r.outboundMessageEnvelope.SetLocalAddr(netutil.UnmapAddrPort(addr))
}

func (r *OutboundRequestEnvelope) RemoteAddr() netip.AddrPort {
	if r == nil {
		return netip.AddrPort{}
	}
	return r.outboundMessageEnvelope.RemoteAddr()
}

func (r *OutboundRequestEnvelope) SetRemoteAddr(addr netip.AddrPort) {
	if r == nil {
		return
	}

	r.outboundMessageEnvelope.SetRemoteAddr(netutil.UnmapAddrPort(addr))
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

	return r.msg.Method
}

func (r *OutboundRequestEnvelope) URI() URI {
	if r == nil {
		return nil
	}

	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	return r.msg.URI.Clone()
}

func (r *OutboundRequestEnvelope) RenderTo(w io.Writer, opts *RenderOptions) (int, error) {
	if r == nil {
		return 0, nil
	}
	return errors.Wrap2(r.outboundMessageEnvelope.RenderTo(w, opts))
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

	return r.msg.String()
}

func (r *OutboundRequestEnvelope) Format(f fmt.State, verb rune) {
	if r == nil {
		f.Write(bNilTag) //nolint:errcheck
		return
	}

	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	r.msg.Format(f, verb)
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
		return errors.NewInvalidArgumentErrorWrap("nil envelope")
	}
	return errors.Wrap(r.outboundMessageEnvelope.Validate())
}

func (r *OutboundRequestEnvelope) MarshalJSON() ([]byte, error) {
	if r == nil {
		return jsonNull, nil
	}
	return errors.Wrap2(r.outboundMessageEnvelope.MarshalJSON())
}

func (r *OutboundRequestEnvelope) UnmarshalJSON(data []byte) error {
	if r == nil {
		return errors.NewInvalidArgumentErrorWrap("nil envelope")
	}

	if r.outboundMessageEnvelope == nil {
		r.outboundMessageEnvelope = new(outboundMessageEnvelope[*Request])
	}

	r.msgMu.Lock()
	defer r.msgMu.Unlock()

	if err := r.unmarshalUnsafe(data); err != nil {
		*r = OutboundRequestEnvelope{}
		return errors.Wrap(err)
	}

	return nil
}

func (r *OutboundRequestEnvelope) LogValue() slog.Value {
	if r == nil {
		return slog.Value{}
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
	return errors.Wrap(fn(ctx, req))
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
	return errors.Wrap(fn(ctx, req, opts))
}

// SendRequestOptions are options for sending a request.
type SendRequestOptions struct {
	// Timeout is the timeout for the request sending process.
	// If zero, the default timeout [MessageWriteTimeout] is used.
	Timeout time.Duration `json:"timeout,omitempty"`
	// RenderCompact is the flag that indicates whether the message should be rendered in compact form.
	// See [RenderOptions] for more details.
	RenderCompact bool `json:"render_compact,omitempty"`
	// TODO: options for multicast
}

func (o *SendRequestOptions) timeout() time.Duration {
	if o == nil || o.Timeout == 0 {
		return ConnWriteTimeout
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
