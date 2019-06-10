package gosip

import "fmt"

const (
	Unknown = iota
	RequestCanceled
	TransactionTerminated
)

type Error struct {
	Message string
	Code    int
}

func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}

	return fmt.Sprintf("SIP error (%d): %s", err.Code, err.Message)
}

func (err *Error) String() string {
	if err == nil {
		return "<nil>"
	}

	return err.String()
}
