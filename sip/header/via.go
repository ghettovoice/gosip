package header

import (
	"bytes"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/shared"
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
func (hdr Via) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr Via) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrValsTo(w, hdr))
}

// Render returns the string representation of the header.
func (hdr Via) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr Via) RenderValue() string {
	return renderHdrVal(hdr)
}

// String returns the string representation of the header value.
func (hdr Via) String() string {
	return hdr.RenderValue()
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Via) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Via
		Via         hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), Via(hdr))
}

// Clone returns a copy of the header.
func (hdr Via) Clone() Header {
	return cloneHdrVals(hdr)
}

// Equal compares this header with another for equality.
func (hdr Via) Equal(val any) bool {
	return util.EqualValue(val, func(other Via) bool {
		return slices.EqualFunc(hdr, other, func(hop1, hop2 ViaHop) bool {
			return hop1.Equal(hop2)
		})
	})
}

// IsValid checks whether the header is syntactically valid.
func (hdr Via) IsValid() bool {
	return len(hdr) > 0 &&
		!slices.ContainsFunc(hdr, func(hop ViaHop) bool {
			return !hop.IsValid()
		})
}

func (hdr Via) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Via) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
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
	addr, err := shared.AddrFromABNF(grammar.MustGetNode(node, "sent-by"))
	if err != nil {
		panic(errors.Wrap(err))
	}
	return ViaHop{
		Proto:     ProtoInfo{Name: protoNode.Children[0].String(), Version: protoNode.Children[2].String()},
		Transport: TransportProto(protoNode.Children[4].String()),
		Addr:      addr,
		Params:    buildFromHdrParamNodes(node.GetNodes("via-params"), nil),
	}
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
	return renderVal(hop)
}

func (hop ViaHop) renderTo(w io.Writer) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(hop.Proto, "/", hop.Transport, " ", hop.Addr)
	cw.Call(func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrParamsTo(w, hop.Params, false))
	})
	return errors.Wrap2(cw.Result())
}

// Format implements fmt.Formatter for custom formatting of the ViaHop.
func (hop ViaHop) Format(f fmt.State, verb rune) {
	if formatVal(f, verb, hop) {
		return
	}
	type (
		hideMethods ViaHop
		ViaHop      hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), ViaHop(hop))
}

// Equal compares this ViaHop with another for equality.
func (hop ViaHop) Equal(val any) bool {
	return util.EqualValue(val, func(other ViaHop) bool {
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

func (hop ViaHop) AppendText(b []byte) ([]byte, error) {
	return append(b, hop.String()...), nil
}

func (hop *ViaHop) UnmarshalText(data []byte) (finErr error) {
	if len(data) == 0 || bytes.Equal(data, []byte("// ")) {
		*hop = ViaHop{}
		return nil
	}

	defer func() {
		if rv := recover(); rv != nil {
			if e, ok := rv.(error); ok {
				finErr = errors.Wrap(e)
			} else {
				finErr = errors.ErrorfWrap("%v", rv)
			}
		}
	}()

	node, err := grammar.ParseViaParm(data)
	if err != nil {
		return errors.Wrap(err)
	}

	*hop = buildFromViaParmNode(node)
	return nil
}

func (hop ViaHop) Branch() (string, bool) {
	return hop.Params.Last("branch")
}

func (hop ViaHop) Received() (netip.Addr, bool) {
	val, ok := hop.Params.Last("received")
	if !ok {
		return netip.Addr{}, false
	}

	addr, err := netip.ParseAddr(val)
	if err != nil {
		return netip.Addr{}, false
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

func (hop ViaHop) MAddr() (Addr, bool) {
	ma, ok := hop.Params.Last("maddr")
	if !ok {
		return Addr{}, false
	}
	return MakeHostAddr(ma), true
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
