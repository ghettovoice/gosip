package sip_test

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type customHeader struct {
	name string
	num  int
	str  string
}

func parseCustomHeader(name string, value []byte) sip.Header {
	parts := strings.Split(string(value), " ")
	num, _ := strconv.Atoi(parts[0])
	return &customHeader{name: name, num: num, str: parts[1]}
}

func (hdr *customHeader) HeaderName() string { return header.CanonicName(hdr.name) }

func (hdr *customHeader) Clone() sip.Header {
	if hdr == nil {
		return nil
	}
	hdr2 := *hdr
	return &hdr2
}

func (hdr *customHeader) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	var sb strings.Builder
	hdr.RenderHeaderTo(&sb)
	return sb.String()
}

func (hdr *customHeader) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	_, err := fmt.Fprint(w, hdr.HeaderName(), ": ", hdr.num, " ", hdr.str)
	return err
}

func (hdr *customHeader) Equal(val any) bool {
	var other *customHeader
	switch v := val.(type) {
	case *customHeader:
		other = v
	case customHeader:
		other = &v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return header.CanonicName(hdr.name) == header.CanonicName(other.name) &&
		hdr.num == other.num &&
		strings.EqualFold(hdr.str, other.str)
}

func (hdr *customHeader) IsValid() bool {
	return hdr != nil && grammar.IsToken(hdr.name) && hdr.num > 0 && len(hdr.str) > 0
}
