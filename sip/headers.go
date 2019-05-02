package sip

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/util"
)

// SIP Headers structs
// Originally forked from github.com/stefankopieczek/gossip by @StefanKopieczek
// with a tiny changes

// Whitespace recognised by SIP protocol.
const abnfWs = " \t"

// Header is a single SIP header.
type Header interface {
	// Name returns header name.
	Name() string
	// Clone returns copy of header struct.
	Clone() Header
	String() string
	Equals(other interface{}) bool
}

// A URI from any schema (e.g. sip:, tel:, callto:)
type Uri interface {
	// Determine if the two URIs are equal according to the rules in RFC 3261 s. 19.1.4.
	Equals(other interface{}) bool
	String() string
	Clone() Uri

	IsEncrypted() bool
	SetEncrypted(flag bool)
	User() MaybeString
	SetUser(user MaybeString)
	Password() MaybeString
	SetPassword(pass MaybeString)
	Host() string
	SetHost(host string)
	Port() *Port
	SetPort(port *Port)
	UriParams() Params
	SetUriParams(params Params)
	Headers() Params
	SetHeaders(params Params)
	// Return true if and only if the URI is the special wildcard URI '*'; that is, if it is
	// a WildcardUri struct.
	IsWildcard() bool
}

// A URI from a schema suitable for inclusion in a Contact: header.
// The only such URIs are sip/sips URIs and the special wildcard URI '*'.
// hold this interface to not break other code
type ContactUri interface {
	Uri
}

// Generic list of parameters on a header.
type Params interface {
	Get(key string) (MaybeString, bool)
	Add(key string, val MaybeString) Params
	Clone() Params
	Equals(params interface{}) bool
	ToString(sep uint8) string
	String() string
	Length() int
	Items() map[string]MaybeString
	Keys() []string
	Has(key string) bool
}

// IMPLEMENTATION

// Params implementation.
type headerParams struct {
	params     map[string]MaybeString
	paramOrder []string
}

// Create an empty set of parameters.
func NewParams() Params {
	return &headerParams{
		params:     make(map[string]MaybeString),
		paramOrder: []string{},
	}
}

// Returns the entire parameter map.
func (params *headerParams) Items() map[string]MaybeString {
	return params.params
}

// Returns a slice of keys, in order.
func (params *headerParams) Keys() []string {
	return params.paramOrder
}

// Returns the requested parameter value.
func (params *headerParams) Get(key string) (MaybeString, bool) {
	v, ok := params.params[key]
	return v, ok
}

// Put a new parameter.
func (params *headerParams) Add(key string, val MaybeString) Params {
	// Add param to order list if new.
	if _, ok := params.params[key]; !ok {
		params.paramOrder = append(params.paramOrder, key)
	}

	// Set param value.
	params.params[key] = val

	// Return the params so calls can be chained.
	return params
}

func (params *headerParams) Has(key string) bool {
	_, ok := params.params[key]

	return ok
}

// Copy a list of params.
func (params *headerParams) Clone() Params {
	dup := NewParams()
	for _, key := range params.Keys() {
		if val, ok := params.Get(key); ok {
			dup.Add(key, val)
		} else {
			log.Errorf("internal consistency error: key %v present in param.Keys() but failed to Get()", key)
			continue
		}
	}

	return dup
}

// Render params to a string.
// Note that this does not escape special characters, this should already have been done before calling this method.
func (params *headerParams) ToString(sep uint8) string {
	var buffer bytes.Buffer
	first := true

	for _, key := range params.Keys() {
		val, ok := params.Get(key)
		if !ok {
			log.Errorf("internal consistency error: key %v present in param.Keys() but failed to Get()", key)
			continue
		}

		if !first {
			buffer.WriteString(fmt.Sprintf("%c", sep))
		}
		first = false

		buffer.WriteString(fmt.Sprintf("%s", key))

		if val, ok := val.(String); ok {
			if strings.ContainsAny(val.String(), abnfWs) {
				buffer.WriteString(fmt.Sprintf("=\"%s\"", val.String()))
			} else {
				buffer.WriteString(fmt.Sprintf("=%s", val.String()))
			}
		}
	}

	return buffer.String()
}

// String returns params joined with '&' char.
func (params *headerParams) String() string {
	return params.ToString('&')
}

// Returns number of params.
func (params *headerParams) Length() int {
	return len(params.params)
}

// Check if two maps of parameters are equal in the sense of having the same keys with the same values.
// This does not rely on any ordering of the keys of the map in memory.
func (params *headerParams) Equals(other interface{}) bool {
	q, ok := other.(*headerParams)
	if !ok {
		return false
	}

	if params.Length() == 0 && q.Length() == 0 {
		return true
	}

	if params.Length() != q.Length() {
		return false
	}

	for key, pVal := range params.Items() {
		qVal, ok := q.Get(key)
		if !ok {
			return false
		}
		if pVal != qVal {
			return false
		}
	}

	return true
}

func cloneWithNil(params Params) Params {
	if params == nil {
		return NewParams()
	}
	return params.Clone()
}

// SipUri
// A SIP or SIPS URI, including all params and URI header params.
type SipUri struct {
	// True if and only if the URI is a SIPS URI.
	FIsEncrypted bool

	// The user part of the URI: the 'joe' in sip:joe@bloggs.com
	// This is a pointer, so that URIs without a user part can have 'nil'.
	FUser MaybeString

	// The password field of the URI. This is represented in the URI as joe:hunter2@bloggs.com.
	// Note that if a URI has a password field, it *must* have a user field as well.
	// This is a pointer, so that URIs without a password field can have 'nil'.
	// Note that RFC 3261 strongly recommends against the use of password fields in SIP URIs,
	// as they are fundamentally insecure.
	FPassword MaybeString

	// The host part of the URI. This can be a domain, or a string representation of an IP address.
	FHost string

	// The port part of the URI. This is optional, and so is represented here as a pointer type.
	FPort *Port

	// Any parameters associated with the URI.
	// These are used to provide information about requests that may be constructed from the URI.
	// (For more details, see RFC 3261 section 19.1.1).
	// These appear as a semicolon-separated list of key=value pairs following the host[:port] part.
	FUriParams Params

	// Any headers to be included on requests constructed from this URI.
	// These appear as a '&'-separated list at the end of the URI, introduced by '?'.
	// Although the values of the map are MaybeStrings, they will never be NoString in practice as the parser
	// guarantees to not return blank values for header elements in SIP URIs.
	// You should not set the values of headers to NoString.
	FHeaders Params
}

func (uri *SipUri) IsEncrypted() bool {
	return uri.FIsEncrypted
}

func (uri *SipUri) SetEncrypted(flag bool) {
	uri.FIsEncrypted = flag
}

func (uri *SipUri) User() MaybeString {
	return uri.FUser
}

func (uri *SipUri) SetUser(user MaybeString) {
	uri.FUser = user
}

func (uri *SipUri) Password() MaybeString {
	return uri.FPassword
}

func (uri *SipUri) SetPassword(pass MaybeString) {
	uri.FPassword = pass
}

func (uri *SipUri) Host() string {
	return uri.FHost
}

func (uri *SipUri) SetHost(host string) {
	uri.FHost = host
}

func (uri *SipUri) Port() *Port {
	return uri.FPort
}

func (uri *SipUri) SetPort(port *Port) {
	uri.FPort = port
}

func (uri *SipUri) UriParams() Params {
	return uri.FUriParams
}

func (uri *SipUri) SetUriParams(params Params) {
	uri.FUriParams = params
}

func (uri *SipUri) Headers() Params {
	return uri.FHeaders
}

func (uri *SipUri) SetHeaders(params Params) {
	uri.FHeaders = params
}

func (uri *SipUri) IsWildcard() bool {
	return false
}

// Determine if the SIP URI is equal to the specified URI according to the rules laid down in RFC 3261 s. 19.1.4.
// TODO: The Equals method is not currently RFC-compliant; fix this!
func (uri *SipUri) Equals(val interface{}) bool {
	otherPtr, ok := val.(*SipUri)
	if !ok {
		return false
	}

	other := *otherPtr
	result := uri.FIsEncrypted == other.FIsEncrypted &&
		uri.FUser == other.FUser &&
		uri.FPassword == other.FPassword &&
		uri.FHost == other.FHost &&
		util.Uint16PtrEq((*uint16)(uri.FPort), (*uint16)(other.FPort))

	if !result {
		return false
	}

	if !uri.FUriParams.Equals(other.FUriParams) {
		return false
	}

	if !uri.FHeaders.Equals(other.FHeaders) {
		return false
	}

	return true
}

// Generates the string representation of a SipUri struct.
func (uri *SipUri) String() string {
	var buffer bytes.Buffer

	// Compulsory protocol identifier.
	if uri.FIsEncrypted {
		buffer.WriteString("sips")
		buffer.WriteString(":")
	} else {
		buffer.WriteString("sip")
		buffer.WriteString(":")
	}

	// Optional userinfo part.
	if user, ok := uri.FUser.(String); ok && user.String() != "" {
		buffer.WriteString(uri.FUser.String())
		if pass, ok := uri.FPassword.(String); ok && pass.String() != "" {
			buffer.WriteString(":")
			buffer.WriteString(pass.String())
		}
		buffer.WriteString("@")
	}

	// Compulsory hostname.
	buffer.WriteString(uri.FHost)

	// Optional port number.
	if uri.FPort != nil {
		buffer.WriteString(fmt.Sprintf(":%d", *uri.FPort))
	}

	if (uri.FUriParams != nil) && uri.FUriParams.Length() > 0 {
		buffer.WriteString(";")
		buffer.WriteString(uri.FUriParams.ToString(';'))
	}

	if (uri.FHeaders != nil) && uri.FHeaders.Length() > 0 {
		buffer.WriteString("?")
		buffer.WriteString(uri.FHeaders.ToString('&'))
	}

	return buffer.String()
}

// Clone the Sip URI.
func (uri *SipUri) Clone() Uri {
	return &SipUri{
		FIsEncrypted: uri.FIsEncrypted,
		FUser:        uri.FUser,
		FPassword:    uri.FPassword,
		FHost:        uri.FHost,
		FPort:        uri.FPort.Clone(),
		FUriParams:   cloneWithNil(uri.FUriParams),
		FHeaders:     cloneWithNil(uri.FHeaders),
	}
}

// The special wildcard URI used in Contact: headers in REGISTER requests when expiring all registrations.
type WildcardUri struct{}

func (uri WildcardUri) IsEncrypted() bool { return false }

func (uri WildcardUri) SetEncrypted(flag bool) {}

func (uri WildcardUri) User() MaybeString { return nil }

func (uri WildcardUri) SetUser(user MaybeString) {}

func (uri WildcardUri) Password() MaybeString { return nil }

func (uri WildcardUri) SetPassword(pass MaybeString) {}

func (uri WildcardUri) Host() string { return "" }

func (uri WildcardUri) SetHost(host string) {}

func (uri WildcardUri) Port() *Port { return nil }

func (uri WildcardUri) SetPort(port *Port) {}

func (uri WildcardUri) UriParams() Params { return nil }

func (uri WildcardUri) SetUriParams(params Params) {}

func (uri WildcardUri) Headers() Params { return nil }

func (uri WildcardUri) SetHeaders(params Params) {}

// Copy the wildcard URI. Not hard!
func (uri WildcardUri) Clone() Uri { return uri }

// Always returns 'true'.
func (uri WildcardUri) IsWildcard() bool {
	return true
}

// Always returns '*' - the representation of a wildcard URI in a SIP message.
func (uri WildcardUri) String() string {
	return "*"
}

// Determines if this wildcard URI equals the specified other URI.
// This is true if and only if the other URI is also a wildcard URI.
func (uri WildcardUri) Equals(other interface{}) bool {
	switch other.(type) {
	case WildcardUri:
		return true
	default:
		return false
	}
}

// Encapsulates a header that gossip does not natively support.
// This allows header data that is not understood to be parsed by gossip and relayed to the parent application.
type GenericHeader struct {
	// The name of the header.
	HeaderName string
	// The contents of the header, including any parameters.
	// This is transparent data that is not natively understood by gossip.
	Contents string
}

// Convert the header to a flat string representation.
func (header *GenericHeader) String() string {
	return header.HeaderName + ": " + header.Contents
}

// Pull out the header name.
func (header *GenericHeader) Name() string {
	return header.HeaderName
}

// Copy the header.
func (header *GenericHeader) Clone() Header {
	return &GenericHeader{
		HeaderName: header.HeaderName,
		Contents:   header.Contents,
	}
}

func (header *GenericHeader) Equals(other interface{}) bool {
	if h, ok := other.(*GenericHeader); ok {
		return header.HeaderName == h.HeaderName &&
			header.Contents == h.Contents
	}

	return false
}

// ToHeader introduces SIP 'To' header
type ToHeader struct {
	// The display name from the header, may be omitted.
	DisplayName MaybeString
	Address     Uri
	// Any parameters present in the header.
	Params Params
}

func (to *ToHeader) String() string {
	var buffer bytes.Buffer
	buffer.WriteString("To: ")

	if displayName, ok := to.DisplayName.(String); ok && displayName.String() != "" {
		buffer.WriteString(fmt.Sprintf("\"%s\" ", displayName))
	}

	buffer.WriteString(fmt.Sprintf("<%s>", to.Address))

	if to.Params.Length() > 0 {
		buffer.WriteString(";")
		buffer.WriteString(to.Params.ToString(';'))
	}

	return buffer.String()
}

func (to *ToHeader) Name() string { return "To" }

// Copy the header.
func (to *ToHeader) Clone() Header {
	return &ToHeader{
		DisplayName: to.DisplayName,
		Address:     to.Address.Clone(),
		Params:      to.Params.Clone(),
	}
}

func (to *ToHeader) Equals(other interface{}) bool {
	if h, ok := other.(*ToHeader); ok {
		return to.DisplayName.Equals(h.DisplayName) &&
			to.Address.Equals(h.Address) &&
			to.Params.Equals(h.Params)
	}

	return false
}

type FromHeader struct {
	// The display name from the header, may be omitted.
	DisplayName MaybeString

	Address Uri

	// Any parameters present in the header.
	Params Params
}

func (from *FromHeader) String() string {
	var buffer bytes.Buffer
	buffer.WriteString("From: ")

	if displayName, ok := from.DisplayName.(String); ok && displayName.String() != "" {
		buffer.WriteString(fmt.Sprintf("\"%s\" ", displayName))
	}

	buffer.WriteString(fmt.Sprintf("<%s>", from.Address))
	if from.Params.Length() > 0 {
		buffer.WriteString(";")
		buffer.WriteString(from.Params.ToString(';'))
	}

	return buffer.String()
}

func (from *FromHeader) Name() string { return "From" }

// Copy the header.
func (from *FromHeader) Clone() Header {
	return &FromHeader{
		DisplayName: from.DisplayName,
		Address:     from.Address.Clone(),
		Params:      from.Params.Clone(),
	}
}

func (from *FromHeader) Equals(other interface{}) bool {
	if h, ok := other.(*FromHeader); ok {
		return from.DisplayName.Equals(h.DisplayName) &&
			from.Address.Equals(h.Address) &&
			from.Params.Equals(h.Params)
	}

	return false
}

type ContactHeader struct {
	// The display name from the header, may be omitted.
	DisplayName MaybeString
	Address     ContactUri
	// Any parameters present in the header.
	Params Params
}

func (contact *ContactHeader) String() string {
	var buffer bytes.Buffer
	buffer.WriteString("Contact: ")

	if displayName, ok := contact.DisplayName.(String); ok && displayName.String() != "" {
		buffer.WriteString(fmt.Sprintf("\"%s\" ", displayName))
	}

	switch contact.Address.(type) {
	case *WildcardUri:
		// Treat the Wildcard URI separately as it must not be contained in < > angle brackets.
		buffer.WriteString("*")
	default:
		buffer.WriteString(fmt.Sprintf("<%s>", contact.Address.String()))
	}

	if (contact.Params != nil) && (contact.Params.Length() > 0) {
		buffer.WriteString(";")
		buffer.WriteString(contact.Params.ToString(';'))
	}

	return buffer.String()
}

func (contact *ContactHeader) Name() string { return "Contact" }

// Copy the header.
func (contact *ContactHeader) Clone() Header {
	return &ContactHeader{
		DisplayName: contact.DisplayName,
		Address:     contact.Address.Clone().(ContactUri),
		Params:      contact.Params.Clone(),
	}
}

func (contact *ContactHeader) Equals(other interface{}) bool {
	if h, ok := other.(*ContactHeader); ok {
		return contact.DisplayName.Equals(h.DisplayName) &&
			contact.Address.Equals(h.Address) &&
			contact.Params.Equals(h.Params)
	}

	return false
}

// CallID - 'Call-ID' header.
type CallID string

func (callId CallID) String() string {
	return "Call-ID: " + string(callId)
}

func (callId *CallID) Name() string { return "Call-ID" }

func (callId *CallID) Clone() Header {
	return callId
}

func (callId *CallID) Equals(other interface{}) bool {
	if h, ok := other.(CallID); ok {
		return *callId == h
	}
	if h, ok := other.(*CallID); ok {
		return *callId == *h
	}

	return false
}

type CSeq struct {
	SeqNo      uint32
	MethodName RequestMethod
}

func (cseq *CSeq) String() string {
	return fmt.Sprintf("CSeq: %d %s", cseq.SeqNo, cseq.MethodName)
}

func (cseq *CSeq) Name() string { return "CSeq" }

func (cseq *CSeq) Clone() Header {
	return &CSeq{
		SeqNo:      cseq.SeqNo,
		MethodName: cseq.MethodName,
	}
}

func (cseq *CSeq) Equals(other interface{}) bool {
	if h, ok := other.(*CSeq); ok {
		return cseq.SeqNo == h.SeqNo &&
			cseq.MethodName == h.MethodName
	}

	return false
}

type MaxForwards uint32

func (maxForwards MaxForwards) String() string {
	return fmt.Sprintf("Max-Forwards: %d", int(maxForwards))
}

func (maxForwards *MaxForwards) Name() string { return "Max-Forwards" }

func (maxForwards *MaxForwards) Clone() Header { return maxForwards }

func (maxForwards *MaxForwards) Equals(other interface{}) bool {
	if h, ok := other.(MaxForwards); ok {
		return *maxForwards == h
	}
	if h, ok := other.(*MaxForwards); ok {
		return *maxForwards == *h
	}

	return false
}

type Expires uint32

func (expires Expires) String() string {
	return fmt.Sprintf("Expires: %d", int(expires))
}

func (expires *Expires) Name() string { return "Expires" }

func (expires *Expires) Clone() Header { return expires }

func (expires *Expires) Equals(other interface{}) bool {
	if h, ok := other.(Expires); ok {
		return *expires == h
	}
	if h, ok := other.(*Expires); ok {
		return *expires == *h
	}

	return false
}

type ContentLength uint32

func (contentLength ContentLength) String() string {
	return fmt.Sprintf("Content-Length: %d", int(contentLength))
}

func (contentLength *ContentLength) Name() string { return "Content-Length" }

func (contentLength *ContentLength) Clone() Header { return contentLength }

func (contentLength *ContentLength) Equals(other interface{}) bool {
	if h, ok := other.(ContentLength); ok {
		return *contentLength == h
	}
	if h, ok := other.(*ContentLength); ok {
		return *contentLength == *h
	}

	return false
}

type ViaHeader []*ViaHop

func (via ViaHeader) String() string {
	var buffer bytes.Buffer
	buffer.WriteString("Via: ")
	for idx, hop := range via {
		buffer.WriteString(hop.String())
		if idx != len(via)-1 {
			buffer.WriteString(", ")
		}
	}

	return buffer.String()
}

func (via ViaHeader) Name() string { return "Via" }

func (via ViaHeader) Clone() Header {
	dup := make([]*ViaHop, 0, len(via))
	for _, hop := range via {
		dup = append(dup, hop.Clone())
	}
	return ViaHeader(dup)
}

func (via ViaHeader) Equals(other interface{}) bool {
	if h, ok := other.(ViaHeader); ok {
		if len(via) != len(h) {
			return false
		}

		for i, hop := range via {
			if !hop.Equals(h[i]) {
				return false
			}
		}

		return true
	}

	return false
}

// A single component in a Via header.
// Via headers are composed of several segments of the same structure, added by successive nodes in a routing chain.
type ViaHop struct {
	// E.g. 'SIP'.
	ProtocolName string
	// E.g. '2.0'.
	ProtocolVersion string
	Transport       string
	Host            string
	// The port for this via hop. This is stored as a pointer type, since it is an optional field.
	Port   *Port
	Params Params
}

func (hop *ViaHop) SentBy() string {
	var buf bytes.Buffer
	buf.WriteString(hop.Host)
	if hop.Port != nil {
		buf.WriteString(fmt.Sprintf(":%d", *hop.Port))
	}

	return buf.String()
}

func (hop *ViaHop) String() string {
	var buffer bytes.Buffer
	buffer.WriteString(
		fmt.Sprintf(
			"%s/%s/%s %s",
			hop.ProtocolName,
			hop.ProtocolVersion,
			hop.Transport,
			hop.Host,
		),
	)
	if hop.Port != nil {
		buffer.WriteString(fmt.Sprintf(":%d", *hop.Port))
	}

	if hop.Params.Length() > 0 {
		buffer.WriteString(";")
		buffer.WriteString(hop.Params.ToString(';'))
	}

	return buffer.String()
}

// Return an exact copy of this ViaHop.
func (hop *ViaHop) Clone() *ViaHop {
	return &ViaHop{
		ProtocolName:    hop.ProtocolName,
		ProtocolVersion: hop.ProtocolVersion,
		Transport:       hop.Transport,
		Host:            hop.Host,
		Port:            hop.Port.Clone(),
		Params:          hop.Params.Clone(),
	}
}

func (hop *ViaHop) Equals(other interface{}) bool {
	if h, ok := other.(*ViaHop); ok {
		return hop.ProtocolName == h.ProtocolName &&
			hop.ProtocolVersion == h.ProtocolVersion &&
			hop.Transport == h.Transport &&
			hop.Host == h.Host &&
			util.Uint16PtrEq((*uint16)(hop.Port), (*uint16)(h.Port)) &&
			hop.Params.Equals(h.Params)
	}

	return false
}

type RequireHeader struct {
	Options []string
}

func (require *RequireHeader) String() string {
	return fmt.Sprintf("Require: %s",
		strings.Join(require.Options, ", "))
}

func (require *RequireHeader) Name() string { return "Require" }

func (require *RequireHeader) Clone() Header {
	dup := make([]string, len(require.Options))
	copy(dup, require.Options)
	return &RequireHeader{dup}
}

func (require *RequireHeader) Equals(other interface{}) bool {
	if h, ok := other.(*RequireHeader); ok {
		if len(require.Options) != len(h.Options) {
			return false
		}

		for i, opt := range require.Options {
			if opt != h.Options[i] {
				return false
			}
		}

		return true
	}

	return false
}

type SupportedHeader struct {
	Options []string
}

func (support *SupportedHeader) String() string {
	return fmt.Sprintf("Supported: %s",
		strings.Join(support.Options, ", "))
}

func (support *SupportedHeader) Name() string { return "Supported" }

func (support *SupportedHeader) Clone() Header {
	dup := make([]string, len(support.Options))
	copy(dup, support.Options)
	return &SupportedHeader{dup}
}

func (support *SupportedHeader) Equals(other interface{}) bool {
	if h, ok := other.(*SupportedHeader); ok {
		if len(support.Options) != len(h.Options) {
			return false
		}

		for i, opt := range support.Options {
			if opt != h.Options[i] {
				return false
			}
		}

		return true
	}

	return false
}

type ProxyRequireHeader struct {
	Options []string
}

func (proxyRequire *ProxyRequireHeader) String() string {
	return fmt.Sprintf("Proxy-Require: %s",
		strings.Join(proxyRequire.Options, ", "))
}

func (proxyRequire *ProxyRequireHeader) Name() string { return "Proxy-Require" }

func (proxyRequire *ProxyRequireHeader) Clone() Header {
	dup := make([]string, len(proxyRequire.Options))
	copy(dup, proxyRequire.Options)
	return &ProxyRequireHeader{dup}
}

func (proxyRequire *ProxyRequireHeader) Equals(other interface{}) bool {
	if h, ok := other.(*ProxyRequireHeader); ok {
		if len(proxyRequire.Options) != len(h.Options) {
			return false
		}

		for i, opt := range proxyRequire.Options {
			if opt != h.Options[i] {
				return false
			}
		}

		return true
	}

	return false
}

// 'Unsupported:' is a SIP header type - this doesn't indicate that the
// header itself is not supported by gossip!
type UnsupportedHeader struct {
	Options []string
}

func (unsupported *UnsupportedHeader) String() string {
	return fmt.Sprintf("Unsupported: %s",
		strings.Join(unsupported.Options, ", "))
}

func (unsupported *UnsupportedHeader) Name() string { return "Unsupported" }

func (unsupported *UnsupportedHeader) Clone() Header {
	dup := make([]string, len(unsupported.Options))
	copy(dup, unsupported.Options)
	return &UnsupportedHeader{dup}
}

func (unsupported *UnsupportedHeader) Equals(other interface{}) bool {
	if h, ok := other.(*UnsupportedHeader); ok {
		if len(unsupported.Options) != len(h.Options) {
			return false
		}

		for i, opt := range unsupported.Options {
			if opt != h.Options[i] {
				return false
			}
		}

		return true
	}

	return false
}

type UserAgentHeader string

func (ua UserAgentHeader) String() string {
	return "User-Agent: " + string(ua)
}

func (ua *UserAgentHeader) Name() string { return "User-Agent" }

func (ua *UserAgentHeader) Clone() Header { return ua }

func (ua *UserAgentHeader) Equals(other interface{}) bool {
	if h, ok := other.(UserAgentHeader); ok {
		return *ua == h
	}
	if h, ok := other.(*UserAgentHeader); ok {
		return *ua == *h
	}

	return false
}

type AllowHeader []RequestMethod

func (allow AllowHeader) String() string {
	parts := make([]string, 0)
	for _, method := range allow {
		parts = append(parts, string(method))
	}

	return fmt.Sprintf("Allow: %s", strings.Join(parts, ", "))
}

func (allow AllowHeader) Name() string { return "Allow" }

func (allow AllowHeader) Clone() Header {
	newAllow := make(AllowHeader, len(allow))
	copy(newAllow, allow)

	return newAllow
}

func (allow AllowHeader) Equals(other interface{}) bool {
	if h, ok := other.(AllowHeader); ok {
		if len(allow) != len(h) {
			return false
		}

		for i, v := range allow {
			if v != h[i] {
				return false
			}
		}

		return true
	}

	return false
}

type ContentType string

func (ct ContentType) String() string { return "Content-Type: " + string(ct) }

func (ct *ContentType) Name() string { return "Content-Type" }

func (ct *ContentType) Clone() Header { return ct }

func (ct *ContentType) Equals(other interface{}) bool {
	if h, ok := other.(ContentType); ok {
		return *ct == h
	}
	if h, ok := other.(*ContentType); ok {
		return *ct == *h
	}

	return false
}

type RouteHeader struct {
	Addresses []Uri
}

func (route *RouteHeader) Name() string { return "Route" }

func (route *RouteHeader) String() string {
	var addrs []string

	for _, uri := range route.Addresses {
		addrs = append(addrs, "<"+uri.String()+">")
	}

	return fmt.Sprintf("Route: %s", strings.Join(addrs, ", "))
}

func (route *RouteHeader) Clone() Header {
	newRoute := &RouteHeader{
		Addresses: make([]Uri, len(route.Addresses)),
	}

	for i, uri := range route.Addresses {
		newRoute.Addresses[i] = uri.Clone()
	}

	return newRoute
}

func (route *RouteHeader) Equals(other interface{}) bool {
	if h, ok := other.(*RouteHeader); ok {
		for i, uri := range route.Addresses {
			if !uri.Equals(h.Addresses[i]) {
				return false
			}
		}

		return true
	}

	return false
}

type RecordRouteHeader struct {
	Addresses []Uri
}

func (route *RecordRouteHeader) Name() string { return "Record-Route" }

func (route *RecordRouteHeader) String() string {
	var addrs []string

	for _, uri := range route.Addresses {
		addrs = append(addrs, "<"+uri.String()+">")
	}

	return fmt.Sprintf("Record-Route: %s", strings.Join(addrs, ", "))
}

func (route *RecordRouteHeader) Clone() Header {
	newRoute := &RecordRouteHeader{
		Addresses: make([]Uri, len(route.Addresses)),
	}

	for i, uri := range route.Addresses {
		newRoute.Addresses[i] = uri.Clone()
	}

	return newRoute
}

func (route *RecordRouteHeader) Equals(other interface{}) bool {
	if h, ok := other.(*RecordRouteHeader); ok {
		for i, uri := range route.Addresses {
			if !uri.Equals(h.Addresses[i]) {
				return false
			}
		}

		return true
	}

	return false
}
