package sip

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/internal/utils"
)

type transportBase struct {
	TransportOptions
	proto string

	mu      sync.Mutex
	lsPorts map[uint16]struct{}
	lss     map[*net.Listener]struct{}
	conns   map[string]*connection

	lssWg   sync.WaitGroup
	connsWg sync.WaitGroup
	closing atomic.Bool
}

func (tp *transportBase) serveStream(ls net.Listener, onMsg func(Message)) error {
	if ls == nil {
		return errors.New("listener is nil")
	}
	if onMsg == nil {
		return errors.New("message handler is nil")
	}

	ls = &closeOnceListener{Listener: ls}
	tp.trackListener(&ls, true)
	defer func() {
		tp.trackListener(&ls, false)
		ls.Close()
	}()

	logger := tp.GetLog().WithFields(map[string]any{
		TransportField: tp.proto,
		LocalAddrField: ls.Addr(),
	})

	var tempDelay time.Duration
	for {
		c, err := ls.Accept()
		if err != nil {
			if utils.IsTemporaryErr(err) {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := time.Second; tempDelay > max {
					tempDelay = max
				}
				logger.Warn(
					"failed to accept inbound connection due to the temporary error; continue serving the listener...",
					map[string]any{
						"error":       err,
						"retry_after": tempDelay,
					},
				)
				time.Sleep(tempDelay)
				continue
			}
			return err
		}

		logger.Debug("inbound connection accepted", map[string]any{
			RemoteAddrField: c.RemoteAddr(),
		})

		opts := tp.TransportOptions
		opts.Log = logger.WithFields(map[string]any{
			RemoteAddrField: c.RemoteAddr(),
		})
		conn := newConn(&closeOnceConn{Conn: c}, tp.proto, onMsg, opts)
		tp.trackConn(conn, true, false)
		go func() {
			defer func() {
				tp.trackConn(conn, false, false)
				freeConn(conn)
			}()
			conn.serve()
		}()
	}
}

func (tp *transportBase) servePacket(c net.PacketConn, onMsg func(Message)) error {
	if c == nil {
		return errors.New("connection is nil")
	}
	if onMsg == nil {
		return errors.New("message handler is nil")
	}

	opts := tp.TransportOptions
	opts.ConnTTL = -1 // unlimited
	opts.Log = tp.GetLog().WithFields(map[string]any{
		TransportField: tp.proto,
		LocalAddrField: c.LocalAddr(),
	})
	conn := newConn(&closeOncePacketConn{PacketConn: c}, tp.proto, onMsg, opts)
	tp.trackConn(conn, true, true)
	defer func() {
		tp.trackConn(conn, false, true)
		freeConn(conn)
	}()
	return conn.serve()
}

func (tp *transportBase) close() error {
	tp.closing.Store(true)

	var errs []error

	tp.mu.Lock()
	for ls := range tp.lss {
		errs = append(errs, (*ls).Close())
	}
	tp.mu.Unlock()
	tp.lssWg.Wait()

	tp.mu.Lock()
	for _, c := range tp.conns {
		errs = append(errs, c.close())
	}
	tp.mu.Unlock()
	tp.connsWg.Wait()

	return errors.Join(errs...)
}

func (tp *transportBase) trackListener(ls *net.Listener, add bool) bool {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	_, port, _ := net.SplitHostPort((*ls).Addr().String())
	iport, _ := strconv.Atoi(port)
	if add {
		if tp.closing.Load() {
			return false
		}
		if tp.lss == nil {
			tp.lss = make(map[*net.Listener]struct{})
		}
		tp.lss[ls] = struct{}{}
		tp.addLsPort(uint16(iport))
		tp.lssWg.Add(1)
	} else {
		if tp.lss != nil {
			delete(tp.lss, ls)
		}
		tp.delLsPort(uint16(iport))
		tp.lssWg.Done()
	}
	return true
}

func (tp *transportBase) trackConn(c *connection, add, memPort bool) bool {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	laddr := c.locAddr().String()
	_, port, _ := net.SplitHostPort(laddr)
	iport, _ := strconv.Atoi(port)
	if add {
		if tp.closing.Load() {
			return false
		}
		if tp.conns == nil {
			tp.conns = make(map[string]*connection)
		}
		keys := []string{laddr}
		if raddr := c.rmtAddr(); raddr != nil {
			keys = append(keys, raddr.String())
		}
		for _, key := range keys {
			tp.conns[key] = c
		}
		if memPort {
			tp.addLsPort(uint16(iport))
		}
		tp.connsWg.Add(1)
	} else {
		if tp.conns != nil {
			keys := []string{laddr}
			if raddr := c.rmtAddr(); raddr != nil {
				keys = append(keys, raddr.String())
			}
			for _, key := range keys {
				delete(tp.conns, key)
			}
		}
		tp.delLsPort(uint16(iport))
		tp.connsWg.Done()
	}
	return true
}

func (tp *transportBase) addLsPort(port uint16) {
	if tp.lsPorts == nil {
		tp.lsPorts = make(map[uint16]struct{})
	}
	tp.lsPorts[port] = struct{}{}
}

func (tp *transportBase) delLsPort(port uint16) {
	if tp.lsPorts == nil {
		return
	}
	delete(tp.lsPorts, port)
}

var connPool = sync.Pool{
	New: func() any { return &connection{} },
}

func newConn(conn any, proto string, onMsg func(Message), opts TransportOptions) *connection {
	c := connPool.Get().(*connection)
	c.conn = conn
	c.proto = proto
	c.onMsg = onMsg
	c.TransportOptions = opts
	return c
}

func freeConn(c *connection) {
	c.conn = nil
	c.proto = ""
	c.onMsg = nil
	c.Parser = nil
	c.SentByHost = ""
	c.Log = nil
	connPool.Put(c)
}

type connection struct {
	conn  any
	proto string
	onMsg func(msg Message)
	TransportOptions

	mu       sync.Mutex
	ttlTimer *time.Timer
}

func (c *connection) locAddr() net.Addr {
	switch conn := c.conn.(type) {
	case net.PacketConn:
		return conn.LocalAddr()
	case net.Conn:
		return conn.LocalAddr()
	default:
		panic(fmt.Errorf("unexpected connection type %T", conn))
	}
}

func (c *connection) rmtAddr() net.Addr {
	switch conn := c.conn.(type) {
	case net.PacketConn:
		return nil
	case net.Conn:
		return conn.RemoteAddr()
	default:
		panic(fmt.Errorf("unexpected connection type %T", conn))
	}
}

func (c *connection) close() error {
	switch conn := c.conn.(type) {
	case net.PacketConn:
		return conn.Close()
	case net.Conn:
		return conn.Close()
	default:
		panic(fmt.Errorf("unexpected connection type %T", conn))
	}
}

func (c *connection) serve() error {
	defer func() {
		if err := recover(); err != nil {
			c.GetLog().Error("panic occurred while serving the connection", map[string]any{
				"error": err,
				"~dump": utils.GetStack4,
			})
		}
		c.close()
	}()

	c.updateTTL()
	switch c.conn.(type) {
	case net.PacketConn:
		return c.servePacket()
	case net.Conn:
		return c.serveStream()
	default:
		panic(fmt.Errorf("unexpected connection type %T", c.conn))
	}
}

func (c *connection) serveStream() error {
	rdr := &io.LimitedReader{R: c.conn.(net.Conn), N: maxMsgSize}
	prs := c.GetParser().ParseStream(rdr)
	for msg, err := range prs.Messages() {
		if err != nil {
			// If the error is due to exceeding the message size limit, we should continue parsing the connection.
			if (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) && rdr.N <= 0 {
				rdr.N = maxMsgSize
				// If a message was partially read, log a warning.
				// Then, if the message is a request, try to respond 400 Bad Request.
				if msg != nil {
					c.GetLog().Warn(fmt.Sprintf(
						"failed to read inbound message due to exceeding the message size limit %d; continue serving the connection...",
						maxMsgSize,
					), nil)
					if req, ok := msg.(*Request); ok {
						if res, err := BuildResponse(req, ResponseStatusRequestEntityTooLarge, ""); err == nil {
							c.writeMsg(res, nil)
						}
					}
				}
				continue
			}
			// And also, any grammar errors should not break the parsing loop.
			if utils.IsGrammarErr(err) {
				rdr.N = maxMsgSize
				// If a message was partially read, log a warning.
				// Then, if the message is a request, try to respond 400 Bad Request.
				if msg != nil {
					c.GetLog().Warn("failed to parse inbound message; continue serving the connection...", map[string]any{
						"error": err,
					})
					if req, ok := msg.(*Request); ok {
						if res, err := BuildResponse(req, ResponseStatusBadRequest, ""); err == nil {
							c.writeMsg(res, nil)
						}
					}
				}
				continue
			}
			return err
		}

		rdr.N = maxMsgSize
		c.handleMsg(msg, c.conn.(net.Conn).RemoteAddr())
	}
	return nil
}

func (c *connection) servePacket() error {
	var tempDelay time.Duration
	buf := make([]byte, maxMsgSize)
	for {
		num, raddr, err := c.conn.(net.PacketConn).ReadFrom(buf)
		if err != nil {
			if utils.IsTemporaryErr(err) {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := time.Second; tempDelay > max {
					tempDelay = max
				}
				c.GetLog().Warn(
					"failed to read inbound message due to the temporary error; continue serving the connection...",
					map[string]any{
						RemoteAddrField: raddr,
						"error":         err,
						"retry_after":   tempDelay,
					},
				)
				time.Sleep(tempDelay)
				continue
			}
			return err
		}

		msg, err := c.GetParser().ParsePacket(buf[:num])
		if err != nil {
			// All errors from parser are considered continuable.
			// If a message was partially read, log a warning.
			// Then, if the message is a request, try to respond 400 Bad Request.
			if msg != nil {
				c.GetLog().Warn("failed to parse inbound message; continue serving the connection...", map[string]any{
					RemoteAddrField: raddr,
					"error":         err,
				})
				if req, ok := msg.(*Request); ok {
					if res, err := BuildResponse(req, ResponseStatusBadRequest, ""); err == nil {
						c.writeMsg(res, raddr)
					}
				}
			}
			continue
		}

		c.handleMsg(msg, raddr)
	}
}

func (c *connection) handleMsg(msg Message, rmtAddr net.Addr) {
	if !utils.IsValid(msg) {
		c.GetLog().Warn("inbound message is invalid; discarding it...", map[string]any{
			RemoteAddrField: rmtAddr,
		})
		if req, ok := msg.(*Request); ok {
			if res, err := BuildResponse(req, ResponseStatusBadRequest, ""); err == nil {
				c.writeMsg(res, rmtAddr)
			}
		}
		return
	}

	c.updateTTL()

	msg.SetMessageMetadata(Metadata{
		TransportField:     c.proto,
		LocalAddrField:     c.locAddr().String(),
		RemoteAddrField:    rmtAddr.String(),
		RequestTstampField: time.Now(),
	})

	_, viaHop := utils.IterFirst2(msg.MessageHeaders().ViaHops())
	lflds := map[string]any{
		RemoteAddrField:   rmtAddr,
		"message_via":     viaHop,
		"message_from":    msg.MessageHeaders().From(),
		"message_to":      msg.MessageHeaders().To(),
		"message_call_id": msg.MessageHeaders().CallID(),
		"message_cseq":    msg.MessageHeaders().CSeq(),
	}
	switch m := msg.(type) {
	case *Request:
		// RFC 3261 Section 18.2.1.
		rhost, rport, err := net.SplitHostPort(rmtAddr.String())
		if err != nil {
			panic(fmt.Errorf("parse of the remote address %q: %w", rmtAddr, err))
		}
		if viaHop.Addr.IP() == nil || !viaHop.Addr.IP().Equal(net.ParseIP(rhost)) {
			viaHop.Params.Set("received", rhost)
		}
		if _, ok := c.conn.(net.PacketConn); ok {
			// RFC 3581 Section 4.
			if viaHop.Params.Has("rport") {
				viaHop.Params.Set("rport", rport)
			}
		}

		msg = &inboundRequest{
			Request: m,
			conn:    c,
			rmtAddr: rmtAddr,
		}
	case *Response:
		// RFC 3261 Section 18.1.2.
		if utils.LCase(viaHop.Addr.Host()) != utils.LCase(c.GetSentByHost()) {
			c.GetLog().Debug(fmt.Sprintf(`discarding inbound response due to Via's "sent-by" mismatch with transport's host = %q`, c.GetSentByHost()), lflds)
			return
		}
	}

	c.GetLog().Debug("inbound message received; passing it on...", lflds)

	c.onMsg(msg)
}

func (c *connection) writeMsg(msg Message, rmtAddr net.Addr) error {
	bb := getBytesBuf()
	defer freeBytesBuf(bb)
	msg.RenderMessageTo(bb)

	var err error
	// check that the connection socket is connected
	if c.rmtAddr() == nil {
		_, err = c.conn.(net.PacketConn).WriteTo(bb.Bytes(), rmtAddr)
	} else {
		_, err = c.conn.(net.Conn).Write(bb.Bytes())
	}
	if err == nil {
		c.updateTTL()
	}
	return err
}

func (c *connection) updateTTL() {
	ttl := c.GetConnTTL()
	if ttl <= 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ttlTimer == nil {
		c.ttlTimer = time.NewTimer(ttl)
	} else {
		c.ttlTimer.Reset(ttl)
	}
}

type closeOnceListener struct {
	net.Listener
	once     sync.Once
	closeErr error
}

func (oc *closeOnceListener) Close() error {
	oc.once.Do(oc.close)
	return oc.closeErr
}

func (oc *closeOnceListener) close() { oc.closeErr = oc.Listener.Close() }

type closeOnceConn struct {
	net.Conn
	once     sync.Once
	closeErr error
}

func (oc *closeOnceConn) Close() error {
	oc.once.Do(oc.close)
	return oc.closeErr
}

func (oc *closeOnceConn) close() { oc.closeErr = oc.Conn.Close() }

type closeOncePacketConn struct {
	net.PacketConn
	once     sync.Once
	closeErr error
}

func (oc *closeOncePacketConn) Close() error {
	oc.once.Do(oc.close)
	return oc.closeErr
}

func (oc *closeOncePacketConn) close() { oc.closeErr = oc.PacketConn.Close() }
