package ws

import (
	"net"
	"net/url"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"

	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip"
)

type Config struct {
	UpgradeTimeout time.Duration
}

type Dialer struct {
	ws.Dialer
	cfg *Config
}

func NewDialer(cfg *Config) *Dialer {
	d := &Dialer{
		cfg: cfg,
	}
	d.Protocols = []string{stringutils.LCase(sip.ProtoVer20().Name)}
	return d
}

func (d *Dialer) Upgrade(c net.Conn, u *url.URL) (net.Conn, error) {
	if d.cfg != nil && d.cfg.UpgradeTimeout > 0 {
		if err := c.SetDeadline(time.Now().Add(d.cfg.UpgradeTimeout)); err != nil {
			return c, err
		}
		defer c.SetDeadline(time.Time{})
	}

	_, hs, err := d.Dialer.Upgrade(c, u)
	if err != nil {
		return c, err
	}
	return &Conn{c, ws.StateClientSide, hs}, nil
}

type Listener struct {
	net.Listener
	ws.Upgrader
	cfg *Config
}

func NewListener(ls net.Listener, cfg *Config) *Listener {
	wsLs := &Listener{
		Listener: ls,
		cfg:      cfg,
	}
	wsLs.Protocol = func(b []byte) bool { return stringutils.LCase(string(b)) == stringutils.LCase(sip.ProtoVer20().Name) }
	return wsLs
}

func (l *Listener) Accept() (c net.Conn, err error) {
	c, err = l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			c.Close()
		}
	}()

	return l.Upgrade(c)
}

func (l *Listener) Upgrade(c net.Conn) (net.Conn, error) {
	if l.cfg != nil && l.cfg.UpgradeTimeout > 0 {
		if err := c.SetDeadline(time.Now().Add(l.cfg.UpgradeTimeout)); err != nil {
			return c, err
		}
		defer c.SetDeadline(time.Time{})
	}

	hs, err := l.Upgrader.Upgrade(c)
	if err != nil {
		return c, err
	}
	return &Conn{c, ws.StateServerSide, hs}, nil
}

type Conn struct {
	net.Conn
	state ws.State
	hs    ws.Handshake
}

func (c *Conn) Read(b []byte) (n int, err error) {
	var msg []byte
	// var op ws.OpCode
	if c.state.ClientSide() {
		msg, _, err = wsutil.ReadServerData(c.Conn)
	} else {
		msg, _, err = wsutil.ReadClientData(c.Conn)
	}
	if err != nil {
		// var wsErr wsutil.ClosedError
		// if errors.As(err, &wsErr) {
		// 	err = io.EOF
		// }
		return n, err
	}
	// if op == ws.OpClose {
	// 	return n, io.EOF
	// }
	n = copy(b, msg)
	return n, err
}

func (c *Conn) Write(b []byte) (n int, err error) {
	if c.state.ClientSide() {
		err = wsutil.WriteClientMessage(c.Conn, ws.OpText, b)
	} else {
		err = wsutil.WriteServerMessage(c.Conn, ws.OpText, b)
	}
	if err != nil {
		// var wsErr wsutil.ClosedError
		// if errors.As(err, &wsErr) {
		// 	err = io.EOF
		// }
		return n, err
	}
	return len(b), nil
}
