package header

import (
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
)

type Via []ViaHop

func (Via) CanonicName() Name { return "Via" }

func (hdr Via) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr Via) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr Via) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr Via) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	sb.WriteByte('[')
	_ = hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr Via) Clone() Header { return cloneHeaderEntries(hdr) }

func (hdr Via) Equal(val any) bool {
	var other Via
	switch v := val.(type) {
	case Via:
		other = v
	case *Via:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(hop1, hop2 ViaHop) bool { return hop1.Equal(hop2) })
}

func (hdr Via) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(hop ViaHop) bool { return !hop.IsValid() })
}

func buildFromViaNode(node *abnf.Node) Via {
	hopNodes := node.GetNodes("via-parm")
	h := make(Via, len(hopNodes))
	for i, hopNode := range hopNodes {
		protoNode := abnfutils.MustGetNode(hopNode, "sent-protocol")
		h[i] = ViaHop{
			Proto:     ProtoInfo{Name: protoNode.Children[0].String(), Version: protoNode.Children[2].String()},
			Transport: TransportProto(protoNode.Children[4].String()),
			Addr:      buildFromSentByNode(abnfutils.MustGetNode(hopNode, "sent-by")),
			Params:    buildFromHeaderParamNodes(hopNode.GetNodes("via-params"), nil),
		}
	}
	return h
}

func buildFromSentByNode(node *abnf.Node) Addr {
	host := abnfutils.MustGetNode(node, "host").String()
	if portNode := node.GetNode("port"); portNode != nil {
		port, _ := strconv.Atoi(portNode.String())
		return HostPort(host, uint16(port))
	}
	return Host(host)
}

type ViaHop struct {
	Proto     ProtoInfo
	Transport TransportProto
	Addr      Addr
	Params    Values
}

func (hop ViaHop) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_, _ = fmt.Fprint(sb, hop.Proto, "/", hop.Transport, " ", hop.Addr)
	_ = renderHeaderParams(sb, hop.Params, false)
	return sb.String()
}

func (hop ViaHop) Equal(val any) bool {
	var other ViaHop
	switch v := val.(type) {
	case ViaHop:
		other = v
	case *ViaHop:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return hop.Proto.Equal(other.Proto) &&
		hop.Transport.Equal(other.Transport) &&
		hop.Addr.Equal(other.Addr) &&
		compareHeaderParams(hop.Params, other.Params, map[string]bool{
			"maddr":    true,
			"ttl":      true,
			"received": true,
			"branch":   true,
		})
}

func (hop ViaHop) IsValid() bool {
	return hop.Proto.IsValid() &&
		hop.Transport.IsValid() &&
		hop.Addr.IsValid() &&
		validateHeaderParams(hop.Params)
}

func (hop ViaHop) IsZero() bool {
	return hop.Proto.IsZero() &&
		hop.Transport == "" &&
		hop.Addr.IsZero() &&
		len(hop.Params) == 0
}

func (hop ViaHop) Clone() ViaHop {
	hop.Addr = hop.Addr.Clone()
	hop.Params = hop.Params.Clone()
	return hop
}
