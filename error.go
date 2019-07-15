package gosip

import "fmt"

const (
	Unknown = iota
	RequestCanceled
)

type Error interface {
	Prefix(prefix string)
	Code() int
	Canceled() bool
}

type gserror struct {
	msg  string
	code int
}

func (err *gserror) Error() string {
	if err == nil {
		return "<nil>"
	}

	return fmt.Sprintf("SIP error (%d): %s", err.code, err.msg)
}

func (err *gserror) String() string {
	if err == nil {
		return "<nil>"
	}

	return err.String()
}

func (err *gserror) Code() int {
	return err.code
}

func (err *gserror) Canceled() bool {
	if err == nil {
		return false
	}
	return err.code == RequestCanceled
}

func (err *gserror) Prefix(prefix string) {
	err.msg = prefix + err.msg
}
