package sip

import "fmt"

type RequestError struct {
	Request string
	Code    uint
	Reason  string
}

func (err *RequestError) Error() string {
	if err == nil {
		return "<nil>"
	}

	reason := err.Reason
	if err.Code != 0 {
		reason += fmt.Sprintf(" (Code %d)", err.Code)
	}

	return fmt.Sprintf("request '%s' failed with reason '%s'", err.Request, reason)
}
