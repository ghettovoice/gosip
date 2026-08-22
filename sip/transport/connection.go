package transport

import (
	"bytes"
	"context"
	"io"
	"iter"
	"log/slog"
	"math"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/netutil"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
)

type ConnectionOptions struct {
	// Parser is the parser to use for the connection.
	// If nil, [sip.DefaultParser] is used.
	Parser sip.Parser
	// Logger is the logger to use for the connection.
	// If nil, [log.Default] is used.
	Logger *slog.Logger
	// MaxMessageReadSize is the maximum buffer size for reading messages from the connection.
	// If zero, [math.MaxUint16] is used.
	MaxMessageReadSize uint
	// MaxMessageWriteSize is the maximum size of the message to write to the connection.
	// If zero, [math.MaxUint16] is used.
	MaxMessageWriteSize uint
	// ReadTimeout is the timeout for reading from the connection.
	// If zero, [time.Second] is used.
	ReadTimeout time.Duration
	// WriteTimeout is the timeout for writing to the connection.
	// If zero, 10*[time.Second] is used.
	WriteTimeout time.Duration
	// IdleTimeout is the timeout for idle connections.
	//
	// Idle timer resets every time a new message is received or sent.
	// If the TTL is set to -1, then no idle timer is used, connections will stay
	// opened until transport shutdown.
	// If the TTL is set to 0, then the value of [sip.TimingConfig.TimeC] + [sip.TimeD]
	// is used, which is 5m by default.
	IdleTimeout time.Duration
}

func (o ConnectionOptions) prsr() sip.Parser {
	if o.Parser == nil {
		return sip.DefaultParser()
	}
	return o.Parser
}

func (o ConnectionOptions) log() *slog.Logger {
	if o.Logger == nil {
		return log.Default()
	}
	return o.Logger
}

func (o ConnectionOptions) maxMsgReadSize() uint {
	if o.MaxMessageReadSize == 0 {
		return math.MaxUint16
	}
	return o.MaxMessageReadSize
}

func (o ConnectionOptions) maxMsgWriteSize() uint {
	if o.MaxMessageWriteSize == 0 {
		return math.MaxUint16
	}
	return o.MaxMessageWriteSize
}

func (o ConnectionOptions) readTimeout() time.Duration {
	if o.ReadTimeout == 0 {
		return time.Second
	}
	return o.ReadTimeout
}

func (o ConnectionOptions) writeTimeout() time.Duration {
	if o.WriteTimeout == 0 {
		return 10 * time.Second
	}
	return o.WriteTimeout
}

func (o ConnectionOptions) idleTimeout() time.Duration {
	if o.IdleTimeout == 0 {
		var tmCfg sip.TimingConfig
		return tmCfg.TimeC() + tmCfg.TimeD
	}
	return o.IdleTimeout
}

type connBase struct {
	laddr, raddr netip.AddrPort
	meta         sip.TransportMetadata
	prsr         sip.Parser
	log          *slog.Logger

	maxMsgReadSize,
	maxMsgWriteSize uint

	readTimeout,
	writeTimeout,
	idleTimeout time.Duration

	closeOnce sync.Once
	closeErr  error
	closed    chan struct{}
}

func (cb *connBase) LocalAddr() netip.AddrPort       { return cb.laddr }
func (cb *connBase) RemoteAddr() netip.AddrPort      { return cb.raddr }
func (cb *connBase) Metadata() sip.TransportMetadata { return cb.meta }
func (cb *connBase) Logger() *slog.Logger            { return cb.log }

func (cb *connBase) String() string {
	if cb == nil {
		return "<nil>"
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	sb.WriteString(cb.meta.Network)
	sb.WriteRune(':')
	sb.WriteString(cb.laddr.String())
	if cb.raddr.IsValid() {
		sb.WriteRune('-')
		sb.WriteString(cb.raddr.String())
	}
	return sb.String()
}

func (cb *connBase) isClosed() bool {
	select {
	case <-cb.closed:
		return true
	default:
		return false
	}
}

func (cb *connBase) wrapInMsg(msg sip.Message, laddr, raddr netip.AddrPort) sip.Message {
	switch m := msg.(type) {
	case *sip.Request:
		return sip.NewRequestEnvelope(m).
			SetTransport(cb.meta).
			SetLocalAddr(laddr).
			SetRemoteAddr(raddr)
	case *sip.Response:
		return sip.NewResponseEnvelope(m).
			SetTransport(cb.meta).
			SetLocalAddr(laddr).
			SetRemoteAddr(raddr)
	default:
		return sip.NewMessageEnvelope(msg).
			SetTransport(cb.meta).
			SetLocalAddr(laddr).
			SetRemoteAddr(raddr)
	}
}

type readPacketConn interface {
	LocalAddr() netip.AddrPort
	ReadFrom(ctx context.Context, b []byte) (int, netip.AddrPort, error)
}

type streamToPacketAdapter struct {
	readStreamConn
}

func (a *streamToPacketAdapter) ReadFrom(ctx context.Context, buf []byte) (int, netip.AddrPort, error) {
	n, err := a.Read(ctx, buf)
	if err != nil {
		return 0, netip.AddrPort{}, errors.Wrap(err)
	}
	return n, a.RemoteAddr(), nil
}

var (
	crlf   = []byte("\r\n")
	crlf2x = []byte("\r\n\r\n")
)

//nolint:gocognit
func (cb *connBase) packetMsgs(
	ctx context.Context,
	conn readPacketConn,
	onKeepAlive func(context.Context, uint8, netip.AddrPort),
) iter.Seq2[sip.Message, error] {
	return func(yield func(sip.Message, error) bool) {
		var (
			rdrDelay    time.Duration
			rdrDelayTmr *time.Timer
		)
		defer func() {
			if rdrDelayTmr != nil {
				rdrDelayTmr.Stop()
			}
		}()

		buf := make([]byte, cb.maxMsgReadSize)
		for {
			num, raddr, err := conn.ReadFrom(ctx, buf)
			if err != nil {
				// conn read error
				if err = cb.continueOnTempReadErr(
					ctx, errors.Wrap(err),
					conn.LocalAddr(), netip.AddrPort{},
					&rdrDelay, &rdrDelayTmr,
				); err == nil {
					// continue on temp, read deadline error
					continue
				}

				// stop on final error
				yield(nil, errors.Wrap(err))
				return
			}

			rdrDelay = 0

			switch {
			// TODO: implement STUN multiplexing for RFC 5626
			case bytes.Equal(buf[:num], crlf2x):
				if onKeepAlive != nil {
					onKeepAlive(ctx, kaPingCRLF, raddr)
				}
				continue
			case bytes.Equal(buf[:num], crlf):
				if onKeepAlive != nil {
					onKeepAlive(ctx, kaPongCRLF, raddr)
				}
				continue
			}

			msg, err := cb.prsr.ParsePacket(buf[:num])
			if err != nil {
				perr, ok := errors.AsType[*sip.ParseError](err)
				if !ok || perr.Msg == nil {
					// skip any empty buffer and parse errors without message
					continue
				}

				perr.Msg = cb.wrapInMsg(perr.Msg, conn.LocalAddr(), raddr)
			}
			if msg != nil {
				msg = cb.wrapInMsg(msg, conn.LocalAddr(), raddr)
			}

			if !yield(msg, errors.Wrap(err)) {
				return
			}

			select {
			case <-ctx.Done():
				yield(nil, errors.Wrap(ctx.Err()))
				return
			default:
			}
		}
	}
}

type readStreamConn interface {
	LocalAddr() netip.AddrPort
	RemoteAddr() netip.AddrPort
	Read(ctx context.Context, b []byte) (int, error)
}

type streamConnReader struct {
	ctx context.Context
	readStreamConn
}

func (r *streamConnReader) Read(b []byte) (int, error) {
	return errors.Wrap2(r.readStreamConn.Read(r.ctx, b))
}

const (
	kaPingCRLF uint8 = iota + 1
	kaPongCRLF
)

type crlfKeepAliveState struct {
	crlfNum    atomic.Uint32
	pingWndTmr *time.Timer
	recvPing   func()
	recvPong   func()
}

func (ka *crlfKeepAliveState) crlf() {
	switch ka.crlfNum.Add(1) {
	case 1:
		ka.pingWndTmr = time.AfterFunc(100*time.Millisecond, func() {
			if ka.crlfNum.CompareAndSwap(1, 0) {
				ka.recvPong()
			}
		})
	case 2:
		ka.recvPing()
		ka.crlfNum.Store(0)

		if ka.pingWndTmr != nil {
			ka.pingWndTmr.Stop()
		}
	}
}

func (ka *crlfKeepAliveState) reset() {
	if ka.crlfNum.CompareAndSwap(1, 0) {
		ka.recvPong()

		if ka.pingWndTmr != nil {
			ka.pingWndTmr.Stop()
		}
	}
}

//nolint:gocognit
func (cb *connBase) streamMsgs(
	ctx context.Context,
	conn readStreamConn,
	onKeepAlive func(ctx context.Context, kaType uint8),
) iter.Seq2[sip.Message, error] {
	return func(yield func(sip.Message, error) bool) {
		kas := &crlfKeepAliveState{
			recvPing: func() {
				if onKeepAlive != nil {
					onKeepAlive(ctx, kaPingCRLF)
				}
			},
			recvPong: func() {
				if onKeepAlive != nil {
					onKeepAlive(ctx, kaPongCRLF)
				}
			},
		}

		var (
			rdrDelay    time.Duration
			rdrDelayTmr *time.Timer
		)
		defer func() {
			if rdrDelayTmr != nil {
				rdrDelayTmr.Stop()
			}
		}()

		rd := &io.LimitedReader{
			R: &streamConnReader{ctx, conn},
			N: int64(cb.maxMsgReadSize),
		}
		sp := cb.prsr.ParseStream(rd)
		for msg, err := range sp.Messages() {
			if err != nil {
				isTooLong := rd.N <= 0
				rd.N = int64(cb.maxMsgReadSize)

				perr, ok := errors.AsType[*sip.ParseError](err)
				if !ok {
					// failed on reading conn before message start
					kas.reset()

					if isTooLong {
						err = sip.ErrMessageTooLarge
					}

					if err = cb.continueOnTempReadErr(
						ctx, errors.Wrap(err),
						conn.LocalAddr(), conn.RemoteAddr(),
						&rdrDelay, &rdrDelayTmr,
					); err == nil {
						// continue on temp, read deadline error
						continue
					}

					// stop iterator on final read errors
					yield(nil, errors.Wrap(err))
					return
				}

				rdrDelay = 0

				if perr.Msg == nil {
					// failed on parsing of message start line
					if errors.Is(perr.Err, grammar.ErrEmptyInput) {
						// got CRLF
						kas.crlf()
						continue
					}

					kas.reset()

					if !yield(nil, errors.Wrap(err)) {
						return
					}
					continue
				}

				// failed at reading/parsing of message headers or body
				kas.reset()

				if isTooLong {
					err = errors.Errorf("%w: %w", err, sip.ErrMessageTooLarge)
				}

				perr.Msg = cb.wrapInMsg(perr.Msg, conn.LocalAddr(), conn.RemoteAddr())
			}

			if msg != nil {
				rdrDelay = 0
				kas.reset()
				rd.N = int64(cb.maxMsgReadSize)

				msg = cb.wrapInMsg(msg, conn.LocalAddr(), conn.RemoteAddr())
			}

			if !yield(msg, errors.Wrap(err)) {
				return
			}

			select {
			case <-ctx.Done():
				yield(nil, errors.Wrap(ctx.Err()))
				return
			default:
			}
		}
	}
}

func (cb *connBase) continueOnTempReadErr(
	ctx context.Context,
	err error,
	laddr, raddr netip.AddrPort,
	rdrDelay *time.Duration,
	rdrDelayTmr **time.Timer,
) error {
	if !errors.IsTemporaryError(err) {
		*rdrDelay = 0
		if *rdrDelayTmr != nil {
			(*rdrDelayTmr).Stop()
		}
		return errors.Wrap(err)
	}

	// retry after delay on temp conn errors
	if errors.IsDeadlineError(err) {
		// our read deadline, no need for exponetial rise
		*rdrDelay = time.Millisecond
	} else {
		// network read error
		if *rdrDelay == 0 {
			*rdrDelay = 5 * time.Millisecond
		} else {
			*rdrDelay *= 2
		}
		if v := time.Minute; *rdrDelay > v {
			*rdrDelay = v
		}

		attrs := append(make([]slog.Attr, 0, 4),
			slog.Any("error", err),
			slog.Duration("delay", *rdrDelay),
			slog.Any("local_addr", laddr),
		)
		if raddr.IsValid() {
			attrs = append(attrs, slog.Any("remote_addr", raddr))
		}

		cb.log.LogAttrs(ctx, slog.LevelDebug,
			"failed to read connection due to the temporary error, continue reading after delay...",
			attrs...,
		)
	}

	if *rdrDelayTmr == nil {
		*rdrDelayTmr = time.NewTimer(*rdrDelay)
	} else {
		(*rdrDelayTmr).Reset(*rdrDelay)
	}

	select {
	case <-cb.closed:
		*rdrDelay = 0
		if *rdrDelayTmr != nil {
			(*rdrDelayTmr).Stop()
		}
		return errors.Wrap(ErrNetworkClosed)
	case <-ctx.Done():
		*rdrDelay = 0
		if *rdrDelayTmr != nil {
			(*rdrDelayTmr).Stop()
		}
		return errors.Wrap(ctx.Err())
	case <-(*rdrDelayTmr).C:
		return nil
	}
}

// AcquireConnectionOptions represents options for acquiring a connection.
type AcquireConnectionOptions struct {
	LocalAddr netip.AddrPort
	Dial      bool
	Host      string
}

func (o AcquireConnectionOptions) locAddr() netip.AddrPort {
	return netutil.UnmapAddrPort(o.LocalAddr)
}
