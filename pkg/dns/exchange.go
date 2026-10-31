package dns

import (
	"context"
	"io"
	"net"
	"time"

	"codeberg.org/miekg/dns"

	"github.com/ghettovoice/gosip/internal/errors"
)

// exchangeNAPTR sends a NAPTR query over a connected UDP socket owned by this
// call and waits for a single datagram response. The library client is not
// used for IO because it applies its own fixed deadlines; the message is used
// only as a codec. The caller's context controls how long the exchange may take.
func (r *Resolver) exchangeNAPTR(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	conn, err := r.dialer.DialContext(ctx, "udp", r.nameSrv)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer func() { _ = conn.Close() }()

	return errors.Wrap2(exchangeDNS(ctx, conn, req))
}

// exchangeDNS performs a single UDP request/response exchange on an already
// connected socket. The caller's context controls the exchange duration: a
// context deadline is installed as the connection deadline, and cancellation
// closes the socket to unblock pending IO even when no deadline is set.
func exchangeDNS(ctx context.Context, conn net.Conn, req *dns.Msg) (*dns.Msg, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Wrap(err)
	}

	connDL, _ := ctx.Deadline()
	if !connDL.IsZero() {
		if err := conn.SetDeadline(connDL); err != nil {
			return nil, errors.Wrap(err)
		}
	}

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if err := req.Pack(); err != nil {
		return nil, errors.Wrap(err)
	}

	n, err := conn.Write(req.Data)
	switch {
	case err != nil:
		return nil, exchangeErr(ctx, connDL, err)
	case n < len(req.Data):
		return nil, errors.Wrap(io.ErrShortWrite)
	}

	buf := make([]byte, max(dns.MinMsgSize, int(req.UDPSize), len(req.Data)))
	n, err = conn.Read(buf)
	if err != nil {
		return nil, exchangeErr(ctx, connDL, err)
	}

	if err := ctx.Err(); err != nil {
		return nil, errors.Wrap(err)
	}

	resp := &dns.Msg{Data: buf[:n]}
	if err := resp.Unpack(); err != nil {
		return nil, errors.Wrap(err)
	}
	if !resp.Response {
		return nil, errors.ErrorWrap("received non-response DNS message")
	}
	if resp.ID != req.ID {
		return nil, errors.Wrap(dns.ErrID)
	}
	return resp, nil
}

// exchangeErr prefers the context error when the exchange was aborted by the
// caller. A socket timeout is also mapped to the context deadline error when it
// came from the connection deadline installed from that context, since the
// context timer callback may not have run yet at the same instant.
func exchangeErr(ctx context.Context, connDL time.Time, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return errors.Wrap(cerr)
	}
	if !connDL.IsZero() && !time.Now().Before(connDL) {
		if nerr, ok := errors.AsType[net.Error](err); ok && nerr.Timeout() {
			return errors.Wrap(context.DeadlineExceeded)
		}
	}
	return errors.Wrap(err)
}
