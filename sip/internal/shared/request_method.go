package shared

import (
	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

const (
	RequestMethodAck       RequestMethod = "ACK"
	RequestMethodBye       RequestMethod = "BYE"
	RequestMethodCancel    RequestMethod = "CANCEL"
	RequestMethodInfo      RequestMethod = "INFO"
	RequestMethodInvite    RequestMethod = "INVITE"
	RequestMethodMessage   RequestMethod = "MESSAGE"
	RequestMethodNotify    RequestMethod = "NOTIFY"
	RequestMethodOptions   RequestMethod = "OPTIONS"
	RequestMethodPrack     RequestMethod = "PRACK"
	RequestMethodPublish   RequestMethod = "PUBLISH"
	RequestMethodRefer     RequestMethod = "REFER"
	RequestMethodRegister  RequestMethod = "REGISTER"
	RequestMethodSubscribe RequestMethod = "SUBSCRIBE"
	RequestMethodUpdate    RequestMethod = "UPDATE"
)

type RequestMethod string

func (m RequestMethod) ToUpper() RequestMethod { return stringutils.UCase(m) }

func (m RequestMethod) ToLower() RequestMethod { return stringutils.LCase(m) }

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
	return stringutils.UCase(m) == stringutils.UCase(other)
}
