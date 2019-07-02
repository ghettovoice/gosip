// Forked from github.com/StefanKopieczek/gossip by @StefanKopieczek
package util

import (
	"github.com/ghettovoice/gosip/log"
)

// The buffer size of the primitive input and output chans.
const c_ELASTIC_CHANSIZE = 3

// A dynamic channel that does not block on send, but has an unlimited buffer capacity.
// ElasticChan uses a dynamic slice to buffer signals received on the input channel until
// the output channel is ready to process them.
type ElasticChan struct {
	In      chan interface{}
	Out     chan interface{}
	buffer  []interface{}
	stopped bool
	logger  log.LocalLogger
	done    chan struct{}
}

// Initialise the Elastic channel, and start the management goroutine.
func (c *ElasticChan) Init() {
	c.In = make(chan interface{}, c_ELASTIC_CHANSIZE)
	c.Out = make(chan interface{}, c_ELASTIC_CHANSIZE)
	c.buffer = make([]interface{}, 0)
	c.logger = log.NewSafeLocalLogger()
	c.done = make(chan struct{})
}

func (c *ElasticChan) Run() {
	go c.manage()
}

func (c *ElasticChan) Stop() {
	select {
	case <-c.done:
		return
	default:
	}
	c.Log().Debugf("stopping ElasticChan %p", c)
	close(c.In)
	<-c.done
	c.Log().Debugf("ElasticChan %p stopped", c)
}

func (c *ElasticChan) Log() log.Logger {
	return c.logger.Log()
}

func (c *ElasticChan) SetLog(logger log.Logger) {
	c.logger.SetLog(logger)
}

// Poll for input from one end of the channel and add it to the buffer.
// Also poll sending buffered signals out over the output chan.
// TODO: add cancel chan
func (c *ElasticChan) manage() {
	defer close(c.done)

loop:
	for {
		if len(c.buffer) > 0 {
			// The buffer has something in it, so try to send as well as
			// receive.
			// (Receive first in order to minimize blocked Send() calls).
			select {
			case in, ok := <-c.In:
				if !ok {
					c.Log().Debugf("ElasticChan %p will dispose", c)
					break loop
				}
				// c.Log().Debugf("ElasticChan %p gets '%v'", c, in)
				c.buffer = append(c.buffer, in)
			case c.Out <- c.buffer[0]:
				// c.Log().Debugf("ElasticChan %p sends '%v'", c, c.buffer[0])
				c.buffer = c.buffer[1:]
			}
		} else {
			// The buffer is empty, so there's nothing to send.
			// Just wait to receive.
			in, ok := <-c.In
			if !ok {
				c.Log().Debugf("ElasticChan %p will dispose", c)
				break loop
			}
			// c.Log().Debugf("ElasticChan %p gets '%v'", c, in)
			c.buffer = append(c.buffer, in)
		}
	}

	c.dispose()
}

func (c *ElasticChan) dispose() {
	c.Log().Debugf("ElasticChan %p disposing...", c)
	for len(c.buffer) > 0 {
		select {
		case c.Out <- c.buffer[0]:
			c.buffer = c.buffer[1:]
		default:
		}
	}
	c.Log().Debugf("ElasticChan %p disposed", c)
}
