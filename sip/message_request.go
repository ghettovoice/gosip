package sip

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/netip"
	"slices"

	"github.com/ghettovoice/gosip/internal/iterutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/shared"
)

type RequestMethod = shared.RequestMethod

const (
	RequestMethodAck       = shared.RequestMethodAck
	RequestMethodBye       = shared.RequestMethodBye
	RequestMethodCancel    = shared.RequestMethodCancel
	RequestMethodInfo      = shared.RequestMethodInfo
	RequestMethodInvite    = shared.RequestMethodInvite
	RequestMethodMessage   = shared.RequestMethodMessage
	RequestMethodNotify    = shared.RequestMethodNotify
	RequestMethodOptions   = shared.RequestMethodOptions
	RequestMethodPrack     = shared.RequestMethodPrack
	RequestMethodPublish   = shared.RequestMethodPublish
	RequestMethodRefer     = shared.RequestMethodRefer
	RequestMethodRegister  = shared.RequestMethodRegister
	RequestMethodSubscribe = shared.RequestMethodSubscribe
	RequestMethodUpdate    = shared.RequestMethodUpdate
)

type Request struct {
	Method   RequestMethod
	URI      URI
	Proto    ProtoInfo
	Headers  Headers
	Body     []byte
	Metadata MessageMetadata
}

func (req *Request) RenderTo(w io.Writer) error {
	if req == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, req.Method, " "); err != nil {
		return err
	}
	if req.URI != nil {
		if err := stringutils.RenderTo(w, req.URI); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(w, " ", req.Proto, "\r\n"); err != nil {
		return err
	}
	if err := renderHeaders(w, req.Headers); err != nil {
		return err
	}
	if _, err := fmt.Fprint(w, "\r\n"); err != nil {
		return err
	}
	if _, err := w.Write(req.Body); err != nil {
		return err
	}
	return nil
}

func (req *Request) Render() string {
	if req == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = req.RenderTo(sb)
	return sb.String()
}

func (req *Request) String() string {
	if req == nil {
		return "<nil>"
	}
	return req.Render()
}

func (req *Request) LogValue() slog.Value {
	if req == nil {
		return slog.Value{}
	}
	_, viaHop := iterutils.IterFirst2(req.Headers.ViaHops())
	return slog.GroupValue(
		slog.String("type", fmt.Sprintf("%T", req)),
		slog.String("ptr", fmt.Sprintf("%p", req)),
		slog.Any("method", req.Method),
		slog.Any("uri", req.URI),
		slog.Group("headers",
			slog.Any("Via", utils.ValOrNil(viaHop)),
			slog.Any("From", req.Headers.From()),
			slog.Any("To", req.Headers.To()),
			slog.Any("Call-ID", req.Headers.CallID()),
			slog.Any("CSeq", req.Headers.CSeq()),
		),
		slog.Group("metadata",
			slog.Any(LocalAddrField, req.Metadata[LocalAddrField]),
			slog.Any(RemoteAddrField, req.Metadata[RemoteAddrField]),
			slog.Any(RequestTstampField, req.Metadata[RequestTstampField]),
		),
	)
}

func (req *Request) Clone() Message {
	if req == nil {
		return nil
	}
	req2 := *req
	req2.URI = utils.Clone[URI](req.URI)
	req2.Headers = req.Headers.Clone()
	req2.Body = slices.Clone(req.Body)
	req2.Metadata = maps.Clone(req.Metadata)
	return &req2
}

func (req *Request) Equal(val any) bool {
	var other *Request
	switch v := val.(type) {
	case Request:
		other = &v
	case *Request:
		other = v
	default:
		return false
	}

	if req == other {
		return true
	} else if req == nil || other == nil {
		return false
	}

	return req.Method.Equal(other.Method) &&
		req.Proto.Equal(other.Proto) &&
		utils.IsEqual(req.URI, other.URI) &&
		compareHeaders(req.Headers, other.Headers) &&
		slices.Equal(req.Body, other.Body)
}

func (req *Request) IsValid() bool {
	return req != nil &&
		req.Method.IsValid() &&
		utils.IsValid(req.URI) &&
		req.Proto.IsValid() &&
		validateHeaders(req.Headers) &&
		req.Headers.Has("From") &&
		req.Headers.Has("To") &&
		req.Headers.Has("Call-ID") &&
		req.Headers.Has("CSeq") &&
		req.Headers.Has("Max-Forwards") &&
		req.Headers.Has("Via")
}

type RequestWriter interface {
	RemoteAddr() netip.AddrPort
	WriteRequest(ctx context.Context, req *Request, opts ...any) error
}
