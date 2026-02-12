// Package uri provides comprehensive support for parsing, manipulating, and rendering
// SIP-related Uniform Resource Identifiers (URIs) according to RFC 3261 and RFC 3966.
//
// # Overview
//
// This package implements three specialized URI types, each designed for specific use cases
// in SIP communications:
//
//   - [SIP]: Represents SIP and SIPS URIs (sip:, sips:), the primary addressing mechanism
//     in Session Initiation Protocol communications. Supports user credentials, host:port
//     addressing, URI parameters, and headers as defined in RFC 3261.
//
//   - [Tel]: Represents telephone number URIs (tel:) according to RFC 3966. Handles both
//     global (+E.164) and local telephone numbers with phone-context parameter. Provides
//     conversion to SIP URIs and proper comparison semantics for telephone numbers.
//
//   - [Any]: A generic URI type based on [net/url.URL] for handling arbitrary URI schemes
//     that don't fit the SIP or Tel patterns (e.g., http:, https:, urn:).
//
// All URI types implement the [URI] interface, providing uniform access to common operations
// such as rendering, cloning, validation, and equality comparison.
//
// # Parsing
//
// The package provides flexible parsing functions that automatically detect and return
// the appropriate URI type:
//
//	// Parse automatically detects URI type
//	u, err := uri.Parse("sip:user@example.com:5060;transport=tcp")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	// Returns *uri.SIP
//
//	// Parse telephone URI
//	tel, err := uri.Parse("tel:+1-555-123-4567")
//	// Returns *uri.Tel
//
//	// Parse generic URI
//	any, err := uri.Parse("https://example.com/path")
//	// Returns *uri.Any
//
// Type-specific parsing functions [ParseSIP], [ParseTel], and [ParseAny] are available
// when the URI scheme is known in advance.
//
// # SIP URI Structure
//
// SIP and SIPS URIs follow the pattern:
//
//	sip:user:password@host:port;param1=value1;param2?header1=value1&header2=value2
//	sips:user@host:port;transport=tls
//
// The [SIP] type provides structured access to all components:
//
//	sip := &uri.SIP{
//	    User:    uri.UserPassword("alice", "secret"),
//	    Addr:    uri.HostPort("example.com", 5060),
//	    Params:  uri.Values{"transport": []string{"udp"}},
//	    Headers: uri.Values{"Subject": []string{"Meeting"}},
//	    Secured: false, // false for "sip:", true for "sips:"
//	}
//
// SIP URI equality follows RFC 3261 Section 19.1.4 rules, where special parameters
// (transport, user, method, maddr, ttl, lr) must match, but non-special parameters
// are optional for equality.
//
// # Tel URI Structure
//
// Tel URIs represent telephone numbers and follow RFC 3966:
//
//	tel:+1-555-123-4567                    // global number (E.164)
//	tel:555-1234;phone-context=+1-555      // local number with context
//	tel:+1-555-123-4567;ext=789            // with extension
//
// The [Tel] type handles visual separators, parameter ordering, and provides conversion
// to SIP URIs via the [Tel.ToSIP] method:
//
//	tel := &uri.Tel{
//	    Number: "+1-555-123-4567",
//	    Params: uri.Values{"ext": []string{"789"}},
//	}
//	sipURI := tel.ToSIP() // Converts to SIP format with user=phone parameter
//
// # URI Interface
//
// The [URI] interface unifies all URI types and provides:
//
//   - Rendering: [types.Renderer] interface for string serialization with custom options
//   - Cloning: [types.Cloneable] interface for creating deep copies
//   - Validation: [types.ValidFlag] interface for syntax validation
//   - Equality: [types.Equalable] interface for RFC-compliant comparison
//
// # Serialization
//
// All URI types implement [encoding.TextMarshaler] and [encoding.TextUnmarshaler],
// enabling seamless JSON/XML serialization:
//
//	type Contact struct {
//	    SIPAddr *uri.SIP `json:"sip_addr,omitempty"`
//	    TelAddr *uri.Tel `json:"tel_addr,omitempty"`
//	}
//
// Note: Use concrete types (*SIP, *Tel, *Any) in struct fields for full
// Marshal/Unmarshal support, rather than the URI interface.
//
// # Network Addresses
//
// The [Addr] type (alias for [types.Addr]) represents host:port combinations with
// optional port. Helper functions [Host] and [HostPort] construct addresses, and
// [ParseAddr] parses them from strings.
//
// # Parameters and Headers
//
// The [Values] type (alias for [types.Values]) is a multi-value map for URI parameters
// and headers, similar to [net/url.Values]. Keys are case-insensitive and normalized
// to lowercase. See [types.Values] for available methods.
//
// # RFC Compliance
//
// This package implements:
//
//   - RFC 3261: Session Initiation Protocol (SIP URI syntax and comparison)
//   - RFC 3966: The tel URI for Telephone Numbers (Tel URI syntax and comparison)
//
// URI rendering follows RFC 3261 rules for parameter ordering, escaping, and
// canonicalization. Equality comparison implements the exact semantics specified
// in the respective RFCs.
//
// # Thread Safety
//
// URI types are not safe for concurrent modification. When sharing URIs across
// goroutines, either use synchronization or create copies using the Clone method.
package uri
