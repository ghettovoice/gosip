package transport

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/syntax"
)

const (
	netErrRetryTime = 5 * time.Second
)

// Protocol implements network specific transport features.
type Protocol interface {
	log.WithLogger
	Network() string
	IsReliable() bool
	IsStream() bool
	Listen(target *Target) error
	Send(target *Target, msg core.Message) error
	String() string
}

type protocol struct {
	log      log.Logger
	ctx      context.Context
	network  string
	reliable bool
	stream   bool
	output   chan<- *IncomingMessage
	errs     chan<- error
}

func (pr *protocol) init(
	ctx context.Context,
	network string,
	reliable bool,
	stream bool,
	output chan<- *IncomingMessage,
	errs chan<- error,
) {
	pr.ctx = ctx
	pr.network = network
	pr.reliable = reliable
	pr.stream = stream
	pr.output = output
	pr.errs = errs
	pr.SetLog(log.StandardLogger())
}

func (pr *protocol) SetLog(logger log.Logger) {
	pr.log = logger.WithFields(map[string]interface{}{
		"protocol": pr.String(),
	})
}

func (pr *protocol) String() string {
	var name, network string
	if pr == nil {
		name = "<nil>"
		network = ""
	} else {
		name = fmt.Sprintf("%p", pr)
		network = pr.Network() + " "
	}

	return fmt.Sprintf("%sprotocol %p", network, name)
}

func (pr *protocol) Log() log.Logger {
	return pr.log
}

func (pr *protocol) Network() string {
	return strings.ToUpper(pr.network)
}

func (pr *protocol) IsReliable() bool {
	return pr.reliable
}

func (pr *protocol) IsStream() bool {
	return pr.stream
}
