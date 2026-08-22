package sip

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/ioutil"
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
	URI     AnyURI        `json:"uri"`
	Proto   ProtoInfo     `json:"proto"`
	Headers Headers       `json:"headers,omitempty"`
	Body    []byte        `json:"body,omitempty"`
}

var _ Message = (*Request)(nil)

// RenderTo renders the SIP request to the given writer.
func (req *Request) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if req == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Call(func(w io.Writer) (int, error) {
		return errors.Wrap2(req.renderStartLine(w, opts...))
	})
	cw.Fprint("\r\n")
	cw.Call(func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrs(w, req.Headers, opts...))
	})
	cw.Fprint("\r\n")
	cw.Write(req.Body)
	return errors.Wrap2(cw.Result())
}

func (req *Request) renderStartLine(w io.Writer, opts ...RenderOptions) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(req.Method, " ")
	if req.URI != nil {
		cw.Call(func(w io.Writer) (int, error) {
			return errors.Wrap2(req.URI.RenderTo(w, opts...))
		})
	}
	cw.Fprint(" ", req.Proto)
	return errors.Wrap2(cw.Result())
}

// Render renders the SIP request to a string.
func (req *Request) Render(opts ...RenderOptions) string {
	if req == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	_, _ = req.RenderTo(sb, opts...)
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
	_, _ = req.renderStartLine(sb)
	return sb.String()
}

// Format implements [fmt.Formatter] for custom formatting.
func (req *Request) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			_, _ = req.RenderTo(f)
			return
		}
		f.Write([]byte(req.String()))
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(req.Render()))
			return
		}
		f.Write([]byte(strconv.Quote(req.String())))
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

	attrs := append(make([]slog.Attr, 0, 7),
		slog.Any("method", req.Method),
		slog.Any("uri", req.URI),
	)
	if hop, ok := util.SeqFirst(req.Headers.Vias()); ok {
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

	clone := *req
	clone.URI = types.Clone[AnyURI](req.URI)
	clone.Headers = req.Headers.Clone()
	clone.Body = slices.Clone(req.Body)
	return &clone
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
func (req *Request) IsValid() bool { return req.Validate() == nil }

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
		return errors.ErrorWrap("nil request")
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
	if len(errs) == 0 {
		return nil
	}
	return errors.Wrap(newInvalidMsgErr(errors.Join(errs...)))
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
		return errors.Wrap(err)
	}

	req.Method = reqData.Method
	req.Proto = reqData.Proto
	req.Headers = reqData.Headers
	req.Body = reqData.Body
	if u, err := ParseAnyURI(reqData.URI); err == nil {
		req.URI = u
	} else {
		req.URI = nil
	}
	return nil
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

func (o RequestOptions) transp() TransportProto {
	if o.Transport == "" {
		return udpMeta.Proto
	}
	return o.Transport
}

func (o RequestOptions) branch() string {
	if !IsRFC3261Branch(o.Branch) {
		return GenerateBranch(0)
	}
	return o.Branch
}

func (o RequestOptions) locTag() string {
	if o.LocalTag == "" {
		return GenerateTag(0)
	}
	return o.LocalTag
}

func (o RequestOptions) callID() string {
	if o.CallID == "" {
		return GenerateCallID(0, "")
	}
	return o.CallID
}

func (o RequestOptions) seqNum() uint {
	if o.SeqNum == 0 {
		return 1
	}
	return o.SeqNum
}

func (o RequestOptions) maxFwd() uint {
	if o.MaxForwards == 0 {
		return uint(DefaultMaxForwards)
	}
	return o.MaxForwards
}

// NewRequest is a helper function to create a minimally valid SIP request.
// The returned request has all mandatory headers and parameters set following the rules of RFC 3261 Section 8.1.1.
func NewRequest(mtd RequestMethod, ruri, furi, turi AnyURI, opts ...RequestOptions) (*Request, error) {
	if !mtd.IsValid() {
		return nil, errors.ErrorfWrap("invalid request method %q", mtd)
	}
	if !ruri.IsValid() {
		return nil, errors.ErrorfWrap("invalid request URI %q", ruri)
	}
	if !furi.IsValid() {
		return nil, errors.ErrorfWrap("invalid from URI %q", furi)
	}
	if !turi.IsValid() {
		return nil, errors.ErrorfWrap("invalid to URI %q", turi)
	}

	reqOpts := util.LastSliceElemOr(opts, RequestOptions{})

	if u, ok := ruri.(*URI); ok {
		ruri = cleanReqURI(u)
	}

	via := header.ViaHop{
		Proto:     protoVer20,
		Transport: reqOpts.transp(),
		Addr:      AddrFromHost(util.RandString(8) + ".invalid"), // will be replaced by the transport
		Params:    make(Values).Set("branch", reqOpts.branch()),
	}
	if reqOpts.RPort {
		via.Params.Set("rport", "")
	}

	toHdr := &header.To{URI: turi}
	if reqOpts.RemoteTag != "" {
		toHdr.Params = make(Values).Set("tag", reqOpts.RemoteTag)
	}

	req := &Request{
		Method: mtd,
		URI:    ruri,
		Proto:  protoVer20,
		Headers: make(Headers).
			Set(header.Via{via}).
			Set(&header.From{
				URI:    furi,
				Params: make(Values).Set("tag", reqOpts.locTag()),
			}).
			Set(toHdr).
			Set(header.CallID(reqOpts.callID())).
			Set(&header.CSeq{Method: mtd, SeqNum: reqOpts.seqNum()}).
			Set(header.MaxForwards(reqOpts.maxFwd())),
		Body: reqOpts.Body,
	}
	for n, hs := range reqOpts.Headers {
		if _, ok := reqMandatoryHdrs[n]; ok || len(hs) == 0 {
			continue
		}
		req.Headers.Append(hs[0], hs[1:]...)
	}
	EnsureMessageContentLength(req)
	return req, nil
}

func EnsureRequestVia(req *Request, tp TransportProto, addr Addr) {
	via, ok := req.Headers.FirstVia()
	if !ok {
		req.Headers.PrependVia(header.ViaHop{Proto: protoVer20})
		via, _ = req.Headers.FirstVia()
	}

	if tp.IsValid() {
		via.Transport = tp
	} else if !via.Transport.IsValid() {
		via.Transport = udpMeta.Proto
	}

	if addr.IsValid() {
		via.Addr = addr
	} else if !via.Addr.IsValid() {
		via.Addr = AddrFromHost(util.RandString(8) + ".invalid")
	}

	if b, ok := via.Branch(); !ok || !IsRFC3261Branch(b) {
		if via.Params == nil {
			via.Params = make(Values)
		}
		via.Params.Set("branch", GenerateBranch(0))
	}
}

func EnsureRequestViaMulticast(req *Request, addr netip.Addr) {
	addr = addr.Unmap()
	if !addr.IsMulticast() {
		return
	}

	via, ok := req.Headers.FirstVia()
	if !ok {
		return
	}

	via.Params.Set("maddr", addr.String())
	if addr.Is4() {
		via.Params.Set("ttl", "1")
	}
}

func EnsureRequestFromTag(req *Request) {
	from, ok := req.Headers.From()
	if !ok || from == nil {
		return
	}
	if t, ok := from.Tag(); ok && t != "" {
		return
	}

	if from.Params == nil {
		from.Params = make(Values)
	}
	from.Params.Set("tag", GenerateTag(0))
}

func EnsureRequestMaxForwards(req *Request) {
	if h, ok := req.Headers.MaxForwards(); ok && h > 0 {
		return
	}
	req.Headers.Set(DefaultMaxForwards)
}

func NewCancelRequest(inv *Request) (*Request, error) {
	if err := inv.Validate(); err != nil {
		return nil, errors.Wrap(err)
	}
	if !inv.Method.Equal(RequestMethodInvite) {
		return nil, errors.Wrap(ErrMethodNotAllowed)
	}

	cnc := &Request{
		Method:  RequestMethodCancel,
		Proto:   protoVer20,
		URI:     inv.URI.Clone(),
		Headers: make(Headers),
	}

	hop, _ := inv.Headers.FirstVia()
	cnc.Headers.Set(header.Via{hop.Clone()})

	from, _ := inv.Headers.From()
	cnc.Headers.Set(from.Clone())

	to, _ := inv.Headers.To()
	cnc.Headers.Set(to.Clone())

	cid, _ := inv.Headers.CallID()
	cnc.Headers.Set(cid.Clone())

	cseq, _ := inv.Headers.CSeq()
	cseq = cseq.Clone().(*header.CSeq) //nolint:forcetypeassert
	cseq.Method = RequestMethodCancel
	cnc.Headers.Set(cseq)

	if routes := inv.Headers.Get("Route"); len(routes) > 0 {
		for _, h := range routes {
			cnc.Headers.Append(h.Clone())
		}
	}

	EnsureRequestMaxForwards(cnc)
	EnsureMessageContentLength(cnc)
	return cnc, nil
}

type RequestEnvelope struct {
	*MessageEnvelope[*Request]
}

func NewRequestEnvelope(res *Request) *RequestEnvelope {
	return &RequestEnvelope{NewMessageEnvelope(res)}
}

func (r *RequestEnvelope) WithMessage(fn func(*Request)) *RequestEnvelope {
	r.MessageEnvelope.WithMessage(fn)
	return r
}

func (r *RequestEnvelope) SetTransport(tp TransportMetadata) *RequestEnvelope {
	r.MessageEnvelope.SetTransport(tp)
	return r
}

func (r *RequestEnvelope) SetLocalAddr(addr netip.AddrPort) *RequestEnvelope {
	r.MessageEnvelope.SetLocalAddr(addr)
	return r
}

func (r *RequestEnvelope) SetRemoteAddr(addr netip.AddrPort) *RequestEnvelope {
	r.MessageEnvelope.SetRemoteAddr(addr)
	return r
}

func (r *RequestEnvelope) UpdateFrom(other *RequestEnvelope) *RequestEnvelope {
	r.MessageEnvelope.UpdateFrom(other.MessageEnvelope)
	return r
}

func (r *RequestEnvelope) Method() RequestMethod {
	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	return r.msg.Method
}

func (r *RequestEnvelope) URI() AnyURI {
	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	return r.msg.URI.Clone()
}

const reqTimeMetaKey = "sip.request_time"

func (r *RequestEnvelope) NewResponse(sts ResponseStatus, opts ...ResponseOptions) (*ResponseEnvelope, error) {
	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	res, err := r.msg.NewResponse(sts, opts...)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	resEnv := NewResponseEnvelope(res).
		SetTransport(r.transp()).
		SetLocalAddr(r.locAddr()).
		SetRemoteAddr(r.rmtAddr())
	resEnv.Metadata().
		Set(reqTimeMetaKey, r.msgTime)
	return resEnv, nil
}

func (r *RequestEnvelope) String() string {
	if r == nil {
		return sNilTag
	}

	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	return r.msg.String()
}

func (r *RequestEnvelope) Format(f fmt.State, verb rune) {
	if r == nil {
		f.Write(bNilTag)
		return
	}

	r.msgMu.RLock()
	defer r.msgMu.RUnlock()

	r.msg.Format(f, verb)
}

func (r *RequestEnvelope) Clone() Message {
	if r == nil {
		return nil
	}

	cloned := r.MessageEnvelope.Clone()
	if cloned == nil {
		return nil
	}
	return &RequestEnvelope{
		cloned.(*MessageEnvelope[*Request]), //nolint:forcetypeassert
	}
}

func (r *RequestEnvelope) Equal(val any) bool {
	var other *RequestEnvelope
	switch v := val.(type) {
	case RequestEnvelope:
		other = &v
	case *RequestEnvelope:
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

func (r *RequestEnvelope) UnmarshalJSON(data []byte) error {
	if r.MessageEnvelope == nil {
		r.MessageEnvelope = new(MessageEnvelope[*Request])
	}
	return errors.Wrap(r.MessageEnvelope.UnmarshalJSON(data))
}

// NewCancelRequestEnvelope creates a CANCEL request from an INVITE request.
// The CANCEL request preserves the original request's transport, local/remote addresses,
// and metadata while updating the method to CANCEL and setting the CSeq method to CANCEL.
// RFC 3261 Section 9.
func NewCancelRequestEnvelope(inv *RequestEnvelope) (*RequestEnvelope, error) {
	if err := inv.Validate(); err != nil {
		return nil, errors.Wrap(err)
	}

	var (
		cnc *Request
		err error
	)
	inv.WithMessage(func(r *Request) {
		cnc, err = NewCancelRequest(r)
	})
	if err != nil {
		return nil, errors.Wrap(err)
	}

	cncEnv := NewRequestEnvelope(cnc).
		SetTransport(inv.Transport()).
		SetLocalAddr(inv.LocalAddr()).
		SetRemoteAddr(inv.RemoteAddr())
	return cncEnv, nil
}
