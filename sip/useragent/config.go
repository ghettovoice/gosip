package useragent

import (
	"context"
	"log/slog"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

// Default bounds applied by [New] when the corresponding option is left at
// its zero value.
const (
	// defMaxAuthAttempts is the default per-operation authentication retry
	// limit.
	defMaxAuthAttempts = 3
	// defRedirectMaxDepth is the default bound on followed redirect hops.
	defRedirectMaxDepth = 5
	// defRedirectMaxAttempts is the default bound on total request sends
	// across a redirect chain, including the initial send.
	defRedirectMaxAttempts = 10
	// defRedirectTimeout is the default shared deadline of a redirect
	// chain; it matches [sip.TimeD], the maximum duration a message
	// remains in the network.
	defRedirectTimeout = 32 * time.Second
)

// Options configures a [UserAgent].
type Options struct {
	// AOR is the local identity URI carried into outgoing
	// requests. It is required and must be a valid SIP URI.
	AOR *sip.URI
	// Contact is the URI carried in the Contact header of outgoing
	// OPTIONS requests — the only place this slice stamps one: a
	// MESSAGE request never carries Contact, and inbound-request
	// responses carry none. Nil emits no Contact header.
	Contact *sip.URI
	// Authenticator supplies credentials answering 401/407 challenges.
	// Nil disables authentication retry: a challenge response is the
	// final outcome.
	Authenticator sip.ClientAuthenticator
	// RedirectPolicy bounds automatic 3xx following. Nil disables
	// redirect following: a redirect response is the final outcome.
	RedirectPolicy *RedirectPolicy
	// MaxAuthAttempts bounds the number of authentication retries per
	// operation. Zero selects the default; negative is invalid.
	MaxAuthAttempts int
	// DisableOptionsAutoAnswer turns off the automatic 200 capabilities
	// answer to inbound OPTIONS requests no handler claims.
	DisableOptionsAutoAnswer bool
	// OnMessage handles inbound MESSAGE requests. Nil means MESSAGE is
	// not claimed and passes to the next interceptor.
	OnMessage RequestHandler
	// OnOptions handles inbound OPTIONS requests, overriding the
	// auto-answer. Nil falls back to the auto-answer unless disabled.
	OnOptions RequestHandler
	// Logger is the logger handed to the exchange's client
	// transactions. Nil lets each transaction inherit the element's
	// logger.
	Logger *slog.Logger
}

// RedirectPolicy bounds automatic 3xx following by an operation. Every
// limit is explicit: a redirect chain never exceeds the configured depth,
// total sends, or shared deadline, a visited-target set detects loops, and
// AllowTarget gates each new target before credentials are recomputed.
type RedirectPolicy struct {
	// MaxDepth bounds the number of followed redirect hops. Zero selects
	// the default; negative is invalid.
	MaxDepth int
	// MaxAttempts bounds the total number of request sends, including the
	// initial send. Zero selects the default; negative is invalid.
	MaxAttempts int
	// Timeout bounds the whole redirect chain with a shared deadline.
	// Zero selects the default; negative is invalid.
	Timeout time.Duration
	// AllowTarget decides whether the redirect target may be followed; it
	// runs before credentials are recomputed for the new target. It is
	// required: [New] rejects a policy without it.
	AllowTarget func(context.Context, *sip.RequestEnvelope, *sip.URI) (bool, error)
}

// New creates a [UserAgent] component.
//
// The options are validated and copied: later changes to the caller's
// URIs or policy do not affect the running user agent. The constructor
// binds nothing: an explicit [sip.Element.UseComponent] registration
// attaches the user agent to an element's inbound chain and capabilities.
//
// Options are optional: no value selects the zero [Options], and when
// several values are supplied the last whole value wins — option fields
// are not merged.
func New(opts ...Options) (*UserAgent, error) {
	o := util.LastSliceElem(opts, Options{})

	if err := o.validate(); err != nil {
		return nil, errors.Wrap(err)
	}
	o.normalize()

	return &UserAgent{opts: o}, nil
}

// validate reports whether the options describe a usable user agent.
func (o *Options) validate() error {
	if o.AOR == nil {
		return errors.Wrap(NewInvalidConfigError("missing address of record"))
	}
	if !o.AOR.IsValid() {
		return errors.Wrap(NewInvalidConfigError("invalid address of record %q", o.AOR))
	}
	if o.Contact != nil && !o.Contact.IsValid() {
		return errors.Wrap(NewInvalidConfigError("invalid contact %q", o.Contact))
	}
	if o.MaxAuthAttempts < 0 {
		return errors.Wrap(NewInvalidConfigError("negative max auth attempts %d", o.MaxAuthAttempts))
	}
	if o.RedirectPolicy != nil {
		return errors.Wrap(o.RedirectPolicy.validate())
	}
	return nil
}

// normalize replaces caller-owned option values with user-agent-owned
// copies and applies the documented defaults. It runs only after
// [Options.validate] accepted the options.
func (o *Options) normalize() {
	o.AOR = o.AOR.Clone().(*sip.URI) //nolint:forcetypeassert // Clone preserves the concrete type
	if o.Contact != nil {
		o.Contact = o.Contact.Clone().(*sip.URI) //nolint:forcetypeassert // Clone preserves the concrete type
	}
	if o.MaxAuthAttempts == 0 {
		o.MaxAuthAttempts = defMaxAuthAttempts
	}
	if o.RedirectPolicy != nil {
		p := *o.RedirectPolicy
		p.normalize()
		o.RedirectPolicy = &p
	}
}

// validate reports whether the policy bounds are usable.
func (p *RedirectPolicy) validate() error {
	if p.AllowTarget == nil {
		return errors.Wrap(NewInvalidConfigError("redirect policy: nil target check"))
	}
	if p.MaxDepth < 0 {
		return errors.Wrap(NewInvalidConfigError("redirect policy: negative max depth %d", p.MaxDepth))
	}
	if p.MaxAttempts < 0 {
		return errors.Wrap(NewInvalidConfigError("redirect policy: negative max attempts %d", p.MaxAttempts))
	}
	if p.Timeout < 0 {
		return errors.Wrap(NewInvalidConfigError("redirect policy: negative timeout %s", p.Timeout))
	}
	return nil
}

// normalize applies the documented defaults to a validated policy copy.
func (p *RedirectPolicy) normalize() {
	if p.MaxDepth == 0 {
		p.MaxDepth = defRedirectMaxDepth
	}
	if p.MaxAttempts == 0 {
		p.MaxAttempts = defRedirectMaxAttempts
	}
	if p.Timeout == 0 {
		p.Timeout = defRedirectTimeout
	}
}
