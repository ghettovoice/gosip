// Package dns provides comprehensive DNS resolution utilities specifically designed for SIP applications.
// It extends the standard net.Resolver with additional capabilities for looking up SRV and NAPTR records
// as required by RFC 3263 for SIP server location and transport protocol discovery.
//
// Key Features:
//   - SRV record lookup for service discovery (e.g., _sip._udp.example.com)
//   - NAPTR record lookup for URI resolution and transport protocol selection
//   - Configurable DNS nameserver and dialer settings
//   - Automatic record sorting according to RFC specifications
//   - Support for both IPv4 and IPv6 address resolution
//
// The package provides a default resolver instance for convenience, along with package-level
// functions that delegate to this default resolver. For custom configurations, create a new
// Resolver instance with the desired NameServer and Dialer settings.
//
// The caller's context controls how long a lookup may take; use context.WithTimeout to bound
// the query duration. The default dialer bounds connection establishment with a 5s timeout.
//
// Usage:
//
//	// Using the default resolver
//	ips, err := dns.LookupIP(ctx, "ip", "example.com")
//	srvs, err := dns.LookupSRV(ctx, "sip", "udp", "example.com")
//	naptrs, err := dns.LookupNAPTR(ctx, "example.com")
//
//	// Using a custom resolver and bounding the query duration
//	resolver := &dns.Resolver{
//	    NameServer: "8.8.8.8:53",
//	}
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	ips, err := resolver.LookupIP(ctx, "ip", "example.com")
//
// Caching:
//
// Results can be cached by wrapping any Lookup implementation with
// NewCachedResolver. Caching is strictly opt-in and is never enabled
// automatically. The cache TTL is a fixed application-level lifetime, not
// the DNS record TTL: a cached result may outlive the real DNS TTL and is
// never invalidated early, so choose the TTL accordingly.
//
//	cached, err := dns.NewCachedResolver(dns.DefaultResolver(), dns.CacheOptions{
//	    TTL:      5 * time.Second,
//	    Capacity: 256,
//	})
//	// cached implements dns.Lookup and is assignable to sip.DNSResolver.
//
// RFC Compliance:
//   - RFC 3263: Locating SIP Servers
//   - RFC 3403: Dynamic Delegation Discovery System (DDDS) NAPTR records
package dns
