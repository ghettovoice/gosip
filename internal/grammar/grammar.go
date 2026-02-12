package grammar

//go:generate errtrace -w .

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/grammar/rfc3261"
	"github.com/ghettovoice/gosip/internal/grammar/rfc3966"
)

func init() {
	// abnf.NodesCap = 1000
	abnf.EnableNodeCache(10 * 1024)
}

type Error string

func (e Error) Error() string { return string(e) }

func (Error) Grammar() bool { return true }

const (
	ErrNodeNotFound Error = "node not found"
	ErrUnexpectNode Error = "unexpected node"
)

// MustGetNode returns a pointer to the ABNF node with the given key.
func MustGetNode(n *abnf.Node, k string) *abnf.Node {
	sn, ok := n.GetNode(k)
	if !ok {
		panic(fmt.Errorf("get node %q from node %q: %w", k, n.Key, ErrNodeNotFound))
	}
	return sn
}

func IsToken[T ~string | ~[]byte](s T) bool {
	if len(s) == 0 {
		return false
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().Token([]byte(s), ns); err != nil {
		return false
	}
	return ns.Best().Len() == len(s)
}

func IsHost[T ~string | ~[]byte](s T) bool {
	if len(s) == 0 {
		return false
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().Host([]byte(s), ns); err != nil {
		return false
	}
	return ns.Best().Len() == len(s)
}

func IsQuoted[T ~string | ~[]byte](s T) bool {
	if len(s) == 0 {
		return false
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().QuotedString([]byte(s), ns); err != nil {
		return false
	}
	return ns.Best().Len() == len(s)
}

func Quote(s string) string {
	return strconv.Quote(s)
}

func Unquote(s string) string {
	qs, err := strconv.Unquote(s)
	if err != nil {
		qs = s
	}
	return qs
}

func IsTelNum[T ~string | ~[]byte](s T) bool {
	if len(s) == 0 {
		return false
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	var err error
	if s[0] == '+' {
		err = rfc3966.Rules().GlobalNumberDigits([]byte(s), ns)
	} else {
		err = rfc3966.Rules().LocalNumberDigits([]byte(s), ns)
	}
	if err != nil {
		return false
	}
	return ns.Best().Len() == len(s)
}

func IsGlobTelNum[T ~string | ~[]byte](s T) bool {
	return IsTelNum(s) && s[0] == '+'
}

var telVisSepRpl = strings.NewReplacer(" ", "", "-", "", ".", "", "(", "", ")", "")

// CleanTelNum removes all visual separators.
func CleanTelNum[T ~string | ~[]byte](s T) T { return T(telVisSepRpl.Replace(string(s))) }

func IsTelURIParamName[T ~string | ~[]byte](s T) bool {
	if len(s) == 0 {
		return false
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3966.Rules().Pname([]byte(s), ns); err != nil {
		return false
	}
	return ns.Best().Len() == len(s)
}

func IsUsername[T ~string | ~[]byte](s T) bool {
	if len(s) == 0 {
		return false
	}

	ns := abnf.NewNodes()
	defer ns.Free()

	if err := rfc3261.Rules().User([]byte(s), ns); err != nil {
		return false
	}
	return ns.Best().Len() == len(s)
}
