// Package dns provides comprehensive DNS resolution utilities specifically designed for SIP applications.
// It extends the standard net.Resolver with additional capabilities for looking up SRV and NAPTR records
// as required by RFC 3263 for SIP server location and transport protocol discovery.
//
// Key Features:
//   - SRV record lookup for service discovery (e.g., _sip._udp.example.com)
//   - NAPTR record lookup for URI resolution and transport protocol selection
//   - Configurable DNS nameserver and timeout settings
//   - Automatic record sorting according to RFC specifications
//   - Support for both IPv4 and IPv6 address resolution
//
// The package provides a default resolver instance for convenience, along with package-level
// functions that delegate to this default resolver. For custom configurations, create a new
// Resolver instance with the desired NameServer and Timeout settings.
//
// Usage:
//
//	// Using the default resolver
//	ips, err := dns.LookupIP(ctx, "ip", "example.com")
//	srvs, err := dns.LookupSRV(ctx, "sip", "udp", "example.com")
//	naptrs, err := dns.LookupNAPTR(ctx, "example.com")
//
//	// Using a custom resolver
//	resolver := &dns.Resolver{
//	    NameServer: "8.8.8.8:53",
//	    Timeout:    5 * time.Second,
//	}
//	ips, err := resolver.LookupIP(ctx, "ip", "example.com")
//
// RFC Compliance:
//   - RFC 3263: Locating SIP Servers
//   - RFC 3403: Dynamic Delegation Discovery System (DDDS) NAPTR records
package dns
