package dialog

import (
	"sync"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// ID identifies a SIP dialog by its Call-ID and role-relative endpoint
// tags: the local tag belongs to the endpoint holding the [Dialog] and the
// remote tag to its peer. Tags compare byte for byte.
type ID struct {
	CallID              string
	LocalTag, RemoteTag string
}

// Role is the endpoint role of the local party in a dialog.
type Role uint8

const (
	// RoleUAC is the caller side that sent the initial INVITE.
	RoleUAC Role = iota + 1
	// RoleUAS is the callee side that received the initial INVITE.
	RoleUAS
)

// State is the lifecycle state of a [Dialog].
type State uint8

const (
	// StateEarly is a dialog established by a tagged provisional response.
	StateEarly State = iota + 1
	// StateConfirmed is a dialog established or confirmed by a 2xx response.
	StateConfirmed
	// StateTerminated is a dialog explicitly ended by its consumer.
	StateTerminated
)

// View is a detached snapshot of a [Dialog]. Mutating a view does not
// affect the dialog it was taken from. A zero LocalCSeq or RemoteCSeq
// means that counter has not been initialized.
type View struct {
	ID                    ID
	Role                  Role
	State                 State
	Local, Remote         header.NameAddr
	LocalCSeq, RemoteCSeq uint
	RemoteTarget          *sip.URI
	RouteSet              []header.RouteHop
}

// Dialog is a single SIP dialog leg created by an INVITE exchange.
//
// Create a Dialog only with [NewUAC] or [NewUAS]; a Dialog must not be
// copied. Its identity, role, parties, initial INVITE CSeq number, and
// transaction key are immutable after construction. All mutable state is
// serialized by one mutex, and no method performs I/O, runs timers, or
// invokes callbacks: state changes never send anything on the wire.
type Dialog struct {
	id   ID
	role Role

	loc, rmt header.NameAddr
	target   *sip.URI
	cseqNum  uint
	key      sip.ClientTransactionKey

	mu      sync.Mutex
	state   State
	routes  []header.RouteHop
	locCSeq uint
	rmtCSeq uint
}

// ID returns the dialog's immutable identity.
func (d *Dialog) ID() ID { return d.id }

// View returns a detached snapshot of the dialog's current state. The
// party addresses, remote target, and route hops are clones, so mutating
// them does not affect the dialog.
func (d *Dialog) View() View {
	d.mu.Lock()
	v := View{
		ID:         d.id,
		Role:       d.role,
		State:      d.state,
		LocalCSeq:  d.locCSeq,
		RemoteCSeq: d.rmtCSeq,
		RouteSet:   d.routes,
	}
	d.mu.Unlock()

	v.Local = d.loc.Clone()
	v.Remote = d.rmt.Clone()
	v.RouteSet = cloneRouteSet(v.RouteSet)
	if d.target != nil {
		if u, ok := d.target.Clone().(*sip.URI); ok {
			v.RemoteTarget = u
		}
	}
	return v
}

// Confirm processes a response to the dialog's initial INVITE. The
// candidate is fully validated before the dialog state is examined, so a
// malformed or unrelated response reports its own error even on a
// terminated dialog.
//
// It returns (true, nil) the first time a valid 2xx confirms an early
// dialog, and (false, nil) for a matching tagged provisional response or a
// repeated same-leg 2xx, which change nothing. [ErrInvalidMessage] reports
// a malformed response, [ErrNotEstablishing] a response that cannot
// establish a dialog (100 Trying, an untagged provisional, or an
// unsuccessful final response), [ErrMismatch] a well-formed response
// belonging to another fork or INVITE attempt, and [ErrTerminated] a valid
// matched response after termination. Failed calls commit no state.
func (d *Dialog) Confirm(res *sip.Response) (bool, error) {
	est, err := validRes(res)
	if err != nil {
		return false, errors.Wrap(err)
	}

	localTag, remoteTag := est.fromTag, est.toTag
	if d.role == RoleUAS {
		localTag, remoteTag = est.toTag, est.fromTag
	}
	if est.callID != d.id.CallID ||
		localTag != d.id.LocalTag ||
		remoteTag != d.id.RemoteTag ||
		est.cseqNum != d.cseqNum ||
		!est.key.Equal(d.key) {
		return false, errors.PrefixWrap(ErrMismatch, "response does not match the dialog")
	}

	// Validate the final response's routes before changing state on either
	// role. Only the UAC installs this route set; the UAS keeps the INVITE's.
	var routes []header.RouteHop
	if !est.early {
		routes, err = recRouteSet(res.Headers, d.role == RoleUAC)
		if err != nil {
			return false, errors.Wrap(err)
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state == StateTerminated {
		return false, errors.PrefixWrap(ErrTerminated)
	}
	if est.early || d.state != StateEarly {
		return false, nil
	}
	if d.role == RoleUAC {
		d.routes = routes
	}
	d.state = StateConfirmed
	return true, nil
}

// Terminate ends the dialog. It is idempotent, sends nothing on the wire,
// and leaves the immutable identity intact for later inspection. It
// returns whether this call changed the state.
func (d *Dialog) Terminate() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.state == StateTerminated {
		return false
	}
	d.state = StateTerminated
	return true
}

// IDFromRequest extracts the dialog identity of an incoming in-dialog
// request: the To tag is local and the From tag remote. It returns
// [ErrInvalidMessage] when the required headers or tags are unusable. It
// only reads the identity fields; Contact, Via, and CSeq are not required.
func IDFromRequest(req *sip.Request) (ID, error) {
	if req == nil {
		return ID{}, newInvalidMsgErr("nil request")
	}
	callID, err := singleCallID(req.Headers)
	if err != nil {
		return ID{}, errors.Wrap(err)
	}
	from, err := singleFrom(req.Headers)
	if err != nil {
		return ID{}, errors.Wrap(err)
	}
	to, err := singleTo(req.Headers)
	if err != nil {
		return ID{}, errors.Wrap(err)
	}
	fromTag, present, err := tagParam(from.Params)
	if err != nil {
		return ID{}, errors.Wrap(err)
	}
	if !present {
		return ID{}, newInvalidMsgErr("missing From tag")
	}
	toTag, present, err := tagParam(to.Params)
	if err != nil {
		return ID{}, errors.Wrap(err)
	}
	if !present {
		return ID{}, newInvalidMsgErr("missing To tag")
	}
	return ID{CallID: callID, LocalTag: toTag, RemoteTag: fromTag}, nil
}

// IDFromResponse extracts the dialog identity of a response to the local
// initial INVITE: the From tag is local and the To tag remote. It returns
// [ErrInvalidMessage] when the required headers or tags are unusable or the
// response is not for an INVITE.
func IDFromResponse(res *sip.Response) (ID, error) {
	if res == nil {
		return ID{}, newInvalidMsgErr("nil response")
	}
	callID, err := singleCallID(res.Headers)
	if err != nil {
		return ID{}, errors.Wrap(err)
	}
	from, err := singleFrom(res.Headers)
	if err != nil {
		return ID{}, errors.Wrap(err)
	}
	to, err := singleTo(res.Headers)
	if err != nil {
		return ID{}, errors.Wrap(err)
	}
	fromTag, present, err := tagParam(from.Params)
	if err != nil {
		return ID{}, errors.Wrap(err)
	}
	if !present {
		return ID{}, newInvalidMsgErr("missing From tag")
	}
	toTag, present, err := tagParam(to.Params)
	if err != nil {
		return ID{}, errors.Wrap(err)
	}
	if !present {
		return ID{}, newInvalidMsgErr("missing To tag")
	}
	if _, err := singleCSeq(res.Headers); err != nil {
		return ID{}, errors.Wrap(err)
	}
	return ID{CallID: callID, LocalTag: fromTag, RemoteTag: toTag}, nil
}
