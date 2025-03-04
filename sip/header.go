package sip

import (
	"encoding/json"
	"io"
	"iter"
	"slices"
	"strings"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/types"
)

// Header represents a generic SIP header.
// See [header.Header].
type Header = header.Header

// HeaderName represents a SIP header name.
// See [header.Name].
type HeaderName = header.Name

// HeaderParser represents a custom SIP header parser.
// See [header.Parser].
type HeaderParser = header.Parser

// ParseHeader parses a generic SIP header.
// See [header.Parse].
func ParseHeader[T ~string | ~[]byte](s T) (Header, error) {
	return errtrace.Wrap2(header.Parse(s))
}

// HeaderFromJSON parses a generic SIP header from JSON.
// See [header.FromJSON].
func HeaderFromJSON[T ~string | ~[]byte](b T) (Header, error) {
	return errtrace.Wrap2(header.FromJSON(b))
}

// HeaderToJSON serializes a generic SIP header to JSON.
// See [header.ToJSON].
func HeaderToJSON(h Header) ([]byte, error) {
	return errtrace.Wrap2(header.ToJSON(h))
}

// CanonicHeaderName returns a canonicalized header name.
// See [header.CanonicName].
func CanonicHeaderName[T ~string](name T) HeaderName { return header.CanonicName(name) }

// Headers maps string header name to a list of headers.
// The keys in the map are canonical header names.
type Headers map[header.Name][]Header

// All returns all headers as slice sorted by the canonical order.
func (hdrs Headers) All() []Header { return sortHdrs(hdrs) }

// Get returns all headers with the given name.
func (hdrs Headers) Get(name HeaderName) []Header { return hdrs[name.ToCanonic()] }

// Set replaces all headers with the given name(s) with the provided header(s).
func (hdrs Headers) Set(hdr Header, hds ...Header) Headers {
	hdrs[hdr.CanonicName()] = []Header{hdr}
	for _, h := range hds {
		hdrs[h.CanonicName()] = []Header{h}
	}
	return hdrs
}

// Append appends the given header(s) to the existing headers.
func (hdrs Headers) Append(hdr Header, hds ...Header) Headers {
	n := hdr.CanonicName()
	hdrs[n] = append(hdrs[n], hdr)
	for _, h := range hds {
		n = h.CanonicName()
		hdrs[n] = append(hdrs[n], h)
	}
	return hdrs
}

// Prepend prepends the given header(s) to the existing headers.
func (hdrs Headers) Prepend(hdr Header, hds ...Header) Headers {
	n := hdr.CanonicName()
	hdrs[n] = append([]Header{hdr}, hdrs[n]...)
	for _, h := range hds {
		n = h.CanonicName()
		hdrs[n] = append([]Header{h}, hdrs[n]...)
	}
	return hdrs
}

// Del deletes all headers with the given name(s).
func (hdrs Headers) Del(name HeaderName, names ...HeaderName) Headers {
	delete(hdrs, name.ToCanonic())
	for _, n := range names {
		delete(hdrs, n.ToCanonic())
	}
	return hdrs
}

// PopFirst removes and returns the first header with the given name.
// It returns false if no such header exists.
func (hdrs Headers) PopFirst(name HeaderName) (Header, bool) {
	name = name.ToCanonic()
	hs, ok := hdrs[name]
	if !ok || len(hs) == 0 {
		return nil, false
	}
	h := hs[0]
	if len(hs[1:]) == 0 {
		delete(hdrs, name)
	} else {
		copy(hs, hs[1:])
		clear(hs[len(hs)-1:])
		hdrs[name] = hs[:len(hs)-1]
	}
	return h, true
}

// PopLast removes and returns the last header with the given name.
// It returns false if no such header exists.
func (hdrs Headers) PopLast(name HeaderName) (Header, bool) {
	name = name.ToCanonic()
	hs, ok := hdrs[name]
	if !ok || len(hs) == 0 {
		return nil, false
	}
	h := hs[len(hs)-1]
	if len(hs[:len(hs)-1]) == 0 {
		delete(hdrs, name)
	} else {
		clear(hs[len(hs)-1:])
		hdrs[name] = hs[:len(hs)-1]
	}
	return h, true
}

// Has returns whether there is at least one header with the given name.
func (hdrs Headers) Has(name HeaderName) bool {
	_, ok := hdrs[name.ToCanonic()]
	return ok
}

// Clear removes all headers.
func (hdrs Headers) Clear() Headers {
	clear(hdrs)
	return hdrs
}

// Clone returns a deep copy of the headers.
func (hdrs Headers) Clone() Headers {
	var hdrs2 Headers
	for n, hs := range hdrs {
		if hdrs2 == nil {
			hdrs2 = make(Headers, len(hdrs))
		}
		hdrs2[n] = make([]Header, len(hs))
		for i := range hs {
			hdrs2[n][i] = types.Clone[Header](hs[i])
		}
	}
	return hdrs2
}

// CopyFrom copies headers with the given name(s) from another Headers map.
func (hdrs Headers) CopyFrom(other Headers, name HeaderName, names ...HeaderName) Headers {
	copyHdrs(hdrs, other, name)
	for _, n := range names {
		copyHdrs(hdrs, other, n)
	}
	return hdrs
}

func (hdrs *Headers) UnmarshalJSON(data []byte) error {
	var hdrsData map[string][]json.RawMessage
	if err := json.Unmarshal(data, &hdrsData); err != nil {
		return errtrace.Wrap(err)
	}

	for _, hds := range hdrsData {
		for _, hd := range hds {
			hdr, err := HeaderFromJSON(hd)
			if err != nil {
				return errtrace.Wrap(err)
			}
			if *hdrs == nil {
				*hdrs = make(Headers)
			}
			hdrs.Append(hdr)
		}
	}
	return nil
}

// AcceptEncoding returns an iterator over all Accept-Encoding header elements.
func (hdrs Headers) AcceptEncoding() iter.Seq[*header.EncodingRange] {
	return AllHeaderElems[header.AcceptEncoding](hdrs, "Accept-Encoding")
}

// AcceptLanguage returns an iterator over all Accept-Language header elements.
func (hdrs Headers) AcceptLanguage() iter.Seq[*header.LanguageRange] {
	return AllHeaderElems[header.AcceptLanguage](hdrs, "Accept-Language")
}

// Accept returns an iterator over all Accept header elements.
func (hdrs Headers) Accept() iter.Seq[*header.MIMERange] {
	return AllHeaderElems[header.Accept](hdrs, "Accept")
}

// AlertInfo returns an iterator over all Alert-Info header elements.
func (hdrs Headers) AlertInfo() iter.Seq[*header.AlertInfoAddr] {
	return AllHeaderElems[header.AlertInfo](hdrs, "Alert-Info")
}

// Allow returns an iterator over all Allow header elements.
func (hdrs Headers) Allow() iter.Seq[*header.RequestMethod] {
	return AllHeaderElems[header.Allow](hdrs, "Allow")
}

// AuthenticationInfo returns the first Authentication-Info header.
func (hdrs Headers) AuthenticationInfo() (*header.AuthenticationInfo, bool) {
	return FirstHeader[*header.AuthenticationInfo](hdrs, "Authentication-Info")
}

// Authorization returns an iterator over all Authorization headers.
func (hdrs Headers) Authorization() iter.Seq[*header.Authorization] {
	return func(yield func(*header.Authorization) bool) {
		for _, hdr := range hdrs.Get("Authorization") {
			if h, ok := hdr.(*header.Authorization); ok {
				if !yield(h) {
					return
				}
			}
		}
	}
}

// CallID returns the first Call-ID header.
func (hdrs Headers) CallID() (header.CallID, bool) {
	return FirstHeader[header.CallID](hdrs, "Call-ID")
}

// CallInfo returns an iterator over all Call-Info header elements.
func (hdrs Headers) CallInfo() iter.Seq[*header.CallInfoAddr] {
	return AllHeaderElems[header.CallInfo](hdrs, "Call-Info")
}

// Contact returns an iterator over all Contact header elements.
func (hdrs Headers) Contact() iter.Seq[*header.ContactAddr] {
	return AllHeaderElems[header.Contact](hdrs, "Contact")
}

// ContentDisposition returns the first Content-Disposition header.
func (hdrs Headers) ContentDisposition() (*header.ContentDisposition, bool) {
	return FirstHeader[*header.ContentDisposition](hdrs, "Content-Disposition")
}

// ContentEncoding returns an iterator over all Content-Encoding header elements.
func (hdrs Headers) ContentEncoding() iter.Seq[*header.Encoding] {
	return AllHeaderElems[header.ContentEncoding](hdrs, "Content-Encoding")
}

// ContentLanguage returns an iterator over all Content-Language header elements.
func (hdrs Headers) ContentLanguage() iter.Seq[*header.Language] {
	return AllHeaderElems[header.ContentLanguage](hdrs, "Content-Language")
}

// ContentLength returns the first Content-Length header.
func (hdrs Headers) ContentLength() (header.ContentLength, bool) {
	return FirstHeader[header.ContentLength](hdrs, "Content-Length")
}

// ContentType returns the first Content-Type header.
func (hdrs Headers) ContentType() (*header.ContentType, bool) {
	return FirstHeader[*header.ContentType](hdrs, "Content-Type")
}

// CSeq returns the first CSeq header.
func (hdrs Headers) CSeq() (*header.CSeq, bool) {
	return FirstHeader[*header.CSeq](hdrs, "CSeq")
}

// Date returns the first Date header.
func (hdrs Headers) Date() (*header.Date, bool) {
	return FirstHeader[*header.Date](hdrs, "Date")
}

// ErrorInfo returns an iterator over all Error-Info header elements.
func (hdrs Headers) ErrorInfo() iter.Seq[*header.ErrorInfoAddr] {
	return AllHeaderElems[header.ErrorInfo](hdrs, "Error-Info")
}

// Expires returns the first Expires header.
func (hdrs Headers) Expires() (*header.Expires, bool) {
	return FirstHeader[*header.Expires](hdrs, "Expires")
}

// From returns the first From header.
func (hdrs Headers) From() (*header.From, bool) {
	return FirstHeader[*header.From](hdrs, "From")
}

// InReplyTo returns an iterator over all In-Reply-To header elements.
func (hdrs Headers) InReplyTo() iter.Seq[*header.CallID] {
	return AllHeaderElems[header.InReplyTo](hdrs, "In-Reply-To")
}

// MaxForwards returns the first Max-Forwards header.
func (hdrs Headers) MaxForwards() (header.MaxForwards, bool) {
	return FirstHeader[header.MaxForwards](hdrs, "Max-Forwards")
}

// MIMEVersion returns the first MIME-Version header.
func (hdrs Headers) MIMEVersion() (header.MIMEVersion, bool) {
	return FirstHeader[header.MIMEVersion](hdrs, "MIME-Version")
}

// MinExpires returns the first Min-Expires header.
func (hdrs Headers) MinExpires() (*header.MinExpires, bool) {
	return FirstHeader[*header.MinExpires](hdrs, "Min-Expires")
}

// Organization returns the first Organization header.
func (hdrs Headers) Organization() (header.Organization, bool) {
	return FirstHeader[header.Organization](hdrs, "Organization")
}

// Priority returns the first Priority header.
func (hdrs Headers) Priority() (header.Priority, bool) {
	return FirstHeader[header.Priority](hdrs, "Priority")
}

// ProxyAuthenticate returns an iterator over all Proxy-Authenticate header elements.
func (hdrs Headers) ProxyAuthenticate() iter.Seq[*header.ProxyAuthenticate] {
	return func(yield func(*header.ProxyAuthenticate) bool) {
		for _, hdr := range hdrs.Get("Proxy-Authenticate") {
			if h, ok := hdr.(*header.ProxyAuthenticate); ok {
				if !yield(h) {
					return
				}
			}
		}
	}
}

// ProxyAuthorization returns an iterator over all Proxy-Authorization header elements.
func (hdrs Headers) ProxyAuthorization() iter.Seq[*header.ProxyAuthorization] {
	return func(yield func(*header.ProxyAuthorization) bool) {
		for _, hdr := range hdrs.Get("Proxy-Authorization") {
			if h, ok := hdr.(*header.ProxyAuthorization); ok {
				if !yield(h) {
					return
				}
			}
		}
	}
}

// ProxyRequire returns an iterator over all Proxy-Require header elements.
func (hdrs Headers) ProxyRequire() iter.Seq[*header.Option] {
	return AllHeaderElems[header.ProxyRequire](hdrs, "Proxy-Require")
}

// RecordRoute returns an iterator over all Record-Route header elements.
func (hdrs Headers) RecordRoute() iter.Seq[*header.RouteHop] {
	return AllHeaderElems[header.RecordRoute](hdrs, "Record-Route")
}

// ReplyTo returns the first Reply-To header.
func (hdrs Headers) ReplyTo() (*header.ReplyTo, bool) {
	return FirstHeader[*header.ReplyTo](hdrs, "Reply-To")
}

// Require returns an iterator over all Require header elements.
func (hdrs Headers) Require() iter.Seq[*header.Option] {
	return AllHeaderElems[header.Require](hdrs, "Require")
}

// RetryAfter returns the first Retry-After header.
func (hdrs Headers) RetryAfter() (*header.RetryAfter, bool) {
	return FirstHeader[*header.RetryAfter](hdrs, "Retry-After")
}

// Route returns an iterator over all Route header elements.
func (hdrs Headers) Route() iter.Seq[*header.RouteHop] {
	return AllHeaderElems[header.Route](hdrs, "Route")
}

// Server returns the first Server header.
func (hdrs Headers) Server() (header.Server, bool) {
	return FirstHeader[header.Server](hdrs, "Server")
}

// Subject returns the first Subject header.
func (hdrs Headers) Subject() (header.Subject, bool) {
	return FirstHeader[header.Subject](hdrs, "Subject")
}

// Supported returns an iterator over all Supported header elements.
func (hdrs Headers) Supported() iter.Seq[*header.Option] {
	return AllHeaderElems[header.Supported](hdrs, "Supported")
}

// Timestamp returns the first Timestamp header.
func (hdrs Headers) Timestamp() (*header.Timestamp, bool) {
	return FirstHeader[*header.Timestamp](hdrs, "Timestamp")
}

// To returns the first To header.
func (hdrs Headers) To() (*header.To, bool) {
	return FirstHeader[*header.To](hdrs, "To")
}

// Unsupported returns an iterator over all Unsupported header elements.
func (hdrs Headers) Unsupported() iter.Seq[*header.Option] {
	return AllHeaderElems[header.Unsupported](hdrs, "Unsupported")
}

// UserAgent returns the first User-Agent header.
func (hdrs Headers) UserAgent() (header.UserAgent, bool) {
	return FirstHeader[header.UserAgent](hdrs, "User-Agent")
}

// Via returns an iterator over all Via header elements.
func (hdrs Headers) Via() iter.Seq[*header.ViaHop] {
	return AllHeaderElems[header.Via](hdrs, "Via")
}

func (hdrs Headers) FirstVia() (*header.ViaHop, bool) {
	return FirstHeaderElem[header.Via](hdrs, "Via")
}

// PopFirstVia removes and returns the first Via header element.
// It returns false if no such element exists.
func (hdrs Headers) PopFirstVia() (*header.ViaHop, bool) {
	return PopFirstHeaderElem[header.Via](hdrs, "Via")
}

// Warning returns an iterator over all Warning header elements.
func (hdrs Headers) Warning() iter.Seq[*header.WarningEntry] {
	return AllHeaderElems[header.Warning](hdrs, "Warning")
}

// WWWAuthenticate returns an iterator over all WWW-Authenticate header elements.
func (hdrs Headers) WWWAuthenticate() iter.Seq[*header.WWWAuthenticate] {
	return func(yield func(*header.WWWAuthenticate) bool) {
		for _, hdr := range hdrs.Get("WWW-Authenticate") {
			if h, ok := hdr.(*header.WWWAuthenticate); ok {
				if !yield(h) {
					return
				}
			}
		}
	}
}

func copyHdrs(dst, src Headers, name HeaderName) {
	for _, hdr := range src.Get(name) {
		dst.Append(types.Clone[Header](hdr))
	}
}

func validateHdrs(hdrs Headers) error {
	if len(hdrs) == 0 {
		return errtrace.Wrap(newMissHdrErr(""))
	}

	errs := make([]error, 0, len(hdrs))
	for n, hs := range hdrs {
		for i := range hs {
			if hs[i] == nil || !hs[i].IsValid() {
				errs = append(errs, errorutil.Errorf("invalid header %q", n))
			}
		}
	}
	return errtrace.Wrap(errorutil.JoinPrefix("invalid headers:", errs...))
}

func compareHdrs(hdrs, other Headers) bool {
	if len(hdrs) != len(other) {
		return false
	}
	for k, hs1 := range hdrs {
		if !other.Has(k) {
			return false
		}
		hs2 := other.Get(k)
		if len(hs1) != len(hs2) {
			return false
		}
		for i := range hs1 {
			if !types.IsEqual(hs1[i], hs2[i]) {
				return false
			}
		}
	}
	return true
}

var hdrsOrder = []HeaderName{
	"Route",
	"Record-Route",
	"Via",
	"From",
	"To",
	"Call-ID",
	"CSeq",
	"Contact",
	"Max-Forwards",
	"Authorization",
	"Proxy-Authorization",
	"WWW-Authenticate",
	"Proxy-Authenticate",
	"Expires",
	"Allow",
	"Accept",
	"Accept-Encoding",
	"Accept-Language",
	"Require",
	"Proxy-Require",
	"Supported",
	"Unsupported",
	"Timestamp",
	"Date",
	"Subject",
	"Min-SE",
	"Session-Expires",
	"Refer-To",
	"In-Reply-To",
	"User-Agent",
	"Server",
	"Content-Type",
	"Content-Length",
}

func renderHdrs(w io.Writer, hdrs Headers, opts *RenderOptions) (num int, err error) {
	if len(hdrs) == 0 {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	for _, h := range sortHdrs(hdrs) {
		cw.Call(func(w io.Writer) (int, error) {
			return errtrace.Wrap2(h.RenderTo(w, opts))
		})
		cw.Fprint("\r\n")
	}
	return errtrace.Wrap2(cw.Result())
}

func sortHdrs(hdrs Headers) []Header {
	var hds []Header
	for _, hs := range hdrs {
		hds = append(hds, hs...)
	}
	slices.SortStableFunc(hds, func(h1, h2 Header) int {
		n1, n2 := h1.CanonicName(), h2.CanonicName()
		i1, i2 := slices.Index(hdrsOrder, n1), slices.Index(hdrsOrder, n2)
		switch {
		case i1 == -1 && i2 == -1:
			return strings.Compare(string(n1), string(n2))
		case i1 == -1:
			return 1
		case i2 == -1:
			return -1
		default:
			return i1 - i2
		}
	})
	return hds
}

// FirstHeader returns the first header of the given name.
func FirstHeader[H Header](hdrs Headers, name HeaderName) (hdr H, ok bool) {
	hs := hdrs.Get(name)
	if len(hs) == 0 {
		return hdr, false
	}
	h, ok := hs[0].(H)
	return h, ok
}

// FirstHeaderElem returns the first element of the first header of the given name.
func FirstHeaderElem[H ~[]E, E any](hdrs Headers, name HeaderName) (el *E, ok bool) {
	hdr, ok := FirstHeader[Header](hdrs, name)
	if !ok {
		return nil, false
	}
	es, ok := hdr.(H)
	if !ok || len(es) == 0 {
		return nil, false
	}
	return &es[0], true
}

// LastHeader returns the last header of the given name.
func LastHeader[H Header](hdrs Headers, name HeaderName) (hdr H, ok bool) {
	hs := hdrs.Get(name)
	if len(hs) == 0 {
		return hdr, false
	}
	h, ok := hs[len(hs)-1].(H)
	return h, ok
}

// LastHeaderElem returns the last element of the last header of the given name.
func LastHeaderElem[H ~[]E, E any](hdrs Headers, name HeaderName) (el *E, ok bool) {
	hdr, ok := LastHeader[Header](hdrs, name)
	if !ok {
		return nil, false
	}
	es, ok := hdr.(H)
	if !ok || len(es) == 0 {
		return nil, false
	}
	return &es[len(es)-1], true
}

// AllHeaderElems returns all elements of all headers of the given name.
func AllHeaderElems[H ~[]E, E any](hdrs Headers, name HeaderName) iter.Seq[*E] {
	return func(yield func(*E) bool) {
	loop:
		for _, hdr := range hdrs.Get(name) {
			if h, ok := hdr.(H); ok {
				for i := range h {
					if !yield(&h[i]) {
						break loop
					}
				}
			}
		}
	}
}

// PopFirstHeaderElem returns the first element of the first header of the given name
// and removes it from the headers.
func PopFirstHeaderElem[H ~[]E, E any](hdrs Headers, name HeaderName) (*E, bool) {
	hdr, ok := FirstHeader[Header](hdrs, name)
	if !ok {
		return nil, false
	}
	es, ok := hdr.(H)
	if !ok || len(es) == 0 {
		return nil, false
	}
	el := es[0]
	if len(es[1:]) == 0 {
		hdrs.PopFirst(name)
	} else {
		copy(es, es[1:])
		clear(es[len(es)-1:])
		hdrs[name][0] = any(es[:len(es)-1]).(Header) //nolint:forcetypeassert
	}
	return &el, true
}

// PopLastHeaderElem returns the last element of the last header of the given name
// and removes it from the headers.
func PopLastHeaderElem[H ~[]E, E any](hdrs Headers, name HeaderName) (*E, bool) {
	hdr, ok := LastHeader[Header](hdrs, name)
	if !ok {
		return nil, false
	}
	es, ok := hdr.(H)
	if !ok || len(es) == 0 {
		return nil, false
	}
	el := es[len(es)-1]
	if len(es[:len(es)-1]) == 0 {
		hdrs.PopLast(name)
	} else {
		clear(es[len(es)-1:])
		hdrs[name][len(hdrs[name])-1] = any(es[:len(es)-1]).(Header) //nolint:forcetypeassert
	}
	return &el, true
}
