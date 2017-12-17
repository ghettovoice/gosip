package txs

import (
	"fmt"
	"time"
)

const (
	T1      = 500 * time.Millisecond
	T2      = 4 * time.Second
	T4      = 5 * time.Second
	Timer_A = T1
	Timer_B = 64 * T1
	Timer_D = 32 * time.Second
	Timer_H = 64 * T1
)

type Error interface {
}

type UnexpectedMessageError struct {
	Err error
	Msg string
}

func (err *UnexpectedMessageError) Broken() bool    { return false }
func (err *UnexpectedMessageError) Malformed() bool { return false }
func (err *UnexpectedMessageError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "UnexpectedMessageError: " + err.Err.Error()
	if err.Msg != "" {
		s += fmt.Sprintf("\nMessage dump:\n%s", err.Msg)
	}

	return s
}
