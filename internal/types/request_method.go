package types

import (
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

const (
	RequestMethodAck       RequestMethod = "ACK"
	RequestMethodBye       RequestMethod = "BYE"
	RequestMethodCancel    RequestMethod = "CANCEL"
	RequestMethodInfo      RequestMethod = "INFO" // RFC 6086
	RequestMethodInvite    RequestMethod = "INVITE"
	RequestMethodMessage   RequestMethod = "MESSAGE" // RFC 3428
	RequestMethodNotify    RequestMethod = "NOTIFY"  // RFC 6665
	RequestMethodOptions   RequestMethod = "OPTIONS"
	RequestMethodPrack     RequestMethod = "PRACK"   // RFC 3262
	RequestMethodPublish   RequestMethod = "PUBLISH" // RFC 3903
	RequestMethodRefer     RequestMethod = "REFER"   // RFC 3515
	RequestMethodRegister  RequestMethod = "REGISTER"
	RequestMethodSubscribe RequestMethod = "SUBSCRIBE" // RFC 6665
	RequestMethodUpdate    RequestMethod = "UPDATE"    // RFC 3311
)

type RequestMethod string

func (m RequestMethod) ToUpper() RequestMethod { return util.UCase(m) }

func (m RequestMethod) ToLower() RequestMethod { return util.LCase(m) }

func (m RequestMethod) IsValid() bool { return grammar.IsToken(m) }

func (m RequestMethod) Equal(val any) bool {
	var other RequestMethod
	switch v := val.(type) {
	case RequestMethod:
		other = v
	case *RequestMethod:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return util.EqFold(m, other)
}

func IsKnownRequestMethod[T ~string](mtd T) bool {
	switch util.UCase(RequestMethod(mtd)) {
	case RequestMethodAck,
		RequestMethodBye,
		RequestMethodCancel,
		RequestMethodInfo,
		RequestMethodInvite,
		RequestMethodMessage,
		RequestMethodNotify,
		RequestMethodOptions,
		RequestMethodPrack,
		RequestMethodPublish,
		RequestMethodRefer,
		RequestMethodRegister,
		RequestMethodSubscribe,
		RequestMethodUpdate:
		return true
	}
	return false
}
