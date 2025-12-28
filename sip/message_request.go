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

// IsKnownRequestMethod returns whether the method is a known SIP request method.
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
		return "<nil>"
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
		return slog.Value{}
	}

	attrs := make([]slog.Attr, 0, 7)
	attrs = append(attrs, slog.String("method", string(req.Method)), slog.Any("uri", req.URI))
	if hop, ok := util.IterFirst(req.Headers.Via()); ok {
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
	"Via":          true,
	"From":         true,
	"To":           true,
	"Call-ID":      true,
	"CSeq":         true,
	"Max-Forwards": true,
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
		return ""
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
	if to, ok := res.Headers.To(); sts != ResponseStatusTrying && ok && to != nil {
		locTag := opts.locTag()
		if locTag == "" {
			locTag = GenerateTag(0)
		}

		if to.Params == nil || !to.Params.Has("tag") {
			if to.Params == nil {
				to.Params = make(header.Values)
			}
			to.Params.Set("tag", locTag)
		}
	}

	// append additional headers
	for n, hs := range opts.headers() {
		if reqCopyHdrsMap[n] {
			continue
		}
		for _, h := range hs {
			res.Headers.Append(h)
		}
	}

	return res, nil
}

type InboundRequest struct {
	inboundMessage[*Request]
}

func NewInboundRequest(req *Request, laddr, raddr netip.AddrPort) *InboundRequest {
	return &InboundRequest{
		inboundMessage[*Request]{
			msg:     req,
			msgTime: time.Now(),
			locAddr: laddr,
			rmtAddr: raddr,
			data:    new(MessageMetadata),
		},
	}
}

func (r *InboundRequest) Method() RequestMethod {
	if r == nil {
		return ""
	}
	return r.msg.Method
}

func (r *InboundRequest) URI() URI {
	if r == nil {
		return nil
	}
	return r.msg.URI.Clone()
}

var reqTimeDataKey = "sip.request_time"

func (r *InboundRequest) NewResponse(sts ResponseStatus, opts *ResponseOptions) (*OutboundResponse, error) {
	if r == nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid request"))
	}
	msg, err := r.msg.NewResponse(sts, opts)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	res := NewOutboundResponse(msg)
	res.locAddr = r.locAddr
	res.rmtAddr = r.rmtAddr
	res.data.Set(reqTimeDataKey, r.msgTime)
	return res, nil
}

func (r *InboundRequest) RenderTo(w io.Writer, opts *RenderOptions) (int, error) {
	if r == nil {
		return 0, nil
	}
	return errtrace.Wrap2(r.msg.RenderTo(w, opts))
}

func (r *InboundRequest) Render(opts *RenderOptions) string {
	if r == nil {
		return ""
	}
	return r.msg.Render(opts)
}

func (r *InboundRequest) String() string {
	if r == nil {
		return "<nil>"
	}
	return r.msg.String()
}

func (r *InboundRequest) Format(f fmt.State, verb rune) {
	if r == nil {
		f.Write([]byte("<nil>"))
		return
	}
	r.msg.Format(f, verb)
}

func (r *InboundRequest) Clone() Message {
	if r == nil {
		return nil
	}
	return &InboundRequest{
		inboundMessage[*Request]{
			msg:     r.msg.Clone().(*Request), //nolint:forcetypeassert
			msgTime: time.Now(),
			locAddr: r.locAddr,
			rmtAddr: r.rmtAddr,
			data:    r.data.Clone(),
		},
	}
}

func (r *InboundRequest) Equal(v any) bool {
	if r == nil {
		return v == nil
	}
	if other, ok := v.(*InboundRequest); ok {
		return r.msg.Equal(other.msg)
	}
	return false
}

func (r *InboundRequest) IsValid() bool {
	if r == nil {
		return false
	}
	return r.msg.IsValid()
}

func (r *InboundRequest) Validate() error {
	if r == nil {
		return errtrace.Wrap(NewInvalidArgumentError("invalid request"))
	}
	return errtrace.Wrap(r.msg.Validate())
}

type OutboundRequest struct {
	outboundMessage[*Request]
}

func NewOutboundRequest(req *Request) *OutboundRequest {
	return &OutboundRequest{
		outboundMessage[*Request]{
			message: message[*Request]{
				msg:     req,
				msgTime: time.Now(),
				data:    new(MessageMetadata),
			},
		},
	}
}

func (r *OutboundRequest) Method() RequestMethod {
	if r == nil {
		return ""
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.msg.Method
}

func (r *OutboundRequest) URI() URI {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.msg.URI.Clone()
}

func (r *OutboundRequest) RenderTo(w io.Writer, opts *RenderOptions) (int, error) {
	if r == nil {
		return 0, nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return errtrace.Wrap2(r.msg.RenderTo(w, opts))
}

func (r *OutboundRequest) Render(opts *RenderOptions) string {
	if r == nil {
		return ""
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.msg.Render(opts)
}

func (r *OutboundRequest) String() string {
	if r == nil {
		return "<nil>"
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.msg.String()
}

func (r *OutboundRequest) Format(f fmt.State, verb rune) {
	if r == nil {
		f.Write([]byte("<nil>"))
		return
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	r.msg.Format(f, verb)
}

func (r *OutboundRequest) Clone() Message {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return &OutboundRequest{
		outboundMessage[*Request]{
			message: message[*Request]{
				msg:     r.msg.Clone().(*Request), //nolint:forcetypeassert
				msgTime: time.Now(),
				locAddr: r.locAddr,
				rmtAddr: r.rmtAddr,
				data:    r.data.Clone(),
			},
		},
	}
}

func (r *OutboundRequest) Equal(v any) bool {
	if r == nil {
		return v == nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	if other, ok := v.(*OutboundRequest); ok {
		return r.msg.Equal(other.msg)
	}
	return false
}

func (r *OutboundRequest) IsValid() bool {
	if r == nil {
		return false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.msg.IsValid()
}

func (r *OutboundRequest) Validate() error {
	if r == nil {
		return errtrace.Wrap(NewInvalidArgumentError("invalid request"))
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return errtrace.Wrap(r.msg.Validate())
}
