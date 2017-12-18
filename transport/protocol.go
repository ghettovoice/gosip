package transport

import (
	"fmt"
	"strings"
	"time"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
)

const (
	netErrRetryTime = 5 * time.Second
	sockTTL         = time.Hour
)

// Protocol implements network specific features.
type Protocol interface {
	log.LocalLogger
	core.Awaiting
	Network() string
	Reliable() bool
	Streamed() bool
	Listen(target *Target) error
	Send(target *Target, msg core.Message) error
	String() string
}

type ProtocolFactory func(
	network string,
	output chan<- core.Message,
	errs chan<- error,
	cancel <-chan struct{},
) (Protocol, error)

type protocol struct {
	logger   log.LocalLogger
	network  string
	reliable bool
	streamed bool
}

func (pr *protocol) SetLog(logger log.Logger) {
	pr.logger.SetLog(logger.WithFields(map[string]interface{}{
		"protocol": pr.String(),
	}))
}

func (pr *protocol) Log() log.Logger {
	return pr.logger.Log()
}

func (pr *protocol) String() string {
	if pr == nil {
		return "Protocol <nil>"
	}

	return fmt.Sprintf("Protocol %p (net %s)", pr, pr.Network())
}

func (pr *protocol) Network() string {
	return strings.ToUpper(pr.network)
}

func (pr *protocol) Reliable() bool {
	return pr.reliable
}

func (pr *protocol) Streamed() bool {
	return pr.streamed
}
