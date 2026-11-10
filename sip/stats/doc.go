// Package stats provides SIP stack statistics recording.
//
// [Recorder] implements the element middleware and transaction handler
// interfaces of the core sip package plus the observation event handler
// contract of [github.com/ghettovoice/gosip/sip/proxy]. One recorder serves
// both roles at once: register the middleware role with [sip.Element.UseMiddleware] on
// the element, and bind the observer role with [proxy.Proxy.BindEventHandler] on the
// proxy. [Recorder.Report] produces a JSON-serializable [Report]
// carrying the Transports, Transactions, and Proxy counter sections.
package stats
