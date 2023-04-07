package grammar

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/constraints"
	"github.com/ghettovoice/gosip/sip/internal/grammar/rfc3261"
	"github.com/ghettovoice/gosip/sip/internal/grammar/rfc3966"
)

type Error string

func (e Error) Error() string { return fmt.Sprintf("grammar error: %s", string(e)) }

func (e Error) Grammar() bool { return true }

var nodesPool = sync.Pool{
	New: func() any {
		ns := make(abnf.Nodes, 0, 1)
		return &ns
	},
}

func getNodes() abnf.Nodes {
	ns := nodesPool.Get().(*abnf.Nodes)
	return *ns
}

func putNodes(ns abnf.Nodes) {
	clear(ns)
	ns = ns[:0]
	if cap(ns) > 1000 {
		return
	}
	nodesPool.Put(&ns)
}

func IsToken[T constraints.Byteseq](s T) bool {
	if len(s) == 0 {
		return false
	}

	ns := getNodes()
	defer putNodes(ns)

	n := rfc3261.Token([]byte(s), ns).Best()
	return n.Len() == len(s)
}

func IsHost[T constraints.Byteseq](s T) bool {
	if len(s) == 0 {
		return false
	}

	ns := getNodes()
	defer putNodes(ns)

	n := rfc3261.Host([]byte(s), ns).Best()
	return n.Len() == len(s)
}

func IsQuoted[T constraints.Byteseq](s T) bool {
	if len(s) == 0 {
		return false
	}

	ns := getNodes()
	defer putNodes(ns)

	n := rfc3261.QuotedString([]byte(s), ns).Best()
	return n.Len() == len(s)
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

func IsTelNum[T constraints.Byteseq](s T) bool {
	if len(s) == 0 {
		return false
	}

	ns := getNodes()
	defer putNodes(ns)

	var n *abnf.Node
	if s[0] == '+' {
		n = rfc3966.GlobalNumberDigits([]byte(s), ns).Best()
	} else {
		n = rfc3966.LocalNumberDigits([]byte(s), ns).Best()
	}
	return n.Len() == len(s)
}

func IsGlobTelNum[T constraints.Byteseq](s T) bool {
	return IsTelNum(s) && s[0] == '+'
}

var telVisSepRpl = strings.NewReplacer(" ", "", "-", "", ".", "", "(", "", ")", "")

// CleanTelNum removes all visual separators.
func CleanTelNum[T constraints.Byteseq](s T) T { return T(telVisSepRpl.Replace(string(s))) }

func IsTelURIParamName[T constraints.Byteseq](s T) bool {
	if len(s) == 0 {
		return false
	}

	ns := getNodes()
	defer putNodes(ns)

	n := rfc3966.Pname([]byte(s), ns).Best()
	return n.Len() == len(s)
}

func IsUsername[T constraints.Byteseq](s T) bool {
	if len(s) == 0 {
		return false
	}

	ns := getNodes()
	defer putNodes(ns)

	n := rfc3261.User([]byte(s), ns).Best()
	return n.Len() == len(s)
}
