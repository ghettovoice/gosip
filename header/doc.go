// Package header provides facilities for working with SIP message headers
// defined by RFC 3261 and related extensions.
//
// This package offers typed representations, parsing, validation, normalization,
// comparison, rendering, and cloning of headers and their parameters. It exposes
// a generic Header interface implemented by concrete header types, utilities for
// converting to and from ABNF nodes, and adapters for JSON serialization.
//
// # Overview
//
// The package provides concrete types for all standard SIP headers from RFC 3261,
// including Accept, Authorization, Call-ID, Contact, Content-Type, CSeq, From, To,
// Via, and many others. Custom or extension headers can be handled through the Any
// type or by registering custom parsers.
//
// All header types implement the [Header] interface, which combines [types.Renderer],
// [types.Cloneable[Header]], [types.ValidFlag], and [types.Equalable]. This ensures
// consistent behavior across all header implementations for rendering, cloning,
// validation, and equality comparisons.
//
// # Parsing
//
// Use [Parse] to parse a header from string or []byte input:
//
//	hdr, err := header.Parse("From: <sip:alice@example.com>;tag=1234")
//
// The [Parse] function accepts both string and []byte through generics. Internally,
// it uses ABNF parsing and converts the result via [FromABNF].
//
// # Header Naming and Canonicalization
//
// Header names are canonicalized using [textproto.CanonicalMIMEHeaderKey] combined
// with an internal mapping for SIP-specific capitalization rules. The package also
// supports compact header names defined in RFC 3261:
//
//	"c" → "Content-Type"
//	"e" → "Content-Encoding"
//	"f" → "From"
//	"i" → "Call-ID"
//	"k" → "Supported"
//	"l" → "Content-Length"
//	"m" → "Contact"
//	"s" → "Subject"
//	"t" → "To"
//	"v" → "Via"
//
// Additional normalization rules handle special cases:
//
//	"Call-Id" → "Call-ID"
//	"Cseq" → "CSeq"
//	"Mime-Version" → "MIME-Version"
//	"Www-Authenticate" → "WWW-Authenticate"
//
// Use [CanonicName] to normalize any header name, or [Name.ToCanonic] as a
// convenient method alias.
//
// # Header Interface
//
// Every header type implements the [Header] interface, which provides methods
// for rendering, cloning, validation, and equality comparison. See [Header]
// for details.
//
// # Standard Headers
//
// The package includes concrete types for all headers defined in RFC 3261.
// Extension headers not in this list are parsed as the generic [Any] type.
//
// # Custom Parsers
//
// Applications can register custom parsers for extension headers via [RegisterParser]
// and remove them with [UnregisterParser]. Parsers must conform to the signature:
//
//	type Parser func(name string, value []byte) Header
//
// [RegisterParser] lowercases the key automatically and stores the parser in an internal
// concurrent map, so it is safe to call during init of multiple packages.
// Registration should still finish before headers are parsed to avoid partially
// configured behavior:
//
//	func init() {
//		header.RegisterParser("x-custom", func(name string, value []byte) header.Header {
//			return &MyCustomHeader{
//				Name:  name,
//				Value: string(value),
//			}
//		})
//	}
//
// If a custom parser returns nil, the header is parsed as [Any]. Use [UnregisterParser]
// to remove previously registered parsers when they are no longer needed.
//
// # Parameters
//
// Many headers support parameters (key-value pairs following the main header value).
// Parameters are represented by the Values type (a multi-value map).
//
// Parameter rendering follows deterministic rules:
//
//   - Parameters are sorted alphabetically
//   - The "q" (quality) parameter always appears first
//   - A missing "q" parameter may be added with default value "1" (RFC 2616 Section 14.1)
//
// Parameter comparison uses special semantics for header equality:
//
//   - Parameters present in both headers must have matching values
//   - Non-special parameters present in only one header are ignored
//   - Special parameters (defined per header type) must be present in both or neither
//   - Unquoted parameter values are compared case-insensitively
//
// Parameter validation requires:
//
//   - Names must be valid tokens
//   - Values must be tokens, hosts, or quoted strings
//
// # Validation
//
// All header types provide an IsValid method that checks syntactic validity
// according to RFC 3261 grammar rules. Header names must be valid tokens;
// use [Name.IsValid] for explicit name validation.
//
// # Rendering
//
// Headers can be rendered to strings or written to [io.Writer]:
//
//	str := hdr.Render()                    // returns "Name: Value"
//	val := hdr.RenderValue()               // returns "Value" without name
//	hdr.RenderTo(writer, opts)             // writes to io.Writer
//
// [RenderOptions] can be nil for default formatting: headers are rendered with
// canonical names. Short names can be enabled with [RenderOptions.Compact] flag,
// then headers that support short names are rendered with single character names.
//
// # JSON Serialization
//
// Headers can be serialized to and from JSON using [ToJSON] and [FromJSON]:
//
//	data, err := header.ToJSON(hdr)
//	hdr, err := header.FromJSON(data)
//
// The JSON format is:
//
//	{"name":"<CanonicName>","value":"<RenderValue>"}
//
// [FromJSON] validates by re-parsing through the full Parse pipeline, ensuring that
// only syntactically valid headers are deserialized.
//
// # Helper Functions
//
// The package exports several helpers for working with addresses:
//
//   - Host(host string) creates an Addr without a port
//   - HostPort(host string, port uint16) creates an Addr with a port
//   - ParseAddr[T ~string | ~[]byte](s T) parses an address from input
//
// # Best Practices
//
//   - Register custom parsers with [RegisterParser] during init before any parsing occurs
//   - Use typed headers and the Equal method rather than string comparisons
//   - Call IsValid() to check syntactic validity before using parsed headers
//   - Use RenderOptions when consistent formatting is required
//   - Prefer Clone() for creating independent copies of headers
//
// # References
//
//   - RFC 3261 - SIP: Session Initiation Protocol
//   - RFC 2616 Section 14.1 - Accept header and quality factor
package header
