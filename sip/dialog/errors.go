package dialog

import (
	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip"
)

// Dialog errors.
const (
	// ErrInvalidMessage reports a message that cannot supply the required
	// dialog fields: nil input, missing headers, or empty Call-ID or tag.
	ErrInvalidMessage errors.Error = "invalid message"
	// ErrNotEstablishing reports a well-formed message that cannot
	// establish or confirm a dialog, such as an untagged provisional or an
	// unsuccessful final response.
	ErrNotEstablishing errors.Error = "non-establishing message"
	// ErrMismatch reports a well-formed message whose Call-ID, tags,
	// sequence number, or transaction attempt belongs to another dialog.
	ErrMismatch errors.Error = "dialog mismatch"
	// ErrTerminated reports an operation on an already terminated dialog.
	ErrTerminated errors.Error = "dialog terminated"
	// ErrUnsupportedMethod reports a request method that must not be sent
	// as an ordinary in-dialog request: ACK, CANCEL, INVITE, or UPDATE.
	ErrUnsupportedMethod errors.Error = "unsupported method"
	// ErrCSeqExhausted reports that the local CSeq counter cannot
	// allocate another sequence number.
	ErrCSeqExhausted errors.Error = "cseq exhausted"
	// ErrCSeqOutOfOrder reports an inbound in-dialog request whose CSeq
	// number is lower than the remote baseline already seen.
	ErrCSeqOutOfOrder errors.Error = "cseq out of order"
	// ErrInvalidState reports an operation that is not defined for the
	// dialog's current role and lifecycle state.
	ErrInvalidState errors.Error = "invalid dialog state"
)

// newInvalidMsgErr returns an [ErrInvalidMessage] error classified as a
// SIP message error, with the optional detail attached.
func newInvalidMsgErr(args ...any) error {
	return errclass.Classify(
		errors.GetCaller().Prefix(ErrInvalidMessage, args...),
		sip.ErrClassMessage,
	)
}
