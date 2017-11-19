package message

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/ghettovoice/gosip/base"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gossip/utils"
)

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
	Get(key string) (base.MaybeString, bool)
	Add(key string, val base.MaybeString) HeaderParams
	Clone() HeaderParams
	Equals(params HeaderParams) bool
	ToString(sep uint8) string
	String() string
	Length() int
	Items() map[string]base.MaybeString
	Keys() []string
}

// IMPLEMENTATION

// HeaderParams implementation.
type params struct {
	params     map[string]base.MaybeString
	paramOrder []string
}

// Create an empty set of parameters.
func NewParams() HeaderParams {
	return &params{
		params:     make(map[string]base.MaybeString),
		paramOrder: []string{},
	}
}

// Returns the entire parameter map.
func (params *params) Items() map[string]base.MaybeString {
	return params.params
}

// Returns a slice of keys, in order.
func (params *params) Keys() []string {
	return params.paramOrder
}

// Returns the requested parameter value.
func (params *params) Get(key string) (base.MaybeString, bool) {
	v, ok := params.params[key]
	return v, ok
}

// Add a new parameter.
func (params *params) Add(key string, val base.MaybeString) HeaderParams {
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
func (params *params) Clone() HeaderParams {
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
func (params *params) ToString(sep uint8) string {
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

		if strings.ContainsAny(val.String(), abnfWs) {
			buffer.WriteString(fmt.Sprintf("=\"%s\"", val.String()))
		} else {
			buffer.WriteString(fmt.Sprintf("=%s", val.String()))
		}
	}

	return buffer.String()
}

// String returns params joined with '&' char.
func (params *params) String() string {
	return params.ToString('&')
}

// Returns number of params.
func (params *params) Length() int {
	return len(params.params)
}

// Check if two maps of parameters are equal in the sense of having the same keys with the same values.
// This does not rely on any ordering of the keys of the map in memory.
func (params *params) Equals(q HeaderParams) bool {
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
		return NewParams()
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
	User base.MaybeString

	// The password field of the URI. This is represented in the URI as joe:hunter2@bloggs.com.
	// Note that if a URI has a password field, it *must* have a user field as well.
	// This is a pointer, so that URIs without a password field can have 'nil'.
	// Note that RFC 3261 strongly recommends against the use of password fields in SIP URIs,
	// as they are fundamentally insecure.
	Password base.MaybeString

	// The host part of the URI. This can be a domain, or a string representation of an IP address.
	Host string

	// The port part of the URI. This is optional, and so is represented here as a pointer type.
	Port *uint16

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
		utils.Uint16PtrEq(uri.Port, other.Port)

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
	if uri.User != nil {
		buffer.WriteString(uri.User.String())
		if uri.Password != nil {
			buffer.WriteString(":")
			buffer.WriteString(uri.Password.String())
		}
		buffer.WriteString("@")
	}

	// Compulsory hostname.
	buffer.WriteString(uri.Host)

	// Optional port number.
	if uri.Port != nil {
		buffer.WriteString(":")
		buffer.WriteString(strconv.Itoa(int(*uri.Port)))
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
	var port *uint16
	if uri.Port != nil {
		temp := *uri.Port
		port = &temp
	}

	return &SipUri{
		IsEncrypted: uri.IsEncrypted,
		User:        uri.User,
		Password:    uri.Password,
		Host:        uri.Host,
		Port:        port,
		UriParams:   cloneWithNil(uri.UriParams),
		Headers:     cloneWithNil(uri.Headers),
	}
}
