package header

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

// Via represents the Via header field.
// The Via header field indicates the transport used for the transaction and identifies the location
// where the response is to be sent.
type Via []ViaHop

// CanonicName returns the canonical name of the header.
func (Via) CanonicName() Name { return "Via" }

// CompactName returns the compact name of the header.
func (Via) CompactName() Name { return "v" }

// RenderTo writes the header to the provided writer.
func (hdr Via) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.name(opts), ": ")
	cw.Call(hdr.renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

func (hdr Via) name(opts *RenderOptions) Name {
	if opts != nil && opts.Compact {
		return hdr.CompactName()
	}
	return hdr.CanonicName()
}

func (hdr Via) renderValueTo(w io.Writer) (num int, err error) {
	return errtrace.Wrap2(renderHdrEntries(w, hdr))
}

// Render returns the string representation of the header.
func (hdr Via) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr Via) RenderValue() string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb) //nolint:errcheck
	return sb.String()
}

// String returns the string representation of the header value.
func (hdr Via) String() string {
	return hdr.RenderValue()
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Via) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			hdr.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, hdr.String())
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(hdr.Render(nil)))
			return
		}
		fmt.Fprint(f, strconv.Quote(hdr.String()))
		return
	default:
		type hideMethods Via
		type Via hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), Via(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr Via) Clone() Header { return cloneHdrEntries(hdr) }

// Equal compares this header with another for equality.
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

// IsValid checks whether the header is syntactically valid.
func (hdr Via) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(hop ViaHop) bool { return !hop.IsValid() })
}

func (hdr Via) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *Via) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = nil
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(Via)
	if !ok {
		*hdr = nil
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromViaNode(node *abnf.Node) Via {
	hopNodes := node.GetNodes("via-parm")
	h := make(Via, len(hopNodes))
	for i, hopNode := range hopNodes {
		h[i] = buildFromViaParmNode(hopNode)
	}
	return h
}

func buildFromViaParmNode(node *abnf.Node) ViaHop {
	protoNode := grammar.MustGetNode(node, "sent-protocol")
	return ViaHop{
		Proto:     ProtoInfo{Name: protoNode.Children[0].String(), Version: protoNode.Children[2].String()},
		Transport: TransportProto(protoNode.Children[4].String()),
		Addr:      buildFromSentByNode(grammar.MustGetNode(node, "sent-by")),
		Params:    buildFromHeaderParamNodes(node.GetNodes("via-params"), nil),
	}
}

func buildFromSentByNode(node *abnf.Node) Addr {
	host := grammar.MustGetNode(node, "host").String()
	if portNode, ok := node.GetNode("port"); ok {
		port, _ := strconv.Atoi(portNode.String())
		return HostPort(host, uint16(port))
	}
	return Host(host)
}

// ViaHop represents a single hop in the Via header.
type ViaHop struct {
	Proto     ProtoInfo
	Transport TransportProto
	Addr      Addr
	Params    Values
}

// String returns the string representation of the ViaHop.
func (hop ViaHop) String() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	fmt.Fprint(sb, hop.Proto, "/", hop.Transport, " ", hop.Addr)
	renderHdrParams(sb, hop.Params, false) //nolint:errcheck
	return sb.String()
}

// Format implements fmt.Formatter for custom formatting of the ViaHop.
func (hop ViaHop) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		fmt.Fprint(f, hop.String())
		return
	case 'q':
		fmt.Fprint(f, strconv.Quote(hop.String()))
		return
	default:
		if !f.Flag('+') && !f.Flag('#') {
			fmt.Fprint(f, hop.String())
			return
		}

		type hideMethods ViaHop
		type ViaHop hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), ViaHop(hop))
		return
	}
}

// Equal compares this ViaHop with another for equality.
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
		compareHdrParams(hop.Params, other.Params, map[string]bool{
			"maddr":    true,
			"ttl":      true,
			"received": true,
			"rport":    true,
			"branch":   true,
		})
}

// IsValid checks whether the ViaHop is syntactically valid.
func (hop ViaHop) IsValid() bool {
	return hop.Proto.IsValid() &&
		hop.Transport.IsValid() &&
		hop.Addr.IsValid() &&
		validateHdrParams(hop.Params)
}

// IsZero checks whether the ViaHop is empty.
func (hop ViaHop) IsZero() bool {
	return hop.Proto.IsZero() &&
		hop.Transport == "" &&
		hop.Addr.IsZero() &&
		len(hop.Params) == 0
}

// Clone returns a copy of the ViaHop.
func (hop ViaHop) Clone() ViaHop {
	hop.Addr = hop.Addr.Clone()
	hop.Params = hop.Params.Clone()
	return hop
}

func (hop ViaHop) MarshalText() ([]byte, error) {
	return []byte(hop.String()), nil
}

func (hop *ViaHop) UnmarshalText(data []byte) error {
	node, err := grammar.ParseViaParm(data)
	if err != nil {
		*hop = ViaHop{}
		if errors.Is(err, grammar.ErrEmptyInput) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	*hop = buildFromViaParmNode(node)
	return nil
}

func (hop ViaHop) Branch() (string, bool) {
	return hop.Params.Last("branch")
}

var zeroAddr netip.Addr

func (hop ViaHop) Received() (netip.Addr, bool) {
	val, ok := hop.Params.Last("received")
	if !ok {
		return zeroAddr, false
	}
	addr, err := netip.ParseAddr(val)
	if err != nil {
		return zeroAddr, false
	}
	return addr, true
}

func (hop ViaHop) RPort() (uint16, bool) {
	val, ok := hop.Params.Last("rport")
	if !ok {
		return 0, false
	}
	port, err := strconv.ParseUint(val, 10, 16)
	if err != nil {
		return 0, false
	}
	return uint16(port), true
}

func (hop ViaHop) MAddr() (string, bool) {
	return hop.Params.Last("maddr")
}

func (hop ViaHop) TTL() (uint8, bool) {
	val, ok := hop.Params.Last("ttl")
	if !ok {
		return 0, false
	}
	ttl, err := strconv.ParseUint(val, 10, 8)
	if err != nil {
		return 0, false
	}
	return uint8(ttl), true
}
