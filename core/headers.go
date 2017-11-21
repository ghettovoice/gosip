package core

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gossip/utils"
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
}

// A URI from any schema (e.g. sip:, tel:, callto:)
type Uri interface {
	// Determine if the two URIs are equal according to the rules in RFC 3261 s. 19.1.4.
	Equals(other Uri) bool
	String() string
	Clone() Uri
}

// A URI from a schema suitable for inclusion in a Contact: header.
// The only such URIs are sip/sips URIs and the special wildcard URI '*'.
type ContactUri interface {
	Uri
	// Return true if and only if the URI is the special wildcard URI '*'; that is, if it is
	// a WildcardUri struct.
	IsWildcard() bool
}

// Generic list of parameters on a header.
type HeaderParams interface {
	Get(key string) (MaybeString, bool)
	Add(key string, val MaybeString) HeaderParams
	Clone() HeaderParams
	Equals(params HeaderParams) bool
	ToString(sep uint8) string
	String() string
	Length() int
	Items() map[string]MaybeString
	Keys() []string
}

// IMPLEMENTATION

// HeaderParams implementation.
type headerParams struct {
	params     map[string]MaybeString
	paramOrder []string
}

// Create an empty set of parameters.
func NewHeaderParams() HeaderParams {
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

// Add a new parameter.
func (params *headerParams) Add(key string, val MaybeString) HeaderParams {
	// Add param to order list if new.
	if _, ok := params.params[key]; !ok {
		params.paramOrder = append(params.paramOrder, key)
	}

	// Set param value.
	params.params[key] = val

	// Return the params so calls can be chained.
	return params
}

// Copy a list of params.
func (params *headerParams) Clone() HeaderParams {
	dup := NewHeaderParams()
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
func (params *headerParams) Equals(q HeaderParams) bool {
	if params.Length() == 0 && q.Length() == 0 {
		return true
	}

	if params.Length() != q.Length() {
		return false
	}

	for key, p_val := range params.Items() {
		q_val, ok := q.Get(key)
		if !ok {
			return false
		}
		if p_val != q_val {
			return false
		}
	}

	return true
}

func cloneWithNil(params HeaderParams) HeaderParams {
	if params == nil {
		return NewHeaderParams()
	}
	return params.Clone()
}

// SipUri
// A SIP or SIPS URI, including all params and URI header params.
type SipUri struct {
	// True if and only if the URI is a SIPS URI.
	IsEncrypted bool

	// The user part of the URI: the 'joe' in sip:joe@bloggs.com
	// This is a pointer, so that URIs without a user part can have 'nil'.
	User MaybeString

	// The password field of the URI. This is represented in the URI as joe:hunter2@bloggs.com.
	// Note that if a URI has a password field, it *must* have a user field as well.
	// This is a pointer, so that URIs without a password field can have 'nil'.
	// Note that RFC 3261 strongly recommends against the use of password fields in SIP URIs,
	// as they are fundamentally insecure.
	Password MaybeString

	// The host part of the URI. This can be a domain, or a string representation of an IP address.
	Host string

	// The port part of the URI. This is optional, and so is represented here as a pointer type.
	Port *Port

	// Any parameters associated with the URI.
	// These are used to provide information about requests that may be constructed from the URI.
	// (For more details, see RFC 3261 section 19.1.1).
	// These appear as a semicolon-separated list of key=value pairs following the host[:port] part.
	UriParams HeaderParams

	// Any headers to be included on requests constructed from this URI.
	// These appear as a '&'-separated list at the end of the URI, introduced by '?'.
	// Although the values of the map are MaybeStrings, they will never be NoString in practice as the parser
	// guarantees to not return blank values for header elements in SIP URIs.
	// You should not set the values of headers to NoString.
	Headers HeaderParams
}

func (uri *SipUri) IsWildcard() bool {
	return false
}

// Determine if the SIP URI is equal to the specified URI according to the rules laid down in RFC 3261 s. 19.1.4.
// TODO: The Equals method is not currently RFC-compliant; fix this!
func (uri *SipUri) Equals(otherUri Uri) bool {
	otherPtr, ok := otherUri.(*SipUri)
	if !ok {
		return false
	}

	other := *otherPtr
	result := uri.IsEncrypted == other.IsEncrypted &&
		uri.User == other.User &&
		uri.Password == other.Password &&
		uri.Host == other.Host &&
		utils.Uint16PtrEq((*uint16)(uri.Port), (*uint16)(other.Port))

	if !result {
		return false
	}

	if !uri.UriParams.Equals(other.UriParams) {
		return false
	}

	if !uri.Headers.Equals(other.Headers) {
		return false
	}

	return true
}

// Generates the string representation of a SipUri struct.
func (uri *SipUri) String() string {
	var buffer bytes.Buffer

	// Compulsory protocol identifier.
	if uri.IsEncrypted {
		buffer.WriteString("sips")
		buffer.WriteString(":")
	} else {
		buffer.WriteString("sip")
		buffer.WriteString(":")
	}

	// Optional userinfo part.
	if user, ok := uri.User.(String); ok && user.String() != "" {
		buffer.WriteString(uri.User.String())
		if pass, ok := uri.Password.(String); ok && pass.String() != "" {
			buffer.WriteString(":")
			buffer.WriteString(pass.String())
		}
		buffer.WriteString("@")
	}

	// Compulsory hostname.
	buffer.WriteString(uri.Host)

	// Optional port number.
	if uri.Port != nil {
		buffer.WriteString(":")
		buffer.WriteString(uri.Port.String())
	}

	if (uri.UriParams != nil) && uri.UriParams.Length() > 0 {
		buffer.WriteString(";")
		buffer.WriteString(uri.UriParams.ToString(';'))
	}

	if (uri.Headers != nil) && uri.Headers.Length() > 0 {
		buffer.WriteString("?")
		buffer.WriteString(uri.Headers.ToString('&'))
	}

	return buffer.String()
}

// Clone the Sip URI.
func (uri *SipUri) Clone() Uri {
	return &SipUri{
		IsEncrypted: uri.IsEncrypted,
		User:        uri.User,
		Password:    uri.Password,
		Host:        uri.Host,
		Port:        uri.Port.Clone(),
		UriParams:   cloneWithNil(uri.UriParams),
		Headers:     cloneWithNil(uri.Headers),
	}
}

// The special wildcard URI used in Contact: headers in REGISTER requests when expiring all registrations.
type WildcardUri struct{}

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
func (uri WildcardUri) Equals(other Uri) bool {
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

// ToHeader introduces SIP 'To' header
type ToHeader struct {
	// The display name from the header, may be omitted.
	DisplayName MaybeString
	Address     Uri
	// Any parameters present in the header.
	Params HeaderParams
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

type FromHeader struct {
	// The display name from the header, may be omitted.
	DisplayName MaybeString

	Address Uri

	// Any parameters present in the header.
	Params HeaderParams
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

type ContactHeader struct {
	// The display name from the header, may be omitted.
	DisplayName MaybeString
	Address     ContactUri
	// Any parameters present in the header.
	Params HeaderParams
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

// CallId - 'Call-Id' header.
type CallId string

func (callId CallId) String() string {
	return "Call-Id: " + (string)(callId)
}

func (callId *CallId) Name() string { return "Call-Id" }

func (callId *CallId) Clone() Header {
	temp := *callId
	return &temp
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

type MaxForwards uint32

func (maxForwards MaxForwards) String() string {
	return fmt.Sprintf("Max-Forwards: %d", int(maxForwards))
}

func (maxForwards MaxForwards) Name() string { return "Max-Forwards" }

func (maxForwards MaxForwards) Clone() Header { return maxForwards }

type ContentLength uint32

func (contentLength ContentLength) String() string {
	return fmt.Sprintf("Content-Length: %d", int(contentLength))
}

func (contentLength ContentLength) Name() string { return "Content-Length" }

func (contentLength ContentLength) Clone() Header { return contentLength }

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
	Params HeaderParams
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
	copy(require.Options, dup)
	return &RequireHeader{dup}
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
	copy(support.Options, dup)
	return &SupportedHeader{dup}
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
	copy(proxyRequire.Options, dup)
	return &ProxyRequireHeader{dup}
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
	copy(unsupported.Options, dup)
	return &UnsupportedHeader{dup}
}
